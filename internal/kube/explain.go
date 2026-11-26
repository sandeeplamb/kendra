package kube

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Finding is one line of the "why is this unhealthy?" answer.
type Finding struct {
	Level  string `json:"level"` // bad | warn | ok
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

// Explain reads a pod's status and events and says what is wrong in plain words.
func (c *Cluster) Explain(ctx context.Context, ns, name string) ([]Finding, error) {
	p, err := c.Cs.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, cd := range p.Status.Conditions {
		if cd.Type == "PodScheduled" && cd.Status == "False" {
			out = append(out, Finding{"bad", "No node can run this pod", cd.Message, scheduleHint(cd.Message)})
		}
	}
	var sts []contStatus
	for _, group := range [][]corev1.ContainerStatus{p.Status.InitContainerStatuses, p.Status.ContainerStatuses} {
		for _, s := range group {
			cs := contStatus{name: s.Name, restarts: s.RestartCount}
			if s.State.Waiting != nil {
				cs.waitR, cs.waitM = s.State.Waiting.Reason, s.State.Waiting.Message
			}
			if t := s.LastTerminationState.Terminated; t != nil {
				cs.termR, cs.exit = t.Reason, t.ExitCode
			}
			sts = append(sts, cs)
		}
	}
	for _, s := range sts {
		if s.waitR != "" && s.waitR != "PodInitializing" {
			out = append(out, Finding{"bad", fmt.Sprintf("Container %q is waiting: %s", s.name, s.waitR), s.waitM, waitHint(s.waitR, s.waitM)})
		}
		if s.termR == "OOMKilled" {
			out = append(out, Finding{"bad", fmt.Sprintf("Container %q was killed for using too much memory", s.name),
				fmt.Sprintf("Restarted %d times.", s.restarts), "Raise the memory limit, or find the leak. Check the memory bar in the pods list."})
		} else if s.termR != "" && s.exit != 0 {
			out = append(out, Finding{"warn", fmt.Sprintf("Container %q last exited with code %d (%s)", s.name, s.exit, s.termR),
				fmt.Sprintf("Restarted %d times.", s.restarts), "Read the previous container's logs (Logs tab → Previous)."})
		}
	}
	if p.Status.Reason == "Evicted" {
		out = append(out, Finding{"bad", "The pod was evicted", p.Status.Message, "The node ran low on resources. Check node pressure and the pod's requests."})
	}
	evs, err := c.Cs.CoreV1().Events(ns).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.name=" + name + ",involvedObject.kind=Pod,type=Warning"})
	if err == nil {
		type agg struct {
			reason, msg string
			n           int32
		}
		seen := map[string]*agg{}
		for _, e := range evs.Items {
			k := e.Reason + "|" + e.Message
			if a, ok := seen[k]; ok {
				a.n += max(e.Count, 1)
			} else {
				seen[k] = &agg{e.Reason, e.Message, max(e.Count, 1)}
			}
		}
		var list []*agg
		for _, a := range seen {
			list = append(list, a)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].n > list[j].n })
		for i, a := range list {
			if i == 4 {
				break
			}
			out = append(out, Finding{"warn", fmt.Sprintf("Event %s (×%d)", a.reason, a.n), a.msg, ""})
		}
	}
	if len(out) == 0 {
		out = append(out, Finding{"ok", "Nothing wrong found", "Status: " + string(p.Status.Phase) + ".", ""})
	}
	return out, nil
}

type contStatus struct {
	name, waitR, waitM, termR string
	exit, restarts            int32
}

func scheduleHint(msg string) string {
	l := strings.ToLower(msg)
	switch {
	case strings.Contains(l, "insufficient memory"):
		return "No node has enough free memory. Lower the pod's memory request, or add nodes."
	case strings.Contains(l, "insufficient cpu"):
		return "No node has enough free CPU. Lower the CPU request, or add nodes."
	case strings.Contains(l, "taint"):
		return "Every node has a taint the pod does not tolerate. Add a toleration or use another node pool."
	case strings.Contains(l, "affinity") || strings.Contains(l, "selector"):
		return "The node selector or affinity matches no node. Check the labels on your nodes."
	case strings.Contains(l, "persistentvolumeclaim") || strings.Contains(l, "volume"):
		return "A volume claim cannot be bound. Check the claim and its storage class."
	}
	return ""
}

func waitHint(reason, msg string) string {
	switch reason {
	case "CrashLoopBackOff":
		return "The container starts and then exits. Read its logs, including the previous run."
	case "ImagePullBackOff", "ErrImagePull":
		return "The image cannot be pulled. Check the image name and tag, and the pull secret."
	case "InvalidImageName":
		return "The image name is not valid."
	case "CreateContainerConfigError":
		return "A ConfigMap or Secret the pod needs is missing or has no such key."
	case "ContainerCreating":
		return "Usually a volume that cannot be mounted. Check the Events."
	}
	return ""
}
