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

func TestDeleteField_ArrayWildcard(t *testing.T) {
	obj := map[string]interface{}{
		"status": map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{
					"type":               "Ready",
					"lastHeartbeatTime":  "2024-01-01T00:00:00Z",
					"lastTransitionTime": "2024-01-01T00:00:00Z",
				},
				map[string]interface{}{
					"type":               "MemoryPressure",
					"lastHeartbeatTime":  "2024-01-01T00:01:00Z",
					"lastTransitionTime": "2024-01-01T00:00:00Z",
				},
			},
		},
	}
	DeleteField(obj, "status.conditions[*].lastHeartbeatTime")
	conditions := obj["status"].(map[string]interface{})["conditions"].([]interface{})
	for _, c := range conditions {
		cond := c.(map[string]interface{})
		assert.NotContains(t, cond, "lastHeartbeatTime")
		assert.Contains(t, cond, "lastTransitionTime")
		assert.Contains(t, cond, "type")
	}
}

func TestDeleteField_ArrayWildcard_NonMapElementSkipped(t *testing.T) {
	obj := map[string]interface{}{
		"items": []interface{}{"string-element", 42},
	}
	DeleteField(obj, "items[*].field") // must not panic
	assert.Equal(t, []interface{}{"string-element", 42}, obj["items"])
}

func TestDeleteField_ArrayWildcard_NotASlice(t *testing.T) {
	obj := map[string]interface{}{
		"status": map[string]interface{}{
			"conditions": "not-a-slice",
		},
	}
	DeleteField(obj, "status.conditions[*].lastHeartbeatTime") // must not panic
	assert.Equal(t, "not-a-slice", obj["status"].(map[string]interface{})["conditions"])
}
