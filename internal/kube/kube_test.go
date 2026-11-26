package kube

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestContextsMarksProd(t *testing.T) {
	m := fakeAPI(t)
	cs, err := m.Contexts()
	if err != nil || len(cs) != 1 || !cs[0].Prod || !cs[0].Current {
		t.Fatalf("contexts = %+v, %v", cs, err)
	}
}

func TestListAppsFindsKappAppInKubeSystem(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	apps, _, err := c.ListApps(ctx(t))
	if err != nil || len(apps) != 1 {
		t.Fatalf("apps = %+v, %v", apps, err)
	}
	a := apps[0]
	// state lives in kube-system, resources live in shop (+ a cluster-scoped ClusterRole)
	if a.Name != "platform" || a.Namespace != "kube-system" {
		t.Errorf("name/ns = %s/%s (the .apps.k14s.io suffix must be stripped)", a.Name, a.Namespace)
	}
	if a.Resources != 5 { // deployment, service, ingress, configmap, clusterrole
		t.Errorf("resources = %d, want 5 (kinds %v)", a.Resources, a.Kinds)
	}
	if len(a.Namespaces) != 1 || a.Namespaces[0] != "shop" {
		t.Errorf("namespaces = %v", a.Namespaces)
	}
	if a.Health != "warn" || a.Warn != 1 {
		t.Errorf("health = %s warn=%d (deployment has 1/2 ready)", a.Health, a.Warn)
	}
	if a.LastChange == nil || !a.LastChange.Successful || a.LastChange.Description != "update: resource" {
		t.Errorf("last change = %+v", a.LastChange)
	}
}

func TestGetAppAndGraph(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	info, err := c.GetApp(ctx(t), "kube-system", "platform")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Changes) != 1 || !info.Changes[0].Successful {
		t.Errorf("changes = %+v", info.Changes)
	}
	g := c.AppGraph(ctx(t), info.Resources)
	kinds := map[string]Node{}
	for _, n := range g.Nodes {
		kinds[n.Kind+"/"+n.Name] = n
	}
	for _, want := range []string{"Ingress/web", "Service/web", "Deployment/web", "ReplicaSet/web-abc", "Pod/web-abc-1", "Pod/web-abc-2", "ConfigMap/cfg"} {
		if _, ok := kinds[want]; !ok {
			t.Errorf("graph is missing %s; has %v", want, keys(kinds))
		}
	}
	if n := kinds["Secret/db-creds"]; !n.Missing || n.Health != "bad" {
		t.Errorf("referenced secret that does not exist must be a red missing node, got %+v", n)
	}
	if kinds["Pod/web-abc-2"].Health != "bad" {
		t.Errorf("crash-looping pod health = %q", kinds["Pod/web-abc-2"].Health)
	}
	has := func(from, to, typ string) bool {
		for _, e := range g.Edges {
			if e.From == from && e.To == to && e.Type == typ {
				return true
			}
		}
		return false
	}
	id := func(k string) string { return kinds[k].ID }
	for _, e := range [][3]string{{"Ingress/web", "Service/web", "routes"}, {"Service/web", "Deployment/web", "selects"},
		{"Deployment/web", "ReplicaSet/web-abc", "owns"}, {"ReplicaSet/web-abc", "Pod/web-abc-1", "owns"},
		{"Deployment/web", "ConfigMap/cfg", "uses"}, {"Deployment/web", "Secret/db-creds", "uses"}} {
		if !has(id(e[0]), id(e[1]), e[2]) {
			t.Errorf("missing edge %s -%s-> %s", e[0], e[2], e[1])
		}
	}
	if len(g.Other) != 1 || g.Other[0].Kind != "ClusterRole" {
		t.Errorf("other = %+v", g.Other)
	}
}

func TestUnmanagedFindsHandMadeWorkload(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	l, err := c.Unmanaged(ctx(t))
	names := map[string]bool{}
	for _, w := range l {
		names[w.Name] = true
	}
	if err != nil || !names["handmade"] || names["web"] {
		t.Fatalf("unmanaged = %v, %v (kapp-owned web must not be listed)", names, err)
	}
}

func TestIstioTopology(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	r, err := c.IstioTopology(ctx(t), "")
	if err != nil || !r.Installed {
		t.Fatalf("istio = %+v, %v", r, err)
	}
	by := map[string]Node{}
	for _, n := range r.Graph.Nodes {
		by[n.Kind+"/"+n.Name] = n
	}
	for _, want := range []string{"Gateway/gw", "VirtualService/web", "DestinationRule/web", "Service/web", "ExternalHost/api.stripe.com"} {
		if _, ok := by[want]; !ok {
			t.Errorf("missing node %s; have %v", want, keys(by))
		}
	}
	if n := by["Gateway/gone"]; !n.Missing {
		t.Errorf("a gateway the VirtualService names but that does not exist must be flagged missing: %+v", n)
	}
	edge := func(from, to string) (Edge, bool) {
		for _, e := range r.Graph.Edges {
			if e.From == by[from].ID && e.To == by[to].ID {
				return e, true
			}
		}
		return Edge{}, false
	}
	if e, ok := edge("Gateway/gw", "VirtualService/web"); !ok || e.Label != "app.example.com" {
		t.Errorf("gateway->virtualservice edge = %+v %v", e, ok)
	}
	if e, ok := edge("VirtualService/web", "DestinationRule/web"); !ok || e.Label != "/api port 80 subset v1 90%" {
		t.Errorf("virtualservice->destinationrule edge = %+v %v", e, ok)
	}
	if _, ok := edge("DestinationRule/web", "Service/web"); !ok {
		t.Error("destination rule must point to its service")
	}
	if n := by["Service/web"]; n.Detail != "ports 80→8080 · 1/2 ready" && n.Detail != "ports 80→8080" {
		t.Errorf("service detail = %q", n.Detail)
	}
	if _, ok := edge("Service/web", "Pod/pods"); !ok {
		t.Error("service must point to its pods")
	}
	// scoped to another namespace: no virtual services
	r2, _ := c.IstioTopology(ctx(t), "other")
	if r2.Counts["VirtualService"] != 0 {
		t.Errorf("namespace filter leaked: %v", r2.Counts)
	}
}

func TestResolveHost(t *testing.T) {
	for _, tc := range []struct {
		host, from, ns, name string
		ok                   bool
	}{
		{"web", "shop", "shop", "web", true}, {"web.shop", "x", "shop", "web", true}, {"web.shop.svc.cluster.local", "x", "shop", "web", true},
		{"*.example.com", "x", "", "", false}, {"api.stripe.com", "x", "", "", false},
	} {
		ns, n, ok := resolveHost(tc.host, tc.from)
		if ns != tc.ns || n != tc.name || ok != tc.ok {
			t.Errorf("resolveHost(%q) = %q %q %v", tc.host, ns, n, ok)
		}
	}
}

func TestClassify(t *testing.T) {
	pc := []Column{{Name: "Name"}, {Name: "Ready"}, {Name: "Status"}}
	for _, tc := range []struct{ status, ready, want string }{
		{"Running", "1/1", "ok"}, {"Running", "1/2", "warn"}, {"CrashLoopBackOff", "0/1", "bad"}, {"Pending", "0/1", "warn"},
		{"Completed", "0/1", "idle"}, {"Init:CrashLoopBackOff", "0/1", "bad"}, {"Init:0/1", "0/1", "warn"}, {"ImagePullBackOff", "0/1", "bad"},
	} {
		if h, _ := Classify("pods", pc, []string{"p", tc.ready, tc.status}); h != tc.want {
			t.Errorf("pod %s %s = %s, want %s", tc.status, tc.ready, h, tc.want)
		}
	}
	dc := []Column{{Name: "Name"}, {Name: "Ready"}}
	for ready, want := range map[string]string{"2/2": "ok", "1/2": "warn", "0/2": "bad", "0/0": "idle"} {
		if h, _ := Classify("deployments", dc, []string{"d", ready}); h != want {
			t.Errorf("deployment %s = %s, want %s", ready, h, want)
		}
	}
}

func TestKindPath(t *testing.T) {
	d, _ := FindKind("deployments")
	if p := d.Path("shop", "web"); p != "/apis/apps/v1/namespaces/shop/deployments/web" {
		t.Error(p)
	}
	n, _ := FindKind("nodes")
	if p := n.Path("ignored", ""); p != "/api/v1/nodes" {
		t.Error(p)
	}
}

func keys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestClusterTopology(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	g, err := c.ClusterTopology(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Node{}
	for _, n := range g.Nodes {
		by[n.Kind+"/"+n.Name] = n
	}
	if n := by["Node pool/general"]; n.Detail != "2 nodes" || n.Health != "warn" {
		t.Errorf("pool = %+v (one node is cordoned: warn)", n)
	}
	if n := by["Node/node-b"]; n.Health != "warn" {
		t.Errorf("cordoned node = %+v", n)
	}
	if n := by["Namespace/shop"]; n.Health != "bad" || n.Detail != "2 pods" {
		t.Errorf("namespace shop = %+v (has a crash-looping pod)", n)
	}
	find := func(from, to string) (Edge, bool) {
		for _, e := range g.Edges {
			if e.From == by[from].ID && e.To == by[to].ID {
				return e, true
			}
		}
		return Edge{}, false
	}
	if _, ok := find("Node pool/general", "Node/node-a"); !ok {
		t.Error("pool must own its nodes")
	}
	if e, ok := find("Node/node-a", "Namespace/shop"); !ok || e.Label != "1 pod" {
		t.Errorf("node-a -> shop = %+v %v", e, ok)
	}
}

func TestNamespaceTopology(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	g, err := c.NamespaceTopology(ctx(t), "shop")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Node{}
	for _, n := range g.Nodes {
		by[n.Kind+"/"+n.Name] = n
	}
	for _, want := range []string{"Ingress/web", "Service/web", "Deployment/web", "Deployment/cache", "ReplicaSet/web-abc", "Pod/web-abc-1"} {
		if _, ok := by[want]; !ok {
			t.Errorf("namespace topology lacks %s; has %v", want, keys(by))
		}
	}
	if n := by["ConfigMap/cfg"]; !n.External || n.Missing {
		t.Errorf("an existing configmap the workload mounts is a plain (not missing) node: %+v", n)
	}
	if n := by["Secret/db-creds"]; !n.Missing {
		t.Errorf("db-creds does not exist and must be flagged missing: %+v", n)
	}
}

func TestHelmReleases(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	apps, _, err := c.ListHelmReleases(ctx(t))
	if err != nil || len(apps) != 1 {
		t.Fatalf("releases = %+v, %v", apps, err)
	}
	a := apps[0]
	if a.Name != "redis" || a.Namespace != "shop" || a.Revision != 2 || a.Status != "deployed" || a.Resources != 1 || a.Health != "ok" || a.Chart != "redis-18.1.0" || a.AppVersion != "7.2.4" {
		t.Errorf("release = %+v", a)
	}
	info, err := c.GetHelmRelease(ctx(t), "shop", "redis")
	if err != nil || len(info.Changes) != 2 || info.Changes[0].Description != "revision 2: deployed" || info.App.Chart != "redis-18.1.0" || len(info.Resources) != 1 {
		t.Fatalf("info = %+v, %v", info, err)
	}
	if _, err := c.GetHelmRelease(ctx(t), "shop", "nope"); err == nil {
		t.Error("an unknown release must be an error")
	}
}

func TestCatalogueListsEveryRequestedKind(t *testing.T) {
	want := map[string][]string{
		"Config":           {"configmaps", "secrets", "resourcequotas", "limitranges", "poddisruptionbudgets", "priorityclasses", "runtimeclasses", "leases"},
		"Network":          {"services", "endpoints", "endpointslices", "ingresses", "ingressclasses", "gateways", "virtualservices", "networkpolicies"},
		"Storage":          {"persistentvolumeclaims", "persistentvolumes", "storageclasses"},
		"Access Control":   {"serviceaccounts", "clusterroles", "roles", "clusterrolebindings", "rolebindings"},
		"Pod Autoscaling":  {"horizontalpodautoscalers", "scaledobjects", "triggerauthentications", "clustertriggerauthentications"},
		"Node Autoscaling": {"nodepools", "ec2nodeclasses", "nodeclaims", "nodeoverlays"},
		"Kyverno":          {"clusterpolicies", "policies", "validatingpolicies", "mutatingpolicies", "policyexceptions", "clusterpolicyreports", "policyreports", "cleanuppolicies", "clustercleanuppolicies"},
	}
	for cat, names := range want {
		var got []string
		for _, k := range Kinds {
			if k.Category == cat {
				got = append(got, k.Name)
			}
		}
		for i, n := range names {
			if i >= len(got) || got[i] != n {
				t.Errorf("%s = %v, want it to start with %v (in this order)", cat, got, names)
				break
			}
		}
	}
}

func TestResolveFindsServedVersionAndReportsMissingAddOns(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	gw, _ := FindKind("gateways")
	k, err := c.Resolve(gw)
	if err != nil || k.Version != "v1" || !k.Namespaced || k.Path("shop", "") != "/apis/networking.istio.io/v1/namespaces/shop/gateways" {
		t.Fatalf("gateways = %+v, %v", k, err)
	}
	so, _ := FindKind("scaledobjects")
	_, err = c.Resolve(so)
	var ni *NotInstalledError
	if !errors.As(err, &ni) {
		t.Fatalf("KEDA is not in the fake cluster: want NotInstalledError, got %v", err)
	}
	// and listing says so instead of failing in a confusing way
	if _, err := c.List(ctx(t), so, ""); !errors.As(err, &ni) {
		t.Errorf("List of a missing add-on kind: %v", err)
	}
	// leases are served but are skipped by the label scans: they must still resolve
	lease, _ := FindKind("leases")
	if _, err := c.Resolve(lease); err != nil {
		t.Errorf("leases: %v", err)
	}
}

func TestListIstioGatewayAndCustomResourceByGroup(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	gw, _ := FindKind("gateways")
	tb, err := c.List(ctx(t), gw, "")
	if err != nil || len(tb.Rows) != 1 || tb.Rows[0].Name != "gw" {
		t.Fatalf("gateways = %+v, %v", tb, err)
	}
	k, err := c.KindFor("networking.istio.io", "virtualservices")
	if err != nil || k.Singular != "VirtualService" || !k.Namespaced {
		t.Fatalf("KindFor = %+v, %v", k, err)
	}
	if tb, err = c.List(ctx(t), k, "shop"); err != nil || len(tb.Rows) != 1 {
		t.Fatalf("virtualservices = %+v, %v", tb, err)
	}
}

func TestNamespaceOverview(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	o, err := c.NamespaceOverview(ctx(t))
	if err != nil || len(o) != 3 {
		t.Fatalf("overview = %+v, %v", o, err)
	}
	by := map[string]NsOverview{}
	for _, x := range o {
		by[x.Name] = x
	}
	shop := by["shop"]
	if shop.Pods != 2 || shop.PodsBad != 1 || shop.Health != "bad" {
		t.Errorf("shop pods = %+v", shop)
	}
	if shop.Workloads != 2 || shop.Services != 1 || shop.Ingresses != 1 || shop.ConfigMaps != 1 || shop.Secrets != 2 {
		t.Errorf("shop counts = %+v", shop)
	}
	if by["istio-system"].Health != "idle" {
		t.Errorf("empty namespace should be idle: %+v", by["istio-system"])
	}
}

func TestCRDs(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	l, err := c.CRDs(ctx(t))
	if err != nil || len(l) != 2 {
		t.Fatalf("crds = %+v, %v", l, err)
	}
	if l[0].Group != "cert-manager.io" || l[1].Plural != "gateways" || !l[1].Ready || len(l[1].Versions) != 2 || l[1].Scope != "Namespaced" {
		t.Errorf("crds = %+v", l)
	}
}

func TestLocalHelmCharts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HELM_CONFIG_HOME", filepath.Join(dir, "cfg"))
	t.Setenv("HELM_CACHE_HOME", filepath.Join(dir, "cache"))
	if _, _, err := LocalHelmCharts(3); err == nil {
		t.Error("without repositories.yaml there is nothing to list")
	}
	os.MkdirAll(filepath.Join(dir, "cfg"), 0o755)
	os.MkdirAll(filepath.Join(dir, "cache", "repository"), 0o755)
	os.WriteFile(filepath.Join(dir, "cfg", "repositories.yaml"), []byte("repositories:\n- name: bitnami\n  url: https://charts.bitnami.com/bitnami\n- name: stale\n  url: https://x\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "cache", "repository", "bitnami-index.yaml"), []byte(`entries:
  redis:
  - {version: 18.1.0, appVersion: 7.2.4, description: In-memory store, created: "2026-01-02T00:00:00Z"}
  - {version: 18.0.0, appVersion: 7.2.0, created: "2026-01-01T00:00:00Z"}
  - {version: 17.0.0, created: "2025-01-01T00:00:00Z"}
  - {version: 16.0.0, created: "2024-01-01T00:00:00Z"}
`), 0o644)
	repos, charts, err := LocalHelmCharts(3)
	if err != nil || len(repos) != 2 {
		t.Fatalf("repos = %+v, %v", repos, err)
	}
	if len(charts) != 3 || charts[0].Version != "18.1.0" || charts[0].Repo != "bitnami" || charts[0].AppVersion != "7.2.4" {
		t.Errorf("charts = %+v (want the newest 3 of 4, repo with no index skipped)", charts)
	}
}

func TestPodPortForServiceTargetPorts(t *testing.T) {
	svc := &corev1.Service{}
	svc.Name = "web"
	svc.Spec.Ports = []corev1.ServicePort{
		{Port: 80, TargetPort: intstr.FromInt(8080)},
		{Port: 443, TargetPort: intstr.FromString("https")},
		{Port: 9000},
	}
	pod := &corev1.Pod{}
	pod.Name = "web-1"
	pod.Spec.Containers = []corev1.Container{{Ports: []corev1.ContainerPort{{Name: "https", ContainerPort: 8443}}}}
	for remote, want := range map[int]int{80: 8080, 443: 8443, 9000: 9000} {
		if got, err := podPort(svc, pod, remote); err != nil || got != want {
			t.Errorf("service port %d -> %d, %v; want %d", remote, got, err, want)
		}
	}
	if _, err := podPort(svc, pod, 1234); err == nil {
		t.Error("an unknown service port must be an error")
	}
	one := &corev1.Service{}
	one.Spec.Ports = []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt(3000)}}
	if got, err := podPort(one, pod, 0); err != nil || got != 3000 {
		t.Errorf("single-port service with no port given = %d, %v", got, err)
	}
	// readyPod prefers a Ready pod over merely Running ones
	mk := func(name string, ready bool) corev1.Pod {
		p := corev1.Pod{}
		p.Name = name
		p.Status.Phase = corev1.PodRunning
		st := corev1.ConditionFalse
		if ready {
			st = corev1.ConditionTrue
		}
		p.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: st}}
		return p
	}
	if p := readyPod([]corev1.Pod{mk("a", false), mk("b", true)}); p == nil || p.Name != "b" {
		t.Errorf("readyPod = %+v", p)
	}
}

func TestFleetSummary(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	f, err := c.FleetSummary(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != "v1.31.3-eks-abc" || f.Minor != 31 || f.Platform != "EKS" {
		t.Errorf("version/platform = %q %d %q", f.Version, f.Minor, f.Platform)
	}
	if f.Pods != 2 || f.PodsBad != 1 || f.Health != "bad" {
		t.Errorf("pods/health = %+v", f)
	}
}

func TestDeprecatedAPIs(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	r, err := c.DeprecatedAPIs(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]DeprecatedAPI{}
	for _, it := range r.Items {
		by[it.APIVersion+"/"+it.Resource] = it
	}
	pdb, ok := by["policy/v1beta1/poddisruptionbudgets"]
	if !ok {
		t.Fatalf("PDB v1beta1 not reported: %+v", r.Items)
	}
	// cluster is 1.31, removal in 1.32 = the next upgrade
	if pdb.Urgency != "bad" || pdb.Objects != 1 || pdb.Examples[0] != "shop/web-pdb" || !pdb.Requested || pdb.Managers[0] != "helm" {
		t.Errorf("pdb = %+v", pdb)
	}
	if w, ok := by["v1alpha9/widgets"]; !ok || w.Urgency != "warn" {
		t.Errorf("unknown requested version should be listed from metrics: %+v", w)
	}
}

func TestSplitImage(t *testing.T) {
	for in, want := range map[string][3]any{
		"nginx":                 {"nginx", "", false},
		"nginx:1.25":            {"nginx", "1.25", false},
		"host:5000/a/b:2":       {"host:5000/a/b", "2", false},
		"host:5000/a/b":         {"host:5000/a/b", "", false},
		"reg.io/x:1@sha256:abc": {"reg.io/x", "1", true},
		"reg.io/x@sha256:abc":   {"reg.io/x", "", true},
	} {
		r, tag, dg := splitImage(in)
		if r != want[0] || tag != want[1] || dg != want[2] {
			t.Errorf("splitImage(%q) = %q %q %v", in, r, tag, dg)
		}
	}
}

func TestUsageImagesSnapshot(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	u, err := c.Usage(ctx(t))
	if err != nil {
		t.Fatalf("usage = %+v, %v", u, err)
	}
	var web *UsageRow
	for i := range u.Rows {
		if u.Rows[i].Workload == "Deployment/web" {
			web = &u.Rows[i]
		}
	}
	if web == nil || web.Pods != 2 || web.CPUReq != 100 || web.MemReq != 64<<20 || web.MemLim != 128<<20 || web.NoRequests != 1 {
		t.Errorf("web usage = %+v", web)
	}
	im, err := c.Images(ctx(t))
	if err != nil || len(im) != 1 || im[0].Repo != "reg.io/web" || im[0].Tag != "1.2" || im[0].Pods != 2 || im[0].Namespace != "shop" {
		t.Errorf("images = %+v, %v", im, err)
	}
	sn, err := c.Snapshot(ctx(t), "shop")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range sn {
		if it.Kind == "Deployment" && it.Name == "web" && it.Props["replicas"] == "2" && it.Props["image c"] == "reg.io/web:1.2" {
			found = true
		}
		if it.Kind == "ConfigMap" && it.Name == "cfg" && it.Props["keys"] != "" {
			t.Errorf("fake configmap has no data, keys = %q", it.Props["keys"])
		}
	}
	if !found {
		t.Errorf("snapshot lacks Deployment/web: %+v", sn)
	}
}

func TestCertificates(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	l, err := c.Certificates(ctx(t))
	if err != nil || len(l) != 1 {
		t.Fatalf("certs = %+v, %v", l, err)
	}
	x := l[0]
	if x.Name != "web-tls" || x.Ready != "True" || !x.HasExpiry || x.DaysLeft < 9 || x.DaysLeft > 10 || x.Issuer != "letsencrypt" || x.DNSNames[0] != "app.example.com" {
		t.Errorf("cert = %+v", x)
	}
}

func TestExplainKinds(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	has := func(f []Finding, level, sub string) bool {
		for _, x := range f {
			if x.Level == level && strings.Contains(x.Title+" "+x.Detail, sub) {
				return true
			}
		}
		return false
	}
	f, err := c.ExplainKind(ctx(t), "nodes", "", "node-b")
	if err != nil || !has(f, "warn", "cordoned") {
		t.Errorf("node-b = %+v, %v", f, err)
	}
	f, err = c.ExplainKind(ctx(t), "services", "shop", "web")
	if err != nil || !has(f, "ok", "1 of 2") {
		t.Errorf("service = %+v, %v", f, err)
	}
	f, err = c.ExplainKind(ctx(t), "deployments", "shop", "web")
	if err != nil || !has(f, "warn", "Wanted 2, ready 1") || !has(f, "bad", "CrashLoopBackOff") {
		t.Errorf("deployment = %+v, %v", f, err)
	}
	if _, err := c.ExplainKind(ctx(t), "secrets", "shop", "x"); err == nil {
		t.Error("secrets must not be explained")
	}
}

func TestSecurityAndIdentities(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	r, err := c.Security(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	var web *RiskyWorkload
	for i := range r.Workloads {
		if r.Workloads[i].Workload == "Deployment/web" {
			web = &r.Workloads[i]
		}
	}
	if web == nil || web.Pods != 1 || countBad(web.Risks) != 2 {
		t.Fatalf("risky web = %+v (privileged + host network expected)", web)
	}
	if len(r.Namespaces) == 0 {
		t.Fatal("no namespaces")
	}
	for _, n := range r.Namespaces {
		if n.Namespace == "shop" && n.Enforce != "baseline" {
			t.Errorf("shop enforce = %q", n.Enforce)
		}
	}
	ids, err := c.Identities(ctx(t))
	if err != nil || len(ids) != 1 {
		t.Fatalf("identities = %+v, %v", ids, err)
	}
	if x := ids[0]; x.Provider != "AWS" || x.ServiceAccount != "web-sa" || x.Pods != 2 || x.Workloads[0] != "Deployment/web" || !strings.Contains(x.Identity, "role/web") {
		t.Errorf("identity = %+v", x)
	}
}

func TestAddons(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	l, err := c.Addons(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	var cm *AddonInfo
	for i := range l {
		if l[i].Name == "cert-manager" {
			cm = &l[i]
		}
	}
	if cm == nil || cm.Version != "v1.15.1" || cm.Ready != 1 || cm.Desired != 1 || cm.Level != "ok" || cm.Namespace != "cert-manager" || len(cm.Notes) == 0 || cm.Notes[0] != "1 certificates" {
		t.Errorf("cert-manager = %+v", cm)
	}
}

func TestRelatedAndImpact(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	r, err := c.Related(ctx(t), "deployments", "shop", "web")
	if err != nil {
		t.Fatal(err)
	}
	find := func(l []Rel, kind, name, level string) bool {
		for _, x := range l {
			if x.Kind == kind && x.Name == name && x.Level == level {
				return true
			}
		}
		return false
	}
	if !find(r.Uses, "ConfigMap", "cfg", "") || !find(r.Uses, "Secret", "db-creds", "bad") {
		t.Errorf("uses = %+v (cfg exists, db-creds is missing)", r.Uses)
	}
	if !find(r.UsedBy, "Service", "web", "") || !find(r.UsedBy, "Ingress", "web", "") {
		t.Errorf("usedBy = %+v", r.UsedBy)
	}
	imp, err := c.Impact(ctx(t), "nodes", "", "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(imp.Workloads) != 1 || imp.Workloads[0].Name != "web" || !find(imp.Services, "Service", "web", "bad") || !find(imp.Ingresses, "Ingress", "web", "bad") {
		t.Errorf("impact of node-a = %+v", imp)
	}
	imp, err = c.Impact(ctx(t), "deployments", "shop", "web")
	if err != nil || len(imp.Workloads) != 1 || imp.Workloads[0].Note != "2 pods" || !find(imp.Services, "Service", "web", "bad") || len(imp.Summary) < 2 {
		t.Errorf("impact of deployment web = %+v, %v", imp, err)
	}
}

func TestTimelineShowsRollouts(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	l, err := c.Timeline(ctx(t), "shop", 100000*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ok := false
	for _, it := range l {
		if it.Type == "rollout" && it.Object == "Deployment/web" {
			ok = true
		}
	}
	if !ok {
		t.Errorf("timeline = %+v", l)
	}
}

func TestSpread(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	r, err := c.Spread(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Zones) != 2 || r.Zones[0].Name != "eu-west-1a" || r.Zones[0].Pods != 1 || r.Zones[1].Nodes != 1 {
		t.Errorf("zones = %+v", r.Zones)
	}
	if len(r.Nodes) != 2 || r.Nodes[0].Name != "node-a" || r.Nodes[0].Pods != 1 || r.Nodes[0].Pool != "general" || r.Nodes[0].CPUReq != 100 || !r.Nodes[1].Cordoned {
		t.Errorf("nodes = %+v", r.Nodes)
	}
	if len(r.Workloads) != 1 || r.Workloads[0].Workload != "Deployment/web" || r.Workloads[0].Level != "ok" || len(r.Workloads[0].ByZone) != 2 {
		t.Errorf("workloads = %+v", r.Workloads)
	}
}

func TestJudgeSpread(t *testing.T) {
	zones := []string{"a", "b", "c"}
	for _, tc := range []struct {
		pods           int
		z, n           map[string]int
		level, verdict string
	}{
		{1, map[string]int{"a": 1}, map[string]int{"n1": 1}, "ok", "single pod"},
		{3, map[string]int{"a": 3}, map[string]int{"n1": 3}, "bad", "all pods on one node"},
		{3, map[string]int{"a": 3}, map[string]int{"n1": 1, "n2": 2}, "warn", "all pods in one zone"},
		{3, map[string]int{"a": 2, "b": 1}, map[string]int{"n1": 1, "n2": 1, "n3": 1}, "warn", "not in every zone"},
		{3, map[string]int{"a": 1, "b": 1, "c": 1}, map[string]int{"n1": 1, "n2": 1, "n3": 1}, "ok", "spread"},
	} {
		l, v := judge(tc.pods, tc.z, tc.n, zones)
		if l != tc.level || v != tc.verdict {
			t.Errorf("judge(%d,%v) = %s %q, want %s %q", tc.pods, tc.z, l, v, tc.level, tc.verdict)
		}
	}
}

func TestTakeSample(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	smp, err := c.TakeSample(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if smp.Pods != 2 || smp.PodsBad != 1 || smp.Nodes != 2 || smp.NodesReady != 2 || smp.NsBad["shop"] != 1 || smp.CPUReq != 100 {
		t.Errorf("sample = %+v", smp)
	}
	got := map[string]string{}
	for _, p := range smp.Problems {
		got[p.Kind+"/"+p.Name] = p.Level + ":" + p.Reason
	}
	if got["Pod/web-abc-2"] != "bad:CrashLoopBackOff" || got["Deployment/web"] != "warn:1 of 2 ready" {
		t.Errorf("problems = %+v", got)
	}
}

func TestGatewayAPI(t *testing.T) {
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	r, err := c.GatewayAPI(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Classes) != 1 || r.Classes[0].Controller != "istio.io/gateway-controller" || r.Classes[0].Accepted != "True" {
		t.Errorf("classes = %+v", r.Classes)
	}
	if len(r.Routes) != 1 {
		t.Fatalf("routes = %+v", r.Routes)
	}
	x := r.Routes[0]
	if x.Kind != "HTTPRoute" || x.Accepted != "True" || x.ResolvedRefs != "False" || x.Message != "backend ghost not found" || len(x.Backends) != 2 ||
		x.Backends[0].Missing || x.Backends[0].Weight != 90 || !x.Backends[1].Missing || x.Parents[0] != "istio-system/gw" {
		t.Errorf("route = %+v", x)
	}
}

func TestClassifyPolicyReports(t *testing.T) {
	cols := []Column{{Name: "Pass"}, {Name: "Fail"}, {Name: "Warn"}, {Name: "Error"}, {Name: "Skip"}}
	for _, c := range []struct {
		cells  []string
		health string
	}{
		{[]string{"10", "0", "0", "0", "0"}, "ok"},
		{[]string{"10", "2", "0", "0", "0"}, "bad"},
		{[]string{"10", "0", "0", "1", "0"}, "bad"},
		{[]string{"10", "0", "3", "0", "0"}, "warn"},
	} {
		if h, _ := Classify("policyreports", cols, c.cells); h != c.health {
			t.Errorf("%v = %s, want %s", c.cells, h, c.health)
		}
	}
}
