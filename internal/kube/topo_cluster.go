package kube

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// poolLabels are the labels the common platforms put on nodes of one pool.
var poolLabels = []string{"karpenter.sh/nodepool", "eks.amazonaws.com/nodegroup", "kubernetes.azure.com/agentpool",
	"agentpool", "cloud.google.com/gke-nodepool", "node.kubernetes.io/instance-type"}

func poolOf(n *corev1.Node) string {
	for _, l := range poolLabels {
		if v := n.Labels[l]; v != "" {
			return v
		}
	}
	return "(no pool label)"
}

func podTroubles(p *corev1.Pod) string {
	if p.Status.Phase == corev1.PodFailed {
		return "bad"
	}
	worst := "ok"
	if p.Status.Phase == corev1.PodPending {
		worst = "warn"
	}
	for _, group := range [][]corev1.ContainerStatus{p.Status.InitContainerStatuses, p.Status.ContainerStatuses} {
		for _, cs := range group {
			if w := cs.State.Waiting; w != nil {
				h, _ := podHealth(w.Reason, "1/1")
				if h == "bad" {
					return "bad"
				}
			}
		}
	}
	return worst
}

var rankOf = map[string]int{"ok": 0, "idle": 0, "warn": 1, "bad": 2}

func worse(a, b string) string {
	if rankOf[b] > rankOf[a] {
		return b
	}
	return a
}

// ClusterTopology draws node pools -> nodes -> the namespaces whose pods run on them.
// DaemonSet pods count towards a node's pod total but get no edge, or every node would
// connect to every system namespace.
func (c *Cluster) ClusterTopology(ctx context.Context) (*Graph, error) {
	nl, err := c.Cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	pl, err := c.Cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	g := &Graph{Nodes: []Node{}, Edges: []Edge{}, Other: []Res{}}
	type nsInfo struct {
		pods   int
		health string
	}
	nsStat := map[string]*nsInfo{}
	podsOn := map[string]int{}
	edgeCount := map[[2]string]int{}
	for i := range pl.Items {
		p := &pl.Items[i]
		if p.Status.Phase == corev1.PodSucceeded {
			continue
		}
		node := p.Spec.NodeName
		if node == "" {
			node = "(unscheduled)"
		}
		podsOn[node]++
		st := nsStat[p.Namespace]
		if st == nil {
			st = &nsInfo{health: "ok"}
			nsStat[p.Namespace] = st
		}
		st.pods++
		st.health = worse(st.health, podTroubles(p))
		daemon := false
		for _, o := range p.OwnerReferences {
			if o.Kind == "DaemonSet" {
				daemon = true
			}
		}
		if !daemon {
			edgeCount[[2]string{node, p.Namespace}]++
		}
	}
	pools := map[string]*Node{}
	var poolNames []string
	for i := range nl.Items {
		n := &nl.Items[i]
		pool := poolOf(n)
		health, note := "bad", "not ready"
		for _, cd := range n.Status.Conditions {
			if cd.Type == corev1.NodeReady && cd.Status == corev1.ConditionTrue {
				health, note = "ok", ""
			}
		}
		if health == "ok" && n.Spec.Unschedulable {
			health, note = "warn", "cordoned"
		}
		cpu, mem := n.Status.Allocatable.Cpu(), n.Status.Allocatable.Memory()
		detail := fmt.Sprintf("%s · %s cpu · %dGi", plural(podsOn[n.Name], "pod"), cpu.String(), mem.Value()>>30)
		if note != "" {
			detail = note + " · " + detail
		}
		pid := "pool:" + pool
		if pools[pool] == nil {
			pools[pool] = &Node{ID: pid, Kind: "Node pool", Name: pool, Layer: 0, Health: "ok"}
			poolNames = append(poolNames, pool)
		}
		pools[pool].Count++
		pools[pool].Health = worse(pools[pool].Health, health)
		g.Nodes = append(g.Nodes, Node{ID: "node:" + n.Name, Kind: "Node", Name: n.Name, Layer: 1, Health: health, Detail: detail})
		g.Edges = append(g.Edges, Edge{From: pid, To: "node:" + n.Name, Type: "owns"})
	}
	sort.Strings(poolNames)
	for _, name := range poolNames {
		p := pools[name]
		p.Detail = fmt.Sprintf("%d nodes", p.Count)
		p.Count = 0
		g.Nodes = append(g.Nodes, *p)
	}
	if podsOn["(unscheduled)"] > 0 {
		g.Nodes = append(g.Nodes, Node{ID: "node:(unscheduled)", Kind: "Node", Name: "unscheduled", Layer: 1, Health: "bad",
			Detail: fmt.Sprintf("%d pods have no node", podsOn["(unscheduled)"]), Missing: true})
	}
	var names []string
	for ns := range nsStat {
		names = append(names, ns)
	}
	sort.Strings(names)
	for _, ns := range names {
		st := nsStat[ns]
		g.Nodes = append(g.Nodes, Node{ID: "ns:" + ns, Kind: "Namespace", Name: ns, Layer: 2, Health: st.health, Detail: plural(st.pods, "pod")})
	}
	keys := make([][2]string, 0, len(edgeCount))
	for k := range edgeCount {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return strings.Join(keys[i][:], "/") < strings.Join(keys[j][:], "/") })
	for _, k := range keys {
		g.Edges = append(g.Edges, Edge{From: "node:" + k[0], To: "ns:" + k[1], Type: "routes", Label: plural(edgeCount[k], "pod")})
	}
	return g, nil
}

// NamespaceTopology draws what runs in one namespace and how it is wired, whatever
// tool created it: Ingress -> Service -> workload -> ReplicaSet/Job -> Pods, plus the
// ConfigMaps, Secrets and claims the workloads use. References to objects that do not
// exist show up as red "missing" nodes.
func (c *Cluster) NamespaceTopology(ctx context.Context, ns string) (*Graph, error) {
	specs := []struct{ group, ver, res, kind string }{
		{"networking.k8s.io", "v1", "ingresses", "Ingress"}, {"", "v1", "services", "Service"},
		{"autoscaling", "v2", "horizontalpodautoscalers", "HorizontalPodAutoscaler"},
		{"apps", "v1", "deployments", "Deployment"}, {"apps", "v1", "statefulsets", "StatefulSet"},
		{"apps", "v1", "daemonsets", "DaemonSet"}, {"batch", "v1", "cronjobs", "CronJob"},
		{"batch", "v1", "jobs", "Job"}, {"", "v1", "pods", "Pod"},
	}
	var res []Res
	okKinds := 0
	var lastErr error
	for _, sp := range specs {
		ar := apiRes{gvr: kindGVR(sp.group, sp.ver, sp.res), kind: sp.kind, namespaced: true}
		l, err := c.Dyn.Resource(ar.gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			lastErr = err
			continue
		}
		okKinds++
		for i := range l.Items {
			u := &l.Items[i]
			if (sp.kind == "Job" || sp.kind == "Pod") && len(u.GetOwnerReferences()) > 0 {
				continue // reached through its owner
			}
			res = append(res, newRes(ar, u))
		}
	}
	if okKinds == 0 && lastErr != nil {
		return nil, lastErr
	}
	return c.AppGraph(ctx, res), nil
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
