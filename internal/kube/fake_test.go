package kube

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type obj = map[string]any

func meta(ns, name, uid string, labels obj, owners ...obj) obj {
	m := obj{"name": name, "uid": uid, "creationTimestamp": "2026-11-20T10:00:00Z"}
	if ns != "" {
		m["namespace"] = ns
	}
	if labels != nil {
		m["labels"] = labels
	}
	if len(owners) > 0 {
		m["ownerReferences"] = owners
	}
	return m
}

func withMF(m obj, manager, apiVersion string) obj {
	m["managedFields"] = []obj{{"manager": manager, "apiVersion": apiVersion, "operation": "Update"}}
	return m
}

func withAnn(m, ann obj) obj { m["annotations"] = ann; return m }

// stamp is the time d ago, as the API writes it; ago sets an object's creation time d ago.
func stamp(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339) }

func ago(m obj, d time.Duration) obj { m["creationTimestamp"] = stamp(d); return m }

func owner(kind, name, uid string) obj {
	return obj{"kind": kind, "name": name, "uid": uid, "apiVersion": "v1"}
}

// resource lists served per group/version: path prefix -> [{name, kind, namespaced}]
var disc = map[string][][3]any{
	"/api/v1":                            {{"configmaps", "ConfigMap", true}, {"serviceaccounts", "ServiceAccount", true}, {"secrets", "Secret", true}, {"services", "Service", true}, {"pods", "Pod", true}, {"namespaces", "Namespace", false}, {"nodes", "Node", false}},
	"/apis/apps/v1":                      {{"deployments", "Deployment", true}, {"replicasets", "ReplicaSet", true}, {"statefulsets", "StatefulSet", true}, {"daemonsets", "DaemonSet", true}},
	"/apis/batch/v1":                     {{"jobs", "Job", true}, {"cronjobs", "CronJob", true}},
	"/apis/networking.k8s.io/v1":         {{"ingresses", "Ingress", true}},
	"/apis/rbac.authorization.k8s.io/v1": {{"clusterroles", "ClusterRole", false}},
	"/apis/apiextensions.k8s.io/v1":      {{"customresourcedefinitions", "CustomResourceDefinition", false}},
	"/apis/coordination.k8s.io/v1":       {{"leases", "Lease", true}},
	"/apis/cert-manager.io/v1":           {{"certificates", "Certificate", true}},
	"/apis/gateway.networking.k8s.io/v1": {{"gatewayclasses", "GatewayClass", false}, {"gateways", "Gateway", true}, {"httproutes", "HTTPRoute", true}},
	"/apis/policy/v1beta1":               {{"poddisruptionbudgets", "PodDisruptionBudget", true}},
	"/apis/networking.istio.io/v1":       {{"gateways", "Gateway", true}, {"virtualservices", "VirtualService", true}, {"destinationrules", "DestinationRule", true}, {"serviceentries", "ServiceEntry", true}},
}

// data: resource name -> items
func fixtures() map[string][]obj {
	app := obj{"kapp.k14s.io/app": "1700000000000000000"}
	web := obj{"app": "web"}
	return map[string][]obj{
		"gatewayclasses": {
			{"kind": "GatewayClass", "metadata": meta("", "istio", "gc1", nil), "spec": obj{"controllerName": "istio.io/gateway-controller"}, "status": obj{"conditions": []any{obj{"type": "Accepted", "status": "True"}}}},
		},
		"httproutes": {
			{"kind": "HTTPRoute", "metadata": meta("shop", "web", "hr1", nil), "spec": obj{"hostnames": []any{"app.example.com"}, "parentRefs": []any{obj{"name": "gw", "namespace": "istio-system"}},
				"rules": []any{obj{"backendRefs": []any{obj{"name": "web", "port": 80, "weight": 90}, obj{"name": "ghost", "port": 80}}}}},
				"status": obj{"parents": []any{obj{"conditions": []any{obj{"type": "Accepted", "status": "True"}, obj{"type": "ResolvedRefs", "status": "False", "message": "backend ghost not found"}}}}}},
		},
		"serviceaccounts": {
			{"kind": "ServiceAccount", "metadata": withAnn(meta("shop", "web-sa", "sa1", nil), obj{"eks.amazonaws.com/role-arn": "arn:aws:iam::123456789012:role/web"})},
			{"kind": "ServiceAccount", "metadata": meta("shop", "default", "sa2", nil)},
		},
		"cronjobs": {
			{"kind": "CronJob", "metadata": ago(meta("batch", "backup", "cj1", nil), 30*24*time.Hour), "spec": obj{"schedule": "0 2 * * *"},
				"status": obj{"lastScheduleTime": stamp(10 * time.Hour), "lastSuccessfulTime": stamp(34 * time.Hour)}},
			{"kind": "CronJob", "metadata": ago(meta("batch", "report", "cj2", nil), 30*24*time.Hour), "spec": obj{"schedule": "0 6 * * 1", "suspend": true}},
			{"kind": "CronJob", "metadata": ago(meta("batch", "healthy", "cj3", nil), 30*24*time.Hour), "spec": obj{"schedule": "*/30 * * * *"},
				"status": obj{"lastScheduleTime": stamp(10 * time.Minute), "lastSuccessfulTime": stamp(10 * time.Minute)}},
		},
		"jobs": {
			{"kind": "Job", "metadata": ago(meta("batch", "backup-1", "j1", nil, owner("CronJob", "backup", "cj1")), 34*time.Hour),
				"status": obj{"startTime": stamp(34 * time.Hour), "completionTime": stamp(33 * time.Hour), "conditions": []any{obj{"type": "Complete", "status": "True"}}}},
			{"kind": "Job", "metadata": ago(meta("batch", "backup-2", "j2", nil, owner("CronJob", "backup", "cj1")), 10*time.Hour),
				"status": obj{"startTime": stamp(10 * time.Hour), "conditions": []any{obj{"type": "Failed", "status": "True", "reason": "BackoffLimitExceeded", "lastTransitionTime": stamp(9 * time.Hour)}}}},
			{"kind": "Job", "metadata": ago(meta("batch", "healthy-1", "j3", nil, owner("CronJob", "healthy", "cj3")), 10*time.Minute),
				"status": obj{"startTime": stamp(10 * time.Minute), "completionTime": stamp(9 * time.Minute), "conditions": []any{obj{"type": "Complete", "status": "True"}}}},
			{"kind": "Job", "metadata": ago(meta("tools", "migrate", "j4", nil), 5*time.Hour),
				"status": obj{"startTime": stamp(5 * time.Hour), "conditions": []any{obj{"type": "Failed", "status": "True", "reason": "DeadlineExceeded"}}}},
			{"kind": "Job", "metadata": ago(meta("tools", "stuck-import", "j5", nil), 48*time.Hour),
				"status": obj{"startTime": stamp(48 * time.Hour), "active": 1}},
		},
		"certificates": {
			{"kind": "Certificate", "metadata": meta("shop", "web-tls", "cert1", nil), "spec": obj{"secretName": "web-tls", "dnsNames": []any{"app.example.com"}, "issuerRef": obj{"name": "letsencrypt"}},
				"status": obj{"notAfter": time.Now().Add(10*24*time.Hour + time.Hour).UTC().Format(time.RFC3339), "conditions": []any{obj{"type": "Ready", "status": "True"}}}},
		},
		"poddisruptionbudgets": {
			{"kind": "PodDisruptionBudget", "metadata": withMF(meta("shop", "web-pdb", "pdb1", nil), "helm", "policy/v1beta1")},
			{"kind": "PodDisruptionBudget", "metadata": withMF(meta("shop", "api-pdb", "pdb2", nil), "kubectl", "policy/v1")},
		},
		"configmaps": {
			{"kind": "ConfigMap", "metadata": meta("kube-system", "platform.apps.k14s.io", "cm1", obj{"kapp.k14s.io/is-app": ""}),
				"data": obj{"spec": `{"labelKey":"kapp.k14s.io/app","labelValue":"1700000000000000000"}`}},
			{"kind": "ConfigMap", "metadata": meta("kube-system", "platform-change-abc12", "cm2", obj{"kapp.k14s.io/is-app-change": "", "kapp.k14s.io/app-change-app-label": "1700000000000000000"}),
				"data": obj{"spec": `{"startedAt":"2026-11-24T09:00:00Z","finishedAt":"2026-11-24T09:01:30Z","successful":true,"description":"update: resource","namespaces":["shop"]}`}},
			{"kind": "ConfigMap", "metadata": meta("shop", "cfg", "cm3", app)},
		},
		"namespaces": {
			{"kind": "Namespace", "metadata": meta("", "shop", "ns1", obj{"pod-security.kubernetes.io/enforce": "baseline"})},
			{"kind": "Namespace", "metadata": meta("", "kube-system", "ns2", nil)},
			{"kind": "Namespace", "metadata": meta("", "istio-system", "ns3", nil)},
		},
		"customresourcedefinitions": {
			{"kind": "CustomResourceDefinition", "metadata": meta("", "gateways.networking.istio.io", "crd1", nil),
				"spec": obj{"group": "networking.istio.io", "scope": "Namespaced", "names": obj{"kind": "Gateway", "plural": "gateways"},
					"versions": []any{obj{"name": "v1", "served": true}, obj{"name": "v1beta1", "served": true}}},
				"status": obj{"conditions": []any{obj{"type": "Established", "status": "True"}}}},
			{"kind": "CustomResourceDefinition", "metadata": meta("", "certificates.cert-manager.io", "crd2", nil),
				"spec": obj{"group": "cert-manager.io", "scope": "Namespaced", "names": obj{"kind": "Certificate", "plural": "certificates"},
					"versions": []any{obj{"name": "v1", "served": true}}}},
		},
		"nodes": {
			{"kind": "Node", "metadata": meta("", "node-a", "n1", obj{"karpenter.sh/nodepool": "general", "topology.kubernetes.io/zone": "eu-west-1a"}),
				"status": obj{"conditions": []any{obj{"type": "Ready", "status": "True"}}, "allocatable": obj{"cpu": "4", "memory": "16Gi"}}},
			{"kind": "Node", "metadata": meta("", "node-b", "n2", obj{"karpenter.sh/nodepool": "general", "topology.kubernetes.io/zone": "eu-west-1b"}), "spec": obj{"unschedulable": true},
				"status": obj{"conditions": []any{obj{"type": "Ready", "status": "True"}}, "allocatable": obj{"cpu": "4", "memory": "16Gi"}}},
		},
		"secrets": {
			{"kind": "Secret", "metadata": meta("shop", "sh.helm.release.v1.redis.v1", "hs1", obj{"owner": "helm", "name": "redis", "status": "superseded", "version": "1"})},
			{"kind": "Secret", "metadata": meta("shop", "sh.helm.release.v1.redis.v2", "hs2", obj{"owner": "helm", "name": "redis", "status": "deployed", "version": "2"})},
		},
		"deployments": {
			{"kind": "Deployment", "metadata": meta("cert-manager", "cert-manager", "dep9", obj{"app.kubernetes.io/name": "cert-manager"}),
				"spec": obj{"replicas": 1, "template": obj{"spec": obj{"containers": []any{obj{"name": "c", "image": "quay.io/jetstack/cert-manager-controller:v1.15.1"}}}}}, "status": obj{"readyReplicas": 1}},
			{"kind": "Deployment", "metadata": withAnn(meta("shop", "cache", "dep3", obj{"app.kubernetes.io/managed-by": "Helm", "helm.sh/chart": "redis-18.1.0", "app.kubernetes.io/version": "7.2.4"}), obj{"meta.helm.sh/release-name": "redis", "meta.helm.sh/release-namespace": "shop"}),
				"spec": obj{"replicas": 1}, "status": obj{"readyReplicas": 1}},
			{"kind": "Deployment", "metadata": meta("shop", "web", "dep1", app),
				"spec": obj{"replicas": 2, "selector": obj{"matchLabels": web}, "template": obj{"metadata": obj{"labels": web}, "spec": obj{
					"volumes":    []any{obj{"configMap": obj{"name": "cfg"}}},
					"containers": []any{obj{"name": "c", "image": "reg.io/web:1.2", "envFrom": []any{obj{"secretRef": obj{"name": "db-creds"}}}}}}}},
				"status": obj{"readyReplicas": 1}},
			{"kind": "Deployment", "metadata": meta("tools", "handmade", "dep2", nil), "spec": obj{"replicas": 1}, "status": obj{"readyReplicas": 1}},
		},
		"replicasets": {{"kind": "ReplicaSet", "metadata": meta("shop", "web-abc", "rs1", nil, owner("Deployment", "web", "dep1")), "spec": obj{"replicas": 2}, "status": obj{"readyReplicas": 1}}},
		"pods": {
			{"kind": "Pod", "metadata": meta("shop", "web-abc-1", "p1", web, owner("ReplicaSet", "web-abc", "rs1")),
				"spec": obj{"nodeName": "node-a", "serviceAccountName": "web-sa", "containers": []any{obj{"name": "c", "image": "reg.io/web:1.2", "resources": obj{"requests": obj{"cpu": "100m", "memory": "64Mi"}, "limits": obj{"memory": "128Mi"}}}}}, "status": obj{"phase": "Running", "conditions": []any{obj{"type": "Ready", "status": "True"}}}},
			{"kind": "Pod", "metadata": meta("shop", "web-abc-2", "p2", web, owner("ReplicaSet", "web-abc", "rs1")),
				"spec": obj{"nodeName": "node-b", "serviceAccountName": "web-sa", "hostNetwork": true, "containers": []any{obj{"name": "c", "image": "reg.io/web:1.2", "securityContext": obj{"privileged": true}}}}, "status": obj{"phase": "Running", "conditions": []any{obj{"type": "Ready", "status": "False"}},
					"containerStatuses": []any{obj{"name": "c", "state": obj{"waiting": obj{"reason": "CrashLoopBackOff"}}}}}},
		},
		"services": {
			{"kind": "Service", "metadata": meta("shop", "web", "svc1", app), "spec": obj{"selector": web, "ports": []any{obj{"port": 80, "targetPort": 8080}}}},
		},
		"ingresses": {
			{"kind": "Ingress", "metadata": meta("shop", "web", "ing1", app), "spec": obj{"rules": []any{obj{"http": obj{"paths": []any{obj{"backend": obj{"service": obj{"name": "web"}}}}}}}}},
		},
		"clusterroles": {{"kind": "ClusterRole", "metadata": meta("", "web-reader", "cr1", app)}},
		"gateways": {
			{"kind": "Gateway", "metadata": meta("istio-system", "gw", "g1", nil), "spec": obj{"selector": obj{"istio": "ingressgateway"}, "servers": []any{obj{"port": obj{"number": 443, "protocol": "HTTPS"}, "hosts": []any{"app.example.com"}}}}},
		},
		"virtualservices": {
			{"kind": "VirtualService", "metadata": meta("shop", "web", "vs1", nil), "spec": obj{"hosts": []any{"app.example.com"}, "gateways": []any{"istio-system/gw", "istio-system/gone"},
				"http": []any{obj{"match": []any{obj{"uri": obj{"prefix": "/api"}}}, "route": []any{
					obj{"destination": obj{"host": "web.shop.svc.cluster.local", "subset": "v1", "port": obj{"number": 80}}, "weight": 90},
					obj{"destination": obj{"host": "api.stripe.com"}}}}}}},
		},
		"destinationrules": {
			{"kind": "DestinationRule", "metadata": meta("shop", "web", "dr1", nil), "spec": obj{"host": "web", "subsets": []any{obj{"name": "v1", "labels": obj{"version": "v1"}}}}},
		},
	}
}

func labelMatch(sel string, labels map[string]any) bool {
	if sel == "" {
		return true
	}
	for _, term := range strings.Split(sel, ",") {
		switch {
		case strings.HasPrefix(term, "!"):
			if _, ok := labels[term[1:]]; ok {
				return false
			}
		case strings.Contains(term, "="):
			kv := strings.SplitN(strings.ReplaceAll(term, "==", "="), "=", 2)
			if labels[kv[0]] != kv[1] {
				return false
			}
		default:
			if _, ok := labels[term]; !ok {
				return false
			}
		}
	}
	return true
}

func fakeAPI(t *testing.T) *Manager {
	t.Helper()
	data := fixtures()
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch p {
		case "/version":
			write(w, obj{"major": "1", "minor": "31+", "gitVersion": "v1.31.3-eks-abc"})
			return
		case "/metrics":
			_, _ = w.Write([]byte("# HELP x\napiserver_requested_deprecated_apis{group=\"policy\",removed_release=\"1.32\",resource=\"poddisruptionbudgets\",subresource=\"\",version=\"v1beta1\"} 1\napiserver_requested_deprecated_apis{group=\"\",removed_release=\"1.40\",resource=\"widgets\",subresource=\"\",version=\"v1alpha9\"} 1\n"))
			return
		case "/api":
			write(w, obj{"kind": "APIVersions", "versions": []string{"v1"}})
			return
		case "/apis":
			var groups []any
			for prefix := range disc {
				if !strings.HasPrefix(prefix, "/apis/") {
					continue
				}
				gv := strings.TrimPrefix(prefix, "/apis/")
				g := strings.SplitN(gv, "/", 2)
				v := obj{"groupVersion": gv, "version": g[1]}
				groups = append(groups, obj{"name": g[0], "versions": []any{v}, "preferredVersion": v})
			}
			write(w, obj{"kind": "APIGroupList", "apiVersion": "v1", "groups": groups})
			return
		}
		if rl, ok := disc[p]; ok {
			var res []any
			for _, x := range rl {
				res = append(res, obj{"name": x[0], "kind": x[1], "namespaced": x[2], "verbs": []string{"get", "list", "delete", "patch"}})
			}
			gv := strings.TrimPrefix(strings.TrimPrefix(p, "/apis/"), "/api/")
			write(w, obj{"kind": "APIResourceList", "groupVersion": gv, "resources": res})
			return
		}
		// collection: .../[namespaces/<ns>/]<resource>
		ns := ""
		segs := strings.Split(strings.Trim(p, "/"), "/")
		if i := indexOf(segs, "namespaces"); i >= 0 && i+2 < len(segs) {
			ns, segs = segs[i+1], append(append([]string{}, segs[:i]...), segs[i+2:]...)
		}
		res := segs[len(segs)-1]
		if len(segs) >= 2 {
			if items, ok := data[segs[len(segs)-2]]; ok { // GET one object by name
				for _, it := range items {
					m := it["metadata"].(obj)
					if m["name"] == res && (ns == "" || m["namespace"] == ns) {
						write(w, it)
						return
					}
				}
				w.WriteHeader(http.StatusNotFound)
				write(w, obj{"kind": "Status", "apiVersion": "v1", "status": "Failure", "reason": "NotFound", "code": 404})
				return
			}
		}
		listKind := ""
		for _, rl := range disc {
			for _, x := range rl {
				if x[0] == res {
					listKind = x[1].(string) + "List"
				}
			}
		}
		if listKind == "" {
			http.NotFound(w, r)
			return
		}
		items := data[res]
		sel := r.URL.Query().Get("labelSelector")
		out := []any{}
		for _, it := range items {
			m := it["metadata"].(obj)
			if ns != "" && m["namespace"] != ns {
				continue
			}
			lb, _ := m["labels"].(obj)
			if !labelMatch(sel, lb) {
				continue
			}
			out = append(out, it)
		}
		if strings.Contains(r.Header.Get("Accept"), "as=Table") {
			cols := []any{obj{"name": "Name", "type": "string"}, obj{"name": "Age", "type": "string"}}
			if res == "pods" {
				cols = []any{obj{"name": "Name", "type": "string"}, obj{"name": "Ready", "type": "string"}, obj{"name": "Status", "type": "string"}, obj{"name": "Restarts", "type": "integer"}, obj{"name": "Age", "type": "string"}}
			}
			var rows []any
			for _, o := range out {
				it := o.(obj)
				m := it["metadata"].(obj)
				cells := []any{m["name"], "4d"}
				if res == "pods" {
					st, rd, rs := "Running", "1/1", 0
					if _, bad := it["status"].(obj)["containerStatuses"]; bad {
						st, rd, rs = "CrashLoopBackOff", "0/1", 14
					}
					cells = []any{m["name"], rd, st, rs, "4d"}
				}
				rows = append(rows, obj{"cells": cells, "object": obj{"metadata": m}})
			}
			write(w, obj{"kind": "Table", "apiVersion": "meta.k8s.io/v1", "columnDefinitions": cols, "rows": rows})
			return
		}
		listGV := "v1"
		if strings.HasPrefix(p, "/apis/") {
			g := strings.SplitN(strings.TrimPrefix(p, "/apis/"), "/", 3)
			listGV = g[0] + "/" + g[1]
		}
		write(w, obj{"kind": listKind, "apiVersion": listGV, "metadata": obj{}, "items": out})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := "apiVersion: v1\nkind: Config\ncurrent-context: fake-prod\nclusters:\n- name: f\n  cluster: {server: " + srv.URL + "}\ncontexts:\n- name: fake-prod\n  context: {cluster: f, user: u}\nusers:\n- name: u\n  user: {}\n"
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	m, err := NewManager()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// TestServeFake keeps the fake cluster running so the UI can be tried by hand:
//
//	KND_FAKE_DEMO=/tmp/kc go test ./internal/kube -run TestServeFake -timeout 2h
//	KUBECONFIG=/tmp/kc knd
func TestServeFake(t *testing.T) {
	out := os.Getenv("KND_FAKE_DEMO")
	if out == "" {
		t.Skip("set KND_FAKE_DEMO=<kubeconfig path> to run")
	}
	fakeAPI(t)
	b, _ := os.ReadFile(os.Getenv("KUBECONFIG"))
	if err := os.WriteFile(out, b, 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Hour)
}
