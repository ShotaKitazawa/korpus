package sanitizer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeleteField_TopLevel(t *testing.T) {
	obj := map[string]interface{}{
		"status":   map[string]interface{}{"phase": "Running"},
		"metadata": map[string]interface{}{"name": "test"},
	}
	DeleteField(obj, "status")
	assert.NotContains(t, obj, "status")
	assert.Contains(t, obj, "metadata")
}

func TestDeleteField_Nested(t *testing.T) {
	obj := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":            "test",
			"resourceVersion": "12345",
		},
	}
	DeleteField(obj, "metadata.resourceVersion")
	meta := obj["metadata"].(map[string]interface{})
	assert.NotContains(t, meta, "resourceVersion")
	assert.Contains(t, meta, "name")
}

func TestDeleteField_BracketNotation(t *testing.T) {
	obj := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]interface{}{
				"kubectl.kubernetes.io/last-applied-configuration": "...",
				"other": "keep",
			},
		},
	}
	DeleteField(obj, `metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"]`)
	annots := obj["metadata"].(map[string]interface{})["annotations"].(map[string]interface{})
	assert.NotContains(t, annots, "kubectl.kubernetes.io/last-applied-configuration")
	assert.Contains(t, annots, "other")
}

func TestDeleteField_NonExistentPath(t *testing.T) {
	obj := map[string]interface{}{"foo": "bar"}
	DeleteField(obj, "nonexistent.path") // must not panic
	assert.Equal(t, map[string]interface{}{"foo": "bar"}, obj)
}

func TestDeleteField_MissingIntermediateKey(t *testing.T) {
	obj := map[string]interface{}{"foo": "bar"}
	DeleteField(obj, "metadata.resourceVersion") // must not panic
	assert.Equal(t, map[string]interface{}{"foo": "bar"}, obj)
}

func TestDeleteFields_Multiple(t *testing.T) {
	obj := map[string]interface{}{
		"metadata": map[string]interface{}{
			"resourceVersion": "123",
			"name":            "test",
		},
		"status": map[string]interface{}{"phase": "Running"},
	}
	DeleteFields(obj, []string{"metadata.resourceVersion", "status"})
	assert.NotContains(t, obj, "status")
	assert.NotContains(t, obj["metadata"].(map[string]interface{}), "resourceVersion")
	assert.Equal(t, "test", obj["metadata"].(map[string]interface{})["name"])
}
