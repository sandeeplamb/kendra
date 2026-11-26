package kube

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

// depAPI is one API version that Kubernetes has deprecated or removed.
type depAPI struct {
	Group, Version, Resource, Kind string
	Replacement                    string
	DeprecatedIn, RemovedIn        string // "1.25"; RemovedIn "" = no removal planned
}

// Source: the Kubernetes deprecated API migration guide. A cluster only reports the
// ones it still serves; removed versions simply are not there any more.
var depTable = []depAPI{
	{"flowcontrol.apiserver.k8s.io", "v1beta3", "flowschemas", "FlowSchema", "flowcontrol.apiserver.k8s.io/v1", "1.29", "1.32"},
	{"flowcontrol.apiserver.k8s.io", "v1beta3", "prioritylevelconfigurations", "PriorityLevelConfiguration", "flowcontrol.apiserver.k8s.io/v1", "1.29", "1.32"},
	{"flowcontrol.apiserver.k8s.io", "v1beta2", "flowschemas", "FlowSchema", "flowcontrol.apiserver.k8s.io/v1", "1.26", "1.29"},
	{"flowcontrol.apiserver.k8s.io", "v1beta2", "prioritylevelconfigurations", "PriorityLevelConfiguration", "flowcontrol.apiserver.k8s.io/v1", "1.26", "1.29"},
	{"flowcontrol.apiserver.k8s.io", "v1beta1", "flowschemas", "FlowSchema", "flowcontrol.apiserver.k8s.io/v1", "1.23", "1.26"},
	{"flowcontrol.apiserver.k8s.io", "v1beta1", "prioritylevelconfigurations", "PriorityLevelConfiguration", "flowcontrol.apiserver.k8s.io/v1", "1.23", "1.26"},
	{"autoscaling", "v2beta2", "horizontalpodautoscalers", "HorizontalPodAutoscaler", "autoscaling/v2", "1.23", "1.26"},
	{"autoscaling", "v2beta1", "horizontalpodautoscalers", "HorizontalPodAutoscaler", "autoscaling/v2", "1.22", "1.25"},
	{"batch", "v1beta1", "cronjobs", "CronJob", "batch/v1", "1.21", "1.25"},
	{"policy", "v1beta1", "poddisruptionbudgets", "PodDisruptionBudget", "policy/v1", "1.21", "1.25"},
	{"policy", "v1beta1", "podsecuritypolicies", "PodSecurityPolicy", "Pod Security Admission (no direct replacement)", "1.21", "1.25"},
	{"discovery.k8s.io", "v1beta1", "endpointslices", "EndpointSlice", "discovery.k8s.io/v1", "1.21", "1.25"},
	{"events.k8s.io", "v1beta1", "events", "Event", "events.k8s.io/v1", "1.22", "1.25"},
	{"node.k8s.io", "v1beta1", "runtimeclasses", "RuntimeClass", "node.k8s.io/v1", "1.22", "1.25"},
	{"storage.k8s.io", "v1beta1", "csistoragecapacities", "CSIStorageCapacity", "storage.k8s.io/v1", "1.24", "1.27"},
	{"networking.k8s.io", "v1beta1", "ingresses", "Ingress", "networking.k8s.io/v1", "1.19", "1.22"},
	{"networking.k8s.io", "v1beta1", "ingressclasses", "IngressClass", "networking.k8s.io/v1", "1.19", "1.22"},
	{"extensions", "v1beta1", "ingresses", "Ingress", "networking.k8s.io/v1", "1.14", "1.22"},
	{"rbac.authorization.k8s.io", "v1beta1", "roles", "Role", "rbac.authorization.k8s.io/v1", "1.17", "1.22"},
	{"rbac.authorization.k8s.io", "v1beta1", "rolebindings", "RoleBinding", "rbac.authorization.k8s.io/v1", "1.17", "1.22"},
	{"rbac.authorization.k8s.io", "v1beta1", "clusterroles", "ClusterRole", "rbac.authorization.k8s.io/v1", "1.17", "1.22"},
	{"rbac.authorization.k8s.io", "v1beta1", "clusterrolebindings", "ClusterRoleBinding", "rbac.authorization.k8s.io/v1", "1.17", "1.22"},
	{"apiextensions.k8s.io", "v1beta1", "customresourcedefinitions", "CustomResourceDefinition", "apiextensions.k8s.io/v1", "1.16", "1.22"},
	{"admissionregistration.k8s.io", "v1beta1", "validatingwebhookconfigurations", "ValidatingWebhookConfiguration", "admissionregistration.k8s.io/v1", "1.16", "1.22"},
	{"admissionregistration.k8s.io", "v1beta1", "mutatingwebhookconfigurations", "MutatingWebhookConfiguration", "admissionregistration.k8s.io/v1", "1.16", "1.22"},
	{"certificates.k8s.io", "v1beta1", "certificatesigningrequests", "CertificateSigningRequest", "certificates.k8s.io/v1", "1.19", "1.22"},
	{"coordination.k8s.io", "v1beta1", "leases", "Lease", "coordination.k8s.io/v1", "1.19", "1.22"},
	{"scheduling.k8s.io", "v1beta1", "priorityclasses", "PriorityClass", "scheduling.k8s.io/v1", "1.19", "1.22"},
	{"storage.k8s.io", "v1beta1", "storageclasses", "StorageClass", "storage.k8s.io/v1", "1.19", "1.22"},
	{"storage.k8s.io", "v1beta1", "csidrivers", "CSIDriver", "storage.k8s.io/v1", "1.19", "1.22"},
	{"storage.k8s.io", "v1beta1", "csinodes", "CSINode", "storage.k8s.io/v1", "1.17", "1.22"},
	{"apiregistration.k8s.io", "v1beta1", "apiservices", "APIService", "apiregistration.k8s.io/v1", "1.19", "1.22"},
}

// DeprecatedAPI is one deprecated API version a cluster still serves.
type DeprecatedAPI struct {
	APIVersion   string   `json:"apiVersion"` // group/version
	Resource     string   `json:"resource"`
	Kind         string   `json:"kind,omitempty"`
	Replacement  string   `json:"replacement,omitempty"`
	DeprecatedIn string   `json:"deprecatedIn,omitempty"`
	RemovedIn    string   `json:"removedIn,omitempty"`
	Urgency      string   `json:"urgency"` // bad: gone in the next minor, warn: removal planned, info: no removal planned
	Objects      int      `json:"objects"` // objects whose last write used this version
	More         bool     `json:"more,omitempty"`
	Managers     []string `json:"managers,omitempty"`
	Examples     []string `json:"examples,omitempty"`
	Requested    bool     `json:"requested"` // the API server saw requests for it (metrics)
}

// DeprecatedReport is what the page shows for one cluster.
type DeprecatedReport struct {
	Version string          `json:"version"`
	Items   []DeprecatedAPI `json:"items"`
	Note    string          `json:"note,omitempty"`
}

// DeprecatedAPIs finds deprecated API versions the cluster still serves and, for each,
// the objects whose last write by some client (kubectl, helm, kapp, a controller) used
// that version. That comes from metadata.managedFields[].apiVersion, which the API
// server sets from the version in the request. Where the API server's metrics are
// readable, it also marks versions that were requested since it started. List calls only.
func (c *Cluster) DeprecatedAPIs(ctx context.Context) (*DeprecatedReport, error) {
	sv, err := c.Cs.Discovery().ServerVersion()
	if err != nil {
		return nil, err
	}
	rep := &DeprecatedReport{Version: sv.GitVersion, Items: []DeprecatedAPI{}}
	cm := minorOf(sv.GitVersion)

	groups, err := c.Cs.Discovery().ServerGroups()
	if err != nil {
		return nil, err
	}
	servedGV := map[string]bool{}
	for _, g := range groups.Groups {
		for _, v := range g.Versions {
			servedGV[v.GroupVersion] = true
		}
	}
	requested := c.requestedDeprecated(ctx)
	if requested == nil {
		rep.Note = "The API server's metrics are not readable with this login, so \"requested\" is not shown."
	}
	resCache := map[string]map[string]bool{}
	served := func(gv, res string) bool {
		if !servedGV[gv] {
			return false
		}
		if _, ok := resCache[gv]; !ok {
			set := map[string]bool{}
			if rl, err := c.Cs.Discovery().ServerResourcesForGroupVersion(gv); err == nil {
				for _, r := range rl.APIResources {
					set[r.Name] = true
				}
			}
			resCache[gv] = set
		}
		return resCache[gv][res]
	}

	seen := map[string]bool{}
	for _, d := range depTable {
		gv := d.Group + "/" + d.Version
		key := gv + "/" + d.Resource
		req := requested[key]
		if !served(gv, d.Resource) && req == "" {
			continue
		}
		seen[key] = true
		it := DeprecatedAPI{APIVersion: gv, Resource: d.Resource, Kind: d.Kind, Replacement: d.Replacement,
			DeprecatedIn: d.DeprecatedIn, RemovedIn: d.RemovedIn, Requested: req != ""}
		it.Urgency = urgency(d.RemovedIn, cm)
		c.writtenVia(ctx, d, &it)
		rep.Items = append(rep.Items, it)
	}
	// deprecated versions the API server reports that the table does not know
	for key, removed := range requested {
		if seen[key] {
			continue
		}
		i := strings.LastIndexByte(key, '/')
		if i < 0 {
			continue
		}
		gv, res := key[:i], key[i+1:]
		rep.Items = append(rep.Items, DeprecatedAPI{APIVersion: gv, Resource: res, RemovedIn: removed,
			Urgency: urgency(removed, cm), Requested: true})
	}
	rank := map[string]int{"bad": 0, "warn": 1, "info": 2}
	sort.Slice(rep.Items, func(i, j int) bool {
		a, b := rep.Items[i], rep.Items[j]
		if rank[a.Urgency] != rank[b.Urgency] {
			return rank[a.Urgency] < rank[b.Urgency]
		}
		if a.Objects != b.Objects {
			return a.Objects > b.Objects
		}
		return a.APIVersion+a.Resource < b.APIVersion+b.Resource
	})
	return rep, nil
}

func urgency(removedIn string, clusterMinor int) string {
	r := minorOf(removedIn)
	switch {
	case r == 0:
		return "info"
	case clusterMinor != 0 && r <= clusterMinor+1:
		return "bad"
	}
	return "warn"
}

// requestedDeprecated reads apiserver_requested_deprecated_apis from /metrics:
// "group/version/resource" -> removed release. nil when /metrics is not readable.
func (c *Cluster) requestedDeprecated(ctx context.Context) map[string]string {
	raw, err := c.Cs.CoreV1().RESTClient().Get().AbsPath("/metrics").DoRaw(ctx)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, ln := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(ln, "apiserver_requested_deprecated_apis{") {
			continue
		}
		l := map[string]string{}
		if i, j := strings.IndexByte(ln, '{'), strings.LastIndexByte(ln, '}'); i > 0 && j > i {
			for _, kv := range strings.Split(ln[i+1:j], ",") {
				if k, v, ok := strings.Cut(kv, "="); ok {
					l[k] = strings.Trim(v, `"`)
				}
			}
		}
		if l["version"] == "" || l["resource"] == "" {
			continue
		}
		gv := l["version"]
		if l["group"] != "" {
			gv = l["group"] + "/" + l["version"]
		}
		out[gv+"/"+l["resource"]] = l["removed_release"]
	}
	return out
}

// writtenVia counts the objects of d whose managedFields mention the deprecated version.
func (c *Cluster) writtenVia(ctx context.Context, d depAPI, it *DeprecatedAPI) {
	base := "/apis/" + d.Group + "/" + d.Version + "/" + d.Resource
	mgr := map[string]bool{}
	cont := ""
	for page := 0; page < 5; page++ {
		req := c.Cs.CoreV1().RESTClient().Get().AbsPath(base).SetHeader("Accept", partialMeta).Param("limit", "500")
		if cont != "" {
			req = req.Param("continue", cont)
		}
		raw, err := req.DoRaw(ctx)
		if err != nil {
			return
		}
		var l struct {
			Metadata struct {
				Continue string `json:"continue"`
			} `json:"metadata"`
			Items []struct {
				Metadata struct {
					Name          string `json:"name"`
					Namespace     string `json:"namespace"`
					ManagedFields []struct {
						Manager    string `json:"manager"`
						APIVersion string `json:"apiVersion"`
					} `json:"managedFields"`
				} `json:"metadata"`
			} `json:"items"`
		}
		if json.Unmarshal(raw, &l) != nil {
			return
		}
		gv := d.Group + "/" + d.Version
		for _, o := range l.Items {
			hit := false
			for _, mf := range o.Metadata.ManagedFields {
				if mf.APIVersion == gv {
					hit = true
					mgr[mf.Manager] = true
				}
			}
			if !hit {
				continue
			}
			it.Objects++
			if len(it.Examples) < 5 {
				n := o.Metadata.Name
				if o.Metadata.Namespace != "" {
					n = o.Metadata.Namespace + "/" + n
				}
				it.Examples = append(it.Examples, n)
			}
		}
		if cont = l.Metadata.Continue; cont == "" {
			break
		}
		if page == 4 {
			it.More = true
		}
	}
	for m := range mgr {
		it.Managers = append(it.Managers, m)
	}
	sort.Strings(it.Managers)
}
