package kube

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ---------------------------------------------------------------- usage vs requests

// UsageRow is what one workload uses against what it asked for. CPU in millicores,
// memory in bytes. Requests and limits are summed over the pods.
type UsageRow struct {
	Namespace  string `json:"namespace"`
	Workload   string `json:"workload"` // Deployment/web, StatefulSet/db, Pod/x ...
	Pods       int    `json:"pods"`
	CPUUsed    int64  `json:"cpuUsed"`
	CPUReq     int64  `json:"cpuReq"`
	CPULim     int64  `json:"cpuLim"`
	MemUsed    int64  `json:"memUsed"`
	MemReq     int64  `json:"memReq"`
	MemLim     int64  `json:"memLim"`
	NoRequests int    `json:"noRequests"` // pods with no CPU or memory request at all
}

// UsageReport is the per-cluster answer. Metrics is false when metrics-server is
// missing or unreadable: requests and limits are still shown, usage is 0.
type UsageReport struct {
	Metrics bool       `json:"metrics"`
	Rows    []UsageRow `json:"rows"`
}

// podMetrics reads live usage from metrics-server: "ns/pod" -> {cpu m, memory bytes}.
func (c *Cluster) podMetrics(ctx context.Context) (map[string][2]int64, bool) {
	raw, err := c.Cs.CoreV1().RESTClient().Get().AbsPath("/apis/metrics.k8s.io/v1beta1/pods").DoRaw(ctx)
	if err != nil {
		return nil, false
	}
	var ml struct {
		Items []struct {
			Metadata   struct{ Name, Namespace string } `json:"metadata"`
			Containers []struct {
				Usage map[string]string `json:"usage"`
			} `json:"containers"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &ml) != nil {
		return nil, false
	}
	out := map[string][2]int64{}
	for _, it := range ml.Items {
		var u [2]int64
		for _, ct := range it.Containers {
			if q, err := resource.ParseQuantity(ct.Usage["cpu"]); err == nil {
				u[0] += q.MilliValue()
			}
			if q, err := resource.ParseQuantity(ct.Usage["memory"]); err == nil {
				u[1] += q.Value()
			}
		}
		out[it.Metadata.Namespace+"/"+it.Metadata.Name] = u
	}
	return out, true
}

// workloadOf names the thing that owns a pod: Deployment/web (via its ReplicaSet),
// CronJob/x (via its Job), StatefulSet/db, DaemonSet/agent, or Pod/name for bare pods.
func workloadOf(p *corev1.Pod) string {
	owners := p.OwnerReferences
	for i, o := range owners { // the controller first, else the first owner
		if o.Controller != nil && *o.Controller {
			owners = owners[i : i+1]
			break
		}
	}
	for _, o := range owners[:min(1, len(owners))] {
		name := o.Name
		switch o.Kind {
		case "ReplicaSet":
			if i := strings.LastIndexByte(name, '-'); i > 0 {
				return "Deployment/" + name[:i]
			}
		case "Job":
			if i := strings.LastIndexByte(name, '-'); i > 0 && allDigits(name[i+1:]) {
				return "CronJob/" + name[:i]
			}
		}
		return o.Kind + "/" + name
	}
	return "Pod/" + p.Name
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Usage sums, per namespace and workload, what pods use, request and are limited to.
func (c *Cluster) Usage(ctx context.Context) (*UsageReport, error) {
	pl, err := c.Cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	use, ok := c.podMetrics(ctx)
	rep := &UsageReport{Metrics: ok, Rows: []UsageRow{}}
	idx := map[string]int{}
	for i := range pl.Items {
		p := &pl.Items[i]
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		w := workloadOf(p)
		k := p.Namespace + "|" + w
		j, seen := idx[k]
		if !seen {
			j = len(rep.Rows)
			idx[k] = j
			rep.Rows = append(rep.Rows, UsageRow{Namespace: p.Namespace, Workload: w})
		}
		r := &rep.Rows[j]
		r.Pods++
		none := true
		for _, ct := range p.Spec.Containers {
			rq, lm := ct.Resources.Requests, ct.Resources.Limits
			r.CPUReq += rq.Cpu().MilliValue()
			r.MemReq += rq.Memory().Value()
			r.CPULim += lm.Cpu().MilliValue()
			r.MemLim += lm.Memory().Value()
			if !rq.Cpu().IsZero() || !rq.Memory().IsZero() {
				none = false
			}
		}
		if none {
			r.NoRequests++
		}
		if u, ok := use[p.Namespace+"/"+p.Name]; ok {
			r.CPUUsed += u[0]
			r.MemUsed += u[1]
		}
	}
	return rep, nil
}

// ---------------------------------------------------------------- image inventory

// ImageUse is one image in one namespace: how many pods run it.
type ImageUse struct {
	Image     string `json:"image"`
	Repo      string `json:"repo"` // without tag or digest
	Tag       string `json:"tag"`  // "" when pinned by digest only
	Digest    bool   `json:"digest"`
	Namespace string `json:"namespace"`
	Pods      int    `json:"pods"`
}

// splitImage returns repo, tag and whether a digest is present:
// "host:5000/a/b:1.2@sha256:x" -> "host:5000/a/b", "1.2", true.
func splitImage(img string) (repo, tag string, digest bool) {
	if i := strings.IndexByte(img, '@'); i >= 0 {
		img, digest = img[:i], true
	}
	last := strings.LastIndexByte(img, '/')
	if i := strings.LastIndexByte(img, ':'); i > last {
		return img[:i], img[i+1:], digest
	}
	return img, "", digest
}

// Images lists every container image the pods of the cluster run (init containers
// included), per namespace. Pod specs only; nothing from the images themselves.
func (c *Cluster) Images(ctx context.Context) ([]ImageUse, error) {
	pl, err := c.Cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	out := []ImageUse{}
	for i := range pl.Items {
		p := &pl.Items[i]
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		seen := map[string]bool{}
		for _, list := range [][]corev1.Container{p.Spec.InitContainers, p.Spec.Containers} {
			for _, ct := range list {
				if seen[ct.Image] {
					continue
				}
				seen[ct.Image] = true
				k := p.Namespace + "|" + ct.Image
				if j, ok := idx[k]; ok {
					out[j].Pods++
					continue
				}
				repo, tag, dg := splitImage(ct.Image)
				idx[k] = len(out)
				out = append(out, ImageUse{Image: ct.Image, Repo: repo, Tag: tag, Digest: dg, Namespace: p.Namespace, Pods: 1})
			}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- certificates (cert-manager)

// CertInfo is one cert-manager Certificate.
type CertInfo struct {
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Secret    string   `json:"secret"`
	Issuer    string   `json:"issuer"`
	DNSNames  []string `json:"dnsNames"`
	Ready     string   `json:"ready"` // True, False, Unknown
	Reason    string   `json:"reason,omitempty"`
	Message   string   `json:"message,omitempty"`
	NotAfter  string   `json:"notAfter,omitempty"`
	Renewal   string   `json:"renewal,omitempty"`
	HasExpiry bool     `json:"hasExpiry"`
	DaysLeft  int      `json:"daysLeft"`
}

// Certificates lists cert-manager Certificates with their expiry. Only cert-manager
// objects are read (never the Secrets that hold the keys). A cluster without
// cert-manager answers "not installed".
func (c *Cluster) Certificates(ctx context.Context) ([]CertInfo, error) {
	k, err := c.KindFor("cert-manager.io", "certificates")
	if err != nil {
		return nil, err
	}
	l, err := c.Dyn.Resource(k.GVR()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := []CertInfo{}
	now := time.Now()
	for i := range l.Items {
		u := &l.Items[i]
		ci := CertInfo{Namespace: u.GetNamespace(), Name: u.GetName(), Ready: "Unknown", DNSNames: []string{}}
		ci.Secret, _, _ = unstructured.NestedString(u.Object, "spec", "secretName")
		ci.Issuer, _, _ = unstructured.NestedString(u.Object, "spec", "issuerRef", "name")
		if names, ok, _ := unstructured.NestedStringSlice(u.Object, "spec", "dnsNames"); ok {
			ci.DNSNames = names
		}
		if cn, _, _ := unstructured.NestedString(u.Object, "spec", "commonName"); cn != "" && len(ci.DNSNames) == 0 {
			ci.DNSNames = []string{cn}
		}
		if conds, ok, _ := unstructured.NestedSlice(u.Object, "status", "conditions"); ok {
			for _, x := range conds {
				m, _ := x.(map[string]any)
				if m["type"] == "Ready" {
					ci.Ready, _ = m["status"].(string)
					ci.Reason, _ = m["reason"].(string)
					ci.Message, _ = m["message"].(string)
				}
			}
		}
		ci.NotAfter, _, _ = unstructured.NestedString(u.Object, "status", "notAfter")
		ci.Renewal, _, _ = unstructured.NestedString(u.Object, "status", "renewalTime")
		if t, err := time.Parse(time.RFC3339, ci.NotAfter); err == nil {
			ci.HasExpiry = true
			ci.DaysLeft = int(t.Sub(now).Hours() / 24)
			if t.Before(now) {
				ci.DaysLeft = -int(now.Sub(t).Hours()/24) - 1
			}
		}
		out = append(out, ci)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.HasExpiry != b.HasExpiry {
			return a.HasExpiry
		}
		if a.DaysLeft != b.DaysLeft {
			return a.DaysLeft < b.DaysLeft
		}
		return a.Namespace+a.Name < b.Namespace+b.Name
	})
	return out, nil
}

// ---------------------------------------------------------------- snapshot (compare clusters)

// SnapItem is one object reduced to the properties worth comparing between clusters.
type SnapItem struct {
	Kind      string            `json:"kind"`
	Namespace string            `json:"namespace"`
	Name      string            `json:"name"`
	Props     map[string]string `json:"props"`
}

func imageProps(props map[string]string, prefix string, cts []corev1.Container) {
	for _, ct := range cts {
		props[prefix+ct.Name] = ct.Image
	}
}

// Snapshot lists workloads, services, ingresses, config maps (key names only) and
// autoscalers of a namespace (all namespaces when ns is empty) as comparable items.
// No ConfigMap values and no Secrets are read.
func (c *Cluster) Snapshot(ctx context.Context, ns string) ([]SnapItem, error) {
	out := []SnapItem{}
	add := func(kind, ns, name string, p map[string]string) { out = append(out, SnapItem{kind, ns, name, p}) }
	ap := c.Cs.AppsV1()
	if l, err := ap.Deployments(ns).List(ctx, metav1.ListOptions{}); err != nil {
		return nil, err
	} else {
		for _, d := range l.Items {
			p := map[string]string{"replicas": replicasStr(d.Spec.Replicas)}
			imageProps(p, "image ", d.Spec.Template.Spec.InitContainers)
			imageProps(p, "image ", d.Spec.Template.Spec.Containers)
			add("Deployment", d.Namespace, d.Name, p)
		}
	}
	if l, err := ap.StatefulSets(ns).List(ctx, metav1.ListOptions{}); err == nil {
		for _, d := range l.Items {
			p := map[string]string{"replicas": replicasStr(d.Spec.Replicas)}
			imageProps(p, "image ", d.Spec.Template.Spec.InitContainers)
			imageProps(p, "image ", d.Spec.Template.Spec.Containers)
			add("StatefulSet", d.Namespace, d.Name, p)
		}
	}
	if l, err := ap.DaemonSets(ns).List(ctx, metav1.ListOptions{}); err == nil {
		for _, d := range l.Items {
			p := map[string]string{}
			imageProps(p, "image ", d.Spec.Template.Spec.InitContainers)
			imageProps(p, "image ", d.Spec.Template.Spec.Containers)
			add("DaemonSet", d.Namespace, d.Name, p)
		}
	}
	if l, err := c.Cs.BatchV1().CronJobs(ns).List(ctx, metav1.ListOptions{}); err == nil {
		for _, d := range l.Items {
			p := map[string]string{"schedule": d.Spec.Schedule}
			imageProps(p, "image ", d.Spec.JobTemplate.Spec.Template.Spec.Containers)
			add("CronJob", d.Namespace, d.Name, p)
		}
	}
	if l, err := c.Cs.CoreV1().Services(ns).List(ctx, metav1.ListOptions{}); err == nil {
		for _, s := range l.Items {
			var ports []string
			for _, pt := range s.Spec.Ports {
				ports = append(ports, strings.TrimSpace(itoa32(pt.Port)+"/"+string(pt.Protocol)))
			}
			add("Service", s.Namespace, s.Name, map[string]string{"type": string(s.Spec.Type), "ports": strings.Join(ports, ", ")})
		}
	}
	if l, err := c.Cs.NetworkingV1().Ingresses(ns).List(ctx, metav1.ListOptions{}); err == nil {
		for _, s := range l.Items {
			var hosts []string
			for _, r := range s.Spec.Rules {
				hosts = append(hosts, r.Host)
			}
			add("Ingress", s.Namespace, s.Name, map[string]string{"hosts": strings.Join(hosts, ", ")})
		}
	}
	if l, err := c.Cs.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{}); err == nil {
		for _, s := range l.Items {
			if s.Name == "kube-root-ca.crt" {
				continue
			}
			keys := make([]string, 0, len(s.Data)+len(s.BinaryData))
			for k := range s.Data {
				keys = append(keys, k)
			}
			for k := range s.BinaryData {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			add("ConfigMap", s.Namespace, s.Name, map[string]string{"keys": strings.Join(keys, ", ")})
		}
	}
	return out, nil
}

func replicasStr(p *int32) string {
	if p == nil {
		return "1"
	}
	return itoa32(*p)
}

func itoa32(n int32) string {
	b, _ := json.Marshal(n)
	return string(b)
}
