package defaults

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuiltinExcludeResources(t *testing.T) {
	assert.NotEmpty(t, BuiltinExcludeResources)
	assert.Contains(t, BuiltinExcludeResources, "secrets")
	assert.Contains(t, BuiltinExcludeResources, "events")
	assert.Contains(t, BuiltinExcludeResources, "leases.coordination.k8s.io")
	assert.Contains(t, BuiltinExcludeResources, "certificaterequests.cert-manager.io")
}

func TestBuiltinExcludeFields(t *testing.T) {
	assert.NotEmpty(t, BuiltinExcludeFields)
	assert.Contains(t, BuiltinExcludeFields["applications.argoproj.io"], "status.reconciledAt")
}
