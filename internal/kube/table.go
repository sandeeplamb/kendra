package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

const tableAccept = "application/json;as=Table;v=v1;g=meta.k8s.io,application/json;as=Table;v=v1beta1;g=meta.k8s.io,application/json"

// Column is a printer column the API server reports for a kind (what `kubectl get` shows).
type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Priority int    `json:"priority"`
}

// Row is one object in a Table.
type Row struct {
	Cluster   string   `json:"cluster,omitempty"`
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Created   string   `json:"created"`
	Cells     []string `json:"cells"`
	Health    string   `json:"health"` // ok | warn | bad | idle
	Note      string   `json:"note,omitempty"`
	HasUsage  bool     `json:"hasUsage,omitempty"`
	CPUm      int64    `json:"cpuM,omitempty"` // millicores in use (pods)
	MemBytes  int64    `json:"memB,omitempty"` // bytes in use (pods)
}

// Table is a list of one kind with the columns the cluster itself defines.
type Table struct {
	Kind    string   `json:"kind"`
	Columns []Column `json:"columns"`
	Rows    []Row    `json:"rows"`
}

type apiTable struct {
	ColumnDefinitions []Column `json:"columnDefinitions"`
	Rows              []struct {
		Cells  []any `json:"cells"`
		Object struct {
			Metadata struct {
				Name              string `json:"name"`
				Namespace         string `json:"namespace"`
				CreationTimestamp string `json:"creationTimestamp"`
			} `json:"metadata"`
		} `json:"object"`
	} `json:"rows"`
}

// List returns every object of kind k (in ns, or everywhere when ns is empty), with
// health and, for pods, live CPU and memory when metrics-server is installed.
func (c *Cluster) List(ctx context.Context, k Kind, ns string) (*Table, error) {
	k, err := c.Resolve(k)
	if err != nil {
		return nil, err
	}
	raw, err := c.Cs.CoreV1().RESTClient().Get().AbsPath(k.Path(ns, "")).
		SetHeader("Accept", tableAccept).DoRaw(ctx)
	if err != nil {
		return nil, err
	}
	var at apiTable
	if err := json.Unmarshal(raw, &at); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", k.Name, err)
	}
	t := &Table{Kind: k.Name, Columns: at.ColumnDefinitions, Rows: make([]Row, 0, len(at.Rows))}
	for _, r := range at.Rows {
		cells := make([]string, len(r.Cells))
		for i, v := range r.Cells {
			switch x := v.(type) {
			case nil:
				cells[i] = ""
			case string:
				cells[i] = x
			case float64:
				cells[i] = strconv.FormatFloat(x, 'f', -1, 64)
			default:
				cells[i] = fmt.Sprint(x)
			}
		}
		m := r.Object.Metadata
		row := Row{Cluster: c.Name, Namespace: m.Namespace, Name: m.Name, Created: m.CreationTimestamp, Cells: cells}
		row.Health, row.Note = Classify(k.Name, t.Columns, cells)
		t.Rows = append(t.Rows, row)
	}
	if k.Name == "pods" {
		c.addUsage(ctx, t, ns)
	}
	return t, nil
}

func (c *Cluster) addUsage(ctx context.Context, t *Table, ns string) {
	path := "/apis/metrics.k8s.io/v1beta1/pods"
	if ns != "" {
		path = "/apis/metrics.k8s.io/v1beta1/namespaces/" + ns + "/pods"
	}
	raw, err := c.Cs.CoreV1().RESTClient().Get().AbsPath(path).DoRaw(ctx)
	if err != nil {
		return // no metrics-server, or not allowed: the columns just stay empty
	}
	var ml struct {
		Items []struct {
			Metadata   struct{ Name, Namespace string } `json:"metadata"`
			Containers []struct {
				Usage map[string]string `json:"usage"`
			} `json:"containers"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &ml) != nil {
		return
	}
	type use struct{ cpu, mem int64 }
	m := map[string]use{}
	for _, it := range ml.Items {
		if len(it.Containers) == 0 {
			continue
		}
		var u use
		for _, ct := range it.Containers {
			if q, err := resource.ParseQuantity(ct.Usage["cpu"]); err == nil {
				u.cpu += q.MilliValue()
			}
			if q, err := resource.ParseQuantity(ct.Usage["memory"]); err == nil {
				u.mem += q.Value()
			}
		}
		m[it.Metadata.Namespace+"/"+it.Metadata.Name] = u
	}
	for i := range t.Rows {
		if u, ok := m[t.Rows[i].Namespace+"/"+t.Rows[i].Name]; ok {
			t.Rows[i].HasUsage, t.Rows[i].CPUm, t.Rows[i].MemBytes = true, u.cpu, u.mem
		}
	}
}

// Classify turns a row of printer columns into a health level and a short reason.
func Classify(kind string, cols []Column, cells []string) (health, note string) {
	get := func(name string) string {
		for i, c := range cols {
			if strings.EqualFold(c.Name, name) && i < len(cells) {
				return cells[i]
			}
		}
		return ""
	}
	frac := func(s string) (a, b int, ok bool) {
		p := strings.SplitN(s, "/", 2)
		if len(p) != 2 {
			return 0, 0, false
		}
		x, e1 := strconv.Atoi(strings.TrimSpace(p[0]))
		y, e2 := strconv.Atoi(strings.TrimSpace(p[1]))
		return x, y, e1 == nil && e2 == nil
	}
	num := func(name string) int { n, _ := strconv.Atoi(get(name)); return n }
	switch kind {
	case "pods":
		return podHealth(get("Status"), get("Ready"))
	case "deployments", "statefulsets":
		a, b, ok := frac(get("Ready"))
		if !ok {
			return "ok", ""
		}
		return readiness(a, b)
	case "daemonsets":
		return readiness(num("Ready"), num("Desired"))
	case "replicasets":
		return readiness(num("Ready"), num("Desired"))
	case "jobs":
		st := strings.ToLower(get("Status"))
		switch {
		case strings.Contains(st, "fail"):
			return "bad", "job failed"
		case strings.Contains(st, "complete"), strings.Contains(st, "succe"):
			return "idle", "completed"
		case st == "":
			if a, b, ok := frac(get("Completions")); ok && a < b {
				return "warn", fmt.Sprintf("%d/%d completions", a, b)
			}
		}
		return "ok", ""
	case "cronjobs":
		if strings.EqualFold(get("Suspend"), "true") {
			return "idle", "suspended"
		}
	case "nodes":
		st := get("Status")
		switch {
		case strings.Contains(st, "NotReady"):
			return "bad", "node not ready"
		case strings.Contains(st, "SchedulingDisabled"):
			return "warn", "cordoned"
		}
	case "namespaces":
		if strings.EqualFold(get("Status"), "Terminating") {
			return "warn", "terminating"
		}
	case "persistentvolumeclaims", "persistentvolumes":
		switch strings.ToLower(get("Status")) {
		case "pending":
			return "warn", "pending"
		case "lost", "failed":
			return "bad", get("Status")
		case "released":
			return "idle", "released"
		}
	case "policyreports", "clusterpolicyreports":
		// Kyverno / wg-policy-prototype reports: PASS FAIL WARN ERROR SKIP counts.
		switch {
		case num("Fail") > 0:
			return "bad", fmt.Sprintf("%d failing", num("Fail"))
		case num("Error") > 0:
			return "bad", fmt.Sprintf("%d errors", num("Error"))
		case num("Warn") > 0:
			return "warn", fmt.Sprintf("%d warnings", num("Warn"))
		}
		return "ok", ""
	case "events":
		if strings.EqualFold(get("Type"), "Warning") {
			return "warn", get("Reason")
		}
		return "idle", ""
	}
	// Custom resources and add-ons (KEDA, Karpenter, Istio...): trust the usual columns.
	switch strings.ToLower(get("Ready")) {
	case "false":
		return "bad", "not ready"
	case "unknown":
		return "warn", "ready unknown"
	}
	if strings.EqualFold(get("Failing"), "true") {
		return "bad", "failing"
	}
	if strings.EqualFold(get("Fallback"), "true") {
		return "warn", "fallback active"
	}
	if strings.EqualFold(get("Active"), "false") && kind == "scaledobjects" {
		return "idle", "inactive"
	}
	return "ok", ""
}

func readiness(ready, want int) (string, string) {
	switch {
	case want == 0:
		return "idle", "scaled to zero"
	case ready == 0:
		return "bad", fmt.Sprintf("0/%d ready", want)
	case ready < want:
		return "warn", fmt.Sprintf("%d/%d ready", ready, want)
	}
	return "ok", ""
}

var badWords = []string{"crashloop", "error", "imagepull", "errimage", "oomkilled", "failed", "evicted", "unknown",
	"invalid", "createcontainerconfigerror", "runcontainererror", "nodelost", "backoff", "deadlineexceeded"}
var warnWords = []string{"pending", "creating", "init:", "terminating", "initializing", "notready", "unschedulable"}

func podHealth(status, ready string) (string, string) {
	l := strings.ToLower(status)
	if l == "completed" || l == "succeeded" {
		return "idle", "completed"
	}
	for _, w := range badWords {
		if strings.Contains(l, w) {
			return "bad", status
		}
	}
	for _, w := range warnWords {
		if strings.Contains(l, w) {
			return "warn", status
		}
	}
	if p := strings.SplitN(ready, "/", 2); len(p) == 2 && p[0] != p[1] {
		return "warn", "not ready " + ready
	}
	return "ok", ""
}
