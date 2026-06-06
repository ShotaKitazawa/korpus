package gitindex

import (
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"sigs.k8s.io/yaml"
)

// ChangeType describes what happened to a resource in a commit.
type ChangeType string

const (
	Added    ChangeType = "added"
	Modified ChangeType = "modified"
	Deleted  ChangeType = "deleted"
)

// ChangeEvent represents one resource change recorded in the git history.
type ChangeEvent struct {
	Timestamp  time.Time
	SHA        string
	Cluster    string
	Group      string
	Kind       string
	Namespace  string
	Name       string
	ChangeType ChangeType
}

// VolatilityEntry aggregates change counts for a single resource identity.
type VolatilityEntry struct {
	Cluster   string
	Group     string
	Kind      string
	Namespace string
	Name      string
	Count     int
	Total     int // total distinct commits examined
}

// ChangeIndex holds ChangeEvents sorted ascending by Timestamp.
type ChangeIndex struct {
	events []ChangeEvent
}

// BuildChangeIndex walks the git log from HEAD backwards for up to retentionDays,
// extracts per-resource change events, and returns a time-sorted index.
func BuildChangeIndex(repo *git.Repository, clusterName, subDir string, retentionDays int) (*ChangeIndex, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)

	iter, err := repo.Log(&git.LogOptions{Order: git.LogOrderCommitterTime})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var events []ChangeEvent
	err = iter.ForEach(func(commit *object.Commit) error {
		if commit.Author.When.UTC().Before(cutoff) {
			return storer.ErrStop
		}
		if commit.NumParents() == 0 {
			return nil // skip root commit (no parent to diff against)
		}
		ev, err := eventsFromCommit(commit, clusterName, subDir)
		if err != nil {
			return nil // skip on error, don't abort
		}
		events = append(events, ev...)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.Before(events[j].Timestamp)
	})
	return &ChangeIndex{events: events}, nil
}

// eventsFromCommit compares a commit with its first parent and returns ChangeEvents
// for YAML files under subDir.
func eventsFromCommit(commit *object.Commit, clusterName, subDir string) ([]ChangeEvent, error) {
	parent, err := commit.Parents().Next()
	if err != nil {
		return nil, err
	}
	prevTree, err := parent.Tree()
	if err != nil {
		return nil, err
	}
	currTree, err := commit.Tree()
	if err != nil {
		return nil, err
	}

	changes, err := prevTree.Diff(currTree)
	if err != nil {
		return nil, err
	}

	prefix := subDir + "/"
	if subDir == "" {
		prefix = ""
	}

	var events []ChangeEvent
	for _, change := range changes {
		from, to, err := change.Files()
		if err != nil {
			continue
		}

		// Use Change.From/To.Name for the full repo-relative path because
		// object.File.Name from Files() contains only the basename.
		var filePath string
		var ct ChangeType
		var contentFile *object.File

		switch {
		case from == nil && to != nil:
			filePath = change.To.Name
			ct = Added
			contentFile = to
		case from != nil && to == nil:
			filePath = change.From.Name
			ct = Deleted
			contentFile = from
		case from != nil && to != nil:
			filePath = change.To.Name
			ct = Modified
			contentFile = to
		default:
			continue
		}

		if prefix != "" && !strings.HasPrefix(filePath, prefix) {
			continue
		}
		if !strings.HasSuffix(filePath, ".yaml") {
			continue
		}

		relPath := filePath
		if prefix != "" {
			relPath = strings.TrimPrefix(filePath, prefix)
		}
		group, _, namespace, _, name, ok := ParseResourcePath(relPath)
		if !ok {
			continue
		}

		kind := kindFromFile(contentFile)

		events = append(events, ChangeEvent{
			Timestamp:  commit.Author.When.UTC(),
			SHA:        commit.Hash.String(),
			Cluster:    clusterName,
			Group:      group,
			Kind:       kind,
			Namespace:  namespace,
			Name:       name,
			ChangeType: ct,
		})
	}
	return events, nil
}

// Query returns a filtered, paginated slice of ChangeEvents (newest first) with total count.
// All filter parameters are optional (empty string / nil pointer = no filter).
func (ci *ChangeIndex) Query(
	since, until *time.Time,
	cluster, group, kind, namespace, name string,
	ct ChangeType,
	limit, offset int,
) ([]ChangeEvent, int) {
	var matched []ChangeEvent
	for _, e := range ci.events {
		if since != nil && e.Timestamp.Before(*since) {
			continue
		}
		if until != nil && e.Timestamp.After(*until) {
			continue
		}
		if cluster != "" && e.Cluster != cluster {
			continue
		}
		if group != "" && !strings.EqualFold(e.Group, group) {
			continue
		}
		if kind != "" && !strings.EqualFold(e.Kind, kind) {
			continue
		}
		if namespace != "" && e.Namespace != namespace {
			continue
		}
		if name != "" && e.Name != name {
			continue
		}
		if ct != "" && e.ChangeType != ct {
			continue
		}
		matched = append(matched, e)
	}

	total := len(matched)
	if offset >= total {
		return nil, total
	}
	end := offset + limit
	if end > total {
		end = total
	}

	// Return in reverse (newest first): matched is ascending, so reverse the slice.
	page := make([]ChangeEvent, 0, end-offset)
	for i := total - 1 - offset; i >= total-end; i-- {
		page = append(page, matched[i])
	}
	return page, total
}

// Volatility returns per-resource change counts within the most recent maxCommits
// that match the optional filters. maxCommits <= 0 means no limit.
func (ci *ChangeIndex) Volatility(cluster, group, kind, namespace, name string, maxCommits int) []VolatilityEntry {
	type key struct{ cluster, group, kind, ns, name string }

	// Walk events newest-first to respect maxCommits.
	commitSeen := make(map[string]struct{})
	counts := make(map[key]map[string]struct{}) // key → set of commit SHAs

	for i := len(ci.events) - 1; i >= 0; i-- {
		e := ci.events[i]
		if cluster != "" && e.Cluster != cluster {
			continue
		}
		if group != "" && !strings.EqualFold(e.Group, group) {
			continue
		}
		if kind != "" && !strings.EqualFold(e.Kind, kind) {
			continue
		}
		if namespace != "" && e.Namespace != namespace {
			continue
		}
		if name != "" && e.Name != name {
			continue
		}
		if maxCommits > 0 {
			commitSeen[e.SHA] = struct{}{}
			if len(commitSeen) > maxCommits {
				break
			}
		}
		k := key{e.Cluster, e.Group, e.Kind, e.Namespace, e.Name}
		if counts[k] == nil {
			counts[k] = make(map[string]struct{})
		}
		counts[k][e.SHA] = struct{}{}
	}

	total := len(commitSeen)

	result := make([]VolatilityEntry, 0, len(counts))
	for k, shas := range counts {
		result = append(result, VolatilityEntry{
			Cluster:   k.cluster,
			Group:     k.group,
			Kind:      k.kind,
			Namespace: k.ns,
			Name:      k.name,
			Count:     len(shas),
			Total:     total,
		})
	}
	return result
}

// kindFromFile reads a go-git File and extracts the Kubernetes kind field.
func kindFromFile(f *object.File) string {
	if f == nil {
		return ""
	}
	content, err := f.Contents()
	if err != nil {
		return ""
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return ""
	}
	kind, _ := raw["kind"].(string)
	return kind
}

// ParseResourcePath parses a subDir-relative path (without the subDir prefix) and returns
// (group, version, namespace, resource, name, ok).
//
//	4 parts: group/version/resource/name.yaml          (cluster-scoped)
//	6 parts: group/version/namespaces/ns/resource/name.yaml
func ParseResourcePath(relPath string) (group, version, namespace, resource, name string, ok bool) {
	relPath = strings.TrimSuffix(relPath, ".yaml")
	parts := strings.Split(relPath, "/")
	switch len(parts) {
	case 4:
		return parts[0], parts[1], "", parts[2], parts[3], true
	case 6:
		if parts[2] != "namespaces" {
			return "", "", "", "", "", false
		}
		return parts[0], parts[1], parts[3], parts[4], parts[5], true
	}
	return "", "", "", "", "", false
}
