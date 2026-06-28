package defaults

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltinExclusionsDocumented(t *testing.T) {
	for _, e := range BuiltinExcludeResources {
		assert.NotEmpty(t, e.Note, "BuiltinExcludeResources entry %q has no documented rationale", e.Resource)
		assert.NotEmpty(t, e.Reason, "BuiltinExcludeResources entry %q has no ExclusionReason", e.Resource)
	}
	for _, e := range BuiltinExcludeObjects {
		assert.NotEmpty(t, e.Note, "BuiltinExcludeObjects entry %q (ownerKind=%s) has no documented rationale", e.Resource, e.OwnerKind)
		assert.NotEmpty(t, e.Reason, "BuiltinExcludeObjects entry %q has no ExclusionReason", e.Resource)
	}
}

func TestBuiltinExcludeResources(t *testing.T) {
	resources := make([]string, 0, len(BuiltinExcludeResources))
	for _, e := range BuiltinExcludeResources {
		resources = append(resources, e.Resource)
	}
	assert.NotEmpty(t, resources)
	assert.Contains(t, resources, "secrets")
	assert.Contains(t, resources, "events")
	assert.Contains(t, resources, "leases.coordination.k8s.io")
	assert.Contains(t, resources, "certificaterequests.cert-manager.io")
	assert.Contains(t, resources, "workflows.argoproj.io")
}

func TestBuiltinExcludeObjects(t *testing.T) {
	require.NotEmpty(t, BuiltinExcludeObjects)

	find := func(resource, ownerKind string) *ObjectExclusion {
		for i, e := range BuiltinExcludeObjects {
			if e.Resource == resource && e.OwnerKind == ownerKind {
				return &BuiltinExcludeObjects[i]
			}
		}
		return nil
	}

	e := find("jobs", "CronJob")
	require.NotNil(t, e, "expected jobs/CronJob exclusion")
	assert.Equal(t, ReasonExecutionArtifact, e.Reason)

	e = find("pods", "Job")
	require.NotNil(t, e, "expected pods/Job exclusion")
	assert.Equal(t, ReasonExecutionArtifact, e.Reason)
}

func TestBuiltinExcludeFields(t *testing.T) {
	assert.NotEmpty(t, BuiltinExcludeFields)
	assert.Contains(t, BuiltinExcludeFields["applications.argoproj.io"], "status.reconciledAt")
}
