package defaults

// ExclusionReason classifies why a resource or object is excluded from the information base.
// Korpus generates an information base that answers "what is the cluster's intent and state?"
// Resources that do not contribute to this question are excluded under one of these reasons.
type ExclusionReason string

const (
	// ReasonExecutionArtifact covers resources that record what was executed rather than
	// what the cluster intends. Execution history belongs in logs/metrics, not the
	// information base. Examples: CronJob-generated Jobs, their Pods, Argo Workflow runs.
	ReasonExecutionArtifact ExclusionReason = "execution-artifact"

	// ReasonRuntimeDerived covers resources that are automatically computed from other
	// resources or represent transient runtime state. They carry no independent configuration
	// information. Examples: EndpointSlices (derived from Services+Pods), CiliumEndpoints.
	ReasonRuntimeDerived ExclusionReason = "runtime-derived"

	// ReasonSecurity covers resources excluded for security reasons regardless of their
	// information-base classification. This is an explicit exception to the two reasons above.
	ReasonSecurity ExclusionReason = "security"
)

// ResourceExclusion is a builtin rule that skips an entire resource type.
type ResourceExclusion struct {
	Resource string
	Reason   ExclusionReason
	Note     string
}

// ObjectExclusion is a builtin rule that skips individual objects based on ownerReferences.
// This extends resource-level exclusion to the object level using K8s lifecycle semantics:
// an object owned by a particular kind is an execution artifact of that owner.
type ObjectExclusion struct {
	Resource  string
	OwnerKind string
	Reason    ExclusionReason
	Note      string
}

// BuiltinExcludeResources is the built-in list of resource types to skip entirely.
// Entries use "resource" or "resource.group" format.
// Every entry must have a non-empty Note explaining the rationale (enforced by test).
var BuiltinExcludeResources = []ResourceExclusion{
	// K8s core
	{
		Resource: "secrets",
		Reason:   ReasonSecurity,
		Note:     "Contains sensitive credentials; security exception overrides information-base classification.",
	},
	{
		Resource: "events",
		Reason:   ReasonExecutionArtifact,
		Note:     "Records what happened, not what the cluster intends. Use dedicated event storage for history.",
	},
	{
		Resource: "events.events.k8s.io",
		Reason:   ReasonExecutionArtifact,
		Note:     "Same as events; served under events.k8s.io API group.",
	},
	{
		Resource: "leases.coordination.k8s.io",
		Reason:   ReasonRuntimeDerived,
		Note:     "Leader-election heartbeat tokens; pure runtime coordination state with no configuration value.",
	},
	{
		Resource: "endpointslices.discovery.k8s.io",
		Reason:   ReasonRuntimeDerived,
		Note:     "Automatically derived from Services and ready Pods by the EndpointSlice controller.",
	},
	{
		Resource: "componentstatuses",
		Reason:   ReasonRuntimeDerived,
		Note:     "Deprecated live health-check results for control-plane components; no configuration information.",
	},
	// cert-manager
	{
		Resource: "certificaterequests.cert-manager.io",
		Reason:   ReasonExecutionArtifact,
		Note:     "Transient signing request created during certificate issuance; Certificate is the desired state.",
	},
	{
		Resource: "orders.acme.cert-manager.io",
		Reason:   ReasonExecutionArtifact,
		Note:     "Transient ACME order object created during certificate issuance.",
	},
	{
		Resource: "challenges.acme.cert-manager.io",
		Reason:   ReasonExecutionArtifact,
		Note:     "Transient ACME challenge object created during certificate issuance.",
	},
	// Cilium
	{
		Resource: "ciliumendpoints.cilium.io",
		Reason:   ReasonRuntimeDerived,
		Note:     "Per-pod CNI endpoint state maintained automatically by the Cilium agent.",
	},
	{
		Resource: "ciliumidentities.cilium.io",
		Reason:   ReasonRuntimeDerived,
		Note:     "Security identity derived from pod labels by the Cilium operator; recreated on demand.",
	},
	// Argo Workflows
	{
		Resource: "workflowtaskresults.argoproj.io",
		Reason:   ReasonExecutionArtifact,
		Note:     "Per-node execution result emitted during a Workflow run; Workflow/WorkflowTemplate is the desired state.",
	},
	{
		Resource: "workflows.argoproj.io",
		Reason:   ReasonExecutionArtifact,
		Note:     "Individual Workflow run instance; WorkflowTemplate is the desired state. Runs are execution history.",
	},
	// metrics-server
	{
		Resource: "pods.metrics.k8s.io",
		Reason:   ReasonRuntimeDerived,
		Note:     "Live CPU/memory samples from metrics-server; point-in-time values with no configuration information.",
	},
	{
		Resource: "nodes.metrics.k8s.io",
		Reason:   ReasonRuntimeDerived,
		Note:     "Live CPU/memory samples from metrics-server; point-in-time values with no configuration information.",
	},
}

// BuiltinExcludeObjects is the built-in list of object-level exclusion rules.
// An object is excluded when its resource type matches Resource and at least one
// ownerReference has Kind == OwnerKind. This captures execution artifacts that
// cannot be identified at the resource-type level alone.
var BuiltinExcludeObjects = []ObjectExclusion{
	{
		Resource:  "jobs",
		OwnerKind: "CronJob",
		Reason:    ReasonExecutionArtifact,
		Note:      "Jobs generated by CronJobs are routine execution instances. CronJob is the desired state; individual runs are execution history.",
	},
	{
		Resource:  "pods",
		OwnerKind: "Job",
		Reason:    ReasonExecutionArtifact,
		Note:      "Pods owned by Jobs are transient execution containers. The Job (or CronJob) spec is the desired state.",
	},
}

// BuiltinExcludeFields maps resource keys (in "resource.group" or "resource" form)
// to field paths that are always stripped from that resource type.
// These are appended to whatever fields the config provides; use
// disableBuiltinExcludes: true to opt out entirely.
var BuiltinExcludeFields = map[string][]string{
	// ArgoCD sets this on every reconcile loop — pure timestamp noise.
	"applications.argoproj.io": {"status.reconciledAt"},
	// Grafana Operator updates these on every reconcile loop.
	"grafanadashboards.grafana.integreatly.org":      {"status.lastResync"},
	"grafanadatasources.grafana.integreatly.org":     {"status.lastResync", "status.hash"},
	"grafanaserviceaccounts.grafana.integreatly.org": {"status.lastResync", "status.conditions"},
	// Node heartbeat — lastHeartbeatTime updates every ~10s regardless of node state.
	"nodes": {"status.conditions[*].lastHeartbeatTime"},
}
