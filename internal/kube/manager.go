package kube

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"sync"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Manager hands out per-context clients built from the user's kubeconfig, exactly
// the way kubectl resolves it (KUBECONFIG, ~/.kube/config, exec auth plugins).
type Manager struct {
	rules *clientcmd.ClientConfigLoadingRules
	prod  *regexp.Regexp
	local *regexp.Regexp

	mu       sync.Mutex
	clusters map[string]*Cluster
}

// Cluster is a connection to one kubeconfig context.
type Cluster struct {
	Name string
	Cs   *kubernetes.Clientset
	Dyn  dynamic.Interface
	Cfg  *rest.Config // for port forwarding

	discMu  sync.Mutex
	disc    []apiRes
	discAll []apiRes
	discAt  time.Time
}

// ContextInfo is what the UI shows for a context.
type ContextInfo struct {
	Name    string `json:"name"`
	Cluster string `json:"cluster"`
	User    string `json:"user"`
	Current bool   `json:"current"`
	Prod    bool   `json:"prod"`  // name looks like production: extra care in the UI
	Local   bool   `json:"local"` // kind, minikube, docker-desktop...
}

// NewManager reads the kubeconfig lazily. PROD and LOCAL name patterns can be changed
// with KND_PROD and KND_LOCAL (regular expressions).
func NewManager() (*Manager, error) {
	prod, err := regexp.Compile(envOr("KND_PROD", `(?i)(^|[-_/.])(prod|production|prd)([-_/.]|$)`))
	if err != nil {
		return nil, fmt.Errorf("KND_PROD: %w", err)
	}
	local, err := regexp.Compile(envOr("KND_LOCAL", `(?i)^(kind-|minikube|docker-desktop|rancher-desktop|colima|orbstack|k3d-)`))
	if err != nil {
		return nil, fmt.Errorf("KND_LOCAL: %w", err)
	}
	return &Manager{rules: clientcmd.NewDefaultClientConfigLoadingRules(), prod: prod, local: local,
		clusters: map[string]*Cluster{}}, nil
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// Contexts lists the kubeconfig contexts, current one first, then by name.
func (m *Manager) Contexts() ([]ContextInfo, error) {
	cfg, err := m.rules.Load()
	if err != nil {
		return nil, err
	}
	var out []ContextInfo
	for name, c := range cfg.Contexts {
		out = append(out, ContextInfo{Name: name, Cluster: c.Cluster, User: c.AuthInfo,
			Current: name == cfg.CurrentContext, Prod: m.prod.MatchString(name), Local: m.local.MatchString(name)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Current != out[j].Current {
			return out[i].Current
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// IsProd reports whether the context name looks like production.
func (m *Manager) IsProd(name string) bool { return m.prod.MatchString(name) }

// Get returns (and caches) the client for a context.
func (m *Manager) Get(name string) (*Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.clusters[name]; ok {
		return c, nil
	}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(m.rules, &clientcmd.ConfigOverrides{CurrentContext: name})
	rc, err := cc.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("context %q: %w", name, err)
	}
	rc.QPS, rc.Burst = 40, 80
	rc.UserAgent = "kendra"
	cs, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(rc)
	if err != nil {
		return nil, err
	}
	c := &Cluster{Name: name, Cs: cs, Dyn: dyn, Cfg: rc}
	m.clusters[name] = c
	return c, nil
}
