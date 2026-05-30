package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var defaultFields = []string{"metadata.labels", "metadata.creationTimestamp"}

func writeYAML(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

func setup(t *testing.T) (string, *Index) {
	t.Helper()
	dir := t.TempDir()
	writeYAML(t, dir, "pod-default.yaml", `
kind: Pod
metadata:
  name: my-pod
  namespace: default
  labels:
    app: my-app
spec:
  dnsPolicy: ClusterFirst
`)
	writeYAML(t, dir, "deploy-kube-system.yaml", `
kind: Deployment
metadata:
  name: coredns
  namespace: kube-system
`)
	writeYAML(t, dir, "node.yaml", `
kind: Node
metadata:
  name: node-1
`)
	idx := New("test-cluster", defaultFields)
	require.NoError(t, idx.Build(dir))
	return dir, idx
}

func TestBuild_CountsResources(t *testing.T) {
	_, idx := setup(t)
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	assert.Len(t, idx.resources, 3)
	for _, r := range idx.resources {
		assert.Equal(t, "test-cluster", r.Cluster)
	}
}

func TestNamespaces(t *testing.T) {
	_, idx := setup(t)
	ns := idx.Namespaces()
	assert.ElementsMatch(t, []string{"default", "kube-system"}, ns)
}

func TestKinds_All(t *testing.T) {
	_, idx := setup(t)
	kinds := idx.Kinds("")
	assert.ElementsMatch(t, []string{"Deployment", "Node", "Pod"}, kinds)
}

func TestKinds_ByNamespace(t *testing.T) {
	_, idx := setup(t)
	kinds := idx.Kinds("default")
	assert.Equal(t, []string{"Pod"}, kinds)
}

func TestList_ByKind(t *testing.T) {
	_, idx := setup(t)
	pods := idx.List("Pod", "", nil)
	assert.Len(t, pods, 1)
	assert.Equal(t, "my-pod", pods[0].Name)
}

func TestList_ByNamespace(t *testing.T) {
	_, idx := setup(t)
	items := idx.List("", "kube-system", nil)
	assert.Len(t, items, 1)
	assert.Equal(t, "coredns", items[0].Name)
}

func TestList_ByKindAndNamespace(t *testing.T) {
	_, idx := setup(t)
	items := idx.List("Deployment", "kube-system", nil)
	assert.Len(t, items, 1)
	assert.Equal(t, "Deployment", items[0].Kind)
}

func TestList_All(t *testing.T) {
	_, idx := setup(t)
	assert.Len(t, idx.List("", "", nil), 3)
}

func TestGet_Found(t *testing.T) {
	_, idx := setup(t)
	r, ok := idx.Get("Pod", "default", "my-pod")
	assert.True(t, ok)
	assert.Equal(t, "my-pod", r.Name)
	assert.Equal(t, map[string]string{"app": "my-app"}, r.Labels)
}

func TestGet_NotFound(t *testing.T) {
	_, idx := setup(t)
	_, ok := idx.Get("Pod", "default", "nonexistent")
	assert.False(t, ok)
}

func TestBuild_SkipsNonYAML(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o644))
	idx := New("test-cluster", defaultFields)
	require.NoError(t, idx.Build(dir))
	assert.Empty(t, idx.List("", "", nil))
}

func TestBuild_MultiDoc(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "multi.yaml", `
kind: Pod
metadata:
  name: pod-a
  namespace: ns1
---
kind: Service
metadata:
  name: svc-b
  namespace: ns1
`)
	idx := New("test-cluster", defaultFields)
	require.NoError(t, idx.Build(dir))
	assert.Len(t, idx.List("", "", nil), 2)
}

// --- Query tests ---

func TestQuery_KindRequired(t *testing.T) {
	_, idx := setup(t)
	_, err := idx.Query("", "", nil, "")
	assert.Error(t, err)
}

func TestQuery_EmptyExpr(t *testing.T) {
	_, idx := setup(t)
	results, err := idx.Query("Pod", "", nil, "")
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "my-pod", results[0].Name)
}

func TestQuery_Namespace(t *testing.T) {
	_, idx := setup(t)
	results, err := idx.Query("Deployment", "kube-system", nil, "")
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "coredns", results[0].Name)
}

func TestQuery_CELLabel(t *testing.T) {
	_, idx := setup(t)
	results, err := idx.Query("Pod", "", nil, `object.metadata.labels["app"] == "my-app"`)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "my-pod", results[0].Name)
}

func TestQuery_CELLabelNoMatch(t *testing.T) {
	_, idx := setup(t)
	results, err := idx.Query("Pod", "", nil, `object.metadata.labels["app"] == "other"`)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestList_LabelSelector(t *testing.T) {
	_, idx := setup(t)
	results := idx.List("Pod", "", map[string]string{"app": "my-app"})
	require.Len(t, results, 1)
	assert.Equal(t, "my-pod", results[0].Name)
}

func TestList_LabelSelectorNoMatch(t *testing.T) {
	_, idx := setup(t)
	results := idx.List("Pod", "", map[string]string{"app": "other"})
	assert.Empty(t, results)
}

func TestList_LabelKeyOnly(t *testing.T) {
	_, idx := setup(t)
	results := idx.List("", "", map[string]string{"app": ""})
	require.Len(t, results, 1)
	assert.Equal(t, "my-pod", results[0].Name)
}

func TestQuery_CELFallback(t *testing.T) {
	// Build index with no configured fields → IndexedFields will be empty.
	// Query on spec.dnsPolicy must fall back to disk load.
	dir := t.TempDir()
	writeYAML(t, dir, "pod.yaml", `
kind: Pod
metadata:
  name: fallback-pod
  namespace: default
spec:
  dnsPolicy: ClusterFirst
`)
	idx := New("test-cluster", []string{})
	require.NoError(t, idx.Build(dir))

	results, err := idx.Query("Pod", "", nil, `object.spec.dnsPolicy == "ClusterFirst"`)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "fallback-pod", results[0].Name)
}

func TestQuery_InvalidCEL(t *testing.T) {
	_, idx := setup(t)
	_, err := idx.Query("Pod", "", nil, `object.metadata.name ===`)
	assert.Error(t, err)
}
