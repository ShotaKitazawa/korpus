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

// setup creates a dir with flat YAML files (no group/version path structure).
// Group falls back to apiVersion parsing.
func setup(t *testing.T) (string, *Index) {
	t.Helper()
	dir := t.TempDir()
	writeYAML(t, dir, "pod-default.yaml", `
apiVersion: v1
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
apiVersion: apps/v1
kind: Deployment
metadata:
  name: coredns
  namespace: kube-system
`)
	writeYAML(t, dir, "node.yaml", `
apiVersion: v1
kind: Node
metadata:
  name: node-1
`)
	idx := New("test-cluster", defaultFields)
	require.NoError(t, idx.Build(dir))
	return dir, idx
}

// setupWithPaths creates a dir with proper group/version directory structure.
func setupWithPaths(t *testing.T) (string, *Index) {
	t.Helper()
	dir := t.TempDir()
	// apps/v1/namespaces/default/deployments/myapp.yaml
	appsDir := filepath.Join(dir, "apps", "v1", "namespaces", "default", "deployments")
	require.NoError(t, os.MkdirAll(appsDir, 0o755))
	writeYAML(t, appsDir, "myapp.yaml", `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: myapp
  namespace: default
`)
	// core/v1/namespaces/default/pods/mypod.yaml
	coreDir := filepath.Join(dir, "core", "v1", "namespaces", "default", "pods")
	require.NoError(t, os.MkdirAll(coreDir, 0o755))
	writeYAML(t, coreDir, "mypod.yaml", `
apiVersion: v1
kind: Pod
metadata:
  name: mypod
  namespace: default
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

func TestBuild_GroupFromPath(t *testing.T) {
	_, idx := setupWithPaths(t)
	deploy, ok := idx.Get("apps", "Deployment", "default", "myapp")
	require.True(t, ok)
	assert.Equal(t, "apps", deploy.Group)

	pod, ok := idx.Get("core", "Pod", "default", "mypod")
	require.True(t, ok)
	assert.Equal(t, "core", pod.Group)
}

func TestBuild_GroupFromAPIVersion(t *testing.T) {
	_, idx := setup(t)
	// flat files: group extracted from apiVersion
	pod, ok := idx.Get("core", "Pod", "default", "my-pod")
	require.True(t, ok)
	assert.Equal(t, "core", pod.Group)

	dep, ok := idx.Get("apps", "Deployment", "kube-system", "coredns")
	require.True(t, ok)
	assert.Equal(t, "apps", dep.Group)
}

func TestNamespaces(t *testing.T) {
	_, idx := setup(t)
	ns := idx.Namespaces()
	assert.ElementsMatch(t, []string{"default", "kube-system"}, ns)
}

func TestGVKs_All(t *testing.T) {
	_, idx := setup(t)
	gvks := idx.GVKs("")
	assert.ElementsMatch(t, []GVKInfo{
		{Group: "apps", Version: "v1", Kind: "Deployment"},
		{Group: "core", Version: "v1", Kind: "Node"},
		{Group: "core", Version: "v1", Kind: "Pod"},
	}, gvks)
}

func TestGVKs_ByNamespace(t *testing.T) {
	_, idx := setup(t)
	gvks := idx.GVKs("default")
	assert.ElementsMatch(t, []GVKInfo{{Group: "core", Version: "v1", Kind: "Pod"}}, gvks)
}

func TestList_ByKind(t *testing.T) {
	_, idx := setup(t)
	pods := idx.List("", "Pod", "", nil)
	assert.Len(t, pods, 1)
	assert.Equal(t, "my-pod", pods[0].Name)
}

func TestList_ByGroup(t *testing.T) {
	_, idx := setup(t)
	items := idx.List("apps", "", "", nil)
	assert.Len(t, items, 1)
	assert.Equal(t, "coredns", items[0].Name)
}

func TestList_ByNamespace(t *testing.T) {
	_, idx := setup(t)
	items := idx.List("", "", "kube-system", nil)
	assert.Len(t, items, 1)
	assert.Equal(t, "coredns", items[0].Name)
}

func TestList_ByKindAndNamespace(t *testing.T) {
	_, idx := setup(t)
	items := idx.List("", "Deployment", "kube-system", nil)
	assert.Len(t, items, 1)
	assert.Equal(t, "Deployment", items[0].Kind)
}

func TestList_All(t *testing.T) {
	_, idx := setup(t)
	assert.Len(t, idx.List("", "", "", nil), 3)
}

func TestGet_Found(t *testing.T) {
	_, idx := setup(t)
	r, ok := idx.Get("core", "Pod", "default", "my-pod")
	assert.True(t, ok)
	assert.Equal(t, "my-pod", r.Name)
	assert.Equal(t, map[string]string{"app": "my-app"}, r.Labels)
}

func TestGet_FoundWithEmptyGroup(t *testing.T) {
	_, idx := setup(t)
	// empty group means "any group"
	r, ok := idx.Get("", "Pod", "default", "my-pod")
	assert.True(t, ok)
	assert.Equal(t, "my-pod", r.Name)
}

func TestGet_NotFound(t *testing.T) {
	_, idx := setup(t)
	_, ok := idx.Get("core", "Pod", "default", "nonexistent")
	assert.False(t, ok)
}

func TestBuild_SkipsNonYAML(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o644))
	idx := New("test-cluster", defaultFields)
	require.NoError(t, idx.Build(dir))
	assert.Empty(t, idx.List("", "", "", nil))
}

func TestBuild_MultiDoc(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "multi.yaml", `
apiVersion: v1
kind: Pod
metadata:
  name: pod-a
  namespace: ns1
---
apiVersion: v1
kind: Service
metadata:
  name: svc-b
  namespace: ns1
`)
	idx := New("test-cluster", defaultFields)
	require.NoError(t, idx.Build(dir))
	assert.Len(t, idx.List("", "", "", nil), 2)
}

func TestQuery_EmptyExpr(t *testing.T) {
	_, idx := setup(t)
	results, err := idx.Query("", "Pod", "", nil, "")
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "my-pod", results[0].Name)
}

func TestQuery_Namespace(t *testing.T) {
	_, idx := setup(t)
	results, err := idx.Query("", "Deployment", "kube-system", nil, "")
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "coredns", results[0].Name)
}

func TestQuery_CELLabel(t *testing.T) {
	_, idx := setup(t)
	results, err := idx.Query("", "Pod", "", nil, `object.metadata.labels["app"] == "my-app"`)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "my-pod", results[0].Name)
}

func TestQuery_CELLabelNoMatch(t *testing.T) {
	_, idx := setup(t)
	results, err := idx.Query("", "Pod", "", nil, `object.metadata.labels["app"] == "other"`)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestList_LabelSelector(t *testing.T) {
	_, idx := setup(t)
	results := idx.List("", "Pod", "", map[string]string{"app": "my-app"})
	require.Len(t, results, 1)
	assert.Equal(t, "my-pod", results[0].Name)
}

func TestList_LabelSelectorNoMatch(t *testing.T) {
	_, idx := setup(t)
	results := idx.List("", "Pod", "", map[string]string{"app": "other"})
	assert.Empty(t, results)
}

func TestList_LabelKeyOnly(t *testing.T) {
	_, idx := setup(t)
	results := idx.List("", "", "", map[string]string{"app": ""})
	require.Len(t, results, 1)
	assert.Equal(t, "my-pod", results[0].Name)
}

func TestQuery_CELFallback(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "pod.yaml", `
apiVersion: v1
kind: Pod
metadata:
  name: fallback-pod
  namespace: default
spec:
  dnsPolicy: ClusterFirst
`)
	idx := New("test-cluster", []string{})
	require.NoError(t, idx.Build(dir))

	results, err := idx.Query("", "Pod", "", nil, `object.spec.dnsPolicy == "ClusterFirst"`)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "fallback-pod", results[0].Name)
}

func TestQuery_InvalidCEL(t *testing.T) {
	_, idx := setup(t)
	_, err := idx.Query("", "Pod", "", nil, `object.metadata.name ===`)
	assert.Error(t, err)
}
