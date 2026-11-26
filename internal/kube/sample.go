package kube

import (
	"context"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Problem is one thing that was wrong at the moment of a Sample.
type Problem struct {
	Kind      string `json:"kind"` // Pod, Node, Deployment...
	Resource  string `json:"resource,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Level     string `json:"level"` // bad | warn
	Reason    string `json:"reason"`
}

// Sample is a small picture of a cluster at one moment. A series of them gives the
// sparklines and the time machine; the app keeps them in memory only.
type Sample struct {
	T          time.Time      `json:"t"`
	Pods       int            `json:"pods"`
	PodsBad    int            `json:"podsBad"`
	PodsWarn   int            `json:"podsWarn"`
	Nodes      int            `json:"nodes"`
	NodesReady int            `json:"nodesReady"`
	Warnings   int            `json:"warnings"` // Warning events in the last 10 minutes
	CPUUsed    int64          `json:"cpuUsed"`
	CPUReq     int64          `json:"cpuReq"`
	MemUsed    int64          `json:"memUsed"`
	MemReq     int64          `json:"memReq"`
	NsBad      map[string]int `json:"nsBad,omitempty"` // failing pods per namespace
	Problems   []Problem      `json:"problems,omitempty"`
}

const maxProblems = 300

// podReason gives the short reason a pod is unhealthy.
func podReason(p *corev1.Pod) string {
	if p.Status.Phase == corev1.PodFailed {
		if p.Status.Reason != "" {
			return p.Status.Reason
		}
		return "Failed"
	}
	for _, group := range [][]corev1.ContainerStatus{p.Status.InitContainerStatuses, p.Status.ContainerStatuses} {
		for _, s := range group {
			if w := s.State.Waiting; w != nil && w.Reason != "" && w.Reason != "PodInitializing" && w.Reason != "ContainerCreating" {
				return w.Reason
			}
			if t := s.LastTerminationState.Terminated; t != nil && t.Reason == "OOMKilled" && s.State.Running != nil {
				return "OOMKilled"
			}
		}
	}
	if p.Status.Phase == corev1.PodPending {
		for _, cd := range p.Status.Conditions {
			if cd.Type == corev1.PodScheduled && cd.Status == corev1.ConditionFalse {
				return "Unschedulable"
			}
		}
		return "Pending"
	}
	return string(p.Status.Phase)
}

// TakeSample reads the cluster once: pods, nodes, deployments, warning events and live
// usage, and keeps what was wrong. List calls only.
func (c *Cluster) TakeSample(ctx context.Context) (*Sample, error) {
	s := &Sample{T: time.Now().UTC(), NsBad: map[string]int{}}
	pl, err := c.Cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var problems []Problem
	for i := range pl.Items {
		p := &pl.Items[i]
		if p.Status.Phase == corev1.PodSucceeded {
			continue
		}
		s.Pods++
		for _, ct := range p.Spec.Containers {
			s.CPUReq += ct.Resources.Requests.Cpu().MilliValue()
			s.MemReq += ct.Resources.Requests.Memory().Value()
		}
		switch podTroubles(p) {
		case "bad":
			s.PodsBad++
			s.NsBad[p.Namespace]++
			problems = append(problems, Problem{"Pod", "pods", p.Namespace, p.Name, "bad", podReason(p)})
		case "warn":
			s.PodsWarn++
			problems = append(problems, Problem{"Pod", "pods", p.Namespace, p.Name, "warn", podReason(p)})
		}
	}
	if nl, err := c.Cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
		for _, n := range nl.Items {
			s.Nodes++
			ready := false
			for _, cd := range n.Status.Conditions {
				switch cd.Type {
				case corev1.NodeReady:
					ready = cd.Status == corev1.ConditionTrue
				case corev1.NodeMemoryPressure, corev1.NodeDiskPressure, corev1.NodePIDPressure:
					if cd.Status == corev1.ConditionTrue {
						problems = append(problems, Problem{"Node", "nodes", "", n.Name, "warn", string(cd.Type)})
					}
				}
			}
			if ready {
				s.NodesReady++
			} else {
				problems = append(problems, Problem{"Node", "nodes", "", n.Name, "bad", "NotReady"})
			}
		}
	}
	if dl, err := c.Cs.AppsV1().Deployments("").List(ctx, metav1.ListOptions{}); err == nil {
		for _, d := range dl.Items {
			want := int32(1)
			if d.Spec.Replicas != nil {
				want = *d.Spec.Replicas
			}
			if want > 0 && d.Status.ReadyReplicas < want {
				lvl := "warn"
				if d.Status.ReadyReplicas == 0 {
					lvl = "bad"
				}
				problems = append(problems, Problem{"Deployment", "deployments", d.Namespace, d.Name, lvl, fmt.Sprintf("%d of %d ready", d.Status.ReadyReplicas, want)})
			}
		}
	}
	if el, err := c.Cs.CoreV1().Events("").List(ctx, metav1.ListOptions{FieldSelector: "type=Warning", Limit: 500}); err == nil {
		cut := time.Now().Add(-10 * time.Minute)
		for _, e := range el.Items {
			t := e.LastTimestamp.Time
			if t.IsZero() {
				t = e.EventTime.Time
			}
			if t.After(cut) {
				s.Warnings++
			}
		}
	}
	if use, ok := c.podMetrics(ctx); ok {
		for _, u := range use {
			s.CPUUsed += u[0]
			s.MemUsed += u[1]
		}
	}
	rank := map[string]int{"bad": 0, "warn": 1}
	sort.SliceStable(problems, func(i, j int) bool { return rank[problems[i].Level] < rank[problems[j].Level] })
	if len(problems) > maxProblems {
		problems = problems[:maxProblems]
	}
	s.Problems = problems
	return s, nil
}
