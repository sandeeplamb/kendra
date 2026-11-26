package kube

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VersionCount is how many nodes run one kubelet version.
type VersionCount struct {
	Version string `json:"version"`
	Count   int    `json:"count"`
}

// Fleet is the one-card summary of a cluster: version, nodes, pods, warnings.
// It uses list calls only.
type Fleet struct {
	Version       string         `json:"version"` // control plane, e.g. v1.31.3-eks-abc
	Minor         int            `json:"minor"`   // 31 (0 when unknown)
	Platform      string         `json:"platform"`
	Nodes         int            `json:"nodes"`
	NodesReady    int            `json:"nodesReady"`
	NodesPressure int            `json:"nodesPressure"` // memory, disk or PID pressure
	Cordoned      int            `json:"cordoned"`
	NodeVersions  []VersionCount `json:"nodeVersions"`
	NodeSkew      bool           `json:"nodeSkew"` // some node runs another minor than the control plane
	Pods          int            `json:"pods"`
	PodsBad       int            `json:"podsBad"`
	PodsWarn      int            `json:"podsWarn"`
	Namespaces    int            `json:"namespaces"`
	Warnings      int            `json:"warnings"` // Warning events in the last hour (500 at most)
	Health        string         `json:"health"`   // ok, warn or bad
	Notes         []string       `json:"notes,omitempty"`
}

var verRe = regexp.MustCompile(`^v?(\d+)\.(\d+)`)

// minorOf returns 31 for "v1.31.3-eks-x", or 0.
func minorOf(v string) int {
	m := verRe.FindStringSubmatch(v)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[2])
	return n
}

// platformOf guesses AKS, EKS or GKE from the version string and the nodes' provider IDs.
func platformOf(version string, nodes []corev1.Node) string {
	v := strings.ToLower(version)
	switch {
	case strings.Contains(v, "-eks-"):
		return "EKS"
	case strings.Contains(v, "-gke"):
		return "GKE"
	}
	for _, n := range nodes {
		id := n.Spec.ProviderID
		switch {
		case strings.HasPrefix(id, "azure://"):
			return "AKS"
		case strings.HasPrefix(id, "aws://"):
			return "EKS"
		case strings.HasPrefix(id, "gce://"):
			return "GKE"
		case strings.HasPrefix(id, "kind://"):
			return "kind"
		}
	}
	return ""
}

// FleetSummary collects the card for one cluster. Parts that cannot be read (no
// permission, slow API) are left out and named in Notes, so one gap never hides the rest.
func (c *Cluster) FleetSummary(ctx context.Context) (*Fleet, error) {
	sv, err := c.Cs.Discovery().ServerVersion()
	if err != nil {
		return nil, err
	}
	f := &Fleet{Version: sv.GitVersion, Minor: minorOf(sv.GitVersion), Health: "ok", NodeVersions: []VersionCount{}}

	nl, nerr := c.Cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if nerr != nil {
		f.Notes = append(f.Notes, "nodes: "+firstLineOf(nerr.Error()))
	} else {
		byVer := map[string]int{}
		for _, n := range nl.Items {
			f.Nodes++
			if n.Spec.Unschedulable {
				f.Cordoned++
			}
			ready := false
			for _, cd := range n.Status.Conditions {
				switch cd.Type {
				case corev1.NodeReady:
					ready = cd.Status == corev1.ConditionTrue
				case corev1.NodeMemoryPressure, corev1.NodeDiskPressure, corev1.NodePIDPressure:
					if cd.Status == corev1.ConditionTrue {
						f.NodesPressure++
					}
				}
			}
			if ready {
				f.NodesReady++
			}
			kv := n.Status.NodeInfo.KubeletVersion
			byVer[kv]++
			if m := minorOf(kv); m != 0 && f.Minor != 0 && m != f.Minor {
				f.NodeSkew = true
			}
		}
		for v, n := range byVer {
			f.NodeVersions = append(f.NodeVersions, VersionCount{Version: v, Count: n})
		}
		sort.Slice(f.NodeVersions, func(i, j int) bool { return f.NodeVersions[i].Version < f.NodeVersions[j].Version })
		f.Platform = platformOf(sv.GitVersion, nl.Items)
	}
	if f.Platform == "" {
		f.Platform = platformOf(sv.GitVersion, nil)
	}

	if pl, err := c.Cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{}); err != nil {
		f.Notes = append(f.Notes, "pods: "+firstLineOf(err.Error()))
	} else {
		for i := range pl.Items {
			p := &pl.Items[i]
			if p.Status.Phase == corev1.PodSucceeded {
				continue
			}
			f.Pods++
			switch podTroubles(p) {
			case "bad":
				f.PodsBad++
			case "warn":
				f.PodsWarn++
			}
		}
	}
	if raw, err := c.Cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{}); err == nil {
		f.Namespaces = len(raw.Items)
	}
	if el, err := c.Cs.CoreV1().Events("").List(ctx, metav1.ListOptions{FieldSelector: "type=Warning", Limit: 500}); err != nil {
		f.Notes = append(f.Notes, "events: "+firstLineOf(err.Error()))
	} else {
		cut := time.Now().Add(-time.Hour)
		for _, e := range el.Items {
			t := e.LastTimestamp.Time
			if t.IsZero() {
				t = e.EventTime.Time
			}
			if t.IsZero() {
				t = e.CreationTimestamp.Time
			}
			if t.After(cut) {
				f.Warnings++
			}
		}
	}

	switch {
	case f.Nodes > 0 && f.NodesReady < f.Nodes, f.PodsBad > 0:
		f.Health = "bad"
	case f.PodsWarn > 0, f.NodesPressure > 0, f.NodeSkew, f.Warnings > 0, len(f.Notes) > 0:
		f.Health = "warn"
	}
	return f, nil
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}
