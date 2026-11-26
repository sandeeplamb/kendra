// Package kube talks to Kubernetes clusters: kubeconfig contexts, resource tables,
// health, explanations and (audited) changes.
package kube

import (
	"fmt"
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Kind describes a resource type Kendra can list.
type Kind struct {
	Name       string `json:"name"`  // plural, e.g. "deployments"
	Title      string `json:"title"` // shown in the UI
	Group      string `json:"group"` // "" for core
	Version    string `json:"version"`
	Namespaced bool   `json:"namespaced"`
	Category   string `json:"category"`
	Singular   string `json:"singular"`
}

// Kinds is the catalogue, in the order the left rail shows it. An empty Version means
// "whatever version this cluster serves" (add-ons such as KEDA, Karpenter and Istio
// differ between installs); see Cluster.Resolve.
var Kinds = []Kind{
	// Workloads
	{"pods", "Pods", "", "v1", true, "Workloads", "Pod"},
	{"deployments", "Deployments", "apps", "v1", true, "Workloads", "Deployment"},
	{"statefulsets", "StatefulSets", "apps", "v1", true, "Workloads", "StatefulSet"},
	{"daemonsets", "DaemonSets", "apps", "v1", true, "Workloads", "DaemonSet"},
	{"replicasets", "ReplicaSets", "apps", "v1", true, "Workloads", "ReplicaSet"},
	{"jobs", "Jobs", "batch", "v1", true, "Workloads", "Job"},
	{"cronjobs", "CronJobs", "batch", "v1", true, "Workloads", "CronJob"},
	// Cluster
	{"nodes", "Nodes", "", "v1", false, "Cluster", "Node"},
	{"namespaces", "Namespaces", "", "v1", false, "Cluster", "Namespace"},
	{"events", "Events", "", "v1", true, "Cluster", "Event"},
	// Config
	{"configmaps", "ConfigMaps", "", "v1", true, "Config", "ConfigMap"},
	{"secrets", "Secrets", "", "v1", true, "Config", "Secret"},
	{"resourcequotas", "Resource quotas", "", "v1", true, "Config", "ResourceQuota"},
	{"limitranges", "Limit ranges", "", "v1", true, "Config", "LimitRange"},
	{"poddisruptionbudgets", "Pod disruption budgets", "policy", "v1", true, "Config", "PodDisruptionBudget"},
	{"priorityclasses", "Priority classes", "scheduling.k8s.io", "v1", false, "Config", "PriorityClass"},
	{"runtimeclasses", "Runtime classes", "node.k8s.io", "v1", false, "Config", "RuntimeClass"},
	{"leases", "Leases", "coordination.k8s.io", "v1", true, "Config", "Lease"},
	// Network
	{"services", "Services", "", "v1", true, "Network", "Service"},
	{"endpoints", "Endpoints", "", "v1", true, "Network", "Endpoints"},
	{"endpointslices", "Endpoint slices", "discovery.k8s.io", "v1", true, "Network", "EndpointSlice"},
	{"ingresses", "Ingresses", "networking.k8s.io", "v1", true, "Network", "Ingress"},
	{"ingressclasses", "Ingress classes", "networking.k8s.io", "v1", false, "Network", "IngressClass"},
	{"gateways", "Istio gateways", "networking.istio.io", "", true, "Network", "Gateway"},
	{"virtualservices", "Istio virtual services", "networking.istio.io", "", true, "Network", "VirtualService"},
	{"networkpolicies", "Network policies", "networking.k8s.io", "v1", true, "Network", "NetworkPolicy"},
	// Storage
	{"persistentvolumeclaims", "Persistent volume claims", "", "v1", true, "Storage", "PersistentVolumeClaim"},
	{"persistentvolumes", "Persistent volumes", "", "v1", false, "Storage", "PersistentVolume"},
	{"storageclasses", "Storage classes", "storage.k8s.io", "v1", false, "Storage", "StorageClass"},
	// Access Control
	{"serviceaccounts", "Service accounts", "", "v1", true, "Access Control", "ServiceAccount"},
	{"clusterroles", "Cluster roles", "rbac.authorization.k8s.io", "v1", false, "Access Control", "ClusterRole"},
	{"roles", "Roles", "rbac.authorization.k8s.io", "v1", true, "Access Control", "Role"},
	{"clusterrolebindings", "Cluster role bindings", "rbac.authorization.k8s.io", "v1", false, "Access Control", "ClusterRoleBinding"},
	{"rolebindings", "Role bindings", "rbac.authorization.k8s.io", "v1", true, "Access Control", "RoleBinding"},
	// Pod Autoscaling
	{"horizontalpodautoscalers", "HPAs", "autoscaling", "v2", true, "Pod Autoscaling", "HorizontalPodAutoscaler"},
	{"scaledobjects", "ScaledObjects", "keda.sh", "", true, "Pod Autoscaling", "ScaledObject"},
	{"triggerauthentications", "TriggerAuthentications", "keda.sh", "", true, "Pod Autoscaling", "TriggerAuthentication"},
	{"clustertriggerauthentications", "ClusterTriggerAuthentications", "keda.sh", "", false, "Pod Autoscaling", "ClusterTriggerAuthentication"},
	// Node Autoscaling (Karpenter)
	{"nodepools", "NodePools", "karpenter.sh", "", false, "Node Autoscaling", "NodePool"},
	{"ec2nodeclasses", "EC2NodeClasses", "karpenter.k8s.aws", "", false, "Node Autoscaling", "EC2NodeClass"},
	{"nodeclaims", "NodeClaims", "karpenter.sh", "", false, "Node Autoscaling", "NodeClaim"},
	{"nodeoverlays", "NodeOverlays", "karpenter.sh", "", false, "Node Autoscaling", "NodeOverlay"},
	{"aksnodeclasses", "AKSNodeClasses", "karpenter.azure.com", "", false, "Node Autoscaling", "AKSNodeClass"},
	// Kyverno: policies, exceptions, cleanup policies and the policy reports they produce
	{"clusterpolicies", "Cluster policies", "kyverno.io", "", false, "Kyverno", "ClusterPolicy"},
	{"policies", "Policies", "kyverno.io", "", true, "Kyverno", "Policy"},
	{"validatingpolicies", "Validating policies", "policies.kyverno.io", "", false, "Kyverno", "ValidatingPolicy"},
	{"mutatingpolicies", "Mutating policies", "policies.kyverno.io", "", false, "Kyverno", "MutatingPolicy"},
	{"policyexceptions", "Policy exceptions", "kyverno.io", "", true, "Kyverno", "PolicyException"},
	{"clusterpolicyreports", "Cluster policy reports", "wgpolicyk8s.io", "", false, "Kyverno", "ClusterPolicyReport"},
	{"policyreports", "Policy reports", "wgpolicyk8s.io", "", true, "Kyverno", "PolicyReport"},
	{"cleanuppolicies", "Cleanup policies", "kyverno.io", "", true, "Kyverno", "CleanupPolicy"},
	{"clustercleanuppolicies", "Cluster cleanup policies", "kyverno.io", "", false, "Kyverno", "ClusterCleanupPolicy"},
	// Custom Resources: the definitions; the instances of each are listed per CRD
	{"customresourcedefinitions", "Definitions", "apiextensions.k8s.io", "v1", false, "Custom Resources", "CustomResourceDefinition"},
}

// NotInstalledError says a resource type is not served by the cluster (the add-on that
// provides it is not installed). It is not a failure of the cluster.
type NotInstalledError struct{ Title, Group string }

func (e *NotInstalledError) Error() string {
	return fmt.Sprintf("%s (%s) is not installed in this cluster", e.Title, e.Group)
}

// Resolve fills in the served version (and scope) of a kind with an empty Version.
func (c *Cluster) Resolve(k Kind) (Kind, error) {
	if k.Version != "" {
		return k, nil
	}
	for _, ar := range c.discoverAll() {
		if ar.gvr.Group == k.Group && ar.gvr.Resource == k.Name {
			k.Version, k.Namespaced = ar.gvr.Version, ar.namespaced
			return k, nil
		}
	}
	return k, &NotInstalledError{k.Title, k.Group}
}

// KindFor describes any resource the cluster serves (a CRD, for example) by group and plural.
func (c *Cluster) KindFor(group, resource string) (Kind, error) {
	for _, ar := range c.discoverAll() {
		if ar.gvr.Group == group && ar.gvr.Resource == resource {
			return Kind{Name: resource, Title: ar.kind, Group: group, Version: ar.gvr.Version, Namespaced: ar.namespaced,
				Category: "Custom Resources", Singular: ar.kind}, nil
		}
	}
	return Kind{}, &NotInstalledError{resource, group}
}

// FindKind returns the kind with the given plural name.
func FindKind(name string) (Kind, bool) {
	for _, k := range Kinds {
		if k.Name == name {
			return k, true
		}
	}
	return Kind{}, false
}

// GVR is the group/version/resource of the kind.
func (k Kind) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: k.Group, Version: k.Version, Resource: k.Name}
}

// Path is the API path for the collection (name == "") or one object. ns is ignored
// for cluster-scoped kinds; an empty ns on a namespaced kind means all namespaces.
func (k Kind) Path(ns, name string) string {
	var b strings.Builder
	if k.Group == "" {
		b.WriteString("/api/" + k.Version)
	} else {
		b.WriteString("/apis/" + k.Group + "/" + k.Version)
	}
	if k.Namespaced && ns != "" {
		b.WriteString("/namespaces/" + url.PathEscape(ns))
	}
	b.WriteString("/" + k.Name)
	if name != "" {
		b.WriteString("/" + url.PathEscape(name))
	}
	return b.String()
}
