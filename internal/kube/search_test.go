package kube

import "testing"

func searchHits(t *testing.T, term, ns string, scopes ...string) []SearchHit {
	t.Helper()
	m := fakeAPI(t)
	c, _ := m.Get("fake-prod")
	r, err := c.Search(ctx(t), term, scopes, ns)
	if err != nil {
		t.Fatal(err)
	}
	return r.Hits
}

func hasHit(hits []SearchHit, kind, name, scope, match string) bool {
	for _, h := range hits {
		if h.Kind == kind && h.Name == name && h.Scope == scope && (match == "" || h.Match == match) {
			return true
		}
	}
	return false
}

func TestSearchImage(t *testing.T) {
	h := searchHits(t, "CERT-MANAGER-controller", "", "image")
	if !hasHit(h, "Deployment", "cert-manager", "image", "image quay.io/jetstack/cert-manager-controller:v1.15.1") || len(h) != 1 {
		t.Errorf("hits = %+v", h)
	}
}

func TestSearchNameCoversKindsAndRespectsScopes(t *testing.T) {
	h := searchHits(t, "web", "", "name")
	for _, k := range []string{"Deployment", "Service", "Ingress"} {
		if !hasHit(h, k, "web", "name", "") {
			t.Errorf("no %s named web in %+v", k, h)
		}
	}
	// the same text with only the image scope must not return name hits
	for _, x := range searchHits(t, "web", "", "image") {
		if x.Scope != "image" {
			t.Errorf("unexpected scope in %+v", x)
		}
	}
}

func TestSearchLabelAndEnvAndNamespace(t *testing.T) {
	if h := searchHits(t, "managed-by=helm", "", "label"); !hasHit(h, "Deployment", "cache", "label", "label app.kubernetes.io/managed-by=Helm") {
		t.Errorf("label hits = %+v", h)
	}
	// env: only names of ConfigMaps/Secrets used with envFrom, never values
	if h := searchHits(t, "db-creds", "", "env"); !hasHit(h, "Deployment", "web", "env", "envFrom Secret db-creds in c") {
		t.Errorf("env hits = %+v", h)
	}
	for _, x := range searchHits(t, "handmade", "tools", "name") {
		if x.Namespace != "tools" {
			t.Errorf("namespace filter leaked %+v", x)
		}
	}
	if h := searchHits(t, "handmade", "tools", "name"); !hasHit(h, "Deployment", "handmade", "name", "") {
		t.Errorf("namespace search = %+v", h)
	}
}

func TestSearchAnnotationSkipsLastApplied(t *testing.T) {
	s := &searcher{term: "secret", scopes: map[string]bool{"annotation": true}, res: &SearchResult{Hits: []SearchHit{}}, seen: map[SearchHit]bool{}}
	s.maps("Deployment", "shop", "web", "", nil, map[string]string{
		"kubectl.kubernetes.io/last-applied-configuration": `{"data":{"password":"secret"}}`,
		"note": "rotate secret monthly",
	})
	if len(s.res.Hits) != 1 || s.res.Hits[0].Match != "annotation note=rotate secret monthly" {
		t.Errorf("hits = %+v", s.res.Hits)
	}
}
