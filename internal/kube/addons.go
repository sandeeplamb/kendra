package kube

import (
	"context"
	"fmt"
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// AddonInfo is one operator or add-on found in a cluster.
type AddonInfo struct {
	Name      string   `json:"name"`
	Version   string   `json:"version,omitempty"`
	Namespace string   `json:"namespace,omitempty"`
	Ready     int      `json:"ready"`
	Desired   int      `json:"desired"`
	Level     string   `json:"level"` // ok | warn | bad
	Via       string   `json:"via"`   // how it was found: workload, CRDs
	Notes     []string `json:"notes,omitempty"`
}

type addonSpec struct {
	name   string
	group  string      // API group of its custom resources ("" = none)
	labels [][2]string // any of these label pairs on a Deployment or DaemonSet
	names  []string    // or one of these workload names
	extra  func(ctx context.Context, c *Cluster, a *AddonInfo)
}

// The add-ons Kendra recognises. Anything else is simply not listed.
var addonSpecs = []addonSpec{
	{name: "cert-manager", group: "cert-manager.io", labels: [][2]string{{"app.kubernetes.io/name", "cert-manager"}}, names: []string{"cert-manager"}, extra: extraCertManager},
	{name: "External Secrets", group: "external-secrets.io", labels: [][2]string{{"app.kubernetes.io/name", "external-secrets"}}, extra: extraExternalSecrets},
	{name: "ExternalDNS", labels: [][2]string{{"app.kubernetes.io/name", "external-dns"}}, names: []string{"external-dns"}},
	{name: "Velero", group: "velero.io", labels: [][2]string{{"app.kubernetes.io/name", "velero"}}, names: []string{"velero"}, extra: extraVelero},
	{name: "Prometheus Operator", group: "monitoring.coreos.com", labels: [][2]string{{"app.kubernetes.io/name", "prometheus-operator"}, {"app.kubernetes.io/name", "kube-prometheus-stack-prometheus-operator"}, {"app", "kube-prometheus-stack-operator"}}, extra: extraPrometheus},
	{name: "ingress-nginx", labels: [][2]string{{"app.kubernetes.io/name", "ingress-nginx"}}},
	{name: "AWS Load Balancer Controller", labels: [][2]string{{"app.kubernetes.io/name", "aws-load-balancer-controller"}}, names: []string{"aws-load-balancer-controller"}},
	{name: "metrics-server", labels: [][2]string{{"k8s-app", "metrics-server"}, {"app.kubernetes.io/name", "metrics-server"}}, names: []string{"metrics-server"}},
	{name: "KEDA", group: "keda.sh", labels: [][2]string{{"app.kubernetes.io/name", "keda-operator"}}, names: []string{"keda-operator"}},
	{name: "Karpenter", group: "karpenter.sh", labels: [][2]string{{"app.kubernetes.io/name", "karpenter"}}, names: []string{"karpenter"}},
	{name: "Istio", group: "networking.istio.io", labels: [][2]string{{"app", "istiod"}}, names: []string{"istiod"}},
	{name: "Kyverno", group: "kyverno.io", labels: [][2]string{{"app.kubernetes.io/part-of", "kyverno"}}},
	{name: "Gatekeeper", group: "constraints.gatekeeper.sh", labels: [][2]string{{"app", "gatekeeper"}}, names: []string{"gatekeeper-controller-manager"}},
	{name: "Datadog Operator", group: "datadoghq.com", labels: [][2]string{{"app.kubernetes.io/name", "datadog-operator"}}, names: []string{"datadog-operator"}},
}

func imageTag(img string) string {
	_, tag, _ := splitImage(img)
	return tag
}

func (c *Cluster) listObjs(ctx context.Context, group, resource string) []unstructured.Unstructured {
	k, err := c.KindFor(group, resource)
	if err != nil {
		return nil
	}
	l, err := c.Dyn.Resource(k.GVR()).List(ctx, metav1.ListOptions{Limit: 2000})
	if err != nil {
		return nil
	}
	return l.Items
}

func condition(u *unstructured.Unstructured, typ string) (status, reason string) {
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, x := range conds {
		if m, ok := x.(map[string]any); ok && m["type"] == typ {
			status, _ = m["status"].(string)
			reason, _ = m["reason"].(string)
		}
	}
	return
}

func extraCertManager(ctx context.Context, c *Cluster, a *AddonInfo) {
	items := c.listObjs(ctx, "cert-manager.io", "certificates")
	notReady := 0
	for i := range items {
		if st, _ := condition(&items[i], "Ready"); st != "True" {
			notReady++
		}
	}
	a.Notes = append(a.Notes, fmt.Sprintf("%d certificates", len(items)))
	if notReady > 0 {
		a.Notes = append(a.Notes, fmt.Sprintf("%d not ready", notReady))
		a.Level = worse(a.Level, "warn")
	}
}

func extraExternalSecrets(ctx context.Context, c *Cluster, a *AddonInfo) {
	items := c.listObjs(ctx, "external-secrets.io", "externalsecrets")
	notReady := 0
	for i := range items {
		if st, _ := condition(&items[i], "Ready"); st != "True" {
			notReady++
		}
	}
	a.Notes = append(a.Notes, fmt.Sprintf("%d ExternalSecrets", len(items)))
	if notReady > 0 {
		a.Notes = append(a.Notes, fmt.Sprintf("%d not syncing", notReady))
		a.Level = worse(a.Level, "warn")
	}
}

func extraVelero(ctx context.Context, c *Cluster, a *AddonInfo) {
	items := c.listObjs(ctx, "velero.io", "backups")
	if len(items) == 0 {
		a.Notes = append(a.Notes, "no backups yet")
		a.Level = worse(a.Level, "warn")
		return
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].GetCreationTimestamp().After(items[j].GetCreationTimestamp().Time)
	})
	last := &items[0]
	phase, _, _ := unstructured.NestedString(last.Object, "status", "phase")
	when := last.GetCreationTimestamp().Time
	if done, _, _ := unstructured.NestedString(last.Object, "status", "completionTimestamp"); done != "" {
		if t, err := time.Parse(time.RFC3339, done); err == nil {
			when = t
		}
	}
	a.Notes = append(a.Notes, fmt.Sprintf("last backup %s %s ago", phase, shortDur(time.Since(when))))
	switch {
	case phase == "Failed" || phase == "FailedValidation":
		a.Level = worse(a.Level, "bad")
	case phase == "PartiallyFailed" || time.Since(when) > 48*time.Hour:
		a.Level = worse(a.Level, "warn")
	}
	if s := c.listObjs(ctx, "velero.io", "schedules"); len(s) > 0 {
		a.Notes = append(a.Notes, fmt.Sprintf("%d schedules", len(s)))
	}
}

func extraPrometheus(ctx context.Context, c *Cluster, a *AddonInfo) {
	sm := len(c.listObjs(ctx, "monitoring.coreos.com", "servicemonitors"))
	pr := len(c.listObjs(ctx, "monitoring.coreos.com", "prometheusrules"))
	a.Notes = append(a.Notes, fmt.Sprintf("%d ServiceMonitors, %d rule sets", sm, pr))
}

func shortDur(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

type workload struct {
	name, ns string
	labels   map[string]string
	ready    int
	desired  int
	image    string
}

// Addons finds the operators and add-ons installed in the cluster: by their workloads
// (Deployments and DaemonSets, with the image tag as version and ready/desired pods) and
// by the API groups of their custom resources. Read-only.
func (c *Cluster) Addons(ctx context.Context) ([]AddonInfo, error) {
	var ws []workload
	dl, err := c.Cs.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, d := range dl.Items {
		want := 1
		if d.Spec.Replicas != nil {
			want = int(*d.Spec.Replicas)
		}
		ws = append(ws, workload{d.Name, d.Namespace, d.Labels, int(d.Status.ReadyReplicas), want, firstImage(d.Spec.Template.Spec.Containers)})
	}
	if sl, err := c.Cs.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{}); err == nil {
		for _, d := range sl.Items {
			ws = append(ws, daemonWorkload(d))
		}
	}
	groups := map[string]bool{}
	if g, err := c.Cs.Discovery().ServerGroups(); err == nil {
		for _, x := range g.Groups {
			groups[x.Name] = true
		}
	}
	out := []AddonInfo{}
	for _, sp := range addonSpecs {
		a := AddonInfo{Name: sp.name, Level: "ok"}
		for _, w := range ws {
			if !sp.matches(w) {
				continue
			}
			if a.Namespace == "" {
				a.Namespace, a.Version, a.Via = w.ns, imageTag(w.image), "workload"
			}
			a.Ready += w.ready
			a.Desired += w.desired
		}
		if a.Via == "" {
			if sp.group == "" || !groups[sp.group] {
				continue
			}
			a.Via = "CRDs only"
			a.Notes = append(a.Notes, "API "+sp.group+" is served, no controller found")
			a.Level = "warn"
		} else {
			switch {
			case a.Desired > 0 && a.Ready == 0:
				a.Level = "bad"
			case a.Ready < a.Desired:
				a.Level = "warn"
			}
		}
		if sp.extra != nil && (a.Via == "workload" || groups[sp.group]) {
			sp.extra(ctx, c, &a)
		}
		out = append(out, a)
	}
	return out, nil
}

func (sp addonSpec) matches(w workload) bool {
	for _, n := range sp.names {
		if w.name == n {
			return true
		}
	}
	for _, l := range sp.labels {
		if w.labels[l[0]] == l[1] {
			return true
		}
	}
	return false
}

func firstImage(cts []corev1.Container) string {
	if len(cts) == 0 {
		return ""
	}
	return cts[0].Image
}

func daemonWorkload(d appsv1.DaemonSet) workload {
	return workload{d.Name, d.Namespace, d.Labels, int(d.Status.NumberReady), int(d.Status.DesiredNumberScheduled), firstImage(d.Spec.Template.Spec.Containers)}
}
