package kube

import (
	"context"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SearchScopes are the things Fleet search can look in.
var SearchScopes = []string{"image", "label", "name", "env", "annotation"}

// SearchHit is one object that matched, and what matched in it.
type SearchHit struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Scope     string `json:"scope"` // image | label | name | env | annotation
	Match     string `json:"match"` // the text that matched, e.g. "image reg.io/web:1.2" or "label team=pay"
}

// SearchResult is what one cluster found.
type SearchResult struct {
	Hits       []SearchHit `json:"hits"`
	Truncated  bool        `json:"truncated"`            // more than searchCap hits: narrow the search
	Unreadable []string    `json:"unreadable,omitempty"` // kinds this login could not list
}

const searchCap = 500

type searcher struct {
	term   string
	scopes map[string]bool
	res    *SearchResult
	seen   map[SearchHit]bool
}

func (s *searcher) has(v string) bool { return strings.Contains(strings.ToLower(v), s.term) }

func (s *searcher) add(kind, ns, name, scope, match string) {
	if len(s.res.Hits) >= searchCap {
		s.res.Truncated = true
		return
	}
	h := SearchHit{kind, ns, name, scope, match}
	if !s.seen[h] {
		s.seen[h] = true
		s.res.Hits = append(s.res.Hits, h)
	}
}

func clip(v string) string {
	if len(v) > 120 {
		return v[:120] + "…"
	}
	return v
}

func (s *searcher) maps(kind, ns, name, prefix string, labels, annotations map[string]string) {
	if s.scopes["label"] {
		for k, v := range labels {
			if s.has(k + "=" + v) {
				s.add(kind, ns, name, "label", prefix+"label "+k+"="+clip(v))
			}
		}
	}
	if s.scopes["annotation"] {
		for k, v := range annotations {
			if k == "kubectl.kubernetes.io/last-applied-configuration" {
				continue
			}
			if s.has(k + "=" + v) {
				s.add(kind, ns, name, "annotation", prefix+"annotation "+k+"="+clip(v))
			}
		}
	}
}

// object checks the name and the metadata that every kind has.
func (s *searcher) object(kind, ns, name string, labels, annotations map[string]string) {
	if s.scopes["name"] && s.has(name) {
		s.add(kind, ns, name, "name", "name "+name)
	}
	s.maps(kind, ns, name, "", labels, annotations)
}

// podTemplate checks images and env var names (never values) of a workload's pod template.
func (s *searcher) podTemplate(kind, ns, name string, t corev1.PodTemplateSpec) {
	s.maps(kind, ns, name, "pod ", t.Labels, t.Annotations)
	cts := append(append([]corev1.Container{}, t.Spec.InitContainers...), t.Spec.Containers...)
	for _, ct := range cts {
		if s.scopes["image"] && s.has(ct.Image) {
			s.add(kind, ns, name, "image", "image "+ct.Image)
		}
		if !s.scopes["env"] {
			continue
		}
		for _, e := range ct.Env {
			if s.has(e.Name) {
				s.add(kind, ns, name, "env", "env "+e.Name+" in "+ct.Name)
			}
		}
		for _, ef := range ct.EnvFrom {
			switch {
			case ef.ConfigMapRef != nil && s.has(ef.ConfigMapRef.Name):
				s.add(kind, ns, name, "env", "envFrom ConfigMap "+ef.ConfigMapRef.Name+" in "+ct.Name)
			case ef.SecretRef != nil && s.has(ef.SecretRef.Name):
				s.add(kind, ns, name, "env", "envFrom Secret "+ef.SecretRef.Name+" in "+ct.Name)
			}
		}
	}
}

// Search looks for text (case-insensitive, part of a word is enough) in the images, labels, names,
// env var names and annotations of workloads, services, ingresses and namespaces. Env var values and
// Secret contents are never read into the result. List calls only.
func (c *Cluster) Search(ctx context.Context, term string, scopes []string, ns string) (*SearchResult, error) {
	s := &searcher{term: strings.ToLower(strings.TrimSpace(term)), scopes: map[string]bool{}, res: &SearchResult{Hits: []SearchHit{}}, seen: map[SearchHit]bool{}}
	for _, sc := range scopes {
		s.scopes[sc] = true
	}
	skip := func(kind string, err error) bool {
		if err != nil {
			s.res.Unreadable = append(s.res.Unreadable, kind)
			return true
		}
		return false
	}
	lo := metav1.ListOptions{}
	if dl, err := c.Cs.AppsV1().Deployments(ns).List(ctx, lo); !skip("Deployment", err) {
		for _, d := range dl.Items {
			s.object("Deployment", d.Namespace, d.Name, d.Labels, d.Annotations)
			s.podTemplate("Deployment", d.Namespace, d.Name, d.Spec.Template)
		}
	}
	if sl, err := c.Cs.AppsV1().StatefulSets(ns).List(ctx, lo); !skip("StatefulSet", err) {
		for _, d := range sl.Items {
			s.object("StatefulSet", d.Namespace, d.Name, d.Labels, d.Annotations)
			s.podTemplate("StatefulSet", d.Namespace, d.Name, d.Spec.Template)
		}
	}
	if dl, err := c.Cs.AppsV1().DaemonSets(ns).List(ctx, lo); !skip("DaemonSet", err) {
		for _, d := range dl.Items {
			s.object("DaemonSet", d.Namespace, d.Name, d.Labels, d.Annotations)
			s.podTemplate("DaemonSet", d.Namespace, d.Name, d.Spec.Template)
		}
	}
	if cl, err := c.Cs.BatchV1().CronJobs(ns).List(ctx, lo); !skip("CronJob", err) {
		for _, d := range cl.Items {
			s.object("CronJob", d.Namespace, d.Name, d.Labels, d.Annotations)
			s.podTemplate("CronJob", d.Namespace, d.Name, d.Spec.JobTemplate.Spec.Template)
		}
	}
	if sl, err := c.Cs.CoreV1().Services(ns).List(ctx, lo); !skip("Service", err) {
		for _, d := range sl.Items {
			s.object("Service", d.Namespace, d.Name, d.Labels, d.Annotations)
		}
	}
	if il, err := c.Cs.NetworkingV1().Ingresses(ns).List(ctx, lo); !skip("Ingress", err) {
		for _, d := range il.Items {
			s.object("Ingress", d.Namespace, d.Name, d.Labels, d.Annotations)
			if s.scopes["name"] {
				for _, r := range d.Spec.Rules {
					if r.Host != "" && s.has(r.Host) {
						s.add("Ingress", d.Namespace, d.Name, "name", "host "+r.Host)
					}
				}
			}
		}
	}
	if ns == "" {
		if nl, err := c.Cs.CoreV1().Namespaces().List(ctx, lo); !skip("Namespace", err) {
			for _, d := range nl.Items {
				s.object("Namespace", "", d.Name, d.Labels, d.Annotations)
			}
		}
	}
	sort.SliceStable(s.res.Hits, func(i, j int) bool {
		a, b := s.res.Hits[i], s.res.Hits[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Match < b.Match
	})
	return s.res, nil
}
