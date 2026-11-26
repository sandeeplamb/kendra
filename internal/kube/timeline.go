package kube

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TimelineItem is one thing that happened in a cluster.
type TimelineItem struct {
	Time      string `json:"time"`  // RFC 3339
	Type      string `json:"type"`  // event | rollout | restart | node
	Level     string `json:"level"` // bad | warn | info
	Namespace string `json:"namespace,omitempty"`
	Object    string `json:"object"` // Deployment/web
	Title     string `json:"title"`
	Detail    string `json:"detail,omitempty"`
	Count     int    `json:"count,omitempty"`
}

// normalWorthKeeping are the Normal events that mark a change; the rest (Pulled,
// Created, Started...) is noise on a timeline.
var normalWorthKeeping = map[string]bool{"ScalingReplicaSet": true, "SuccessfulRescale": true, "Killing": true, "NodeNotReady": true,
	"NodeReady": true, "TriggeredScaleUp": true, "ScaleDown": true, "Preempted": true, "SawCompletedJob": true, "Completed": true, "Evicted": true}

func at(t metav1.Time) string { return t.UTC().Format(time.RFC3339) }

// Timeline gathers what changed in the last `since` in a namespace (or the whole
// cluster): events that mark a change, warnings grouped by reason, rollouts (new
// ReplicaSets, which keep deploy history longer than events do), container restarts and
// node condition changes. Read-only.
func (c *Cluster) Timeline(ctx context.Context, ns string, since time.Duration) ([]TimelineItem, error) {
	cut := time.Now().Add(-since)
	out := []TimelineItem{}

	if el, err := c.Cs.CoreV1().Events(ns).List(ctx, metav1.ListOptions{Limit: 2000}); err == nil {
		type key struct{ obj, reason, ns string }
		agg := map[key]*TimelineItem{}
		for _, e := range el.Items {
			t := e.LastTimestamp
			if t.IsZero() {
				t = metav1.NewTime(e.EventTime.Time)
			}
			if t.IsZero() {
				t = e.CreationTimestamp
			}
			if t.Time.Before(cut) {
				continue
			}
			if e.Type != "Warning" && !normalWorthKeeping[e.Reason] {
				continue
			}
			obj := e.InvolvedObject.Kind + "/" + e.InvolvedObject.Name
			lvl := "info"
			if e.Type == "Warning" {
				lvl = "warn"
			}
			k := key{obj, e.Reason, e.Namespace}
			n := int(max(e.Count, 1))
			if x := agg[k]; x != nil {
				x.Count += n
				if at(t) > x.Time {
					x.Time, x.Detail = at(t), e.Message
				}
				continue
			}
			agg[k] = &TimelineItem{Time: at(t), Type: "event", Level: lvl, Namespace: e.Namespace, Object: obj, Title: e.Reason, Detail: e.Message, Count: n}
		}
		for _, x := range agg {
			out = append(out, *x)
		}
	}

	if rl, err := c.Cs.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{}); err == nil {
		for _, rs := range rl.Items {
			if rs.CreationTimestamp.Time.Before(cut) {
				continue
			}
			owner := ""
			for _, o := range rs.OwnerReferences {
				if o.Kind == "Deployment" {
					owner = "Deployment/" + o.Name
				}
			}
			if owner == "" {
				continue
			}
			var imgs []string
			for _, ct := range rs.Spec.Template.Spec.Containers {
				imgs = append(imgs, ct.Image)
			}
			rev := rs.Annotations["deployment.kubernetes.io/revision"]
			title := "New revision rolled out"
			if rev != "" {
				title = "Rollout: revision " + rev
			}
			out = append(out, TimelineItem{Time: at(rs.CreationTimestamp), Type: "rollout", Level: "info", Namespace: rs.Namespace, Object: owner, Title: title, Detail: strings.Join(imgs, ", ")})
		}
	}

	if pl, err := c.Cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{}); err == nil {
		for i := range pl.Items {
			p := &pl.Items[i]
			for _, cs := range p.Status.ContainerStatuses {
				t := cs.LastTerminationState.Terminated
				if t == nil || t.FinishedAt.Time.Before(cut) {
					continue
				}
				lvl := "warn"
				if t.Reason == "OOMKilled" || t.Reason == "Error" {
					lvl = "bad"
				}
				out = append(out, TimelineItem{Time: at(t.FinishedAt), Type: "restart", Level: lvl, Namespace: p.Namespace, Object: workloadOf(p),
					Title: fmt.Sprintf("Container %s restarted: %s", cs.Name, orDash(t.Reason)), Detail: fmt.Sprintf("pod %s, exit code %d, %d restarts in total", p.Name, t.ExitCode, cs.RestartCount)})
			}
		}
	}

	if ns == "" {
		if nl, err := c.Cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
			for _, n := range nl.Items {
				for _, cd := range n.Status.Conditions {
					if cd.LastTransitionTime.Time.Before(cut) {
						continue
					}
					bad := (cd.Type == corev1.NodeReady && cd.Status != corev1.ConditionTrue) || (cd.Type != corev1.NodeReady && cd.Status == corev1.ConditionTrue)
					title := "Node " + string(cd.Type) + " is " + string(cd.Status)
					lvl := "info"
					if bad {
						lvl = "bad"
					}
					out = append(out, TimelineItem{Time: at(cd.LastTransitionTime), Type: "node", Level: lvl, Object: "Node/" + n.Name, Title: title, Detail: cd.Message})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time > out[j].Time })
	if len(out) > 600 {
		out = out[:600]
	}
	return out, nil
}

func orDash(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
