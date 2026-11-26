package kube

import (
	"context"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// res is a handle for reading one kind in one namespace (reads only).
func (c *Cluster) res(k Kind, ns string) dynamic.ResourceInterface {
	if k.Namespaced {
		return c.Dyn.Resource(k.GVR()).Namespace(ns)
	}
	return c.Dyn.Resource(k.GVR())
}

// Node is one box in an app topology.
type Node struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Layer     int    `json:"layer"`
	Health    string `json:"health"`
	Detail    string `json:"detail,omitempty"`
	External  bool   `json:"external,omitempty"` // exists, but is not part of this app
	Missing   bool   `json:"missing,omitempty"`  // referenced, but does not exist
	Count     int    `json:"count,omitempty"`    // for grouped pods
}

// Edge links two nodes. Type is owns, selects, routes, scales or uses.
type Edge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Type  string `json:"type"`
	Label string `json:"label,omitempty"`
}

// Graph is the topology of one kapp app.
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
	Other []Res  `json:"other"` // owned objects that are not drawn (RBAC, CRDs, namespaces...)
}

var layerOf = map[string]int{"Ingress": 0, "Service": 1, "HorizontalPodAutoscaler": 1, "Deployment": 2, "StatefulSet": 2,
	"DaemonSet": 2, "CronJob": 2, "Job": 2, "ReplicaSet": 3, "Pod": 4, "ConfigMap": 5, "Secret": 5,
	"PersistentVolumeClaim": 5, "ServiceAccount": 5}

var workloadKinds = map[string]bool{"Deployment": true, "StatefulSet": true, "DaemonSet": true, "CronJob": true}

type builder struct {
	ctx   context.Context
	c     *Cluster
	g     *Graph
	byUID map[string]*Res
	node  map[string]int // id -> index in g.Nodes
	key   map[string]string
	exts  int
}

func key(kind, ns, name string) string { return kind + "|" + ns + "|" + name }

// AppGraph builds the topology of a kapp app from the objects it owns, following
// ownerReferences down to pods, Service selectors, Ingress backends, HPA targets and
// the ConfigMaps, Secrets and claims that workloads mount or read.
func (c *Cluster) AppGraph(ctx context.Context, res []Res) *Graph {
	b := &builder{ctx: ctx, c: c, g: &Graph{Nodes: []Node{}, Edges: []Edge{}, Other: []Res{}}, byUID: map[string]*Res{},
		node: map[string]int{}, key: map[string]string{}}
	all := append([]Res(nil), res...)
	// Pods, ReplicaSets and Jobs created by the app's workloads do not carry the app label.
	all = append(all, b.children(res)...)
	for i := range all {
		r := &all[i]
		b.byUID[r.UID] = r
		if _, ok := layerOf[r.Kind]; !ok {
			b.g.Other = append(b.g.Other, Res{Kind: r.Kind, Namespace: r.Namespace, Name: r.Name, Health: r.Health, Detail: r.Detail, Group: r.Group})
			continue
		}
		b.add(Node{ID: r.UID, Kind: r.Kind, Name: r.Name, Namespace: r.Namespace, Layer: layerOf[r.Kind], Health: r.Health, Detail: r.Detail})
		b.key[key(r.Kind, r.Namespace, r.Name)] = r.UID
	}
	sort.Slice(b.g.Other, func(i, j int) bool {
		if b.g.Other[i].Kind != b.g.Other[j].Kind {
			return b.g.Other[i].Kind < b.g.Other[j].Kind
		}
		return b.g.Other[i].Name < b.g.Other[j].Name
	})
	for i := range all {
		r := &all[i]
		if _, ok := b.node[r.UID]; !ok {
			continue
		}
		for _, o := range r.Owners {
			if _, ok := b.node[o.UID]; ok {
				b.edge(o.UID, r.UID, "owns")
			}
		}
		b.links(r)
	}
	b.groupPods()
	sort.SliceStable(b.g.Nodes, func(i, j int) bool {
		x, y := b.g.Nodes[i], b.g.Nodes[j]
		if x.Layer != y.Layer {
			return x.Layer < y.Layer
		}
		if x.Namespace != y.Namespace {
			return x.Namespace < y.Namespace
		}
		return x.Kind+x.Name < y.Kind+y.Name
	})
	return b.g
}

func (b *builder) add(n Node) {
	if _, ok := b.node[n.ID]; ok {
		return
	}
	b.node[n.ID] = len(b.g.Nodes)
	b.g.Nodes = append(b.g.Nodes, n)
}

func (b *builder) edge(from, to, typ string) {
	for _, e := range b.g.Edges {
		if e.From == from && e.To == to && e.Type == typ {
			return
		}
	}
	b.g.Edges = append(b.g.Edges, Edge{From: from, To: to, Type: typ})
}

// children lists ReplicaSets, Jobs and Pods in the app's namespaces that descend from
// the app's objects.
func (b *builder) children(res []Res) []Res {
	known := map[string]bool{}
	nss := map[string]bool{}
	for _, r := range res {
		known[r.UID] = true
		if workloadKinds[r.Kind] || r.Kind == "Job" || r.Kind == "Pod" {
			nss[r.Namespace] = true
		}
	}
	var out []Res
	for ns := range nss {
		for _, g := range []struct{ group, ver, res, kind string }{
			{"apps", "v1", "replicasets", "ReplicaSet"}, {"batch", "v1", "jobs", "Job"}, {"", "v1", "pods", "Pod"}} {
			gvr := schema.GroupVersionResource{Group: g.group, Version: g.ver, Resource: g.res}
			l, err := b.c.Dyn.Resource(gvr).Namespace(ns).List(b.ctx, metav1.ListOptions{})
			if err != nil {
				continue
			}
			for i := range l.Items {
				u := &l.Items[i]
				if known[string(u.GetUID())] {
					continue
				}
				var owners []Owner
				for _, o := range u.GetOwnerReferences() {
					owners = append(owners, Owner{o.Kind, o.Name, string(o.UID)})
				}
				// kept only if an ancestor is already known; pods come last so chains resolve
				for _, o := range owners {
					if known[o.UID] {
						u.SetKind(g.kind)
						h, d := UnstructuredHealth(u)
						r := Res{Group: g.group, Version: g.ver, Resource: g.res, Kind: g.kind, Namespace: ns, Name: u.GetName(),
							UID: string(u.GetUID()), Health: h, Detail: d, Owners: owners, Labels: u.GetLabels(), Obj: u}
						out = append(out, r)
						known[r.UID] = true
						break
					}
				}
			}
		}
	}
	return out
}

func podSpecOf(r *Res) map[string]any {
	if r.Obj == nil {
		return nil
	}
	var path []string
	switch r.Kind {
	case "Deployment", "StatefulSet", "DaemonSet", "Job":
		path = []string{"spec", "template", "spec"}
	case "CronJob":
		path = []string{"spec", "jobTemplate", "spec", "template", "spec"}
	case "Pod":
		if len(r.Owners) > 0 {
			return nil
		}
		path = []string{"spec"}
	default:
		return nil
	}
	m, _, _ := unstructured.NestedMap(r.Obj.Object, path...)
	return m
}

func (b *builder) links(r *Res) {
	u := r.Obj
	if u == nil {
		return
	}
	switch r.Kind {
	case "Ingress":
		names := map[string]bool{}
		rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
		for _, x := range rules {
			paths, _, _ := unstructured.NestedSlice(x.(map[string]any), "http", "paths")
			for _, p := range paths {
				if n, _, _ := unstructured.NestedString(p.(map[string]any), "backend", "service", "name"); n != "" {
					names[n] = true
				}
			}
		}
		if n, _, _ := unstructured.NestedString(u.Object, "spec", "defaultBackend", "service", "name"); n != "" {
			names[n] = true
		}
		for n := range names {
			b.edge(r.UID, b.ref("Service", r.Namespace, n), "routes")
		}
	case "HorizontalPodAutoscaler":
		k, _, _ := unstructured.NestedString(u.Object, "spec", "scaleTargetRef", "kind")
		n, _, _ := unstructured.NestedString(u.Object, "spec", "scaleTargetRef", "name")
		if id, ok := b.key[key(k, r.Namespace, n)]; ok {
			b.edge(r.UID, id, "scales")
		}
	case "Service":
		sel, _, _ := unstructured.NestedStringMap(u.Object, "spec", "selector")
		if len(sel) == 0 {
			return
		}
		for _, o := range b.byUID {
			if o.Namespace != r.Namespace || !(workloadKinds[o.Kind] || o.Kind == "Job" || (o.Kind == "Pod" && len(o.Owners) == 0)) {
				continue
			}
			lbl := templateLabels(o)
			match := true
			for k, v := range sel {
				if lbl[k] != v {
					match = false
					break
				}
			}
			if match && o.Kind != "CronJob" {
				b.edge(r.UID, o.UID, "selects")
			}
		}
	}
	if spec := podSpecOf(r); spec != nil {
		for _, ref := range podRefs(spec) {
			if ref.kind == "ServiceAccount" {
				if id, ok := b.key[key("ServiceAccount", r.Namespace, ref.name)]; ok {
					b.edge(r.UID, id, "uses")
				}
				continue
			}
			b.edge(r.UID, b.ref(ref.kind, r.Namespace, ref.name), "uses")
		}
	}
}

func templateLabels(r *Res) map[string]string {
	if r.Obj == nil {
		return nil
	}
	if r.Kind == "Pod" {
		return r.Obj.GetLabels()
	}
	if r.Kind == "CronJob" {
		return nil
	}
	m, _, _ := unstructured.NestedStringMap(r.Obj.Object, "spec", "template", "metadata", "labels")
	return m
}

type podRef struct{ kind, name string }

func podRefs(spec map[string]any) []podRef {
	var out []podRef
	add := func(kind, name string) {
		if name != "" {
			out = append(out, podRef{kind, name})
		}
	}
	str := func(m map[string]any, path ...string) string {
		v, _, _ := unstructured.NestedString(m, path...)
		return v
	}
	opt := func(m map[string]any, path ...string) bool { v, _, _ := unstructured.NestedBool(m, path...); return v }
	vols, _, _ := unstructured.NestedSlice(spec, "volumes")
	for _, x := range vols {
		v, _ := x.(map[string]any)
		if !opt(v, "configMap", "optional") {
			add("ConfigMap", str(v, "configMap", "name"))
		}
		if !opt(v, "secret", "optional") {
			add("Secret", str(v, "secret", "secretName"))
		}
		add("PersistentVolumeClaim", str(v, "persistentVolumeClaim", "claimName"))
		srcs, _, _ := unstructured.NestedSlice(v, "projected", "sources")
		for _, s := range srcs {
			sm, _ := s.(map[string]any)
			if !opt(sm, "configMap", "optional") {
				add("ConfigMap", str(sm, "configMap", "name"))
			}
			if !opt(sm, "secret", "optional") {
				add("Secret", str(sm, "secret", "name"))
			}
		}
	}
	for _, key := range []string{"containers", "initContainers"} {
		cs, _, _ := unstructured.NestedSlice(spec, key)
		for _, x := range cs {
			c, _ := x.(map[string]any)
			envFrom, _, _ := unstructured.NestedSlice(c, "envFrom")
			for _, e := range envFrom {
				em, _ := e.(map[string]any)
				if !opt(em, "configMapRef", "optional") {
					add("ConfigMap", str(em, "configMapRef", "name"))
				}
				if !opt(em, "secretRef", "optional") {
					add("Secret", str(em, "secretRef", "name"))
				}
			}
			env, _, _ := unstructured.NestedSlice(c, "env")
			for _, e := range env {
				em, _ := e.(map[string]any)
				if !opt(em, "valueFrom", "configMapKeyRef", "optional") {
					add("ConfigMap", str(em, "valueFrom", "configMapKeyRef", "name"))
				}
				if !opt(em, "valueFrom", "secretKeyRef", "optional") {
					add("Secret", str(em, "valueFrom", "secretKeyRef", "name"))
				}
			}
		}
	}
	add("ServiceAccount", str(spec, "serviceAccountName"))
	return out
}

// ref returns the node id for kind/ns/name, adding an "external" node when the object
// exists outside the app and a "missing" node (red) when it does not exist at all.
func (b *builder) ref(kind, ns, name string) string {
	if id, ok := b.key[key(kind, ns, name)]; ok {
		return id
	}
	id := "ref:" + key(kind, ns, name)
	b.key[key(kind, ns, name)] = id
	if b.exts >= 80 {
		b.add(Node{ID: id, Kind: kind, Name: name, Namespace: ns, Layer: layerOf[kind], Health: "idle", External: true})
		return id
	}
	b.exts++
	n := Node{ID: id, Kind: kind, Name: name, Namespace: ns, Layer: layerOf[kind], Health: "idle", External: true}
	k, ok := FindKind(strings.ToLower(kind) + "s")
	if kind == "Ingress" {
		k, ok = FindKind("ingresses")
	}
	if kind == "PersistentVolumeClaim" {
		k, ok = FindKind("persistentvolumeclaims")
	}
	if ok {
		if _, err := b.c.res(k, ns).Get(b.ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
			n.External, n.Missing, n.Health, n.Detail = false, true, "bad", "not found"
		}
	}
	b.add(n)
	return id
}

// groupPods folds more than five pods of one parent into a single node.
func (b *builder) groupPods() {
	parent := map[string][]int{}
	for i, n := range b.g.Nodes {
		if n.Kind != "Pod" {
			continue
		}
		for _, e := range b.g.Edges {
			if e.To == n.ID && e.Type == "owns" {
				parent[e.From] = append(parent[e.From], i)
			}
		}
	}
	drop := map[string]string{}
	var added []Node
	for p, idx := range parent {
		if len(idx) <= 5 {
			continue
		}
		agg := Node{ID: "agg:" + p, Kind: "Pod", Layer: 4, Count: len(idx), Health: "ok"}
		rank := map[string]int{"ok": 0, "idle": 0, "warn": 1, "bad": 2}
		bad, warn := 0, 0
		for _, i := range idx {
			n := b.g.Nodes[i]
			agg.Namespace = n.Namespace
			drop[n.ID] = agg.ID
			if rank[n.Health] > rank[agg.Health] {
				agg.Health = n.Health
			}
			switch n.Health {
			case "bad":
				bad++
			case "warn":
				warn++
			}
		}
		agg.Name = "pods"
		agg.Detail = strings.TrimSpace(strings.Join([]string{pl(bad, "failing"), pl(warn, "not ready")}, " "))
		added = append(added, agg)
	}
	if len(drop) == 0 {
		return
	}
	var nodes []Node
	for _, n := range b.g.Nodes {
		if _, d := drop[n.ID]; !d {
			nodes = append(nodes, n)
		}
	}
	b.g.Nodes = append(nodes, added...)
	var edges []Edge
	seen := map[Edge]bool{}
	for _, e := range b.g.Edges {
		if to, ok := drop[e.To]; ok {
			e.To = to
		}
		if from, ok := drop[e.From]; ok {
			e.From = from
		}
		if !seen[e] {
			seen[e] = true
			edges = append(edges, e)
		}
	}
	b.g.Edges = edges
}

func pl(n int, w string) string {
	if n == 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join([]string{itoa(n), w}, " "))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}
