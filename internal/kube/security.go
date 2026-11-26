package kube

import (
	"context"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Risk is one security-relevant setting of a workload's pods.
type Risk struct {
	Level string `json:"level"` // bad | warn
	Text  string `json:"text"`
}

// RiskyWorkload groups the pods of one workload that share the same risks.
type RiskyWorkload struct {
	Namespace string `json:"namespace"`
	Workload  string `json:"workload"`
	Pods      int    `json:"pods"`
	Risks     []Risk `json:"risks"`
}

// PSA is a namespace's Pod Security Admission labels ("" = not set).
type PSA struct {
	Namespace string `json:"namespace"`
	Enforce   string `json:"enforce"`
	Audit     string `json:"audit"`
	Warn      string `json:"warn"`
}

// SecurityReport is the answer for one cluster.
type SecurityReport struct {
	Workloads  []RiskyWorkload `json:"workloads"`
	Namespaces []PSA           `json:"namespaces"`
	Pods       int             `json:"pods"` // pods looked at
}

var dangerousCaps = map[string]bool{"ALL": true, "SYS_ADMIN": true, "NET_ADMIN": true, "SYS_PTRACE": true, "SYS_MODULE": true, "DAC_READ_SEARCH": true}

// podRisks reads a pod spec (never a Secret) and lists risky settings.
func podRisks(p *corev1.Pod) []Risk {
	var out []Risk
	add := func(level, text string) {
		for _, r := range out {
			if r.Text == text {
				return
			}
		}
		out = append(out, Risk{level, text})
	}
	s := &p.Spec
	if s.HostNetwork {
		add("bad", "host network")
	}
	if s.HostPID {
		add("bad", "host PID namespace")
	}
	if s.HostIPC {
		add("bad", "host IPC namespace")
	}
	for _, v := range s.Volumes {
		if v.HostPath != nil {
			add("bad", "hostPath volume "+v.HostPath.Path)
		}
	}
	podRoot := s.SecurityContext != nil && s.SecurityContext.RunAsUser != nil && *s.SecurityContext.RunAsUser == 0
	for _, list := range [][]corev1.Container{s.InitContainers, s.Containers} {
		for _, ct := range list {
			sc := ct.SecurityContext
			if sc != nil && sc.Privileged != nil && *sc.Privileged {
				add("bad", "privileged container")
			}
			if (sc != nil && sc.RunAsUser != nil && *sc.RunAsUser == 0) || (podRoot && (sc == nil || sc.RunAsUser == nil)) {
				add("bad", "runs as root (user 0)")
			}
			if sc != nil && sc.AllowPrivilegeEscalation != nil && *sc.AllowPrivilegeEscalation {
				add("warn", "allows privilege escalation")
			}
			if sc != nil && sc.Capabilities != nil {
				for _, c := range sc.Capabilities.Add {
					name := strings.ToUpper(string(c))
					if dangerousCaps[name] {
						add("bad", "adds capability "+name)
					} else {
						add("warn", "adds capability "+name)
					}
				}
			}
			for _, pt := range ct.Ports {
				if pt.HostPort != 0 {
					add("warn", "host port")
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Level == "bad" && out[j].Level != "bad" })
	return out
}

// Security lists workloads with risky pod settings and each namespace's Pod Security
// labels. It reads pod specs and namespace labels only.
func (c *Cluster) Security(ctx context.Context) (*SecurityReport, error) {
	pl, err := c.Cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	rep := &SecurityReport{Workloads: []RiskyWorkload{}, Namespaces: []PSA{}}
	idx := map[string]int{}
	for i := range pl.Items {
		p := &pl.Items[i]
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		rep.Pods++
		risks := podRisks(p)
		if len(risks) == 0 {
			continue
		}
		w := workloadOf(p)
		key := p.Namespace + "|" + w
		if j, ok := idx[key]; ok {
			rep.Workloads[j].Pods++
			continue
		}
		idx[key] = len(rep.Workloads)
		rep.Workloads = append(rep.Workloads, RiskyWorkload{Namespace: p.Namespace, Workload: w, Pods: 1, Risks: risks})
	}
	sort.Slice(rep.Workloads, func(i, j int) bool {
		bi, bj := countBad(rep.Workloads[i].Risks), countBad(rep.Workloads[j].Risks)
		if bi != bj {
			return bi > bj
		}
		return rep.Workloads[i].Namespace+rep.Workloads[i].Workload < rep.Workloads[j].Namespace+rep.Workloads[j].Workload
	})
	if nl, err := c.Cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{}); err == nil {
		for _, n := range nl.Items {
			l := n.Labels
			rep.Namespaces = append(rep.Namespaces, PSA{n.Name, l["pod-security.kubernetes.io/enforce"], l["pod-security.kubernetes.io/audit"], l["pod-security.kubernetes.io/warn"]})
		}
		sort.Slice(rep.Namespaces, func(i, j int) bool { return rep.Namespaces[i].Namespace < rep.Namespaces[j].Namespace })
	}
	return rep, nil
}

func countBad(rs []Risk) int {
	n := 0
	for _, r := range rs {
		if r.Level == "bad" {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------- cloud identities

// Identity is a service account that is tied to a cloud identity, and who uses it.
type Identity struct {
	Namespace      string   `json:"namespace"`
	ServiceAccount string   `json:"serviceAccount"`
	Provider       string   `json:"provider"` // AWS, Azure, GCP
	Identity       string   `json:"identity"` // role ARN, client ID or service account email
	Pods           int      `json:"pods"`
	Workloads      []string `json:"workloads"`
}

var identityAnnotations = []struct{ key, provider string }{
	{"eks.amazonaws.com/role-arn", "AWS"},
	{"azure.workload.identity/client-id", "Azure"},
	{"iam.gke.io/gcp-service-account", "GCP"},
}

// Identities lists service accounts annotated for AWS IAM roles (IRSA), Azure workload
// identity or GKE workload identity, with the pods and workloads that run as them. It
// reads service accounts and pod specs; no tokens or Secrets.
func (c *Cluster) Identities(ctx context.Context) ([]Identity, error) {
	sl, err := c.Cs.CoreV1().ServiceAccounts("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := []Identity{}
	at := map[string]int{}
	for _, sa := range sl.Items {
		for _, a := range identityAnnotations {
			if v := sa.Annotations[a.key]; v != "" {
				at[sa.Namespace+"/"+sa.Name] = len(out)
				out = append(out, Identity{Namespace: sa.Namespace, ServiceAccount: sa.Name, Provider: a.provider, Identity: v, Workloads: []string{}})
				break
			}
		}
	}
	if len(out) == 0 {
		return out, nil
	}
	if pl, err := c.Cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{}); err == nil {
		seen := map[string]bool{}
		for i := range pl.Items {
			p := &pl.Items[i]
			name := p.Spec.ServiceAccountName
			if name == "" {
				name = "default"
			}
			j, ok := at[p.Namespace+"/"+name]
			if !ok || p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
				continue
			}
			out[j].Pods++
			if w := workloadOf(p); !seen[p.Namespace+"|"+name+"|"+w] {
				seen[p.Namespace+"|"+name+"|"+w] = true
				out[j].Workloads = append(out[j].Workloads, w)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Provider+out[i].Namespace+out[i].ServiceAccount < out[j].Provider+out[j].Namespace+out[j].ServiceAccount
	})
	return out, nil
}
