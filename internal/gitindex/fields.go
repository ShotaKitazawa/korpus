package gitindex

import (
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"sigs.k8s.io/yaml"
)

// FieldVolatilityEntry records how often a specific field path changed.
type FieldVolatilityEntry struct {
	Field string
	Count int
	Total int // total commits examined
}

// FieldVolatility analyzes the most recent maxCommits change events for the given resource
// and returns the frequency of each changed field path.
// maxCommits <= 0 means no limit.
func FieldVolatility(
	repo *git.Repository,
	events []ChangeEvent,
	subDir string,
	maxCommits int,
) ([]FieldVolatilityEntry, error) {
	// Work newest-first and cap at maxCommits.
	limit := len(events)
	if maxCommits > 0 && maxCommits < limit {
		limit = maxCommits
	}

	prefix := subDir + "/"
	if subDir == "" {
		prefix = ""
	}

	fieldCount := make(map[string]int)
	total := 0

	for i := len(events) - 1; i >= len(events)-limit; i-- {
		e := events[i]
		if e.ChangeType == Deleted {
			total++
			continue // no "after" to compare
		}

		// Build expected file path in the repo.
		filePath := resourceFilePath(prefix, e.Group, e.Namespace, e.Name)
		if filePath == "" {
			continue
		}

		commit, err := repo.CommitObject(plumbing.NewHash(e.SHA))
		if err != nil {
			continue
		}

		after, err := fileContentAtCommit(commit, filePath)
		if err != nil {
			total++
			continue
		}

		// Find parent content.
		var before map[string]any
		if commit.NumParents() > 0 {
			parent, err := commit.Parents().Next()
			if err == nil {
				bef, err := fileContentAtCommit(parent, filePath)
				if err == nil {
					before = bef
				}
			}
		}

		changed := diffFields(before, after, "")
		for _, f := range changed {
			fieldCount[f]++
		}
		total++
	}

	result := make([]FieldVolatilityEntry, 0, len(fieldCount))
	for field, cnt := range fieldCount {
		result = append(result, FieldVolatilityEntry{
			Field: field,
			Count: cnt,
			Total: total,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Field < result[j].Field
	})
	return result, nil
}

// resourceFilePath reconstructs the expected git file path for a resource.
// This mirrors the backup daemon's path convention:
//
//	<prefix>group/version/namespaces/ns/resource/name.yaml  (namespaced)
//	<prefix>group/version/resource/name.yaml                 (cluster-scoped)
//
// Since we don't know version or resource (plural) from ChangeEvent, we have to
// search the commit tree for the matching file.
func resourceFilePath(prefix, group, namespace, name string) string {
	// We cannot reconstruct the exact path without version/resource information.
	// Return empty to trigger a tree search in the caller.
	// This function is a placeholder; actual lookup is done via treeSearch.
	_ = prefix
	_ = group
	_ = namespace
	_ = name
	return "" // signal caller to use treeSearch
}

// fileContentAtCommit reads a file from a specific commit by path.
func fileContentAtCommit(commit *object.Commit, path string) (map[string]any, error) {
	f, err := commit.File(path)
	if err != nil {
		return nil, err
	}
	content, err := f.Contents()
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// FieldVolatilityFromRepo is the preferred entrypoint that finds the file path by
// searching the commit tree, so it works without knowing version/resource ahead of time.
func FieldVolatilityFromRepo(
	repo *git.Repository,
	events []ChangeEvent,
	subDir string,
	maxCommits int,
) ([]FieldVolatilityEntry, error) {
	limit := len(events)
	if maxCommits > 0 && maxCommits < limit {
		limit = maxCommits
	}

	prefix := subDir + "/"
	if subDir == "" {
		prefix = ""
	}

	fieldCount := make(map[string]int)
	total := 0

	for i := len(events) - 1; i >= len(events)-limit; i-- {
		e := events[i]

		commit, err := repo.CommitObject(plumbing.NewHash(e.SHA))
		if err != nil {
			continue
		}

		// Find the file in the commit tree by scanning for a path that matches
		// group/*/[namespaces/ns/]*/name.yaml.
		afterPath, err := FindResourcePathInCommit(commit, prefix, e.Group, e.Namespace, e.Name)
		if err != nil || afterPath == "" {
			total++
			continue
		}

		var after map[string]any
		if e.ChangeType != Deleted {
			after, err = fileContentAtCommit(commit, afterPath)
			if err != nil {
				total++
				continue
			}
		}

		var before map[string]any
		if commit.NumParents() > 0 {
			parent, _ := commit.Parents().Next()
			if parent != nil {
				before, _ = fileContentAtCommit(parent, afterPath)
			}
		}

		changed := diffFields(before, after, "")
		for _, f := range changed {
			fieldCount[f]++
		}
		total++
	}

	result := make([]FieldVolatilityEntry, 0, len(fieldCount))
	for field, cnt := range fieldCount {
		result = append(result, FieldVolatilityEntry{
			Field: field,
			Count: cnt,
			Total: total,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Field < result[j].Field
	})
	return result, nil
}

// FindResourcePathInCommit searches the commit tree for a YAML file matching the resource identity.
// Path pattern: prefix + group + "/" + version + "/[namespaces/" + ns + "/]" + resource + "/" + name + ".yaml"
func FindResourcePathInCommit(commit *object.Commit, prefix, group, namespace, name string) (string, error) {
	tree, err := commit.Tree()
	if err != nil {
		return "", err
	}

	var found string
	err = tree.Files().ForEach(func(f *object.File) error {
		if !strings.HasSuffix(f.Name, ".yaml") {
			return nil
		}
		rel := f.Name
		if prefix != "" {
			if !strings.HasPrefix(rel, prefix) {
				return nil
			}
			rel = strings.TrimPrefix(rel, prefix)
		}
		g, _, ns, _, n, ok := ParseResourcePath(rel)
		if !ok {
			return nil
		}
		if strings.EqualFold(g, group) && ns == namespace && n == name {
			found = f.Name
			return errFoundSentinel
		}
		return nil
	})
	if err != nil && err != errFoundSentinel {
		return "", err
	}
	return found, nil
}

var errFoundSentinel = errSentinel{}

type errSentinel struct{}

func (errSentinel) Error() string { return "found" }

// diffFields recursively compares two YAML maps and returns dot-separated paths
// where values differ. Additions and deletions are both reported.
func diffFields(before, after map[string]any, prefix string) []string {
	var changed []string

	// Check all keys in "after".
	for k, afterVal := range after {
		path := joinPath(prefix, k)
		beforeVal, exists := before[k]
		if !exists {
			changed = append(changed, path)
			continue
		}
		afterMap, afterIsMap := afterVal.(map[string]any)
		beforeMap, beforeIsMap := beforeVal.(map[string]any)
		if afterIsMap && beforeIsMap {
			changed = append(changed, diffFields(beforeMap, afterMap, path)...)
		} else if !valuesEqual(beforeVal, afterVal) {
			changed = append(changed, path)
		}
	}

	// Check keys that existed in "before" but not in "after" (deletions).
	for k := range before {
		if _, exists := after[k]; !exists {
			changed = append(changed, joinPath(prefix, k))
		}
	}

	return changed
}

func joinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func valuesEqual(a, b any) bool {
	// Simple structural equality via string representation.
	aBytes, _ := yaml.Marshal(a)
	bBytes, _ := yaml.Marshal(b)
	return string(aBytes) == string(bBytes)
}
