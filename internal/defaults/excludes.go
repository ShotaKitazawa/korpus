package defaults

// BuiltinExcludeResources is the built-in list of resources to skip.
// Entries are in "resource" or "resource.group" format.
var BuiltinExcludeResources = []string{
	// K8s core — high-churn or secret data
	"secrets",
	"events",
	"events.events.k8s.io",
	"leases.coordination.k8s.io",
	"endpointslices.discovery.k8s.io",
	"componentstatuses",
	// cert-manager — transient resources
	"certificaterequests.cert-manager.io",
	"orders.acme.cert-manager.io",
	"challenges.acme.cert-manager.io",
	// Cilium — runtime network/security state managed automatically by the CNI
	"ciliumendpoints.cilium.io",
	"ciliumidentities.cilium.io",
	// Argo Workflows — transient task execution results
	"workflowtaskresults.argoproj.io",
}

// BuiltinExcludeFields maps resource keys (in "resource.group" or "resource" form)
// to field paths that are always stripped from that resource type.
// These are appended to whatever fields the config provides; use
// disableBuiltinExcludes: true to opt out entirely.
var BuiltinExcludeFields = map[string][]string{
	// ArgoCD sets this on every reconcile loop — pure timestamp noise.
	"applications.argoproj.io": {"status.reconciledAt"},
}
