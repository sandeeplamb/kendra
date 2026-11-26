package kube

import (
	"context"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// GWListener is one listener of a Gateway.
type GWListener struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Port     int64  `json:"port"`
	Hostname string `json:"hostname,omitempty"`
	Routes   int64  `json:"routes"` // attached routes
	Ready    string `json:"ready"`  // True, False, Unknown
}

// GWInfo is a Gateway API Gateway.
type GWInfo struct {
	Namespace  string       `json:"namespace"`
	Name       string       `json:"name"`
	Class      string       `json:"class"`
	Addresses  []string     `json:"addresses"`
	Programmed string       `json:"programmed"` // True, False, Unknown
	Message    string       `json:"message,omitempty"`
	Listeners  []GWListener `json:"listeners"`
}

// RouteBackend is a service a route sends traffic to.
type RouteBackend struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Port      int64  `json:"port,omitempty"`
	Weight    int64  `json:"weight"`
	Missing   bool   `json:"missing"` // no such Service in the cluster
}

// RouteInfo is an HTTPRoute, GRPCRoute or TLSRoute.
type RouteInfo struct {
	Kind         string         `json:"kind"`
	Namespace    string         `json:"namespace"`
	Name         string         `json:"name"`
	Hostnames    []string       `json:"hostnames"`
	Parents      []string       `json:"parents"` // namespace/gateway
	Backends     []RouteBackend `json:"backends"`
	Accepted     string         `json:"accepted"`
	ResolvedRefs string         `json:"resolvedRefs"`
	Message      string         `json:"message,omitempty"`
}

// GWClass is a GatewayClass.
type GWClass struct {
	Name       string `json:"name"`
	Controller string `json:"controller"`
	Accepted   string `json:"accepted"`
}

// GatewayReport is the Gateway API picture of one cluster.
type GatewayReport struct {
	Classes  []GWClass   `json:"classes"`
	Gateways []GWInfo    `json:"gateways"`
	Routes   []RouteInfo `json:"routes"`
}

const gwGroup = "gateway.networking.k8s.io"

// condFrom finds a condition of the given type ("" = the first one) in a conditions list.
func condFrom(conds []any, typ string) (status, msg string) {
	for _, x := range conds {
		m, _ := x.(map[string]any)
		if typ == "" || m["type"] == typ {
			status, _ = m["status"].(string)
			msg, _ = m["message"].(string)
			return
		}
	}
	return "Unknown", ""
}

func condType(u *unstructured.Unstructured, typ string, path ...string) (string, string) {
	conds, _, _ := unstructured.NestedSlice(u.Object, path...)
	return condFrom(conds, typ)
}

// worst combines statuses of several parents: any False wins, then Unknown.
func worstStatus(a, b string) string {
	if a == "" {
		return b
	}
	rank := map[string]int{"True": 0, "Unknown": 1, "False": 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// GatewayAPI lists GatewayClasses, Gateways (with listeners and addresses) and routes
// (with their backends and acceptance status). A cluster without the Gateway API CRDs
// answers "not installed". Read-only.
func (c *Cluster) GatewayAPI(ctx context.Context) (*GatewayReport, error) {
	gk, err := c.KindFor(gwGroup, "gateways")
	if err != nil {
		return nil, err
	}
	rep := &GatewayReport{Classes: []GWClass{}, Gateways: []GWInfo{}, Routes: []RouteInfo{}}
	if k, err := c.KindFor(gwGroup, "gatewayclasses"); err == nil {
		if l, err := c.Dyn.Resource(k.GVR()).List(ctx, metav1.ListOptions{}); err == nil {
			for i := range l.Items {
				u := &l.Items[i]
				cl := GWClass{Name: u.GetName()}
				cl.Controller, _, _ = unstructured.NestedString(u.Object, "spec", "controllerName")
				cl.Accepted, _ = condType(u, "Accepted", "status", "conditions")
				rep.Classes = append(rep.Classes, cl)
			}
		}
	}
	gl, err := c.Dyn.Resource(gk.GVR()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for i := range gl.Items {
		u := &gl.Items[i]
		g := GWInfo{Namespace: u.GetNamespace(), Name: u.GetName(), Addresses: []string{}, Listeners: []GWListener{}}
		g.Class, _, _ = unstructured.NestedString(u.Object, "spec", "gatewayClassName")
		g.Programmed, g.Message = condType(u, "Programmed", "status", "conditions")
		if addrs, ok, _ := unstructured.NestedSlice(u.Object, "status", "addresses"); ok {
			for _, a := range addrs {
				if m, ok := a.(map[string]any); ok {
					if v, _ := m["value"].(string); v != "" {
						g.Addresses = append(g.Addresses, v)
					}
				}
			}
		}
		attached := map[string]int64{}
		ready := map[string]string{}
		if sl, ok, _ := unstructured.NestedSlice(u.Object, "status", "listeners"); ok {
			for _, x := range sl {
				m, _ := x.(map[string]any)
				name, _ := m["name"].(string)
				if n, ok := m["attachedRoutes"].(int64); ok {
					attached[name] = n
				}
				conds, _ := m["conditions"].([]any)
				ready[name], _ = condFrom(conds, "Programmed")
			}
		}
		if sl, ok, _ := unstructured.NestedSlice(u.Object, "spec", "listeners"); ok {
			for _, x := range sl {
				m, _ := x.(map[string]any)
				l := GWListener{}
				l.Name, _ = m["name"].(string)
				l.Protocol, _ = m["protocol"].(string)
				l.Hostname, _ = m["hostname"].(string)
				l.Port, _ = m["port"].(int64)
				l.Routes = attached[l.Name]
				if l.Ready = ready[l.Name]; l.Ready == "" {
					l.Ready = "Unknown"
				}
				g.Listeners = append(g.Listeners, l)
			}
		}
		rep.Gateways = append(rep.Gateways, g)
	}

	svcs := map[string]bool{}
	if sl, err := c.Cs.CoreV1().Services("").List(ctx, metav1.ListOptions{}); err == nil {
		for _, s := range sl.Items {
			svcs[s.Namespace+"/"+s.Name] = true
		}
	}
	for _, res := range []struct{ plural, kind string }{{"httproutes", "HTTPRoute"}, {"grpcroutes", "GRPCRoute"}, {"tlsroutes", "TLSRoute"}} {
		k, err := c.KindFor(gwGroup, res.plural)
		if err != nil {
			continue
		}
		l, err := c.Dyn.Resource(k.GVR()).List(ctx, metav1.ListOptions{})
		if err != nil {
			continue
		}
		for i := range l.Items {
			u := &l.Items[i]
			r := RouteInfo{Kind: res.kind, Namespace: u.GetNamespace(), Name: u.GetName(), Hostnames: []string{}, Parents: []string{}, Backends: []RouteBackend{}, Accepted: "Unknown", ResolvedRefs: "Unknown"}
			if hn, ok, _ := unstructured.NestedStringSlice(u.Object, "spec", "hostnames"); ok {
				r.Hostnames = hn
			}
			if ps, ok, _ := unstructured.NestedSlice(u.Object, "spec", "parentRefs"); ok {
				for _, x := range ps {
					m, _ := x.(map[string]any)
					name, _ := m["name"].(string)
					ns, _ := m["namespace"].(string)
					if ns == "" {
						ns = u.GetNamespace()
					}
					r.Parents = append(r.Parents, ns+"/"+name)
				}
			}
			seen := map[string]bool{}
			rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
			for _, rl := range rules {
				rm, _ := rl.(map[string]any)
				brs, _ := rm["backendRefs"].([]any)
				for _, b := range brs {
					bm, _ := b.(map[string]any)
					if kind, _ := bm["kind"].(string); kind != "" && kind != "Service" {
						continue
					}
					bk := RouteBackend{Weight: 1}
					bk.Name, _ = bm["name"].(string)
					bk.Namespace, _ = bm["namespace"].(string)
					if bk.Namespace == "" {
						bk.Namespace = u.GetNamespace()
					}
					bk.Port, _ = bm["port"].(int64)
					if w, ok := bm["weight"].(int64); ok {
						bk.Weight = w
					}
					bk.Missing = !svcs[bk.Namespace+"/"+bk.Name]
					if key := bk.Namespace + "/" + bk.Name; !seen[key] {
						seen[key] = true
						r.Backends = append(r.Backends, bk)
					}
				}
			}
			// status: the worst answer over all parent gateways
			r.Accepted, r.ResolvedRefs = "", ""
			if ps, ok, _ := unstructured.NestedSlice(u.Object, "status", "parents"); ok {
				for _, x := range ps {
					m, _ := x.(map[string]any)
					conds, _ := m["conditions"].([]any)
					a, am := condFrom(conds, "Accepted")
					rr, rm := condFrom(conds, "ResolvedRefs")
					r.Accepted, r.ResolvedRefs = worstStatus(r.Accepted, a), worstStatus(r.ResolvedRefs, rr)
					if a == "False" && r.Message == "" {
						r.Message = am
					} else if rr == "False" && r.Message == "" {
						r.Message = rm
					}
				}
			}
			if r.Accepted == "" {
				r.Accepted = "Unknown"
			}
			if r.ResolvedRefs == "" {
				r.ResolvedRefs = "Unknown"
			}
			rep.Routes = append(rep.Routes, r)
		}
	}
	sort.Slice(rep.Gateways, func(i, j int) bool {
		return rep.Gateways[i].Namespace+rep.Gateways[i].Name < rep.Gateways[j].Namespace+rep.Gateways[j].Name
	})
	sort.Slice(rep.Routes, func(i, j int) bool {
		return rep.Routes[i].Namespace+rep.Routes[i].Name < rep.Routes[j].Namespace+rep.Routes[j].Name
	})
	return rep, nil
}
