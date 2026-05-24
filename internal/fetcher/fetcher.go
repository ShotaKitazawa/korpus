package fetcher

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	"github.com/ShotaKitazawa/korpus/internal/discovery"
)

// ListAll fetches all objects for the given GVR.
// For namespaced resources it lists across all namespaces (Namespace("")).
func ListAll(ctx context.Context, dyn dynamic.Interface, gvr discovery.GVRInfo) ([]unstructured.Unstructured, error) {
	list, err := dyn.Resource(gvr.GVR()).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", gvr.Resource, err)
	}
	return list.Items, nil
}
