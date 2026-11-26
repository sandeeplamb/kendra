package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Carvel kapp keeps one ConfigMap per app (label kapp.k14s.io/is-app) in the namespace
// the app was deployed with `-n`, mostly kube-system for platform apps. Every resource
// the app owns carries the label kapp.k14s.io/app=<labelValue>, in any namespace. Each
// deploy writes a "change" ConfigMap next to the app one. Nothing here needs the kapp
// binary or anything installed in the cluster: it only reads those objects.
const (
	kappAppLabel      = "kapp.k14s.io/app"
	kappIsApp         = "kapp.k14s.io/is-app"
	kappIsChange      = "kapp.k14s.io/is-app-change"
	kappChangeAppLbl  = "kapp.k14s.io/app-change-app-label"
	kappAppNameSuffix = ".apps.k14s.io"
)

// Owner is an ownerReference.
type Owner struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	UID  string `json:"uid"`
}

// Res is one object that belongs to a kapp app.
type Res struct {
	Group     string                     `json:"group"`
	Version   string                     `json:"version"`
	Resource  string                     `json:"resource"`
	Kind      string                     `json:"kind"`
	Namespace string                     `json:"namespace"`
	Name      string                     `json:"name"`
	UID       string                     `json:"uid"`
	Created   string                     `json:"created"`
	Health    string                     `json:"health"`
	Detail    string                     `json:"detail,omitempty"`
	Owners    []Owner                    `json:"owners,omitempty"`
	Labels    map[string]string          `json:"-"`
	Obj       *unstructured.Unstructured `json:"-"`
}

// Change is one deploy recorded by kapp.
type Change struct {
	Name        string   `json:"name"`
	StartedAt   string   `json:"startedAt"`
	FinishedAt  string   `json:"finishedAt"`
	Successful  bool     `json:"successful"`
	Description string   `json:"description"`
	Namespaces  []string `json:"namespaces,omitempty"`
}

// App is one kapp app.
type App struct {
	Cluster    string         `json:"cluster"`
	Name       string         `json:"name"`
	Namespace  string         `json:"namespace"` // where kapp keeps its state
	LabelValue string         `json:"labelValue"`
	Created    string         `json:"created"`
	Resources  int            `json:"resources"`
	Namespaces []string       `json:"namespaces"` // namespaces its resources live in
	Ok         int            `json:"ok"`
	Warn       int            `json:"warn"`
	Bad        int            `json:"bad"`
	Health     string         `json:"health"`
	LastChange *Change        `json:"lastChange,omitempty"`
	Kinds      map[string]int `json:"kinds,omitempty"`
	NsCounts   map[string]int `json:"nsCounts,omitempty"`
	Status     string         `json:"status,omitempty"`   // Helm release status
	Revision   int            `json:"revision,omitempty"` // Helm release revision
	Chart      string         `json:"chart,omitempty"`
	AppVersion string         `json:"appVersion,omitempty"`
}

type apiRes struct {
	gvr        schema.GroupVersionResource
	kind       string
	namespaced bool
}

var skipResources = map[string]bool{"events": true, "endpoints": true, "endpointslices": true, "leases": true,
	"componentstatuses": true, "bindings": true}
var skipGroups = map[string]bool{"metrics.k8s.io": true, "events.k8s.io": true, "custom.metrics.k8s.io": true,
	"external.metrics.k8s.io": true, "coordination.k8s.io": true}

// discover lists the resource types worth scanning for labelled objects, cached for
// five minutes. discoverAll is the full list of everything the cluster can list.
func (c *Cluster) discover() []apiRes { c.loadDiscovery(); return c.disc }

func (c *Cluster) discoverAll() []apiRes { c.loadDiscovery(); return c.discAll }

func (c *Cluster) loadDiscovery() {
	c.discMu.Lock()
	defer c.discMu.Unlock()
	if c.disc != nil && time.Since(c.discAt) < 5*time.Minute {
		return
	}
	lists, _ := c.Cs.Discovery().ServerPreferredResources() // partial results are fine
	out, all := []apiRes{}, []apiRes{}
	for _, l := range lists {
		gv, err := schema.ParseGroupVersion(l.GroupVersion)
		if err != nil {
			continue
		}
		for _, r := range l.APIResources {
			if strings.Contains(r.Name, "/") {
				continue
			}
			list := false
			for _, v := range r.Verbs {
				if v == "list" {
					list = true
				}
			}
			if !list {
				continue
			}
			ar := apiRes{gv.WithResource(r.Name), r.Kind, r.Namespaced}
			all = append(all, ar)
			if !skipResources[r.Name] && !skipGroups[gv.Group] {
				out = append(out, ar)
			}
		}
	}
	c.disc, c.discAll, c.discAt = out, all, time.Now()
}

// collect finds every object matching a label selector, across all namespaces and
// every resource type including CRDs. skipped counts types it could not list.
func (c *Cluster) collect(ctx context.Context, selector string) (res []Res, skipped int) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 12)
	seen := map[string]bool{}
	for _, ar := range c.discover() {
		ar := ar
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			l, err := c.Dyn.Resource(ar.gvr).List(ctx, metav1.ListOptions{LabelSelector: selector})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				skipped++
				return
			}
			for i := range l.Items {
				u := &l.Items[i]
				if seen[string(u.GetUID())] {
					continue
				}
				seen[string(u.GetUID())] = true
				r := newRes(ar, u)
				res = append(res, r)
			}
		}()
	}
	wg.Wait()
	return res, skipped
}

// newRes wraps one listed object. The kind is set explicitly: items inside a list
// response do not always carry it.
func newRes(ar apiRes, u *unstructured.Unstructured) Res {
	u.SetKind(ar.kind)
	r := Res{Group: ar.gvr.Group, Version: ar.gvr.Version, Resource: ar.gvr.Resource, Kind: ar.kind,
		Namespace: u.GetNamespace(), Name: u.GetName(), UID: string(u.GetUID()),
		Created: u.GetCreationTimestamp().UTC().Format(time.RFC3339), Labels: u.GetLabels(), Obj: u}
	for _, o := range u.GetOwnerReferences() {
		r.Owners = append(r.Owners, Owner{o.Kind, o.Name, string(o.UID)})
	}
	r.Health, r.Detail = UnstructuredHealth(u)
	return r
}

// fillApp sets the counters of an app from the objects it owns.
func fillApp(a *App, res []Res) {
	a.Kinds, a.NsCounts = map[string]int{}, map[string]int{}
	for _, r := range res {
		a.Resources++
		a.Kinds[r.Kind]++
		switch r.Health {
		case "bad":
			a.Bad++
		case "warn":
			a.Warn++
		default:
			a.Ok++
		}
		if r.Namespace != "" {
			a.NsCounts[r.Namespace]++
		}
	}
	a.Namespaces = nil
	for n := range a.NsCounts {
		a.Namespaces = append(a.Namespaces, n)
	}
	sort.Strings(a.Namespaces)
	a.Health = appHealth(*a)
}

type appSpec struct {
	LabelKey   string `json:"labelKey"`
	LabelValue string `json:"labelValue"`
}

func parseSpec(cm *corev1.ConfigMap) appSpec {
	var s appSpec
	if raw, ok := cm.Data["spec"]; ok {
		_ = json.Unmarshal([]byte(raw), &s)
	}
	if s.LabelValue == "" {
		s.LabelValue = cm.Data["labelValue"]
	}
	if s.LabelKey == "" {
		s.LabelKey = kappAppLabel
	}
	return s
}

// ListApps finds every kapp app in the cluster, in any namespace, with a health
// summary of what it owns. One label query per resource type serves all apps.
func (c *Cluster) ListApps(ctx context.Context) ([]App, int, error) {
	cms, err := c.Cs.CoreV1().ConfigMaps("").List(ctx, metav1.ListOptions{LabelSelector: kappIsApp})
	if err != nil {
		return nil, 0, err
	}
	res, skipped := c.collect(ctx, kappAppLabel)
	byLabel := map[string][]Res{}
	for _, r := range res {
		byLabel[r.Labels[kappAppLabel]] = append(byLabel[r.Labels[kappAppLabel]], r)
	}
	changes := c.latestChanges(ctx)
	apps := make([]App, 0, len(cms.Items))
	for i := range cms.Items {
		cm := &cms.Items[i]
		sp := parseSpec(cm)
		a := App{Cluster: c.Name, Name: strings.TrimSuffix(cm.Name, kappAppNameSuffix), Namespace: cm.Namespace,
			LabelValue: sp.LabelValue, Created: cm.CreationTimestamp.UTC().Format(time.RFC3339), Kinds: map[string]int{}, NsCounts: map[string]int{}}
		nss := map[string]bool{}
		for _, r := range byLabel[sp.LabelValue] {
			a.Resources++
			a.Kinds[r.Kind]++
			switch r.Health {
			case "bad":
				a.Bad++
			case "warn":
				a.Warn++
			default:
				a.Ok++
			}
			if r.Namespace != "" {
				nss[r.Namespace] = true
				a.NsCounts[r.Namespace]++
			}
		}
		for n := range nss {
			a.Namespaces = append(a.Namespaces, n)
		}
		sort.Strings(a.Namespaces)
		a.Health = appHealth(a)
		a.LastChange = changes[sp.LabelValue]
		apps = append(apps, a)
	}
	sort.Slice(apps, func(i, j int) bool {
		if apps[i].Namespace != apps[j].Namespace {
			return apps[i].Namespace < apps[j].Namespace
		}
		return apps[i].Name < apps[j].Name
	})
	return apps, skipped, nil
}

func appHealth(a App) string {
	switch {
	case a.Resources == 0:
		return "idle"
	case a.Bad > 0:
		return "bad"
	case a.Warn > 0:
		return "warn"
	}
	return "ok"
}

func changeFrom(cm *corev1.ConfigMap) Change {
	ch := Change{Name: cm.Name, StartedAt: cm.CreationTimestamp.UTC().Format(time.RFC3339)}
	d := cm.Data
	var spec struct {
		StartedAt   string   `json:"startedAt"`
		FinishedAt  string   `json:"finishedAt"`
		Successful  bool     `json:"successful"`
		Description string   `json:"description"`
		Namespaces  []string `json:"namespaces"`
	}
	if raw, ok := d["spec"]; ok {
		_ = json.Unmarshal([]byte(raw), &spec)
	} else {
		spec.StartedAt, spec.FinishedAt, spec.Description = d["startedAt"], d["finishedAt"], d["description"]
		spec.Successful = d["successful"] == "true"
		_ = json.Unmarshal([]byte(d["namespaces"]), &spec.Namespaces)
	}
	if spec.StartedAt != "" {
		ch.StartedAt = spec.StartedAt
	}
	ch.FinishedAt, ch.Successful, ch.Description, ch.Namespaces = spec.FinishedAt, spec.Successful, spec.Description, spec.Namespaces
	return ch
}

// latestChanges returns the newest change per app label value, from one query.
func (c *Cluster) latestChanges(ctx context.Context) map[string]*Change {
	out := map[string]*Change{}
	cms, err := c.Cs.CoreV1().ConfigMaps("").List(ctx, metav1.ListOptions{LabelSelector: kappIsChange})
	if err != nil {
		return out
	}
	for i := range cms.Items {
		cm := &cms.Items[i]
		lv := cm.Labels[kappChangeAppLbl]
		ch := changeFrom(cm)
		if cur, ok := out[lv]; !ok || ch.StartedAt > cur.StartedAt {
			out[lv] = &ch
		}
	}
	return out
}

// AppChanges lists the recorded deploys of one app, newest first.
func (c *Cluster) AppChanges(ctx context.Context, ns, labelValue string) ([]Change, error) {
	cms, err := c.Cs.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{
		LabelSelector: kappIsChange + "," + kappChangeAppLbl + "=" + labelValue})
	if err != nil {
		return nil, err
	}
	out := make([]Change, 0, len(cms.Items))
	for i := range cms.Items {
		out = append(out, changeFrom(&cms.Items[i]))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	return out, nil
}

// AppInfo is the app's state ConfigMap plus what it owns.
type AppInfo struct {
	App       App      `json:"app"`
	Resources []Res    `json:"resources"`
	Skipped   int      `json:"skipped"`
	Changes   []Change `json:"changes"`
	State     string   `json:"state"` // the app ConfigMap as YAML-ish JSON
	Commands  []string `json:"commands"`
}

// findApp locates the app ConfigMap by name and state namespace (either name form).
func (c *Cluster) findApp(ctx context.Context, ns, name string) (*corev1.ConfigMap, error) {
	for _, n := range []string{name + kappAppNameSuffix, name} {
		cm, err := c.Cs.CoreV1().ConfigMaps(ns).Get(ctx, n, metav1.GetOptions{})
		if err == nil {
			if _, ok := cm.Labels[kappIsApp]; ok {
				return cm, nil
			}
		}
	}
	return nil, fmt.Errorf("kapp app %q not found in namespace %q", name, ns)
}

// GetApp returns one app with its resources and change history.
func (c *Cluster) GetApp(ctx context.Context, ns, name string) (*AppInfo, error) {
	cm, err := c.findApp(ctx, ns, name)
	if err != nil {
		return nil, err
	}
	sp := parseSpec(cm)
	res, skipped := c.collect(ctx, sp.LabelKey+"="+sp.LabelValue)
	sort.Slice(res, func(i, j int) bool {
		if res[i].Kind != res[j].Kind {
			return res[i].Kind < res[j].Kind
		}
		if res[i].Namespace != res[j].Namespace {
			return res[i].Namespace < res[j].Namespace
		}
		return res[i].Name < res[j].Name
	})
	app := App{Cluster: c.Name, Name: name, Namespace: ns, LabelValue: sp.LabelValue,
		Created: cm.CreationTimestamp.UTC().Format(time.RFC3339), Kinds: map[string]int{}, NsCounts: map[string]int{}}
	nss := map[string]bool{}
	for _, r := range res {
		app.Resources++
		app.Kinds[r.Kind]++
		switch r.Health {
		case "bad":
			app.Bad++
		case "warn":
			app.Warn++
		default:
			app.Ok++
		}
		if r.Namespace != "" {
			nss[r.Namespace] = true
			app.NsCounts[r.Namespace]++
		}
	}
	for n := range nss {
		app.Namespaces = append(app.Namespaces, n)
	}
	sort.Strings(app.Namespaces)
	app.Health = appHealth(app)
	changes, _ := c.AppChanges(ctx, ns, sp.LabelValue)
	if len(changes) > 0 {
		app.LastChange = &changes[0]
	}
	data := map[string]string{}
	for k, v := range cm.Data {
		if len(v) > 1500 {
			v = v[:1500] + "… (truncated)"
		}
		data[k] = v
	}
	state, _ := json.MarshalIndent(map[string]any{"name": cm.Name, "namespace": cm.Namespace, "labels": cm.Labels,
		"annotations": cm.Annotations, "data": data}, "", "  ")
	return &AppInfo{App: app, Resources: res, Skipped: skipped, Changes: changes, State: string(state),
		Commands: []string{
			fmt.Sprintf("kapp inspect -a %s -n %s --tree --context %s", name, ns, c.Name),
			fmt.Sprintf("kapp inspect -a %s -n %s --status --context %s", name, ns, c.Name),
			fmt.Sprintf("kapp app-change list -a %s -n %s --context %s", name, ns, c.Name),
			fmt.Sprintf("kapp deploy -a %s -n %s --diff-run -f <path> --context %s", name, ns, c.Name),
		}}, nil
}

// Unmanaged lists workloads that no kapp app owns: a gap in "everything is in kapp".
func (c *Cluster) Unmanaged(ctx context.Context) ([]Res, error) {
	var out []Res
	var lastErr error
	okKinds := 0
	for _, name := range []string{"deployments", "statefulsets", "daemonsets", "cronjobs"} {
		k, _ := FindKind(name)
		l, err := c.Dyn.Resource(k.GVR()).List(ctx, metav1.ListOptions{LabelSelector: "!" + kappAppLabel})
		if err != nil {
			lastErr = err // e.g. no permission for one kind: keep the others
			continue
		}
		okKinds++
		for i := range l.Items {
			u := &l.Items[i]
			if len(u.GetOwnerReferences()) > 0 { // created by an operator, not by a person or pipeline
				continue
			}
			h, d := UnstructuredHealth(u)
			out = append(out, Res{Kind: k.Singular, Resource: k.Name, Namespace: u.GetNamespace(), Name: u.GetName(),
				UID: string(u.GetUID()), Health: h, Detail: d, Created: u.GetCreationTimestamp().UTC().Format(time.RFC3339)})
		}
	}
	if okKinds == 0 && lastErr != nil {
		return nil, lastErr
	}
	return out, nil
}

// UnstructuredHealth judges any object from its status.
func UnstructuredHealth(u *unstructured.Unstructured) (health, detail string) {
	if u.GetDeletionTimestamp() != nil {
		return "warn", "terminating"
	}
	i := func(path ...string) int64 { v, _, _ := unstructured.NestedInt64(u.Object, path...); return v }
	s := func(path ...string) string { v, _, _ := unstructured.NestedString(u.Object, path...); return v }
	ready := func(r, want int64) (string, string) {
		h, n := readiness(int(r), int(want))
		return h, n
	}
	switch u.GetKind() {
	case "Pod":
		for _, key := range []string{"initContainerStatuses", "containerStatuses"} {
			cs, _, _ := unstructured.NestedSlice(u.Object, "status", key)
			for _, x := range cs {
				m, _ := x.(map[string]any)
				if r, _, _ := unstructured.NestedString(m, "state", "waiting", "reason"); r != "" && r != "PodInitializing" {
					if r == "ContainerCreating" {
						return "warn", r
					}
					return "bad", r
				}
			}
		}
		switch s("status", "phase") {
		case "Failed":
			return "bad", s("status", "reason")
		case "Pending":
			return "warn", "pending"
		case "Succeeded":
			return "idle", "completed"
		}
		if conditionFalse(u, "Ready") {
			return "warn", "not ready"
		}
		return "ok", ""
	case "Deployment", "StatefulSet", "ReplicaSet":
		want, found, _ := unstructured.NestedInt64(u.Object, "spec", "replicas")
		if !found {
			want = 1
		}
		return ready(i("status", "readyReplicas"), want)
	case "DaemonSet":
		return ready(i("status", "numberReady"), i("status", "desiredNumberScheduled"))
	case "Job":
		if i("status", "failed") > 0 && i("status", "succeeded") == 0 && i("status", "active") == 0 {
			return "bad", "job failed"
		}
		if i("status", "succeeded") > 0 {
			return "idle", "completed"
		}
		return "ok", ""
	case "Namespace":
		if s("status", "phase") == "Terminating" {
			return "warn", "terminating"
		}
		return "ok", ""
	case "PersistentVolumeClaim":
		switch s("status", "phase") {
		case "Pending":
			return "warn", "pending"
		case "Lost":
			return "bad", "lost"
		}
		return "ok", ""
	}
	// Custom resources and everything else: trust the usual status conditions.
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, x := range conds {
		m, _ := x.(map[string]any)
		t, _ := m["type"].(string)
		st, _ := m["status"].(string)
		msg, _ := m["message"].(string)
		switch t {
		case "Ready", "Available", "Established", "Synced", "Healthy", "ReconcileSucceeded", "Reconciled":
			if st == "False" {
				return "bad", shorten(t + ": " + msg)
			}
		case "Failed", "Stalled", "ReconcileFailed", "ValuesSchemaCheckFailed":
			if st == "True" {
				return "bad", shorten(t + ": " + msg)
			}
		}
	}
	return "ok", ""
}

func conditionFalse(u *unstructured.Unstructured, typ string) bool {
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, x := range conds {
		m, _ := x.(map[string]any)
		if m["type"] == typ && m["status"] == "False" {
			return true
		}
	}
	return false
}

func shorten(s string) string {
	if len(s) > 140 {
		return s[:140] + "…"
	}
	return strings.TrimSpace(s)
}

// PathFor builds the API path of any discovered resource (CRDs included). An empty
// version means "the preferred version of the group".
func (c *Cluster) PathFor(group, version, resource, ns, name string) (string, error) {
	for _, ar := range c.discoverAll() {
		if ar.gvr.Group == group && ar.gvr.Resource == resource && (version == "" || ar.gvr.Version == version) {
			k := Kind{Name: resource, Group: group, Version: ar.gvr.Version, Namespaced: ar.namespaced}
			return k.Path(ns, name), nil
		}
	}
	return "", fmt.Errorf("resource %s.%s is not served by this cluster", resource, group)
}

func kindGVR(group, version, resource string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: group, Version: version, Resource: resource}
}
