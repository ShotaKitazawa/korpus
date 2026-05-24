package discovery

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GVRInfo holds resource identity and scope.
type GVRInfo struct {
	Group      string
	Version    string
	Resource   string
	Namespaced bool
}

// GVR returns the schema.GroupVersionResource for use with the dynamic client.
func (g GVRInfo) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    g.Group,
		Version:  g.Version,
		Resource: g.Resource,
	}
}

// preferredResourcesLister is the minimal interface we need from the discovery client.
type preferredResourcesLister interface {
	ServerPreferredResources() ([]*metav1.APIResourceList, error)
}

// ListPreferredResources returns all available GVRs using the server's preferred versions.
// Sub-resources (e.g. pods/log) are excluded.
// Partial errors from the API server are tolerated when lists is non-nil.
func ListPreferredResources(dc preferredResourcesLister) ([]GVRInfo, error) {
	lists, err := dc.ServerPreferredResources()
	if err != nil && lists == nil {
		return nil, err
	}

	var result []GVRInfo
	for _, list := range lists {
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil {
			continue
		}
		for _, r := range list.APIResources {
			if strings.Contains(r.Name, "/") {
				continue // skip sub-resources
			}
			result = append(result, GVRInfo{
				Group:      gv.Group,
				Version:    gv.Version,
				Resource:   r.Name,
				Namespaced: r.Namespaced,
			})
		}
	}
	return result, nil
}
