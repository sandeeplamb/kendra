package kube

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// IstioGraph is the traffic topology of the Istio resources in a cluster:
//
//	Gateway -> VirtualService -> DestinationRule -> Service (port) -> pods
//
// It is built from networking.istio.io resources only; nothing is installed.
type IstioGraph struct {
	Installed bool           `json:"installed"`
	Graph     *Graph         `json:"graph"`
	Counts    map[string]int `json:"counts"`
}

const (
	lGateway = 0
	lVS      = 1
	lDR      = 2
	lSvc     = 3
	lPods    = 4
)

type ib struct {
	g      *Graph
	node   map[string]int
	svcs   map[string]*corev1.Service
	dr     map[string]string // service key -> destination rule id
	ctx    context.Context
	c      *Cluster
	podsOf map[string]string
}

func (b *ib) add(n Node) string {
	if _, ok := b.node[n.ID]; !ok {
		b.node[n.ID] = len(b.g.Nodes)
		b.g.Nodes = append(b.g.Nodes, n)
	}
	return n.ID
}

func (b *ib) edge(from, to, typ, label string) {
	for i, e := range b.g.Edges {
		if e.From == from && e.To == to && e.Type == typ {
			if label != "" && !strings.Contains(e.Label, label) {
				if e.Label != "" {
					b.g.Edges[i].Label += " · "
				}
				b.g.Edges[i].Label += label
			}
			return
		}
	}
	b.g.Edges = append(b.g.Edges, Edge{From: from, To: to, Type: typ, Label: label})
}

func (c *Cluster) istioList(ctx context.Context, resource string) ([]unstructured.Unstructured, error) {
	for _, ar := range c.discover() {
		if ar.gvr.Group == "networking.istio.io" && ar.gvr.Resource == resource {
			l, err := c.Dyn.Resource(schema.GroupVersionResource{Group: ar.gvr.Group, Version: ar.gvr.Version, Resource: resource}).
				List(ctx, metav1.ListOptions{})
			if err != nil {
				return nil, err
			}
			return l.Items, nil
		}
	}
	return nil, nil
}

// resolveHost turns an Istio host into a service namespace and name relative to the
// namespace of the object that mentions it. ok is false for external or wildcard hosts.
func resolveHost(host, from string) (ns, name string, ok bool) {
	if host == "" || strings.Contains(host, "*") {
		return "", "", false
	}
	p := strings.Split(host, ".")
	switch {
	case len(p) == 1:
		return from, p[0], true
	case len(p) == 2:
		return p[1], p[0], true
	case len(p) >= 3 && p[2] == "svc":
		return p[1], p[0], true
	}
	return "", "", false
}

// IstioTopology builds the graph for one namespace or, when ns is empty, the cluster.
func (c *Cluster) IstioTopology(ctx context.Context, ns string) (*IstioGraph, error) {
	out := &IstioGraph{Graph: &Graph{Nodes: []Node{}, Edges: []Edge{}, Other: []Res{}}, Counts: map[string]int{}}
	gws, err := c.istioList(ctx, "gateways")
	if err != nil {
		return nil, err
	}
	vss, err := c.istioList(ctx, "virtualservices")
	if err != nil {
		return nil, err
	}
	drs, _ := c.istioList(ctx, "destinationrules")
	ses, _ := c.istioList(ctx, "serviceentries")
	for _, ar := range c.discover() {
		if ar.gvr.Group == "networking.istio.io" {
			out.Installed = true
		}
	}
	if !out.Installed {
		return out, nil
	}
	keep := func(u unstructured.Unstructured) bool { return ns == "" || u.GetNamespace() == ns }
	b := &ib{g: out.Graph, node: map[string]int{}, svcs: map[string]*corev1.Service{}, dr: map[string]string{},
		ctx: ctx, c: c, podsOf: map[string]string{}}
	sl, err := c.Cs.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for i := range sl.Items {
		b.svcs[sl.Items[i].Namespace+"/"+sl.Items[i].Name] = &sl.Items[i]
	}

	gwID := func(from, ref string) string {
		gns, gname := from, ref
		if i := strings.Index(ref, "/"); i > 0 {
			gns, gname = ref[:i], ref[i+1:]
		}
		return "gw:" + gns + "/" + gname
	}
	for _, g := range gws {
		if !keep(g) && ns != "" {
			// still shown when a kept VirtualService points at it (below)
			continue
		}
		var servers []string
		sv, _, _ := unstructured.NestedSlice(g.Object, "spec", "servers")
		for _, x := range sv {
			m, _ := x.(map[string]any)
			num, _, _ := unstructured.NestedFieldNoCopy(m, "port", "number")
			proto, _, _ := unstructured.NestedString(m, "port", "protocol")
			hosts, _, _ := unstructured.NestedStringSlice(m, "hosts")
			servers = append(servers, fmt.Sprintf("%v/%s %s", num, proto, strings.Join(hosts, ",")))
		}
		sel, _, _ := unstructured.NestedStringMap(g.Object, "spec", "selector")
		d := strings.Join(servers, " · ")
		if len(sel) > 0 {
			d += "  [" + labels.Set(sel).String() + "]"
		}
		b.add(Node{ID: gwID(g.GetNamespace(), g.GetName()), Kind: "Gateway", Name: g.GetName(), Namespace: g.GetNamespace(),
			Layer: lGateway, Health: "ok", Detail: d})
		out.Counts["Gateway"]++
	}

	// Destination rules by the service they apply to.
	type rule struct {
		id      string
		subsets map[string]map[string]string
	}
	rules := map[string]rule{}
	for _, d := range drs {
		host, _, _ := unstructured.NestedString(d.Object, "spec", "host")
		sns, sname, ok := resolveHost(host, d.GetNamespace())
		key := host
		if ok {
			key = sns + "/" + sname
		}
		ss, _, _ := unstructured.NestedSlice(d.Object, "spec", "subsets")
		subs := map[string]map[string]string{}
		var names []string
		for _, x := range ss {
			m, _ := x.(map[string]any)
			n, _ := m["name"].(string)
			l, _, _ := unstructured.NestedStringMap(m, "labels")
			subs[n] = l
			names = append(names, n)
		}
		det := host
		if len(names) > 0 {
			det += " · subsets " + strings.Join(names, ", ")
		}
		if mode, _, _ := unstructured.NestedString(d.Object, "spec", "trafficPolicy", "tls", "mode"); mode != "" {
			det += " · tls " + mode
		}
		if keep(d) || ns == "" {
			out.Counts["DestinationRule"]++
		}
		rules[key] = rule{id: "dr:" + d.GetNamespace() + "/" + d.GetName(), subsets: subs}
		if keep(d) || ns == "" {
			b.add(Node{ID: "dr:" + d.GetNamespace() + "/" + d.GetName(), Kind: "DestinationRule", Name: d.GetName(),
				Namespace: d.GetNamespace(), Layer: lDR, Health: "ok", Detail: det})
		}
	}

	seMap := map[string]string{}
	for _, se := range ses {
		hosts, _, _ := unstructured.NestedStringSlice(se.Object, "spec", "hosts")
		for _, h := range hosts {
			seMap[h] = "se:" + se.GetNamespace() + "/" + se.GetName()
		}
		if keep(se) {
			out.Counts["ServiceEntry"]++
			b.add(Node{ID: "se:" + se.GetNamespace() + "/" + se.GetName(), Kind: "ServiceEntry", Name: se.GetName(),
				Namespace: se.GetNamespace(), Layer: lSvc, Health: "ok", Detail: strings.Join(hosts, ", ") + " (outside the mesh)"})
		}
	}

	for _, vs := range vss {
		if !keep(vs) {
			continue
		}
		out.Counts["VirtualService"]++
		hosts, _, _ := unstructured.NestedStringSlice(vs.Object, "spec", "hosts")
		gateways, _, _ := unstructured.NestedStringSlice(vs.Object, "spec", "gateways")
		vsID := "vs:" + vs.GetNamespace() + "/" + vs.GetName()
		det := strings.Join(hosts, ", ")
		mesh := len(gateways) == 0
		for _, g := range gateways {
			if g == "mesh" {
				mesh = true
			}
		}
		if mesh {
			det += " · mesh"
		}
		b.add(Node{ID: vsID, Kind: "VirtualService", Name: vs.GetName(), Namespace: vs.GetNamespace(), Layer: lVS, Health: "ok", Detail: det})
		for _, g := range gateways {
			if g == "mesh" {
				continue
			}
			id := gwID(vs.GetNamespace(), g)
			if _, ok := b.node[id]; !ok {
				gns, gname := vs.GetNamespace(), g
				if i := strings.Index(g, "/"); i > 0 {
					gns, gname = g[:i], g[i+1:]
				}
				b.add(Node{ID: id, Kind: "Gateway", Name: gname, Namespace: gns, Layer: lGateway, Health: "bad", Missing: true, Detail: "gateway not found"})
				out.Counts["Gateway"]++
			}
			b.edge(id, vsID, "routes", strings.Join(hosts, ","))
		}
		for _, proto := range []string{"http", "tcp", "tls"} {
			routes, _, _ := unstructured.NestedSlice(vs.Object, "spec", proto)
			for _, rr := range routes {
				rm, _ := rr.(map[string]any)
				match := matchLabel(rm)
				dests, _, _ := unstructured.NestedSlice(rm, "route")
				for _, dd := range dests {
					dm, _ := dd.(map[string]any)
					host, _, _ := unstructured.NestedString(dm, "destination", "host")
					subset, _, _ := unstructured.NestedString(dm, "destination", "subset")
					port, _, _ := unstructured.NestedFieldNoCopy(dm, "destination", "port", "number")
					weight, _, _ := unstructured.NestedFieldNoCopy(dm, "weight")
					parts := []string{}
					if match != "" {
						parts = append(parts, match)
					}
					if port != nil {
						parts = append(parts, fmt.Sprintf("port %v", port))
					}
					if subset != "" {
						parts = append(parts, "subset "+subset)
					}
					if weight != nil {
						parts = append(parts, fmt.Sprintf("%v%%", weight))
					}
					label := strings.Join(parts, " ")
					b.destination(vsID, vs.GetNamespace(), host, label, rules[hostKey(host, vs.GetNamespace())], seMap)
				}
			}
		}
	}
	// DestinationRules were added above; give each a link to its Service.
	for key, r := range rules {
		if _, ok := b.node[r.id]; !ok {
			continue
		}
		if svc, ok := b.svcs[key]; ok {
			b.edge(r.id, b.service(svc.Namespace, svc.Name, "", map[string]map[string]string{}), "applies", "")
		}
	}
	for _, n := range b.g.Nodes {
		if n.Kind == "Service" {
			out.Counts["Service"]++
		}
	}
	sort.SliceStable(b.g.Nodes, func(i, j int) bool {
		x, y := b.g.Nodes[i], b.g.Nodes[j]
		if x.Layer != y.Layer {
			return x.Layer < y.Layer
		}
		return x.Namespace+x.Name < y.Namespace+y.Name
	})
	return out, nil
}

func hostKey(host, from string) string {
	if ns, n, ok := resolveHost(host, from); ok {
		return ns + "/" + n
	}
	return host
}

func matchLabel(rm map[string]any) string {
	ms, _, _ := unstructured.NestedSlice(rm, "match")
	for _, x := range ms {
		m, _ := x.(map[string]any)
		for _, f := range []string{"prefix", "exact", "regex"} {
			if v, _, _ := unstructured.NestedString(m, "uri", f); v != "" {
				return v
			}
		}
	}
	return ""
}

// destination links a VirtualService to where it sends traffic: through the
// DestinationRule of that host when there is one, straight to the Service otherwise.
func (b *ib) destination(vsID, from, host, label string, r struct {
	id      string
	subsets map[string]map[string]string
}, se map[string]string) {
	ns, name, ok := resolveHost(host, from)
	if !ok {
		id := se[host]
		if id == "" {
			id = b.add(Node{ID: "ext:" + host, Kind: "ExternalHost", Name: host, Layer: lSvc, Health: "idle", External: true, Detail: "not a service in the cluster"})
		}
		b.edge(vsID, id, "routes", label)
		return
	}
	subsets := r.subsets
	svc := b.service(ns, name, label, subsets)
	if r.id != "" {
		if _, ok := b.node[r.id]; !ok {
			b.add(Node{ID: r.id, Kind: "DestinationRule", Name: strings.TrimPrefix(r.id, "dr:"), Layer: lDR, Health: "ok"})
		}
		b.edge(vsID, r.id, "routes", label)
		b.edge(r.id, svc, "applies", "")
		return
	}
	b.edge(vsID, svc, "routes", label)
}

// service adds a Service node with its ports and the ready pods behind it.
func (b *ib) service(ns, name, _ string, _ map[string]map[string]string) string {
	id := "svc:" + ns + "/" + name
	if _, ok := b.node[id]; ok {
		return id
	}
	svc, ok := b.svcs[ns+"/"+name]
	if !ok {
		return b.add(Node{ID: id, Kind: "Service", Name: name, Namespace: ns, Layer: lSvc, Health: "bad", Missing: true, Detail: "service not found"})
	}
	var ports []string
	for _, p := range svc.Spec.Ports {
		s := fmt.Sprintf("%d", p.Port)
		if p.TargetPort.String() != "" && p.TargetPort.String() != "0" && p.TargetPort.String() != s {
			s += "→" + p.TargetPort.String()
		}
		ports = append(ports, s)
	}
	n := Node{ID: id, Kind: "Service", Name: name, Namespace: ns, Layer: lSvc, Health: "ok", Detail: "ports " + strings.Join(ports, ", ")}
	b.add(n)
	if len(svc.Spec.Selector) == 0 {
		return id
	}
	pods, err := b.c.Cs.CoreV1().Pods(ns).List(b.ctx, metav1.ListOptions{LabelSelector: labels.Set(svc.Spec.Selector).String()})
	if err != nil {
		return id
	}
	ready := 0
	for _, p := range pods.Items {
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				ready++
			}
		}
	}
	pid := "pods:" + ns + "/" + name
	health, detail := "ok", fmt.Sprintf("%d/%d ready", ready, len(pods.Items))
	switch {
	case len(pods.Items) == 0:
		health, detail = "bad", "no pods match the selector"
	case ready == 0:
		health = "bad"
	case ready < len(pods.Items):
		health = "warn"
	}
	b.add(Node{ID: pid, Kind: "Pod", Name: "pods", Namespace: ns, Layer: lPods, Health: health, Detail: detail, Count: len(pods.Items)})
	b.edge(id, pid, "selects", "")
	if health != "ok" {
		b.g.Nodes[b.node[id]].Health = health
		b.g.Nodes[b.node[id]].Detail += " · " + detail
	}
	return id
}
