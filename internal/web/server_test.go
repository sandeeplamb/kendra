package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sandeeplamb/kendra/internal/kube"
)

func newTestServer(t *testing.T) (*httptest.Server, *server) {
	t.Helper()
	cfg := "apiVersion: v1\nkind: Config\ncurrent-context: c1\nclusters:\n- name: f\n  cluster: {server: 'http://127.0.0.1:1'}\ncontexts:\n- name: c1\n  context: {cluster: f, user: u}\nusers:\n- name: u\n  user: {}\n"
	p := filepath.Join(t.TempDir(), "kc")
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", p)
	t.Setenv("KND_AUDIT_LOG", filepath.Join(t.TempDir(), "audit.log"))
	m, err := kube.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	s := &server{mgr: m, token: "tok"}
	ts := httptest.NewUnstartedServer(nil)
	ts.Config.Handler = s.routes()
	ts.Start()
	s.host = strings.TrimPrefix(ts.URL, "http://")
	t.Cleanup(ts.Close)
	return ts, s
}

func do(t *testing.T, ts *httptest.Server, method, path, host string, cookie bool, hdr map[string]string, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if host != "" {
		req.Host = host
	}
	if cookie {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: "tok"})
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestGuard(t *testing.T) {
	ts, _ := newTestServer(t)
	if r, _ := do(t, ts, "GET", "/api/contexts", "", false, nil, ""); r.StatusCode != 401 {
		t.Errorf("no cookie: %d, want 401", r.StatusCode)
	}
	if r, _ := do(t, ts, "GET", "/api/contexts", "evil.example.com", true, nil, ""); r.StatusCode != 403 {
		t.Errorf("rebinding host: %d, want 403", r.StatusCode)
	}
	if r, _ := do(t, ts, "GET", "/?t=wrong", "", false, nil, ""); r.StatusCode != 403 {
		t.Errorf("bad token: %d, want 403", r.StatusCode)
	}
	r, _ := do(t, ts, "GET", "/?t=tok", "", false, nil, "")
	if r.StatusCode != 302 || !strings.Contains(r.Header.Get("Set-Cookie"), "HttpOnly") || !strings.Contains(r.Header.Get("Set-Cookie"), "SameSite=Strict") {
		t.Errorf("token exchange: %d %q", r.StatusCode, r.Header.Get("Set-Cookie"))
	}
	r, b := do(t, ts, "GET", "/api/contexts", "", true, nil, "")
	if r.StatusCode != 200 || !strings.Contains(b, `"c1"`) {
		t.Errorf("contexts: %d %s", r.StatusCode, b)
	}
	r, b = do(t, ts, "GET", "/", "", true, nil, "")
	if r.StatusCode != 200 || !strings.Contains(b, "Kendra") {
		t.Errorf("index: %d", r.StatusCode)
	}
}

// Kendra is read-only: there is no endpoint that deletes, scales, restarts or cordons, so
// even a request with the cookie and header cannot change a cluster.
func TestThereAreNoChangeEndpoints(t *testing.T) {
	ts, _ := newTestServer(t)
	h := map[string]string{"X-Knd": "1"}
	body := `{"context":"c1","kind":"deployments","namespace":"a","name":"web","confirm":"web","replicas":0}`
	for _, p := range []string{"/api/delete", "/api/scale", "/api/restart", "/api/cordon", "/api/apply", "/api/edit", "/api/create"} {
		for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			if r, _ := do(t, ts, m, p, "", true, h, body); r.StatusCode < 400 {
				t.Errorf("%s %s answered %d: a change endpoint exists", m, p, r.StatusCode)
			}
		}
	}
	if _, b := do(t, ts, "GET", "/api/info", "", true, nil, ""); !strings.Contains(b, `"readOnly":true`) {
		t.Errorf("info must say read-only: %s", b)
	}
}

// A guard against regressions: no code that talks to the cluster may call a write verb.
func TestSourceNeverCallsAWriteVerb(t *testing.T) {
	re := regexp.MustCompile(`\.(Delete|DeleteCollection|Patch|Create|Update|UpdateStatus|Apply|Evict|Bind)\(`)
	for _, dir := range []string{"../kube", "."} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, _ := os.ReadFile(f)
			if loc := re.FindIndex(b); loc != nil {
				t.Errorf("%s calls a write verb: %q", f, b[loc[0]:loc[1]])
			}
		}
	}
}

func TestNormalizeGroupsSimilarMessages(t *testing.T) {
	a := normalize("Back-off restarting failed container web-7d9f8c6b5-abcde in pod web-7d9f8c6b5-abcde")
	b := normalize("Back-off restarting failed container web-7d9f8c6b5-zzzzz in pod web-7d9f8c6b5-zzzzz")
	if a != b {
		t.Errorf("%q != %q", a, b)
	}
}

func TestForwardEndpointsValidateAndNeedHeader(t *testing.T) {
	ts, _ := newTestServer(t)
	h := map[string]string{"X-Knd": "1"}
	if r, b := do(t, ts, "GET", "/api/forwards", "", true, nil, ""); r.StatusCode != 200 || strings.TrimSpace(b) != "[]" {
		t.Errorf("no forwards yet should be an empty list: %d %q", r.StatusCode, b)
	}
	if r, _ := do(t, ts, "POST", "/api/forward", "", true, nil, `{}`); r.StatusCode != 403 {
		t.Errorf("forward without X-Knd: %d, want 403", r.StatusCode)
	}
	if r, b := do(t, ts, "POST", "/api/forward", "", true, h, `{"context":"c1","kind":"pods"}`); r.StatusCode != 400 {
		t.Errorf("forward without namespace/name: %d %s", r.StatusCode, b)
	}
	if r, b := do(t, ts, "POST", "/api/unforward", "", true, h, `{"id":"nope"}`); r.StatusCode != 400 || !strings.Contains(b, "no such port forward") {
		t.Errorf("unforward unknown: %d %s", r.StatusCode, b)
	}
}

func TestListNeedsAKindOrAResource(t *testing.T) {
	ts, _ := newTestServer(t)
	if r, b := do(t, ts, "GET", "/api/list?context=c1", "", true, nil, ""); r.StatusCode != 400 || !strings.Contains(b, "kind or resource") {
		t.Errorf("list without kind: %d %s", r.StatusCode, b)
	}
	if r, b := do(t, ts, "GET", "/api/list?context=c1&kind=nope", "", true, nil, ""); r.StatusCode != 400 || !strings.Contains(b, "unknown kind") {
		t.Errorf("unknown kind: %d %s", r.StatusCode, b)
	}
}

func TestKindsEndpointKeepsRailOrder(t *testing.T) {
	ts, _ := newTestServer(t)
	_, b := do(t, ts, "GET", "/api/kinds", "", true, nil, "")
	for _, want := range []string{`"Node Autoscaling"`, `"Pod Autoscaling"`, `"Access Control"`, `"Kyverno"`, `"clusterpolicies"`, `"Custom Resources"`, `"nodeoverlays"`, `"leases"`} {
		if !strings.Contains(b, want) {
			t.Errorf("kinds lacks %s", want)
		}
	}
}

func TestHelmChartsWithoutLocalRepos(t *testing.T) {
	ts, _ := newTestServer(t)
	t.Setenv("HELM_CONFIG_HOME", t.TempDir())
	r, b := do(t, ts, "GET", "/api/helm/charts", "", true, nil, "")
	if r.StatusCode != 200 || !strings.Contains(b, "helm repo add") || !strings.Contains(b, `"charts":[]`) {
		t.Errorf("charts: %d %s", r.StatusCode, b)
	}
}
