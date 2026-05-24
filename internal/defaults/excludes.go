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
}
