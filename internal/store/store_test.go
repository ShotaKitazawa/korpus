package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func makeItem(name, ns string) unstructured.Unstructured {
	u := unstructured.Unstructured{}
	u.SetName(name)
	u.SetNamespace(ns)
	u.SetKind("Pod")
	u.SetAPIVersion("v1")
	return u
}

func TestWriteSingle_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pod-a.yaml")

	changed, err := WriteSingle(path, makeItem("pod-a", "default"))
	require.NoError(t, err)
	assert.True(t, changed)
	assert.FileExists(t, path)
}

func TestWriteSingle_NoChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pod-a.yaml")
	item := makeItem("pod-a", "default")

	_, err := WriteSingle(path, item)
	require.NoError(t, err)

	changed, err := WriteSingle(path, item)
	require.NoError(t, err)
	assert.False(t, changed)
}

func TestWriteSingle_Changed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pod-a.yaml")

	_, err := WriteSingle(path, makeItem("pod-a", "default"))
	require.NoError(t, err)

	changed, err := WriteSingle(path, makeItem("pod-b", "default"))
	require.NoError(t, err)
	assert.True(t, changed)
}

func TestWriteSingle_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "core", "v1", "namespaces", "default", "pods", "pod-a.yaml")

	_, err := WriteSingle(path, makeItem("pod-a", "default"))
	require.NoError(t, err)
	assert.FileExists(t, path)
}

func TestCleanObsolete(t *testing.T) {
	dir := t.TempDir()

	keep := filepath.Join(dir, "keep.yaml")
	remove := filepath.Join(dir, "remove.yaml")
	require.NoError(t, os.WriteFile(keep, []byte(""), 0o644))
	require.NoError(t, os.WriteFile(remove, []byte(""), 0o644))

	curr := map[string]struct{}{keep: {}}
	require.NoError(t, CleanObsolete(dir, curr))

	assert.FileExists(t, keep)
	assert.NoFileExists(t, remove)
}
