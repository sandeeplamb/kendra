package kube

import (
	"context"
	"encoding/json"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NsOverview is one line of the namespace overview.
type NsOverview struct {
	Cluster      string `json:"cluster"`
	Name         string `json:"name"`
	Phase        string `json:"phase"`
	Created      string `json:"created"`
	Health       string `json:"health"`
	Pods         int    `json:"pods"`
	PodsBad      int    `json:"podsBad"`
	PodsWarn     int    `json:"podsWarn"`
	Workloads    int    `json:"workloads"`
	WorkloadsBad int    `json:"workloadsBad"`
	Services     int    `json:"services"`
	Ingresses    int    `json:"ingresses"`
	ConfigMaps   int    `json:"configMaps"`
	Secrets      int    `json:"secrets"`
	Claims       int    `json:"claims"`
}

// countByNamespace counts objects per namespace from object metadata only, so nothing
// but names ever leaves the API server (important for Secrets).
func (c *Cluster) countByNamespace(ctx context.Context, path string) map[string]int {
	out := map[string]int{}
	raw, err := c.Cs.CoreV1().RESTClient().Get().AbsPath(path).SetHeader("Accept", partialMeta).DoRaw(ctx)
	if err != nil {
		return out // not allowed, or not served: leave the column at 0
	}
	var l struct {
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &l) == nil {
		for _, it := range l.Items {
			out[it.Metadata.Namespace]++
		}
	}
	return out
}

// NamespaceOverview summarises every namespace: pods by health, workloads, and counts of
// the objects that make up a namespace.
func (c *Cluster) NamespaceOverview(ctx context.Context) ([]NsOverview, error) {
	nl, err := c.Cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	by := map[string]*NsOverview{}
	out := make([]NsOverview, 0, len(nl.Items))
	for _, n := range nl.Items {
		out = append(out, NsOverview{Cluster: c.Name, Name: n.Name, Phase: string(n.Status.Phase),
			Created: n.CreationTimestamp.UTC().Format("2006-01-02T15:04:05Z"), Health: "ok"})
	}
	for i := range out {
		by[out[i].Name] = &out[i]
	}
	if pl, err := c.Cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{}); err == nil {
		for i := range pl.Items {
			p := &pl.Items[i]
			o := by[p.Namespace]
			if o == nil || p.Status.Phase == corev1.PodSucceeded {
				continue
			}
			o.Pods++
			switch podTroubles(p) {
			case "bad":
				o.PodsBad++
			case "warn":
				o.PodsWarn++
			}
		}
	}
	for _, name := range []string{"deployments", "statefulsets", "daemonsets"} {
		k, _ := FindKind(name)
		l, err := c.Dyn.Resource(k.GVR()).List(ctx, metav1.ListOptions{})
		if err != nil {
			continue
		}
		for i := range l.Items {
			u := &l.Items[i]
			u.SetKind(k.Singular)
			if o := by[u.GetNamespace()]; o != nil {
				o.Workloads++
				if h, _ := UnstructuredHealth(u); h == "bad" || h == "warn" {
					o.WorkloadsBad++
				}
			}
		}
	}
	counts := []struct {
		path string
		set  func(*NsOverview, int)
	}{
		{"/api/v1/services", func(o *NsOverview, n int) { o.Services = n }},
		{"/apis/networking.k8s.io/v1/ingresses", func(o *NsOverview, n int) { o.Ingresses = n }},
		{"/api/v1/configmaps", func(o *NsOverview, n int) { o.ConfigMaps = n }},
		{"/api/v1/secrets", func(o *NsOverview, n int) { o.Secrets = n }},
		{"/api/v1/persistentvolumeclaims", func(o *NsOverview, n int) { o.Claims = n }},
	}
	for _, cn := range counts {
		for ns, n := range c.countByNamespace(ctx, cn.path) {
			if o := by[ns]; o != nil {
				cn.set(o, n)
			}
		}
	}
	for i := range out {
		o := &out[i]
		switch {
		case o.Phase == "Terminating":
			o.Health = "warn"
		case o.PodsBad > 0 || o.WorkloadsBad > 0 && o.PodsBad > 0:
			o.Health = "bad"
		case o.PodsWarn > 0 || o.WorkloadsBad > 0:
			o.Health = "warn"
		case o.Pods == 0 && o.Workloads == 0:
			o.Health = "idle"
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// CRD is a custom resource definition as the left rail and the CRD page need it.
type CRD struct {
	Name     string   `json:"name"`
	Group    string   `json:"group"`
	Kind     string   `json:"kind"`
	Plural   string   `json:"plural"`
	Scope    string   `json:"scope"`
	Versions []string `json:"versions"`
	Ready    bool     `json:"ready"`
}

// CRDs lists every custom resource definition in the cluster.
func (c *Cluster) CRDs(ctx context.Context) ([]CRD, error) {
	raw, err := c.Cs.CoreV1().RESTClient().Get().AbsPath("/apis/apiextensions.k8s.io/v1/customresourcedefinitions").
		SetHeader("Accept", "application/json").DoRaw(ctx)
	if err != nil {
		return nil, err
	}
	var l struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Group string `json:"group"`
				Scope string `json:"scope"`
				Names struct {
					Kind   string `json:"kind"`
					Plural string `json:"plural"`
				} `json:"names"`
				Versions []struct {
					Name   string `json:"name"`
					Served bool   `json:"served"`
				} `json:"versions"`
			} `json:"spec"`
			Status struct {
				Conditions []struct{ Type, Status string } `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, err
	}
	out := make([]CRD, 0, len(l.Items))
	for _, it := range l.Items {
		d := CRD{Name: it.Metadata.Name, Group: it.Spec.Group, Kind: it.Spec.Names.Kind, Plural: it.Spec.Names.Plural, Scope: it.Spec.Scope}
		for _, v := range it.Spec.Versions {
			if v.Served {
				d.Versions = append(d.Versions, v.Name)
			}
		}
		for _, cd := range it.Status.Conditions {
			if cd.Type == "Established" && cd.Status == "True" {
				d.Ready = true
			}
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}
