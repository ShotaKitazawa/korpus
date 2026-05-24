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

func TestWrite_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pods.yaml")

	changed, err := Write(path, []unstructured.Unstructured{makeItem("pod-a", "default")})
	require.NoError(t, err)
	assert.True(t, changed)
	assert.FileExists(t, path)
}

func TestWrite_NoChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pods.yaml")
	items := []unstructured.Unstructured{makeItem("pod-a", "default")}

	_, err := Write(path, items)
	require.NoError(t, err)

	changed, err := Write(path, items)
	require.NoError(t, err)
	assert.False(t, changed)
}

func TestWrite_Changed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pods.yaml")

	_, err := Write(path, []unstructured.Unstructured{makeItem("pod-a", "default")})
	require.NoError(t, err)

	changed, err := Write(path, []unstructured.Unstructured{makeItem("pod-b", "default")})
	require.NoError(t, err)
	assert.True(t, changed)
}

func TestWrite_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "namespaced", "default", "pods.yaml")

	_, err := Write(path, nil)
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
