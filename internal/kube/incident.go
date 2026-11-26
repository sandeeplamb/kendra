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

// IncidentKinds are the catalogue kinds an incident bundle can be made for.
var IncidentKinds = []string{"pods", "deployments", "statefulsets", "daemonsets", "services", "persistentvolumeclaims", "nodes"}

// Incident is a Markdown report about one object, to paste into a ticket or a chat.
type Incident struct {
	Title    string `json:"title"`
	Markdown string `json:"markdown"`
}

var singularOf = map[string]string{"pods": "Pod", "deployments": "Deployment", "statefulsets": "StatefulSet", "daemonsets": "DaemonSet",
	"services": "Service", "persistentvolumeclaims": "PersistentVolumeClaim", "nodes": "Node"}

func mdCell(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\r", "")
	return strings.ReplaceAll(s, "\n", " ")
}

func mdTable(b *strings.Builder, head []string, rows [][]string) {
	b.WriteString("| " + strings.Join(head, " | ") + " |\n|")
	for range head {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	for _, r := range rows {
		cells := make([]string, len(r))
		for i, c := range r {
			cells[i] = mdCell(c)
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	b.WriteString("\n")
}

var levelMark = map[string]string{"bad": "❌", "warn": "⚠️", "ok": "✅", "": "•", "info": "•"}

func relLines(b *strings.Builder, title string, rs []Rel) {
	if len(rs) == 0 {
		return
	}
	b.WriteString("**" + title + "**\n\n")
	for _, r := range rs {
		line := "- " + levelMark[r.Level] + " " + r.Kind + " `" + r.Name + "`"
		if r.Namespace != "" {
			line += " (" + r.Namespace + ")"
		}
		if r.Note != "" {
			line += ": " + r.Note
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
}

// incidentPods returns the pods that belong to a workload, or the pod itself.
func (c *Cluster) incidentPods(ctx context.Context, kind, ns, name string) []corev1.Pod {
	var sel *metav1.LabelSelector
	switch kind {
	case "pods":
		if p, err := c.Cs.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{}); err == nil {
			return []corev1.Pod{*p}
		}
		return nil
	case "deployments":
		if d, err := c.Cs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{}); err == nil {
			sel = d.Spec.Selector
		}
	case "statefulsets":
		if d, err := c.Cs.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{}); err == nil {
			sel = d.Spec.Selector
		}
	case "daemonsets":
		if d, err := c.Cs.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{}); err == nil {
			sel = d.Spec.Selector
		}
	case "services":
		if s, err := c.Cs.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{}); err == nil && len(s.Spec.Selector) > 0 {
			sel = &metav1.LabelSelector{MatchLabels: s.Spec.Selector}
		}
	}
	if sel == nil {
		return nil
	}
	s, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil || s.Empty() {
		return nil
	}
	l, err := c.Cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: s.String()})
	if err != nil {
		return nil
	}
	return l.Items
}

func podRow(p corev1.Pod) []string {
	ready, total, restarts := 0, len(p.Status.ContainerStatuses), int32(0)
	last := ""
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Ready {
			ready++
		}
		restarts += cs.RestartCount
		switch {
		case cs.State.Waiting != nil && cs.State.Waiting.Reason != "":
			last = cs.State.Waiting.Reason
		case last == "" && cs.LastTerminationState.Terminated != nil:
			t := cs.LastTerminationState.Terminated
			last = fmt.Sprintf("%s (exit %d)", t.Reason, t.ExitCode)
		}
	}
	if last == "" {
		last = "–"
	}
	return []string{p.Name, string(p.Status.Phase), fmt.Sprintf("%d/%d", ready, total), fmt.Sprint(restarts), p.Spec.NodeName, last}
}

// IncidentBundle gathers what is known about one object into a Markdown report: what is wrong
// (the Why? findings), its warning events, the pods behind it, what it uses and what uses it,
// what would stop if it went away, what changed around it in the last 6 hours, and the kubectl
// commands to look further. It never includes logs, YAML or Secret values. Read-only.
func (c *Cluster) IncidentBundle(ctx context.Context, cluster, kind, ns, name string) (*Incident, error) {
	sing, ok := singularOf[kind]
	if !ok {
		return nil, fmt.Errorf("no incident bundle for %q yet", kind)
	}
	why, err := c.ExplainKind(ctx, kind, ns, name)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	title := sing + " " + name
	if ns != "" {
		title += " in " + ns
	}
	fmt.Fprintf(&b, "# Incident bundle: %s\n\n", title)
	ver, plat := "", ""
	if sv, err := c.Cs.Discovery().ServerVersion(); err == nil {
		ver = sv.GitVersion
		if nl, err := c.Cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 5}); err == nil {
			plat = platformOf(ver, nl.Items)
		}
	}
	fmt.Fprintf(&b, "- Cluster: `%s`", cluster)
	if ver != "" {
		fmt.Fprintf(&b, " (%s %s)", strings.TrimSpace(plat), ver)
	}
	fmt.Fprintf(&b, "\n- Object: %s `%s`", sing, name)
	if ns != "" {
		fmt.Fprintf(&b, " in namespace `%s`", ns)
	}
	fmt.Fprintf(&b, "\n- Generated: %s by Kendra (read-only)\n\n", time.Now().UTC().Format("2006-01-02 15:04 UTC"))

	b.WriteString("## What is wrong\n\n")
	for _, f := range why {
		fmt.Fprintf(&b, "- %s **%s**", levelMark[f.Level], f.Title)
		if f.Detail != "" {
			b.WriteString(": " + strings.ReplaceAll(f.Detail, "\n", " "))
		}
		if f.Hint != "" {
			b.WriteString(" _Hint: " + strings.ReplaceAll(f.Hint, "\n", " ") + "_")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")

	if pods := c.incidentPods(ctx, kind, ns, name); len(pods) > 0 {
		sort.Slice(pods, func(i, j int) bool { return pods[i].Name < pods[j].Name })
		rows := [][]string{}
		for i, p := range pods {
			if i == 30 {
				rows = append(rows, []string{fmt.Sprintf("… and %d more", len(pods)-30), "", "", "", "", ""})
				break
			}
			rows = append(rows, podRow(p))
		}
		b.WriteString("## Pods\n\n")
		mdTable(&b, []string{"Pod", "Phase", "Ready", "Restarts", "Node", "Last state"}, rows)
	}

	// events of this object, newest first
	sel := "involvedObject.name=" + name
	if ns == "" {
		sel += ",involvedObject.namespace="
	}
	if el, err := c.Cs.CoreV1().Events(ns).List(ctx, metav1.ListOptions{FieldSelector: sel}); err == nil && len(el.Items) > 0 {
		evs := el.Items
		stamp := func(e corev1.Event) time.Time {
			if !e.LastTimestamp.IsZero() {
				return e.LastTimestamp.Time
			}
			if !e.EventTime.IsZero() {
				return e.EventTime.Time
			}
			return e.CreationTimestamp.Time
		}
		sort.Slice(evs, func(i, j int) bool { return stamp(evs[i]).After(stamp(evs[j])) })
		rows := [][]string{}
		for i, e := range evs {
			if i == 15 {
				break
			}
			rows = append(rows, []string{stamp(e).UTC().Format("2006-01-02 15:04"), e.Type, e.Reason, fmt.Sprint(max(e.Count, 1)), e.Message})
		}
		b.WriteString("## Events\n\n")
		mdTable(&b, []string{"Last seen (UTC)", "Type", "Reason", "Count", "Message"}, rows)
	}

	if rel, err := c.Related(ctx, kind, ns, name); err == nil && (len(rel.Uses) > 0 || len(rel.UsedBy) > 0) {
		b.WriteString("## What it uses and what uses it\n\n")
		relLines(&b, "Uses", rel.Uses)
		relLines(&b, "Used by", rel.UsedBy)
	}
	if imp, err := c.Impact(ctx, kind, ns, name); err == nil && len(imp.Summary) > 0 {
		b.WriteString("## If it goes away\n\n")
		for _, s := range imp.Summary {
			b.WriteString("- " + s + "\n")
		}
		b.WriteString("\n")
		relLines(&b, "Services affected", imp.Services)
		relLines(&b, "Ingresses affected", imp.Ingresses)
	}

	if tl, err := c.Timeline(ctx, ns, 6*time.Hour); err == nil {
		var mine []TimelineItem
		for _, it := range tl {
			parts := strings.SplitN(it.Object, "/", 2)
			on := parts[len(parts)-1]
			if on == name || strings.HasPrefix(on, name+"-") {
				mine = append(mine, it)
			}
		}
		sort.Slice(mine, func(i, j int) bool { return mine[i].Time > mine[j].Time })
		if len(mine) > 0 {
			rows := [][]string{}
			for i, it := range mine {
				if i == 25 {
					break
				}
				rows = append(rows, []string{it.Time, it.Type, it.Object, it.Title, it.Detail})
			}
			b.WriteString("## What changed in the last 6 hours\n\n")
			mdTable(&b, []string{"Time (UTC)", "Type", "Object", "What", "Detail"}, rows)
		}
	}

	b.WriteString("## Commands to look further\n\n```sh\n")
	nsFlag := ""
	if ns != "" {
		nsFlag = " -n " + ns
	}
	k := "kubectl --context " + cluster + nsFlag
	lower := strings.ToLower(sing)
	fmt.Fprintf(&b, "%s describe %s %s\n%s get events --field-selector involvedObject.name=%s --sort-by=.lastTimestamp\n", k, lower, name, k, name)
	switch kind {
	case "pods":
		fmt.Fprintf(&b, "%s logs %s --all-containers --previous --tail=200\n", k, name)
	case "deployments", "statefulsets", "daemonsets":
		fmt.Fprintf(&b, "%s rollout status %s/%s\n%s logs %s/%s --all-containers --tail=200\n", k, lower, name, k, lower, name)
	}
	b.WriteString("```\n\n_Logs, YAML and Secret values are not included in this report._\n")
	return &Incident{Title: title, Markdown: b.String()}, nil
}
