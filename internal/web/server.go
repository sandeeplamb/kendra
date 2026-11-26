// Package web serves the Kendra UI and its JSON API on 127.0.0.1 only.
package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/sandeeplamb/kendra/internal/kube"
	"github.com/sandeeplamb/kendra/internal/version"
)

//go:embed static
var staticFS embed.FS

const cookieName = "knd_token"

// Options configure the server.
type Options struct {
	Addr string
	Open bool
	// Window, when set, shows the UI in a native window instead of a browser. It
	// blocks until the window is closed, and Serve then returns nil. It must run on
	// the main goroutine (Cocoa), so the HTTP server runs in the background.
	Window func(url string)
}

type server struct {
	mgr   *kube.Manager
	token string
	host  string
	fwd   *kube.Forwards
	hist  *history
}

// Serve starts the UI and blocks.
func Serve(mgr *kube.Manager, o Options) error {
	ln, err := net.Listen("tcp", o.Addr)
	if err != nil {
		return err
	}
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	if host != "127.0.0.1" && host != "::1" && host != "localhost" {
		ln.Close()
		return fmt.Errorf("refusing to listen on %s: Kendra only listens on 127.0.0.1", host)
	}
	s := &server{mgr: mgr, token: randomToken(), host: net.JoinHostPort(host, port), hist: newHistory()}
	sctx, stop := context.WithCancel(context.Background())
	defer stop()
	go s.runSampler(sctx)
	url := "http://" + s.host + "/?t=" + s.token
	fmt.Println("Kendra is running at", url)
	fmt.Println("Kendra is read-only: it never changes anything in your clusters.")
	srv := &http.Server{Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	if o.Open && o.Window != nil {
		errc := make(chan error, 1)
		go func() { errc <- srv.Serve(ln) }()
		o.Window(url)
		select {
		case err := <-errc:
			return err
		default:
			_ = srv.Close()
			return nil
		}
	}
	if o.Open {
		go openApp(url)
	}
	return srv.Serve(ln)
}

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// openApp opens the UI as an app window in Chrome, Edge or Brave when one exists,
// otherwise in the default browser.
func openApp(url string) {
	switch runtime.GOOS {
	case "darwin":
		for _, app := range []string{"Google Chrome", "Microsoft Edge", "Brave Browser", "Chromium"} {
			if exec.Command("open", "-Ra", app).Run() == nil {
				args := []string{"-na", app, "--args", "--app=" + url, "--no-first-run", "--no-default-browser-check"}
				if home, err := os.UserHomeDir(); err == nil {
					// own profile: a separate browser instance, so the window is not tied to your normal Chrome
					args = append(args, "--user-data-dir="+filepath.Join(home, ".kendra", "chrome-profile"))
				}
				if exec.Command("open", args...).Start() == nil {
					return
				}
			}
		}
		_ = exec.Command("open", url).Start()
	case "windows":
		for _, b := range []string{"msedge", "chrome"} {
			if exec.Command("cmd", "/c", "start", "", b, "--app="+url).Start() == nil {
				return
			}
		}
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		for _, b := range []string{"google-chrome", "chromium", "microsoft-edge", "brave-browser"} {
			if p, err := exec.LookPath(b); err == nil {
				if exec.Command(p, "--app="+url).Start() == nil {
					return
				}
			}
		}
		_ = exec.Command("xdg-open", url).Start()
	}
}

func (s *server) routes() http.Handler {
	if s.fwd == nil {
		s.fwd = kube.NewForwards()
	}
	if s.hist == nil {
		s.hist = newHistory()
	}
	sub, _ := fs.Sub(staticFS, "static")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(sub)))
	api := func(pattern string, h func(*http.Request) (any, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			v, err := h(r)
			if err != nil {
				writeErr(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(v)
		})
	}
	api("GET /api/info", func(*http.Request) (any, error) {
		return map[string]any{"version": version.Number, "readOnly": true, "audit": kube.AuditPath(), "nativeSave": SaveFile != nil}, nil
	})
	api("GET /api/contexts", func(*http.Request) (any, error) { return s.mgr.Contexts() })
	api("GET /api/kinds", func(*http.Request) (any, error) { return kube.Kinds, nil })
	api("GET /api/namespaces", s.namespaces)
	api("GET /api/list", s.list)
	api("GET /api/yaml", s.yaml)
	api("GET /api/explain", s.explain)
	api("GET /api/events", s.objectEvents)
	api("GET /api/warnings", s.warnings)
	api("GET /api/containers", s.containers)
	api("GET /api/kapp/apps", s.kappApps)
	api("GET /api/kapp/app", s.kappApp)
	api("GET /api/kapp/unmanaged", s.kappUnmanaged)
	api("GET /api/istio", s.istio)
	api("GET /api/overview", s.overview)
	api("GET /api/fleet", s.fleet)
	api("GET /api/usage", s.usage)
	api("GET /api/images", s.images)
	api("GET /api/security", s.security)
	api("GET /api/addons", s.addons)
	api("GET /api/compat", s.compat)
	api("GET /api/search", s.search)
	api("GET /api/cronjobs", s.cronjobs)
	api("GET /api/spread", s.spread)
	api("GET /api/gateway", s.gateway)
	api("GET /api/related", s.related)
	api("GET /api/impact", s.impact)
	api("GET /api/incident", s.incident)
	api("GET /api/timeline", s.timeline)
	api("GET /api/identities", s.identities)
	api("GET /api/certificates", s.certificates)
	api("GET /api/snapshot", s.snapshot)
	api("POST /api/save", s.save)
	api("POST /api/watch", s.watch)
	api("GET /api/history", s.historyAPI)
	api("GET /api/timemachine", s.timemachine)
	api("GET /api/deprecated", s.deprecated)
	api("GET /api/crds", s.crds)
	api("GET /api/helm/charts", s.helmCharts)
	api("GET /api/forwards", func(*http.Request) (any, error) { return s.fwd.List(), nil })
	api("POST /api/forward", s.forward)
	api("POST /api/unforward", s.unforward)
	api("GET /api/topology/cluster", s.topoCluster)
	api("GET /api/topology/namespace", s.topoNamespace)
	api("GET /api/helm/releases", s.helmReleases)
	api("GET /api/helm/release", s.helmRelease)
	mux.HandleFunc("GET /api/logs", s.logs)
	return s.guard(mux)
}

// guard is the security boundary: Host must be our own address (stops DNS rebinding),
// every request needs the per-run token cookie, and writes need a custom header, which
// a web page on another origin cannot send without a CORS preflight we never allow.
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Host
		if h != s.host && h != "localhost:"+portOf(s.host) {
			http.Error(w, "bad host", http.StatusForbidden)
			return
		}
		if t := r.URL.Query().Get("t"); t != "" && r.Method == http.MethodGet && r.URL.Path == "/" {
			if subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) != 1 {
				http.Error(w, "bad token", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: s.token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		ck, err := r.Cookie(cookieName)
		if err != nil || subtle.ConstantTimeCompare([]byte(ck.Value), []byte(s.token)) != 1 {
			http.Error(w, "Open Kendra with the address printed in the terminal (it contains a one-time token).", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-Knd") != "1" {
			http.Error(w, "missing header", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func portOf(hostport string) string { _, p, _ := net.SplitHostPort(hostport); return p }

type httpError struct {
	code int
	msg  string
}

func (e httpError) Error() string { return e.msg }

func bad(format string, a ...any) error {
	return httpError{http.StatusBadRequest, fmt.Sprintf(format, a...)}
}

func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusBadGateway
	var he httpError
	switch {
	case errors.As(err, &he):
		code = he.code
	case apierrors.IsForbidden(err):
		code = http.StatusForbidden
	case apierrors.IsNotFound(err):
		code = http.StatusNotFound
	case errors.Is(err, context.DeadlineExceeded):
		code = http.StatusGatewayTimeout
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": friendly(err)})
}

// friendly shortens the common, noisy client-go errors.
func friendly(err error) string {
	m := err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "The cluster did not answer in time."
	case strings.Contains(m, "getting credentials"):
		return "Login for this cluster failed or expired: " + firstLine(m)
	case strings.Contains(m, "connection refused"), strings.Contains(m, "no such host"), strings.Contains(m, "i/o timeout"):
		return "Cannot reach the cluster: " + firstLine(m)
	}
	return firstLine(m)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

func q(r *http.Request, k string) string { return r.URL.Query().Get(k) }

func (s *server) cluster(r *http.Request) (*kube.Cluster, error) {
	name := q(r, "context")
	if name == "" {
		return nil, bad("context is required")
	}
	return s.mgr.Get(name)
}

func (s *server) ctx(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}

func (s *server) namespaces(r *http.Request) (any, error) {
	c, err := s.cluster(r)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.ctx(r, 20*time.Second)
	defer cancel()
	l, err := c.Cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, n := range l.Items {
		out = append(out, n.Name)
	}
	sort.Strings(out)
	return out, nil
}

// perCluster runs fn for one context, or for every context when context=*.
type clusterResult struct {
	Cluster string `json:"cluster"`
	Error   string `json:"error,omitempty"`
	Data    any    `json:"data,omitempty"`
	Skipped int    `json:"skipped,omitempty"`
}

func (s *server) each(r *http.Request, d time.Duration, fn func(context.Context, *kube.Cluster) (any, int, error)) ([]clusterResult, error) {
	names := []string{q(r, "context")}
	if strings.Contains(names[0], ",") { // a group of clusters picked in the UI
		names = strings.Split(names[0], ",")
	} else if names[0] == "*" {
		cs, err := s.mgr.Contexts()
		if err != nil {
			return nil, err
		}
		names = names[:0]
		for _, c := range cs {
			names = append(names, c.Name)
		}
	} else if names[0] == "" {
		return nil, bad("context is required")
	}
	out := make([]clusterResult, len(names))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i, n := range names {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out[i].Cluster = n
			c, err := s.mgr.Get(n)
			if err != nil {
				out[i].Error = friendly(err)
				return
			}
			ctx, cancel := s.ctx(r, d)
			defer cancel()
			data, skipped, err := fn(ctx, c)
			if err != nil {
				out[i].Error = friendly(err)
				return
			}
			out[i].Data, out[i].Skipped = data, skipped
		}()
	}
	wg.Wait()
	return out, nil
}

func (s *server) list(r *http.Request) (any, error) {
	name, group, resource, ns := q(r, "kind"), q(r, "group"), q(r, "resource"), q(r, "ns")
	var cat kube.Kind
	if name != "" {
		k, ok := kube.FindKind(name)
		if !ok {
			return nil, bad("unknown kind %q", name)
		}
		cat = k
	} else if resource == "" {
		return nil, bad("kind or resource is required")
	}
	return s.each(r, 25*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		k := cat
		if name == "" {
			var err error
			if k, err = c.KindFor(group, resource); err != nil {
				return nil, 0, err
			}
		}
		t, err := c.List(ctx, k, ns)
		return t, 0, err
	})
}

func (s *server) yaml(r *http.Request) (any, error) {
	c, err := s.cluster(r)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.ctx(r, 20*time.Second)
	defer cancel()
	var path string
	isSecret := false
	if k, ok := kube.FindKind(q(r, "kind")); ok {
		if k, err = c.Resolve(k); err != nil {
			return nil, err
		}
		path, isSecret = k.Path(q(r, "ns"), q(r, "name")), k.Name == "secrets"
	} else if res := q(r, "resource"); res != "" {
		if path, err = c.PathFor(q(r, "group"), q(r, "version"), res, q(r, "ns"), q(r, "name")); err != nil {
			return nil, bad("%v", err)
		}
		isSecret = res == "secrets"
	} else {
		return nil, bad("unknown kind")
	}
	raw, err := c.Cs.CoreV1().RESTClient().Get().AbsPath(path).
		SetHeader("Accept", "application/json").DoRaw(ctx)
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if md, ok := obj["metadata"].(map[string]any); ok {
		delete(md, "managedFields")
		if isSecret { // this annotation holds a copy of the secret values
			if an, ok := md["annotations"].(map[string]any); ok {
				delete(an, "kubectl.kubernetes.io/last-applied-configuration")
			}
		}
	}
	masked := false
	if isSecret { // values never reach the browser
		for _, f := range []string{"data", "stringData"} {
			if m, ok := obj[f].(map[string]any); ok {
				for key := range m {
					m[key] = "<hidden by Kendra>"
				}
				masked = true
			}
		}
	}
	y, err := yaml.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return map[string]any{"yaml": string(y), "masked": masked}, nil
}

func (s *server) explain(r *http.Request) (any, error) {
	c, err := s.cluster(r)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.ctx(r, 20*time.Second)
	defer cancel()
	return c.ExplainKind(ctx, q(r, "kind"), q(r, "ns"), q(r, "name"))
}

func (s *server) objectEvents(r *http.Request) (any, error) {
	c, err := s.cluster(r)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.ctx(r, 20*time.Second)
	defer cancel()
	sel := "involvedObject.name=" + q(r, "name")
	if q(r, "ns") == "" {
		sel += ",involvedObject.namespace="
	}
	l, err := c.Cs.CoreV1().Events(q(r, "ns")).List(ctx, metav1.ListOptions{FieldSelector: sel})
	if err != nil {
		return nil, err
	}
	type ev struct {
		Type, Reason, Message, Last string
		Count                       int32
	}
	out := []ev{}
	for _, e := range l.Items {
		t := e.LastTimestamp.Time
		if t.IsZero() {
			t = e.EventTime.Time
		}
		if t.IsZero() {
			t = e.CreationTimestamp.Time
		}
		out = append(out, ev{e.Type, e.Reason, e.Message, t.UTC().Format(time.RFC3339), max(e.Count, 1)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last > out[j].Last })
	return out, nil
}

// warnings groups Warning events so 40 identical messages become one line.
func (s *server) warnings(r *http.Request) (any, error) {
	ns := q(r, "ns")
	type group struct {
		Reason     string   `json:"reason"`
		Message    string   `json:"message"`
		Count      int32    `json:"count"`
		Last       string   `json:"last"`
		Objects    []string `json:"objects"`
		Namespaces []string `json:"namespaces"`
		Hint       string   `json:"hint,omitempty"`
	}
	return s.each(r, 25*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		l, err := c.Cs.CoreV1().Events(ns).List(ctx, metav1.ListOptions{FieldSelector: "type=Warning", Limit: 3000})
		if err != nil {
			return nil, 0, err
		}
		m := map[string]*group{}
		objs := map[string]map[string]bool{}
		nss := map[string]map[string]bool{}
		for _, e := range l.Items {
			k := e.Reason + "|" + normalize(e.Message)
			g := m[k]
			if g == nil {
				g = &group{Reason: e.Reason, Message: e.Message, Hint: warnHint(e.Reason)}
				m[k], objs[k], nss[k] = g, map[string]bool{}, map[string]bool{}
			}
			g.Count += max(e.Count, 1)
			t := e.LastTimestamp.Time
			if t.IsZero() {
				t = e.EventTime.Time
			}
			if ts := t.UTC().Format(time.RFC3339); ts > g.Last {
				g.Last = ts
			}
			objs[k][e.InvolvedObject.Kind+"/"+e.InvolvedObject.Name] = true
			nss[k][e.Namespace] = true
		}
		out := []*group{}
		for k, g := range m {
			for o := range objs[k] {
				g.Objects = append(g.Objects, o)
			}
			for n := range nss[k] {
				g.Namespaces = append(g.Namespaces, n)
			}
			sort.Strings(g.Objects)
			sort.Strings(g.Namespaces)
			if len(g.Objects) > 8 {
				g.Objects = append(g.Objects[:8], fmt.Sprintf("… and %d more", len(objs[k])-8))
			}
			out = append(out, g)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
		return out, 0, nil
	})
}

// normalize drops the parts of a message that differ per object (ids, hashes, numbers).
func normalize(m string) string {
	var b strings.Builder
	for _, w := range strings.Fields(m) {
		digits := 0
		for _, c := range w {
			if c >= '0' && c <= '9' {
				digits++
			}
		}
		if digits > 2 {
			w = "#"
		}
		b.WriteString(w + " ")
	}
	return b.String()
}

func warnHint(reason string) string {
	switch reason {
	case "FailedScheduling":
		return "Pods cannot be placed: look at node capacity, taints and selectors."
	case "BackOff", "CrashLoopBackOff":
		return "A container keeps crashing: read its logs."
	case "Failed", "FailedPull", "ErrImagePull":
		return "An image cannot be pulled: check the name, tag and pull secret."
	case "Unhealthy":
		return "A readiness or liveness probe fails: check the probe path and port."
	case "FailedMount", "FailedAttachVolume":
		return "A volume cannot be mounted: check the claim, the secret or configmap, and the node."
	case "FailedCreate":
		return "A controller cannot create pods: check quotas and the Events of the owner."
	case "Evicted":
		return "The node ran low on resources."
	}
	return ""
}

func (s *server) containers(r *http.Request) (any, error) {
	c, err := s.cluster(r)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.ctx(r, 20*time.Second)
	defer cancel()
	p, err := c.Cs.CoreV1().Pods(q(r, "ns")).Get(ctx, q(r, "name"), metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, c := range p.Spec.InitContainers {
		out = append(out, c.Name)
	}
	for _, c := range p.Spec.Containers {
		out = append(out, c.Name)
	}
	return out, nil
}

// logs streams plain text; with follow=1 it stays open until the browser goes away.
func (s *server) logs(w http.ResponseWriter, r *http.Request) {
	c, err := s.cluster(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	tail, _ := strconv.ParseInt(q(r, "tail"), 10, 64)
	if tail <= 0 || tail > 5000 {
		tail = 500
	}
	opts := &corev1.PodLogOptions{Container: q(r, "container"), Follow: q(r, "follow") == "1",
		Previous: q(r, "previous") == "1", TailLines: &tail, Timestamps: q(r, "ts") == "1"}
	ctx := r.Context()
	if !opts.Follow {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	st, err := c.Cs.CoreV1().Pods(q(r, "ns")).GetLogs(q(r, "name"), opts).Stream(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer st.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fl, _ := w.(http.Flusher)
	buf := make([]byte, 8192)
	for {
		n, err := st.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			if fl != nil {
				fl.Flush()
			}
		}
		if err != nil {
			if err != io.EOF && ctx.Err() == nil {
				fmt.Fprintf(w, "\n[stream ended: %v]\n", err)
			}
			return
		}
	}
}

// ---- kapp ----

func (s *server) kappApps(r *http.Request) (any, error) {
	return s.each(r, 90*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		apps, skipped, err := c.ListApps(ctx)
		if apps == nil {
			apps = []kube.App{}
		}
		return apps, skipped, err
	})
}

func (s *server) kappApp(r *http.Request) (any, error) {
	c, err := s.cluster(r)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.ctx(r, 90*time.Second)
	defer cancel()
	info, err := c.GetApp(ctx, q(r, "ns"), q(r, "name"))
	if err != nil {
		return nil, err
	}
	return map[string]any{"info": info, "graph": c.AppGraph(ctx, info.Resources)}, nil
}

func (s *server) kappUnmanaged(r *http.Request) (any, error) {
	return s.each(r, 60*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		l, err := c.Unmanaged(ctx)
		if l == nil {
			l = []kube.Res{}
		}
		return l, 0, err
	})
}

func (s *server) istio(r *http.Request) (any, error) {
	c, err := s.cluster(r)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.ctx(r, 60*time.Second)
	defer cancel()
	return c.IstioTopology(ctx, q(r, "ns"))
}

func (s *server) topoCluster(r *http.Request) (any, error) {
	c, err := s.cluster(r)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.ctx(r, 60*time.Second)
	defer cancel()
	return c.ClusterTopology(ctx)
}

func (s *server) topoNamespace(r *http.Request) (any, error) {
	c, err := s.cluster(r)
	if err != nil {
		return nil, err
	}
	if q(r, "ns") == "" {
		return nil, bad("pick a namespace")
	}
	ctx, cancel := s.ctx(r, 60*time.Second)
	defer cancel()
	return c.NamespaceTopology(ctx, q(r, "ns"))
}

func (s *server) helmReleases(r *http.Request) (any, error) {
	return s.each(r, 90*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		apps, skipped, err := c.ListHelmReleases(ctx)
		return apps, skipped, err
	})
}

func (s *server) helmRelease(r *http.Request) (any, error) {
	c, err := s.cluster(r)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.ctx(r, 90*time.Second)
	defer cancel()
	info, err := c.GetHelmRelease(ctx, q(r, "ns"), q(r, "name"))
	if err != nil {
		return nil, err
	}
	return map[string]any{"info": info, "graph": c.AppGraph(ctx, info.Resources)}, nil
}

func (s *server) overview(r *http.Request) (any, error) {
	return s.each(r, 60*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		o, err := c.NamespaceOverview(ctx)
		return o, 0, err
	})
}

func (s *server) crds(r *http.Request) (any, error) {
	c, err := s.cluster(r)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.ctx(r, 30*time.Second)
	defer cancel()
	return c.CRDs(ctx)
}

// helmCharts lists the charts of the repositories added with `helm repo add`, from the
// local files only. The charts in use come from the releases themselves.
func (s *server) helmCharts(r *http.Request) (any, error) {
	repos, charts, err := kube.LocalHelmCharts(3)
	note := ""
	if err != nil {
		note = "No local Helm repositories found. Add one with `helm repo add <name> <url>` and run `helm repo update`."
	}
	if err == nil && len(charts) == 0 && len(repos) > 0 {
		note = "Your Helm repositories have no cached index yet. Run `helm repo update`, then reload."
	}
	if repos == nil {
		repos = []kube.HelmRepo{}
	}
	if charts == nil {
		charts = []kube.HelmChart{}
	}
	if len(charts) > 4000 {
		charts = charts[:4000]
	}
	return map[string]any{"repos": repos, "charts": charts, "note": note}, nil
}

type forwardReq struct {
	Context    string `json:"context"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	RemotePort int    `json:"remotePort"`
	LocalPort  int    `json:"localPort"`
	ID         string `json:"id"`
}

// forward opens a port on 127.0.0.1 to a pod or service. It changes nothing in the
// cluster, so read-only mode allows it; it is recorded in the audit log.
func (s *server) forward(r *http.Request) (any, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<16)
	var f forwardReq
	if err := json.NewDecoder(r.Body).Decode(&f); err != nil || f.Name == "" || f.Namespace == "" {
		return nil, bad("context, kind, namespace and name are required")
	}
	c, err := s.mgr.Get(f.Context)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	return s.fwd.Start(ctx, c, f.Kind, f.Namespace, f.Name, f.RemotePort, f.LocalPort)
}

func (s *server) unforward(r *http.Request) (any, error) {
	var f forwardReq
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<16)).Decode(&f); err != nil {
		return nil, bad("invalid request")
	}
	if err := s.fwd.Stop(f.ID); err != nil {
		return nil, bad("%v", err)
	}
	return okMsg("Stopped"), nil
}

// ---- helpers ----

func okMsg(m string) map[string]string { return map[string]string{"ok": m} }

// fleet is the summary card (version, nodes, pods, warnings) of one or more clusters.
func (s *server) fleet(r *http.Request) (any, error) {
	return s.each(r, 45*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		f, err := c.FleetSummary(ctx)
		return f, 0, err
	})
}

// deprecated lists the deprecated API versions each cluster still serves.
func (s *server) deprecated(r *http.Request) (any, error) {
	return s.each(r, 60*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		d, err := c.DeprecatedAPIs(ctx)
		return d, 0, err
	})
}

func (s *server) usage(r *http.Request) (any, error) {
	return s.each(r, 90*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		u, err := c.Usage(ctx)
		return u, 0, err
	})
}

func (s *server) images(r *http.Request) (any, error) {
	return s.each(r, 60*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		i, err := c.Images(ctx)
		return i, 0, err
	})
}

func (s *server) certificates(r *http.Request) (any, error) {
	return s.each(r, 45*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		l, err := c.Certificates(ctx)
		return l, 0, err
	})
}

func (s *server) snapshot(r *http.Request) (any, error) {
	ns := q(r, "ns")
	return s.each(r, 90*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		l, err := c.Snapshot(ctx, ns)
		return l, 0, err
	})
}

// SaveFile, when set, asks the user where to save a text file (the native macOS save
// panel) and writes it there. It returns the chosen path, or "" when cancelled. Without
// it (browser builds) the page downloads the file itself.
var SaveFile func(name, text string) (string, error)

type saveReq struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

// save writes a table the user exported (CSV or Markdown) to a file they choose. It
// touches nothing in any cluster.
func (s *server) save(r *http.Request) (any, error) {
	if SaveFile == nil {
		return nil, httpError{http.StatusNotImplemented, "saving is done by the browser here"}
	}
	var f saveReq
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 32<<20)).Decode(&f); err != nil || f.Name == "" {
		return nil, bad("name and text are required")
	}
	path, err := SaveFile(f.Name, f.Text)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "cancelled": path == ""}, nil
}

func (s *server) security(r *http.Request) (any, error) {
	return s.each(r, 90*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		d, err := c.Security(ctx)
		return d, 0, err
	})
}

func (s *server) identities(r *http.Request) (any, error) {
	return s.each(r, 90*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		d, err := c.Identities(ctx)
		return d, 0, err
	})
}

func (s *server) addons(r *http.Request) (any, error) {
	return s.each(r, 60*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		d, err := c.Addons(ctx)
		return d, 0, err
	})
}

func (s *server) compat(r *http.Request) (any, error) {
	target, _ := strconv.Atoi(q(r, "target"))
	return s.each(r, 60*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		d, err := c.Compat(ctx, target)
		return d, 0, err
	})
}

func (s *server) cronjobs(r *http.Request) (any, error) {
	return s.each(r, 60*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		d, err := c.CronJobs(ctx)
		return d, 0, err
	})
}

func (s *server) search(r *http.Request) (any, error) {
	term := strings.TrimSpace(q(r, "q"))
	if len(term) < 2 {
		return nil, bad("type at least 2 characters")
	}
	var scopes []string
	for _, sc := range strings.Split(q(r, "scopes"), ",") {
		if !slices.Contains(kube.SearchScopes, sc) {
			return nil, bad("unknown scope %q", sc)
		}
		scopes = append(scopes, sc)
	}
	ns := q(r, "ns")
	return s.each(r, 60*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		d, err := c.Search(ctx, term, scopes, ns)
		return d, 0, err
	})
}

func (s *server) incident(r *http.Request) (any, error) {
	c, err := s.cluster(r)
	if err != nil {
		return nil, err
	}
	kind := q(r, "kind")
	if !slices.Contains(kube.IncidentKinds, kind) {
		return nil, bad("no incident bundle for %q", kind)
	}
	if q(r, "name") == "" {
		return nil, bad("name is required")
	}
	ctx, cancel := s.ctx(r, 45*time.Second)
	defer cancel()
	return c.IncidentBundle(ctx, q(r, "context"), kind, q(r, "ns"), q(r, "name"))
}

func (s *server) related(r *http.Request) (any, error) {
	c, err := s.cluster(r)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.ctx(r, 30*time.Second)
	defer cancel()
	return c.Related(ctx, q(r, "kind"), q(r, "ns"), q(r, "name"))
}

func (s *server) impact(r *http.Request) (any, error) {
	c, err := s.cluster(r)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.ctx(r, 45*time.Second)
	defer cancel()
	return c.Impact(ctx, q(r, "kind"), q(r, "ns"), q(r, "name"))
}

// timeline: what changed in the last `hours` (default 6, at most 168) in a namespace or the cluster.
func (s *server) timeline(r *http.Request) (any, error) {
	h, _ := strconv.Atoi(q(r, "hours"))
	if h <= 0 {
		h = 6
	}
	h = min(h, 168)
	ns := q(r, "ns")
	return s.each(r, 60*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		d, err := c.Timeline(ctx, ns, time.Duration(h)*time.Hour)
		return d, 0, err
	})
}

func (s *server) spread(r *http.Request) (any, error) {
	return s.each(r, 90*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		d, err := c.Spread(ctx)
		return d, 0, err
	})
}

func (s *server) gateway(r *http.Request) (any, error) {
	return s.each(r, 60*time.Second, func(ctx context.Context, c *kube.Cluster) (any, int, error) {
		d, err := c.GatewayAPI(ctx)
		return d, 0, err
	})
}
