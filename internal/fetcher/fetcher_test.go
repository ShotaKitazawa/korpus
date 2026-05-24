package fetcher

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/ShotaKitazawa/korpus/internal/discovery"
)

var testScheme = runtime.NewScheme()

func pod(name, ns string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Pod"})
	u.SetName(name)
	u.SetNamespace(ns)
	return u
}

func node(name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Node"})
	u.SetName(name)
	return u
}

func TestListAll_Namespaced(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Version: "v1", Kind: "PodList"}, &unstructured.UnstructuredList{})

	client := dynamicfake.NewSimpleDynamicClient(scheme,
		pod("pod-a", "default"),
		pod("pod-b", "kube-system"),
	)

	gvr := discovery.GVRInfo{Group: "", Version: "v1", Resource: "pods", Namespaced: true}
	items, err := ListAll(context.Background(), client, gvr)
	require.NoError(t, err)
	assert.Len(t, items, 2)

	names := []string{items[0].GetName(), items[1].GetName()}
	assert.ElementsMatch(t, []string{"pod-a", "pod-b"}, names)
}

func TestListAll_ClusterScoped(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Version: "v1", Kind: "Node"}, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Version: "v1", Kind: "NodeList"}, &unstructured.UnstructuredList{})

	client := dynamicfake.NewSimpleDynamicClient(scheme,
		node("node-1"),
		node("node-2"),
	)

	gvr := discovery.GVRInfo{Group: "", Version: "v1", Resource: "nodes", Namespaced: false}
	items, err := ListAll(context.Background(), client, gvr)
	require.NoError(t, err)

	names := make([]string, len(items))
	for i, item := range items {
		names[i] = item.GetName()
	}
	assert.ElementsMatch(t, []string{"node-1", "node-2"}, names)
}

func init() {
	metav1.AddToGroupVersion(testScheme, schema.GroupVersion{Version: "v1"})
}
