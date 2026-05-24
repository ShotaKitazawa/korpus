package store

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// Write serialises items as a v1 List YAML to path.
// Returns changed=true when the written content differs from any existing file.
func Write(path string, items []unstructured.Unstructured) (changed bool, err error) {
	raw, err := marshal(items)
	if err != nil {
		return false, err
	}

	existing, readErr := os.ReadFile(path)
	if readErr == nil && sha256sum(existing) == sha256sum(raw) {
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

// CleanObsolete removes .yaml files under dir that are not in currPaths.
func CleanObsolete(dir string, currPaths map[string]struct{}) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".yaml" {
			return nil
		}
		if _, ok := currPaths[path]; !ok {
			return os.Remove(path)
		}
		return nil
	})
}

func marshal(items []unstructured.Unstructured) ([]byte, error) {
	list := make([]any, len(items))
	for i, item := range items {
		list[i] = item.Object
	}
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      list,
	}
	data, err := yaml.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("marshal yaml: %w", err)
	}
	return data, nil
}

func sha256sum(b []byte) string {
	h := sha256.New()
	_, _ = io.Writer(h).Write(b)
	return fmt.Sprintf("%x", h.Sum(nil))
}
