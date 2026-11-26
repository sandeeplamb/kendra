package kube

import (
	"context"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SpreadNode is one node: where it is, and how full.
type SpreadNode struct {
	Name     string `json:"name"`
	Zone     string `json:"zone"`
	Pool     string `json:"pool,omitempty"`
	Ready    bool   `json:"ready"`
	Cordoned bool   `json:"cordoned"`
	Pods     int    `json:"pods"`
	MaxPods  int    `json:"maxPods"`
	CPUReq   int64  `json:"cpuReq"`
	CPUAlloc int64  `json:"cpuAlloc"`
	MemReq   int64  `json:"memReq"`
	MemAlloc int64  `json:"memAlloc"`
}

// SpreadZone sums nodes and pods per availability zone.
type SpreadZone struct {
	Name       string `json:"name"`
	Nodes      int    `json:"nodes"`
	NodesReady int    `json:"nodesReady"`
	Pods       int    `json:"pods"`
}

// SpreadWorkload shows where the pods of one workload run.
type SpreadWorkload struct {
	Namespace string         `json:"namespace"`
	Workload  string         `json:"workload"`
	Pods      int            `json:"pods"`
	ByZone    map[string]int `json:"byZone"`
	ByNode    map[string]int `json:"byNode"`
	Level     string         `json:"level"` // ok | warn | bad
	Verdict   string         `json:"verdict"`
}

// SpreadReport is the answer for one cluster.
type SpreadReport struct {
	Zones     []SpreadZone     `json:"zones"`
	Nodes     []SpreadNode     `json:"nodes"`
	Workloads []SpreadWorkload `json:"workloads"`
}

const noZone = "(no zone label)"

func zoneOf(n *corev1.Node) string {
	for _, k := range []string{"topology.kubernetes.io/zone", "failure-domain.beta.kubernetes.io/zone"} {
		if z := n.Labels[k]; z != "" {
			return z
		}
	}
	return noZone
}

// judge says whether the pods of a workload are spread well: not all on one node, and
// (when the cluster has several zones) not all in one zone or lopsided between zones.
func judge(pods int, byZone, byNode map[string]int, zonesInCluster []string) (level, verdict string) {
	switch {
	case pods < 2:
		return "ok", "single pod"
	case len(byNode) == 1:
		return "bad", "all pods on one node"
	}
	if len(zonesInCluster) > 1 && len(byZone) == 1 {
		return "warn", "all pods in one zone"
	}
	if len(zonesInCluster) > 1 {
		lo, hi := pods, 0
		for _, z := range zonesInCluster {
			n := byZone[z]
			lo, hi = min(lo, n), max(hi, n)
		}
		if hi-lo > 1 && lo == 0 {
			return "warn", "not in every zone"
		}
		if hi-lo > max(1, pods/2) {
			return "warn", "lopsided between zones"
		}
	}
	return "ok", "spread"
}

// Spread shows how nodes sit in availability zones and how pods are spread over
// nodes and zones, per workload. It reads node labels, node status and pod specs.
func (c *Cluster) Spread(ctx context.Context) (*SpreadReport, error) {
	nl, err := c.Cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	pl, err := c.Cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	rep := &SpreadReport{Zones: []SpreadZone{}, Nodes: []SpreadNode{}, Workloads: []SpreadWorkload{}}
	nodeAt := map[string]int{}
	zoneAt := map[string]int{}
	nodeZone := map[string]string{}
	for i := range nl.Items {
		n := &nl.Items[i]
		sn := SpreadNode{Name: n.Name, Zone: zoneOf(n), Pool: poolOf(n), Cordoned: n.Spec.Unschedulable,
			MaxPods: int(n.Status.Allocatable.Pods().Value()), CPUAlloc: n.Status.Allocatable.Cpu().MilliValue(), MemAlloc: n.Status.Allocatable.Memory().Value()}
		for _, cd := range n.Status.Conditions {
			if cd.Type == corev1.NodeReady {
				sn.Ready = cd.Status == corev1.ConditionTrue
			}
		}
		nodeAt[n.Name] = len(rep.Nodes)
		nodeZone[n.Name] = sn.Zone
		rep.Nodes = append(rep.Nodes, sn)
		j, ok := zoneAt[sn.Zone]
		if !ok {
			j = len(rep.Zones)
			zoneAt[sn.Zone] = j
			rep.Zones = append(rep.Zones, SpreadZone{Name: sn.Zone})
		}
		rep.Zones[j].Nodes++
		if sn.Ready {
			rep.Zones[j].NodesReady++
		}
	}
	var zoneNames []string
	for _, z := range rep.Zones {
		zoneNames = append(zoneNames, z.Name)
	}
	sort.Strings(zoneNames)

	wl := map[string]*SpreadWorkload{}
	var order []string
	for i := range pl.Items {
		p := &pl.Items[i]
		if p.Spec.NodeName == "" || p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		if j, ok := nodeAt[p.Spec.NodeName]; ok {
			n := &rep.Nodes[j]
			n.Pods++
			for _, ct := range p.Spec.Containers {
				n.CPUReq += ct.Resources.Requests.Cpu().MilliValue()
				n.MemReq += ct.Resources.Requests.Memory().Value()
			}
			rep.Zones[zoneAt[n.Zone]].Pods++
		}
		w := workloadOf(p)
		if len(w) > 10 && w[:10] == "DaemonSet/" || len(w) > 4 && w[:4] == "Pod/" {
			continue // one per node by design, or a bare pod
		}
		key := p.Namespace + "|" + w
		x := wl[key]
		if x == nil {
			x = &SpreadWorkload{Namespace: p.Namespace, Workload: w, ByZone: map[string]int{}, ByNode: map[string]int{}}
			wl[key] = x
			order = append(order, key)
		}
		x.Pods++
		x.ByNode[p.Spec.NodeName]++
		z := nodeZone[p.Spec.NodeName]
		if z == "" {
			z = noZone
		}
		x.ByZone[z]++
	}
	sort.Strings(order)
	for _, k := range order {
		x := wl[k]
		x.Level, x.Verdict = judge(x.Pods, x.ByZone, x.ByNode, zoneNames)
		rep.Workloads = append(rep.Workloads, *x)
	}
	rank := map[string]int{"bad": 0, "warn": 1, "ok": 2}
	sort.SliceStable(rep.Workloads, func(i, j int) bool {
		a, b := rep.Workloads[i], rep.Workloads[j]
		if rank[a.Level] != rank[b.Level] {
			return rank[a.Level] < rank[b.Level]
		}
		return a.Pods > b.Pods
	})
	sort.Slice(rep.Zones, func(i, j int) bool { return rep.Zones[i].Name < rep.Zones[j].Name })
	sort.Slice(rep.Nodes, func(i, j int) bool {
		if rep.Nodes[i].Zone != rep.Nodes[j].Zone {
			return rep.Nodes[i].Zone < rep.Nodes[j].Zone
		}
		return rep.Nodes[i].Name < rep.Nodes[j].Name
	})
	return rep, nil
}
