package discovery

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type fakeDiscovery struct {
	resources []*metav1.APIResourceList
	err       error
}

func (f *fakeDiscovery) ServerPreferredResources() ([]*metav1.APIResourceList, error) {
	return f.resources, f.err
}

func TestListPreferredResources(t *testing.T) {
	dc := &fakeDiscovery{
		resources: []*metav1.APIResourceList{
			{
				GroupVersion: "apps/v1",
				APIResources: []metav1.APIResource{
					{Name: "deployments", Namespaced: true},
					{Name: "deployments/scale", Namespaced: true}, // sub-resource — skipped
				},
			},
			{
				GroupVersion: "v1",
				APIResources: []metav1.APIResource{
					{Name: "pods", Namespaced: true},
					{Name: "nodes", Namespaced: false},
				},
			},
		},
	}

	resources, err := ListPreferredResources(dc)
	require.NoError(t, err)
	assert.Len(t, resources, 3)

	names := make([]string, len(resources))
	for i, r := range resources {
		names[i] = r.Resource
	}
	assert.Contains(t, names, "deployments")
	assert.Contains(t, names, "pods")
	assert.Contains(t, names, "nodes")
	assert.NotContains(t, names, "deployments/scale")
}

func TestListPreferredResources_NamespacedFlag(t *testing.T) {
	dc := &fakeDiscovery{
		resources: []*metav1.APIResourceList{
			{
				GroupVersion: "v1",
				APIResources: []metav1.APIResource{
					{Name: "pods", Namespaced: true},
					{Name: "nodes", Namespaced: false},
				},
			},
		},
	}

	resources, err := ListPreferredResources(dc)
	require.NoError(t, err)
	for _, r := range resources {
		if r.Resource == "pods" {
			assert.True(t, r.Namespaced)
		}
		if r.Resource == "nodes" {
			assert.False(t, r.Namespaced)
		}
	}
}

func TestListPreferredResources_PartialError(t *testing.T) {
	dc := &fakeDiscovery{
		resources: []*metav1.APIResourceList{
			{
				GroupVersion: "v1",
				APIResources: []metav1.APIResource{
					{Name: "pods", Namespaced: true},
				},
			},
		},
		err: assert.AnError, // non-nil error but non-nil lists
	}

	resources, err := ListPreferredResources(dc)
	// partial error: we still get results
	require.NoError(t, err)
	assert.Len(t, resources, 1)
}
