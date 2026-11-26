package kube

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Rel is one object another object depends on, or that depends on it.
type Rel struct {
	Kind      string `json:"kind"`
	Resource  string `json:"resource,omitempty"` // catalogue name (to open it), "" when Kendra has no page for it
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Note      string `json:"note,omitempty"`
	Level     string `json:"level,omitempty"` // bad: missing or broken, warn
}

// Related answers "what does this use, and what uses it?".
type Related struct {
	Uses   []Rel `json:"uses"`
	UsedBy []Rel `json:"usedBy"`
}

// Impact answers "what is affected if this goes away?".
type Impact struct {
	Summary   []string `json:"summary"`
	Workloads []Rel    `json:"workloads"`
	Services  []Rel    `json:"services"`
	Ingresses []Rel    `json:"ingresses"`
	Volumes   []Rel    `json:"volumes"`
}

func rel(kind, ns, name, note, level string) Rel {
	r := Rel{Kind: kind, Namespace: ns, Name: name, Note: note, Level: level}
	for _, k := range Kinds {
		if k.Singular == kind {
			r.Resource = k.Name
			break
		}
	}
	return r
}

// graph holds what the questions above are answered from, loaded once.
type graph struct {
	pods []corev1.Pod
	svcs []corev1.Service
	ings []ingressRef
	ns   string
}

type ingressRef struct {
	ns, name string
	services []string
	secrets  []string
	hosts    string
}

func (c *Cluster) loadGraph(ctx context.Context, ns string) (*graph, error) {
	g := &graph{ns: ns}
	pl, err := c.Cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, p := range pl.Items {
		if p.Status.Phase != corev1.PodSucceeded && p.Status.Phase != corev1.PodFailed {
			g.pods = append(g.pods, p)
		}
	}
	if sl, err := c.Cs.CoreV1().Services(ns).List(ctx, metav1.ListOptions{}); err == nil {
		g.svcs = sl.Items
	}
	if il, err := c.Cs.NetworkingV1().Ingresses(ns).List(ctx, metav1.ListOptions{}); err == nil {
		for _, in := range il.Items {
			r := ingressRef{ns: in.Namespace, name: in.Name}
			if in.Spec.DefaultBackend != nil && in.Spec.DefaultBackend.Service != nil {
				r.services = append(r.services, in.Spec.DefaultBackend.Service.Name)
			}
			for _, rule := range in.Spec.Rules {
				if r.hosts == "" {
					r.hosts = rule.Host
				}
				if rule.HTTP != nil {
					for _, p := range rule.HTTP.Paths {
						if p.Backend.Service != nil {
							r.services = append(r.services, p.Backend.Service.Name)
						}
					}
				}
			}
			for _, t := range in.Spec.TLS {
				if t.SecretName != "" {
					r.secrets = append(r.secrets, t.SecretName)
				}
			}
			g.ings = append(g.ings, r)
		}
	}
	return g, nil
}

func selects(sel map[string]string, labels map[string]string) bool {
	if len(sel) == 0 {
		return false
	}
	for k, v := range sel {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func podReady(p *corev1.Pod) bool {
	for _, cd := range p.Status.Conditions {
		if cd.Type == corev1.PodReady {
			return cd.Status == corev1.ConditionTrue
		}
	}
	return false
}

// specRefs lists the ConfigMaps, Secrets and claims a pod spec refers to.
func specRefs(s *corev1.PodSpec) (cms, secrets, pvcs []string) {
	seen := map[string]bool{}
	add := func(list *[]string, kind, n string) {
		if n != "" && !seen[kind+n] {
			seen[kind+n] = true
			*list = append(*list, n)
		}
	}
	for _, v := range s.Volumes {
		if v.ConfigMap != nil {
			add(&cms, "c", v.ConfigMap.Name)
		}
		if v.Secret != nil {
			add(&secrets, "s", v.Secret.SecretName)
		}
		if v.PersistentVolumeClaim != nil {
			add(&pvcs, "p", v.PersistentVolumeClaim.ClaimName)
		}
		if v.Projected != nil {
			for _, src := range v.Projected.Sources {
				if src.ConfigMap != nil {
					add(&cms, "c", src.ConfigMap.Name)
				}
				if src.Secret != nil {
					add(&secrets, "s", src.Secret.Name)
				}
			}
		}
	}
	for _, list := range [][]corev1.Container{s.InitContainers, s.Containers} {
		for _, ct := range list {
			for _, ef := range ct.EnvFrom {
				if ef.ConfigMapRef != nil {
					add(&cms, "c", ef.ConfigMapRef.Name)
				}
				if ef.SecretRef != nil {
					add(&secrets, "s", ef.SecretRef.Name)
				}
			}
			for _, e := range ct.Env {
				if e.ValueFrom == nil {
					continue
				}
				if e.ValueFrom.ConfigMapKeyRef != nil {
					add(&cms, "c", e.ValueFrom.ConfigMapKeyRef.Name)
				}
				if e.ValueFrom.SecretKeyRef != nil {
					add(&secrets, "s", e.ValueFrom.SecretKeyRef.Name)
				}
			}
		}
	}
	for _, ips := range s.ImagePullSecrets {
		add(&secrets, "s", ips.Name)
	}
	return
}

// names returns the set of object names of a kind in a namespace (for "is it there?").
func (c *Cluster) names(ctx context.Context, kind, ns string) map[string]bool {
	out := map[string]bool{}
	switch kind {
	case "configmaps":
		if l, err := c.Cs.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{}); err == nil {
			for _, x := range l.Items {
				out[x.Name] = true
			}
		} else {
			return nil
		}
	case "secrets":
		if l, err := c.Cs.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{}); err == nil {
			for _, x := range l.Items {
				out[x.Name] = true
			}
		} else {
			return nil
		}
	case "pvcs":
		if l, err := c.Cs.CoreV1().PersistentVolumeClaims(ns).List(ctx, metav1.ListOptions{}); err == nil {
			for _, x := range l.Items {
				out[x.Name] = true
			}
		} else {
			return nil
		}
	}
	return out
}

// usesOfSpec turns a pod spec's references into Rels, marking missing ones.
func (c *Cluster) usesOfSpec(ctx context.Context, ns string, s *corev1.PodSpec) []Rel {
	cms, secrets, pvcs := specRefs(s)
	var out []Rel
	check := func(kind, set string, list []string) {
		have := c.names(ctx, set, ns)
		for _, n := range list {
			if have != nil && !have[n] {
				out = append(out, rel(kind, ns, n, "does not exist", "bad"))
			} else {
				out = append(out, rel(kind, ns, n, "", ""))
			}
		}
	}
	check("ConfigMap", "configmaps", cms)
	check("Secret", "secrets", secrets)
	check("PersistentVolumeClaim", "pvcs", pvcs)
	sa := s.ServiceAccountName
	if sa == "" {
		sa = "default"
	}
	out = append(out, rel("ServiceAccount", ns, sa, "", ""))
	return out
}

// workloadsOfPods groups pods by owner: "Deployment/web (3 pods)".
func workloadsOfPods(pods []*corev1.Pod, note string) []Rel {
	count := map[string]int{}
	ns := map[string]string{}
	for _, p := range pods {
		w := workloadOf(p)
		count[p.Namespace+"|"+w]++
		ns[p.Namespace+"|"+w] = p.Namespace
	}
	var keys []string
	for k := range count {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []Rel
	for _, k := range keys {
		w := k[len(ns[k])+1:]
		kind, name := splitOwner(w)
		out = append(out, rel(kind, ns[k], name, fmt.Sprintf("%d pod%s%s", count[k], sfx(count[k]), note), ""))
	}
	return out
}

func splitOwner(w string) (string, string) {
	for i := 0; i < len(w); i++ {
		if w[i] == '/' {
			return w[:i], w[i+1:]
		}
	}
	return "Pod", w
}

func sfx(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func ingressesFor(g *graph, ns string, svcNames map[string]bool) []Rel {
	var out []Rel
	for _, in := range g.ings {
		if in.ns != ns {
			continue
		}
		for _, s := range in.services {
			if svcNames[s] {
				out = append(out, rel("Ingress", in.ns, in.name, in.hosts, ""))
				break
			}
		}
	}
	return out
}

func podsMatching(g *graph, ns string, sel map[string]string) []*corev1.Pod {
	var out []*corev1.Pod
	for i := range g.pods {
		if g.pods[i].Namespace == ns && selects(sel, g.pods[i].Labels) {
			out = append(out, &g.pods[i])
		}
	}
	return out
}

func (c *Cluster) servicesSelecting(g *graph, ns string, labels map[string]string) (rels []Rel, names map[string]bool) {
	names = map[string]bool{}
	for _, s := range g.svcs {
		if s.Namespace == ns && selects(s.Spec.Selector, labels) {
			rels = append(rels, rel("Service", ns, s.Name, "", ""))
			names[s.Name] = true
		}
	}
	return
}

// Related lists what an object uses and what uses it. Read-only; Secrets are only
// referred to by name.
func (c *Cluster) Related(ctx context.Context, kind, ns, name string) (*Related, error) {
	out := &Related{Uses: []Rel{}, UsedBy: []Rel{}}
	switch kind {
	case "pods":
		p, err := c.Cs.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		g, err := c.loadGraph(ctx, ns)
		if err != nil {
			return nil, err
		}
		if w := workloadOf(p); w != "Pod/"+p.Name {
			k, n := splitOwner(w)
			out.Uses = append(out.Uses, rel(k, ns, n, "owner", ""))
		}
		if p.Spec.NodeName != "" {
			out.Uses = append(out.Uses, rel("Node", "", p.Spec.NodeName, "", ""))
		}
		out.Uses = append(out.Uses, c.usesOfSpec(ctx, ns, &p.Spec)...)
		svcs, names := c.servicesSelecting(g, ns, p.Labels)
		out.UsedBy = append(out.UsedBy, svcs...)
		out.UsedBy = append(out.UsedBy, ingressesFor(g, ns, names)...)
	case "deployments", "statefulsets", "daemonsets":
		var spec *corev1.PodSpec
		var labels map[string]string
		switch kind {
		case "deployments":
			d, err := c.Cs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			spec, labels = &d.Spec.Template.Spec, d.Spec.Template.Labels
		case "statefulsets":
			d, err := c.Cs.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			spec, labels = &d.Spec.Template.Spec, d.Spec.Template.Labels
		default:
			d, err := c.Cs.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			spec, labels = &d.Spec.Template.Spec, d.Spec.Template.Labels
		}
		g, err := c.loadGraph(ctx, ns)
		if err != nil {
			return nil, err
		}
		out.Uses = append(out.Uses, c.usesOfSpec(ctx, ns, spec)...)
		svcs, names := c.servicesSelecting(g, ns, labels)
		out.UsedBy = append(out.UsedBy, svcs...)
		out.UsedBy = append(out.UsedBy, ingressesFor(g, ns, names)...)
		if hl, err := c.Cs.AutoscalingV2().HorizontalPodAutoscalers(ns).List(ctx, metav1.ListOptions{}); err == nil {
			for _, h := range hl.Items {
				if h.Spec.ScaleTargetRef.Name == name && h.Spec.ScaleTargetRef.Kind == map[string]string{"deployments": "Deployment", "statefulsets": "StatefulSet", "daemonsets": "DaemonSet"}[kind] {
					out.UsedBy = append(out.UsedBy, rel("HorizontalPodAutoscaler", ns, h.Name, "scales it", ""))
				}
			}
		}
		if pl, err := c.Cs.PolicyV1().PodDisruptionBudgets(ns).List(ctx, metav1.ListOptions{}); err == nil {
			for _, b := range pl.Items {
				if b.Spec.Selector != nil && selects(b.Spec.Selector.MatchLabels, labels) {
					out.UsedBy = append(out.UsedBy, rel("PodDisruptionBudget", ns, b.Name, "limits disruptions", ""))
				}
			}
		}
	case "services":
		s, err := c.Cs.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		g, err := c.loadGraph(ctx, ns)
		if err != nil {
			return nil, err
		}
		pods := podsMatching(g, ns, s.Spec.Selector)
		if len(s.Spec.Selector) > 0 {
			ws := workloadsOfPods(pods, "")
			if len(ws) == 0 {
				out.Uses = append(out.Uses, rel("Pod", ns, "(none match the selector)", "", "bad"))
			}
			out.Uses = append(out.Uses, ws...)
		}
		out.UsedBy = append(out.UsedBy, ingressesFor(g, ns, map[string]bool{name: true})...)
	case "configmaps", "secrets", "persistentvolumeclaims", "serviceaccounts":
		g, err := c.loadGraph(ctx, ns)
		if err != nil {
			return nil, err
		}
		var users []*corev1.Pod
		for i := range g.pods {
			p := &g.pods[i]
			cms, secrets, pvcs := specRefs(&p.Spec)
			sa := p.Spec.ServiceAccountName
			if sa == "" {
				sa = "default"
			}
			hit := map[string]bool{}
			for _, n := range cms {
				hit["configmaps/"+n] = true
			}
			for _, n := range secrets {
				hit["secrets/"+n] = true
			}
			for _, n := range pvcs {
				hit["persistentvolumeclaims/"+n] = true
			}
			hit["serviceaccounts/"+sa] = true
			if hit[kind+"/"+name] {
				users = append(users, p)
			}
		}
		out.UsedBy = append(out.UsedBy, workloadsOfPods(users, "")...)
		if kind == "secrets" {
			for _, in := range g.ings {
				for _, s := range in.secrets {
					if s == name && in.ns == ns {
						out.UsedBy = append(out.UsedBy, rel("Ingress", in.ns, in.name, "TLS certificate", ""))
					}
				}
			}
		}
		if kind == "persistentvolumeclaims" {
			if pvc, err := c.Cs.CoreV1().PersistentVolumeClaims(ns).Get(ctx, name, metav1.GetOptions{}); err == nil {
				if pvc.Spec.VolumeName != "" {
					out.Uses = append(out.Uses, rel("PersistentVolume", "", pvc.Spec.VolumeName, "bound volume", ""))
				}
				if pvc.Spec.StorageClassName != nil && *pvc.Spec.StorageClassName != "" {
					out.Uses = append(out.Uses, rel("StorageClass", "", *pvc.Spec.StorageClassName, "", ""))
				}
			}
		}
	case "ingresses":
		g, err := c.loadGraph(ctx, ns)
		if err != nil {
			return nil, err
		}
		have := map[string]bool{}
		for _, s := range g.svcs {
			have[s.Name] = true
		}
		for _, in := range g.ings {
			if in.name != name || in.ns != ns {
				continue
			}
			seen := map[string]bool{}
			for _, s := range in.services {
				if seen[s] {
					continue
				}
				seen[s] = true
				if have[s] {
					out.Uses = append(out.Uses, rel("Service", ns, s, "", ""))
				} else {
					out.Uses = append(out.Uses, rel("Service", ns, s, "does not exist", "bad"))
				}
			}
			for _, s := range in.secrets {
				out.Uses = append(out.Uses, rel("Secret", ns, s, "TLS certificate", ""))
			}
		}
	case "nodes":
		g, err := c.loadGraph(ctx, "")
		if err != nil {
			return nil, err
		}
		var on []*corev1.Pod
		for i := range g.pods {
			if g.pods[i].Spec.NodeName == name {
				on = append(on, &g.pods[i])
			}
		}
		out.UsedBy = append(out.UsedBy, workloadsOfPods(on, " on this node")...)
	default:
		return nil, fmt.Errorf("no related objects for %q yet", kind)
	}
	return out, nil
}

// Impact lists what stops working if the object is deleted, broken or its node is lost:
// the pods that depend on it, the workloads they belong to, the services that would be
// left with fewer (or no) ready pods, the ingresses behind those services and the
// volumes in use. It reasons from pod specs and selectors only; nothing is changed.
func (c *Cluster) Impact(ctx context.Context, kind, ns, name string) (*Impact, error) {
	scope := ns
	switch kind {
	case "nodes":
		scope = ""
	case "namespaces":
		scope = name
	}
	g, err := c.loadGraph(ctx, scope)
	if err != nil {
		return nil, err
	}
	hitPods := map[string]bool{}
	var affected []*corev1.Pod
	mark := func(p *corev1.Pod) {
		if !hitPods[p.Namespace+"/"+p.Name] {
			hitPods[p.Namespace+"/"+p.Name] = true
			affected = append(affected, p)
		}
	}
	imp := &Impact{Summary: []string{}, Workloads: []Rel{}, Services: []Rel{}, Ingresses: []Rel{}, Volumes: []Rel{}}
	directSvc := map[string]bool{}
	switch kind {
	case "nodes":
		for i := range g.pods {
			if g.pods[i].Spec.NodeName == name {
				mark(&g.pods[i])
			}
		}
	case "namespaces":
		for i := range g.pods {
			mark(&g.pods[i])
		}
		if pl, err := c.Cs.CoreV1().PersistentVolumeClaims(name).List(ctx, metav1.ListOptions{}); err == nil {
			for _, p := range pl.Items {
				imp.Volumes = append(imp.Volumes, rel("PersistentVolumeClaim", name, p.Name, "deleted with the namespace; the data may be lost", "bad"))
			}
		}
		g.ns = name
		ns = name
	case "configmaps", "secrets", "persistentvolumeclaims", "serviceaccounts":
		for i := range g.pods {
			p := &g.pods[i]
			cms, secrets, pvcs := specRefs(&p.Spec)
			sa := p.Spec.ServiceAccountName
			if sa == "" {
				sa = "default"
			}
			lists := map[string][]string{"configmaps": cms, "secrets": secrets, "persistentvolumeclaims": pvcs, "serviceaccounts": {sa}}
			for _, n := range lists[kind] {
				if n == name {
					mark(p)
				}
			}
		}
		if kind == "secrets" {
			for _, in := range g.ings {
				for _, s := range in.secrets {
					if s == name && in.ns == ns {
						imp.Ingresses = append(imp.Ingresses, rel("Ingress", in.ns, in.name, "TLS certificate would be missing", "warn"))
					}
				}
			}
		}
	case "deployments", "statefulsets", "daemonsets":
		var sel map[string]string
		switch kind {
		case "deployments":
			d, err := c.Cs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			sel = d.Spec.Template.Labels
		case "statefulsets":
			d, err := c.Cs.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			sel = d.Spec.Template.Labels
		default:
			d, err := c.Cs.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			sel = d.Spec.Template.Labels
		}
		for i := range g.pods {
			if g.pods[i].Namespace == ns && selects(sel, g.pods[i].Labels) && workloadOf(&g.pods[i]) != "" {
				mark(&g.pods[i])
			}
		}
	case "pods":
		for i := range g.pods {
			if g.pods[i].Namespace == ns && g.pods[i].Name == name {
				mark(&g.pods[i])
			}
		}
	case "services":
		directSvc[ns+"/"+name] = true
	default:
		return nil, fmt.Errorf("no impact view for %q yet", kind)
	}

	imp.Workloads = append(imp.Workloads, workloadsOfPods(affected, "")...)
	// volumes in use by the affected pods
	seenVol := map[string]bool{}
	for _, p := range affected {
		_, _, pvcs := specRefs(&p.Spec)
		for _, v := range pvcs {
			if !seenVol[p.Namespace+v] && !(kind == "namespaces") {
				seenVol[p.Namespace+v] = true
				imp.Volumes = append(imp.Volumes, rel("PersistentVolumeClaim", p.Namespace, v, "in use by an affected pod", "warn"))
			}
		}
	}
	// services: how many ready pods each loses
	outage := map[string]bool{}
	svcNames := map[string]map[string]bool{}
	for _, s := range g.svcs {
		if len(s.Spec.Selector) == 0 {
			continue
		}
		total, lost := 0, 0
		for i := range g.pods {
			p := &g.pods[i]
			if p.Namespace == s.Namespace && selects(s.Spec.Selector, p.Labels) && podReady(p) {
				total++
				if hitPods[p.Namespace+"/"+p.Name] {
					lost++
				}
			}
		}
		if kind == "namespaces" && s.Namespace == name {
			imp.Services = append(imp.Services, rel("Service", s.Namespace, s.Name, "deleted with the namespace", "bad"))
			continue
		}
		if directSvc[s.Namespace+"/"+s.Name] {
			outage[s.Namespace+"/"+s.Name] = true
			imp.Services = append(imp.Services, rel("Service", s.Namespace, s.Name, "the service itself", "bad"))
		} else if lost > 0 {
			lvl, note := "warn", fmt.Sprintf("loses %d of %d ready pods", lost, total)
			if lost == total {
				lvl, note = "bad", fmt.Sprintf("loses all %d ready pods: no traffic", total)
				outage[s.Namespace+"/"+s.Name] = true
			}
			imp.Services = append(imp.Services, rel("Service", s.Namespace, s.Name, note, lvl))
		} else {
			continue
		}
		if svcNames[s.Namespace] == nil {
			svcNames[s.Namespace] = map[string]bool{}
		}
		svcNames[s.Namespace][s.Name] = true
	}
	if kind == "services" {
		svcNames[ns] = map[string]bool{name: true}
	}
	for ins, names := range svcNames {
		for _, in := range ingressesFor(g, ins, names) {
			lvl := "warn"
			for _, ig := range g.ings {
				if ig.name == in.Name && ig.ns == in.Namespace {
					for _, s := range ig.services {
						if outage[ig.ns+"/"+s] {
							lvl = "bad"
						}
					}
				}
			}
			if kind == "namespaces" && in.Namespace == name {
				in.Note, lvl = in.Note+" (deleted with the namespace)", "bad"
			}
			in.Level = lvl
			imp.Ingresses = append(imp.Ingresses, in)
		}
	}
	nOut := len(outage)
	imp.Summary = append(imp.Summary, fmt.Sprintf("%d pod%s on %d workload%s would stop", len(affected), sfx(len(affected)), len(imp.Workloads), sfx(len(imp.Workloads))))
	if nOut > 0 {
		imp.Summary = append(imp.Summary, fmt.Sprintf("%d service%s would have no ready pod", nOut, sfx(nOut)))
	}
	if len(imp.Ingresses) > 0 {
		imp.Summary = append(imp.Summary, fmt.Sprintf("%d ingress%s route%s to affected services", len(imp.Ingresses), map[bool]string{true: "", false: "es"}[len(imp.Ingresses) == 1], map[bool]string{true: "s", false: ""}[len(imp.Ingresses) == 1]))
	}
	return imp, nil
}
