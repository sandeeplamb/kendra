package kube

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ExplainKind is Explain for more than pods: nodes, volume claims, services and
// workloads (stuck rollouts). It reads status, related objects and warning events,
// and says what is wrong in plain words. Read-only.
func (c *Cluster) ExplainKind(ctx context.Context, kind, ns, name string) ([]Finding, error) {
	var out []Finding
	var err error
	switch kind {
	case "", "pods":
		return c.Explain(ctx, ns, name)
	case "nodes":
		out, err = c.explainNode(ctx, name)
	case "persistentvolumeclaims":
		out, err = c.explainPVC(ctx, ns, name)
	case "services":
		out, err = c.explainService(ctx, ns, name)
	case "deployments", "statefulsets", "daemonsets":
		out, err = c.explainWorkload(ctx, kind, ns, name)
	default:
		return nil, fmt.Errorf("no explanation for %q yet", kind)
	}
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		out = append(out, Finding{"ok", "Nothing wrong found", "", ""})
	}
	return out, nil
}

// warningFindings lists the most frequent Warning events of one object.
func (c *Cluster) warningFindings(ctx context.Context, ns, objKind, name string) []Finding {
	evs, err := c.Cs.CoreV1().Events(ns).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.name=" + name + ",involvedObject.kind=" + objKind + ",type=Warning"})
	if err != nil {
		return nil
	}
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
	var out []Finding
	for i, a := range list {
		if i == 4 {
			break
		}
		out = append(out, Finding{"warn", fmt.Sprintf("Event %s (×%d)", a.reason, a.n), a.msg, ""})
	}
	return out
}

func (c *Cluster) explainNode(ctx context.Context, name string) ([]Finding, error) {
	n, err := c.Cs.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, cd := range n.Status.Conditions {
		switch cd.Type {
		case corev1.NodeReady:
			if cd.Status != corev1.ConditionTrue {
				out = append(out, Finding{"bad", "The node is not ready", cd.Reason + ": " + cd.Message,
					"The kubelet is not reporting. Check the instance in the cloud console (AWS EC2 or Azure VMSS), then the kubelet logs. Pods on it will be rescheduled."})
			}
		case corev1.NodeMemoryPressure:
			if cd.Status == corev1.ConditionTrue {
				out = append(out, Finding{"bad", "The node is low on memory", cd.Message, "The kubelet evicts pods. Lower memory requests/limits of heavy pods, or add nodes."})
			}
		case corev1.NodeDiskPressure:
			if cd.Status == corev1.ConditionTrue {
				out = append(out, Finding{"bad", "The node is low on disk", cd.Message, "Usually container images or logs. Clean up, or use a bigger OS disk."})
			}
		case corev1.NodePIDPressure:
			if cd.Status == corev1.ConditionTrue {
				out = append(out, Finding{"bad", "The node has too many processes", cd.Message, "A workload is forking too much. Find the pod with the most processes."})
			}
		case corev1.NodeNetworkUnavailable:
			if cd.Status == corev1.ConditionTrue {
				out = append(out, Finding{"bad", "The node network is not set up", cd.Message, "Check the CNI plugin pods (aws-node, azure-cns, cilium...) on this node."})
			}
		}
	}
	if n.Spec.Unschedulable {
		out = append(out, Finding{"warn", "The node is cordoned", "No new pods are placed on it.", "Normal during an upgrade or drain. If nobody is working on it, someone forgot to uncordon."})
	}
	for _, t := range n.Spec.Taints {
		if t.Effect == corev1.TaintEffectNoSchedule || t.Effect == corev1.TaintEffectNoExecute {
			lvl := "warn"
			if strings.HasPrefix(t.Key, "node.kubernetes.io/") {
				lvl = "bad" // set by the control plane: not ready, unreachable, pressure...
			}
			out = append(out, Finding{lvl, fmt.Sprintf("Taint %s=%s:%s", t.Key, t.Value, t.Effect), "", "Only pods that tolerate this taint can run here."})
		}
	}
	// room left: sum of requests of the pods on this node against what it can allocate
	if pl, err := c.Cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + name}); err == nil {
		var cpu, mem int64
		count := 0
		for i := range pl.Items {
			p := &pl.Items[i]
			if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
				continue
			}
			count++
			for _, ct := range p.Spec.Containers {
				cpu += ct.Resources.Requests.Cpu().MilliValue()
				mem += ct.Resources.Requests.Memory().Value()
			}
		}
		ac, am, ap := n.Status.Allocatable.Cpu().MilliValue(), n.Status.Allocatable.Memory().Value(), n.Status.Allocatable.Pods().Value()
		pct := func(a, b int64) int {
			if b == 0 {
				return 0
			}
			return int(a * 100 / b)
		}
		detail := fmt.Sprintf("CPU requested %d%%, memory requested %d%%, %d of %d pods.", pct(cpu, ac), pct(mem, am), count, ap)
		if pct(cpu, ac) >= 90 || pct(mem, am) >= 90 || (ap > 0 && int64(count)*100/ap >= 90) {
			out = append(out, Finding{"warn", "The node is nearly full", detail, "New pods will not fit here. Add nodes, or check whether requests are larger than needed."})
		} else {
			out = append(out, Finding{"ok", "Capacity", detail, ""})
		}
	}
	return append(out, c.warningFindings(ctx, "", "Node", name)...), nil
}

func (c *Cluster) explainPVC(ctx context.Context, ns, name string) ([]Finding, error) {
	pvc, err := c.Cs.CoreV1().PersistentVolumeClaims(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	var out []Finding
	switch pvc.Status.Phase {
	case corev1.ClaimLost:
		out = append(out, Finding{"bad", "The volume behind this claim is gone", "Claim is Lost.", "The persistent volume was deleted or the disk disappeared. Restore from a snapshot or backup."})
	case corev1.ClaimPending:
		sc := ""
		if pvc.Spec.StorageClassName != nil {
			sc = *pvc.Spec.StorageClassName
		}
		if sc == "" && pvc.Spec.VolumeName == "" {
			out = append(out, Finding{"warn", "The claim names no storage class", "The cluster's default storage class is used, if there is one.", "Set storageClassName, or mark one class as default."})
		} else if sc != "" {
			cl, err := c.Cs.StorageV1().StorageClasses().Get(ctx, sc, metav1.GetOptions{})
			switch {
			case err != nil:
				out = append(out, Finding{"bad", fmt.Sprintf("Storage class %q does not exist", sc), "", "Fix the name, or create the class (check the CSI driver add-on is installed)."})
			case cl.VolumeBindingMode != nil && *cl.VolumeBindingMode == "WaitForFirstConsumer":
				out = append(out, Finding{"warn", "Waiting for a pod to use it", fmt.Sprintf("Class %q binds the volume only when a pod is scheduled.", sc), "Normal until a pod mounts the claim. If a pod already does, look at that pod with Why?."})
			default:
				out = append(out, Finding{"bad", "Pending: the volume is not provisioned yet", fmt.Sprintf("Storage class %q, provisioner %s.", sc, cl.Provisioner), "Read the events below. Common causes: CSI driver not running, no capacity or quota in the zone, missing cloud permissions."})
			}
		}
	}
	for _, cd := range pvc.Status.Conditions {
		if cd.Status == corev1.ConditionTrue {
			out = append(out, Finding{"warn", "Resize: " + string(cd.Type), cd.Message, "The disk was resized in the cloud; the file system grows when a pod mounts it."})
		}
	}
	return append(out, c.warningFindings(ctx, ns, "PersistentVolumeClaim", name)...), nil
}

func (c *Cluster) explainService(ctx context.Context, ns, name string) ([]Finding, error) {
	svc, err := c.Cs.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	var out []Finding
	if svc.Spec.Type == corev1.ServiceTypeExternalName {
		return []Finding{{"ok", "ExternalName service", "Points at " + svc.Spec.ExternalName + ". It has no pods behind it.", ""}}, nil
	}
	if len(svc.Spec.Selector) == 0 {
		out = append(out, Finding{"warn", "The service has no selector", "Its endpoints must be created by hand.", "Check the Endpoints object with the same name."})
	} else {
		sel := metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: svc.Spec.Selector})
		pl, err := c.Cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
		if err != nil {
			return nil, err
		}
		ready, named := 0, map[string]bool{}
		for i := range pl.Items {
			p := &pl.Items[i]
			for _, cd := range p.Status.Conditions {
				if cd.Type == corev1.PodReady && cd.Status == corev1.ConditionTrue {
					ready++
				}
			}
			for _, ct := range p.Spec.Containers {
				for _, pt := range ct.Ports {
					named[pt.Name] = true
				}
			}
		}
		switch {
		case len(pl.Items) == 0:
			out = append(out, Finding{"bad", "No pods match the selector", "Selector: " + sel, "The service selects nothing, so it has no endpoints. Compare the selector with the pod labels (typo, or the app label changed)."})
		case ready == 0:
			out = append(out, Finding{"bad", "No matching pod is ready", fmt.Sprintf("%d pod(s) match %s, none passes its readiness check.", len(pl.Items), sel), "Open one of the pods and use Why?. Check its readiness probe."})
		default:
			out = append(out, Finding{"ok", "Endpoints", fmt.Sprintf("%d of %d matching pods are ready.", ready, len(pl.Items)), ""})
		}
		for _, sp := range svc.Spec.Ports {
			if sp.TargetPort.StrVal != "" && len(pl.Items) > 0 && !named[sp.TargetPort.StrVal] {
				out = append(out, Finding{"bad", fmt.Sprintf("Port name %q is not exposed by any matching pod", sp.TargetPort.StrVal),
					fmt.Sprintf("Service port %d targets it.", sp.Port), "Name the container port in the pod spec, or target a number."})
			}
		}
	}
	if svc.Spec.Type == corev1.ServiceTypeLoadBalancer && len(svc.Status.LoadBalancer.Ingress) == 0 {
		out = append(out, Finding{"warn", "The load balancer has no address yet", "", "The cloud controller creates it. Read the events below for quota, subnet or permission errors."})
	}
	return append(out, c.warningFindings(ctx, ns, "Service", name)...), nil
}

// podProblems groups the pods behind a selector by what is wrong with them.
func (c *Cluster) podProblems(ctx context.Context, ns, selector string) []Finding {
	pl, err := c.Cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil
	}
	type grp struct {
		n       int
		example string
	}
	by := map[string]*grp{}
	add := func(k string, p *corev1.Pod) {
		if g := by[k]; g != nil {
			g.n++
		} else {
			by[k] = &grp{1, p.Name}
		}
	}
	for i := range pl.Items {
		p := &pl.Items[i]
		if p.Status.Phase == corev1.PodPending {
			unsched := false
			for _, cd := range p.Status.Conditions {
				if cd.Type == corev1.PodScheduled && cd.Status == corev1.ConditionFalse {
					unsched = true
				}
			}
			if unsched {
				add("not scheduled (no node fits)", p)
				continue
			}
		}
		for _, group := range [][]corev1.ContainerStatus{p.Status.InitContainerStatuses, p.Status.ContainerStatuses} {
			for _, s := range group {
				if w := s.State.Waiting; w != nil && w.Reason != "PodInitializing" && w.Reason != "ContainerCreating" {
					add(w.Reason, p)
				} else if t := s.LastTerminationState.Terminated; t != nil && t.Reason == "OOMKilled" && s.State.Running != nil {
					add("OOMKilled (restarting)", p)
				}
			}
		}
	}
	var keys []string
	for k := range by {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return by[keys[i]].n > by[keys[j]].n })
	var out []Finding
	for _, k := range keys {
		g := by[k]
		out = append(out, Finding{"bad", fmt.Sprintf("%d pod(s): %s", g.n, k), "For example " + g.example + ".", "Open that pod and use Why? for the cause."})
	}
	return out
}

func (c *Cluster) explainWorkload(ctx context.Context, kind, ns, name string) ([]Finding, error) {
	var out []Finding
	var sel *metav1.LabelSelector
	switch kind {
	case "deployments":
		d, err := c.Cs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		sel = d.Spec.Selector
		want := int32(1)
		if d.Spec.Replicas != nil {
			want = *d.Spec.Replicas
		}
		if d.Spec.Paused {
			out = append(out, Finding{"warn", "The rollout is paused", "", "Someone ran a pause. Resume it with the rollout command."})
		}
		for _, cd := range d.Status.Conditions {
			switch {
			case cd.Type == "Progressing" && cd.Status == corev1.ConditionFalse:
				out = append(out, Finding{"bad", "The rollout is stuck", cd.Reason + ": " + cd.Message, "The new version did not become ready in time. The new pods below say why. The old pods keep serving."})
			case cd.Type == "ReplicaFailure" && cd.Status == corev1.ConditionTrue:
				out = append(out, Finding{"bad", "Pods cannot be created", cd.Reason + ": " + cd.Message, "Often a resource quota, a limit range or a missing service account."})
			case cd.Type == "Available" && cd.Status == corev1.ConditionFalse:
				out = append(out, Finding{"bad", "Not enough pods are available", cd.Message, ""})
			}
		}
		detail := fmt.Sprintf("Wanted %d, ready %d, updated %d, unavailable %d.", want, d.Status.ReadyReplicas, d.Status.UpdatedReplicas, d.Status.UnavailableReplicas)
		switch {
		case d.Status.UpdatedReplicas < want && len(out) == 0:
			out = append(out, Finding{"warn", "A rollout is in progress", detail, "If it stays like this, look at the new pods."})
		case d.Status.ReadyReplicas < want && len(out) == 0:
			out = append(out, Finding{"warn", "Fewer pods are ready than wanted", detail, ""})
		default:
			out = append(out, Finding{"ok", "Replicas", detail, ""})
		}
	case "statefulsets":
		s, err := c.Cs.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		sel = s.Spec.Selector
		want := int32(1)
		if s.Spec.Replicas != nil {
			want = *s.Spec.Replicas
		}
		detail := fmt.Sprintf("Wanted %d, ready %d, updated %d.", want, s.Status.ReadyReplicas, s.Status.UpdatedReplicas)
		if s.Status.ReadyReplicas < want {
			out = append(out, Finding{"warn", "Fewer pods are ready than wanted", detail, "A StatefulSet starts pods one by one: the first unready pod blocks the next ones."})
		} else if s.Status.CurrentRevision != s.Status.UpdateRevision {
			out = append(out, Finding{"warn", "An update is in progress", detail, "Pods are replaced from the highest number down."})
		} else {
			out = append(out, Finding{"ok", "Replicas", detail, ""})
		}
	case "daemonsets":
		d, err := c.Cs.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		sel = d.Spec.Selector
		detail := fmt.Sprintf("Wanted on %d nodes, ready on %d, unavailable %d, misplaced %d.", d.Status.DesiredNumberScheduled, d.Status.NumberReady, d.Status.NumberUnavailable, d.Status.NumberMisscheduled)
		if d.Status.NumberReady < d.Status.DesiredNumberScheduled {
			out = append(out, Finding{"warn", "Not ready on every node", detail, "The pods below say why. Tainted nodes need a toleration."})
		} else {
			out = append(out, Finding{"ok", "Nodes", detail, ""})
		}
	}
	if sel != nil {
		out = append(out, c.podProblems(ctx, ns, metav1.FormatLabelSelector(sel))...)
	}
	return append(out, c.warningFindings(ctx, ns, map[string]string{"deployments": "Deployment", "statefulsets": "StatefulSet", "daemonsets": "DaemonSet"}[kind], name)...), nil
}
