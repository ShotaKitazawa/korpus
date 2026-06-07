package query

import (
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"sigs.k8s.io/yaml"

	"github.com/ShotaKitazawa/korpus/internal/gitindex"
	"github.com/ShotaKitazawa/korpus/internal/index"
)

// Server centralises all query-layer business logic for the korpus read-only viewer.
// Both the REST handler and MCP tools delegate to it, so every fix or feature
// lands in one place and both interfaces stay consistent.
type Server struct {
	clusters     map[string]ClusterQuerier
	clusterNames []string
}

// New creates a Server. clusterNames determines iteration order.
func New(clusters map[string]ClusterQuerier, clusterNames []string) *Server {
	return &Server{clusters: clusters, clusterNames: clusterNames}
}

// resolve returns the ClusterQuerierss to query.
// If cluster is empty all clusters are returned in clusterNames order.
func (s *Server) resolve(cluster string) []ClusterQuerier {
	if cluster != "" {
		if cq, ok := s.clusters[cluster]; ok {
			return []ClusterQuerier{cq}
		}
		return nil
	}
	out := make([]ClusterQuerier, 0, len(s.clusterNames))
	for _, name := range s.clusterNames {
		out = append(out, s.clusters[name])
	}
	return out
}

// paginate clamps offset/limit to the slice length and returns start, end indices.
func paginate(offset, limit, total int) (start, end int) {
	start = offset
	if start > total {
		start = total
	}
	end = start + limit
	if end > total {
		end = total
	}
	return
}

// ListClusters returns all cluster names in order.
func (s *Server) ListClusters() []string {
	return s.clusterNames
}

// ListGVKs returns deduplicated, sorted GVKs across the requested cluster(s).
func (s *Server) ListGVKs(cluster, namespace string) ([]index.GVKInfo, error) {
	type key struct{ g, v, k string }
	seen := make(map[key]struct{})
	for _, cq := range s.resolve(cluster) {
		for _, gvk := range cq.Index().GVKs(namespace) {
			seen[key{gvk.Group, gvk.Version, gvk.Kind}] = struct{}{}
		}
	}
	result := make([]index.GVKInfo, 0, len(seen))
	for k := range seen {
		result = append(result, index.GVKInfo{Group: k.g, Version: k.v, Kind: k.k})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Group != result[j].Group {
			return result[i].Group < result[j].Group
		}
		if result[i].Version != result[j].Version {
			return result[i].Version < result[j].Version
		}
		return result[i].Kind < result[j].Kind
	})
	return result, nil
}

// ListNamespaces returns deduplicated, sorted namespaces across the requested cluster(s).
func (s *Server) ListNamespaces(cluster string) ([]string, error) {
	seen := make(map[string]struct{})
	for _, cq := range s.resolve(cluster) {
		for _, ns := range cq.Index().Namespaces() {
			seen[ns] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for ns := range seen {
		result = append(result, ns)
	}
	sort.Strings(result)
	return result, nil
}

// GetResource returns the raw YAML bytes for a single resource.
func (s *Server) GetResource(cluster, group, kind, ns, name string) ([]byte, error) {
	cq, ok := s.clusters[cluster]
	if !ok {
		return nil, ErrClusterNotFound
	}
	meta, ok := cq.Index().Get(group, kind, ns, name)
	if !ok {
		return nil, ErrNotFound
	}
	data, err := os.ReadFile(meta.FilePath)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// GetDiff returns the before/after YAML for a resource between two commits.
// If the resource is absent from the current index (e.g. deleted), it falls back
// to searching the historical commit tree.
func (s *Server) GetDiff(cluster, group, kind, ns, name, from, to string) (*DiffResult, error) {
	cq, ok := s.clusters[cluster]
	if !ok {
		return nil, ErrClusterNotFound
	}

	var relPath string
	meta, ok := cq.Index().Get(group, kind, ns, name)
	if ok {
		relPath = cq.RelPath(meta.FilePath)
		if relPath == "" {
			return nil, ErrNotFound
		}
	} else {
		found, err := findFileInCommit(cq, group, ns, name, from)
		if err != nil || found == "" {
			return nil, ErrNotFound
		}
		relPath = found
	}

	before, err := cq.FileAt(relPath, from)
	if err != nil {
		return nil, err
	}
	after, err := cq.FileAt(relPath, to)
	if err != nil {
		return nil, err
	}
	return &DiffResult{Before: before, After: after}, nil
}

// findFileInCommit searches the git tree at sha for a resource file matching group/ns/name.
func findFileInCommit(cq ClusterQuerier, group, ns, name, sha string) (string, error) {
	repo := cq.GitRepo()
	if repo == nil {
		return "", nil
	}
	commit, err := repo.CommitObject(plumbing.NewHash(sha))
	if err != nil {
		return "", err
	}
	prefix := cq.SubDir() + "/"
	if cq.SubDir() == "" {
		prefix = ""
	}
	return gitindex.FindResourcePathInCommit(commit, prefix, group, ns, name)
}

// GetCurrentSnapshot returns resources from the live in-memory index.
func (s *Server) GetCurrentSnapshot(cluster, group, kind, ns, name, cel string, labelSel map[string]string, limit, offset int) (*SnapshotResult, error) {
	var all []index.ResourceMeta
	for _, cq := range s.resolve(cluster) {
		var results []index.ResourceMeta
		var err error
		if cel != "" {
			results, err = cq.Index().Query(group, kind, ns, labelSel, cel)
			if err != nil {
				return nil, err
			}
		} else {
			results = cq.Index().List(group, kind, ns, labelSel)
		}
		if name != "" {
			for _, r := range results {
				if r.Name == name {
					all = append(all, r)
				}
			}
		} else {
			all = append(all, results...)
		}
	}

	total := len(all)
	start, end := paginate(offset, limit, total)
	return &SnapshotResult{Items: all[start:end], Total: total}, nil
}

// GetHistoricalSnapshot returns resources as they existed at time t by walking the git tree.
func (s *Server) GetHistoricalSnapshot(t time.Time, cluster, group, kind, ns, name string, limit, offset int) (*SnapshotResult, error) {
	var all []index.ResourceMeta
	var commitSHA string
	var commitTime time.Time

	for _, clusterName := range s.clusterNames {
		if cluster != "" && clusterName != cluster {
			continue
		}
		cq := s.clusters[clusterName]
		commitIdx := cq.CommitIndex()
		repo := cq.GitRepo()
		if commitIdx == nil || repo == nil {
			continue
		}

		ref, ok := commitIdx.FindBefore(t)
		if !ok {
			continue
		}
		commit, err := repo.CommitObject(plumbing.NewHash(ref.SHA))
		if err != nil {
			continue
		}

		resources, err := resourcesAtCommit(commit, clusterName, cq.SubDir(), group, kind, ns, name)
		if err != nil {
			continue
		}
		all = append(all, resources...)

		if commitSHA == "" {
			commitSHA = ref.SHA
			commitTime = ref.Time
		}
	}

	total := len(all)
	start, end := paginate(offset, limit, total)
	return &SnapshotResult{
		Items:      all[start:end],
		Total:      total,
		CommitSHA:  commitSHA,
		CommitTime: commitTime,
	}, nil
}

// resourcesAtCommit walks the git tree at commit and returns matching ResourceMeta entries.
func resourcesAtCommit(commit *object.Commit, clusterName, subDir, group, kind, ns, name string) ([]index.ResourceMeta, error) {
	tree, err := commit.Tree()
	if err != nil {
		return nil, err
	}

	prefix := subDir + "/"
	if subDir == "" {
		prefix = ""
	}

	var results []index.ResourceMeta
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
		g, _, fileNS, _, n, ok := gitindex.ParseResourcePath(rel)
		if !ok {
			return nil
		}
		if group != "" && !strings.EqualFold(g, group) {
			return nil
		}
		if ns != "" && fileNS != ns {
			return nil
		}
		if name != "" && n != name {
			return nil
		}

		content, err := f.Contents()
		if err != nil {
			return nil
		}
		var raw map[string]any
		if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
			return nil
		}
		k, _ := raw["kind"].(string)
		if k == "" {
			return nil
		}
		if kind != "" && !strings.EqualFold(k, kind) {
			return nil
		}

		var labels map[string]string
		var creationTimestamp string
		if meta, ok := raw["metadata"].(map[string]any); ok {
			if rawLabels, ok := meta["labels"].(map[string]any); ok {
				labels = make(map[string]string, len(rawLabels))
				for lk, lv := range rawLabels {
					if sv, ok := lv.(string); ok {
						labels[lk] = sv
					}
				}
			}
			creationTimestamp, _ = meta["creationTimestamp"].(string)
		}

		results = append(results, index.ResourceMeta{
			Cluster:           clusterName,
			Group:             g,
			Kind:              k,
			Namespace:         fileNS,
			Name:              n,
			Labels:            labels,
			CreationTimestamp: creationTimestamp,
		})
		return nil
	})
	return results, err
}

// GetHistory returns change events, sorted newest-first, with pagination.
func (s *Server) GetHistory(cluster, group, kind, ns, name, changeType string, since, until *time.Time, limit, offset int) ([]gitindex.ChangeEvent, int, error) {
	ct := gitindex.ChangeType(changeType)
	var all []gitindex.ChangeEvent
	for _, clusterName := range s.clusterNames {
		if cluster != "" && clusterName != cluster {
			continue
		}
		ci := s.clusters[clusterName].ChangeIndex()
		if ci == nil {
			continue
		}
		events, _ := ci.Query(since, until, clusterName, group, kind, ns, name, ct, 1<<30, 0)
		all = append(all, events...)
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].Timestamp.After(all[j].Timestamp)
	})

	total := len(all)
	start, end := paginate(offset, limit, total)
	return all[start:end], total, nil
}

// GetVolatility returns resources ranked by change ratio, filtered by threshold, with pagination.
func (s *Server) GetVolatility(cluster, group, kind, ns, name string, maxCommits int, threshold float64, limit, offset int) ([]VolatilityResult, int, error) {
	var all []VolatilityResult
	for _, clusterName := range s.clusterNames {
		if cluster != "" && clusterName != cluster {
			continue
		}
		ci := s.clusters[clusterName].ChangeIndex()
		if ci == nil {
			continue
		}
		for _, e := range ci.Volatility(clusterName, group, kind, ns, name, maxCommits) {
			ratio := 0.0
			if e.Total > 0 {
				ratio = float64(e.Count) / float64(e.Total)
			}
			if ratio < threshold {
				continue
			}
			all = append(all, VolatilityResult{
				Cluster:   e.Cluster,
				Group:     e.Group,
				Kind:      e.Kind,
				Namespace: e.Namespace,
				Name:      e.Name,
				Count:     e.Count,
				Total:     e.Total,
				Ratio:     ratio,
			})
		}
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].Ratio != all[j].Ratio {
			return all[i].Ratio > all[j].Ratio
		}
		return all[i].Name < all[j].Name
	})

	total := len(all)
	start, end := paginate(offset, limit, total)
	return all[start:end], total, nil
}

// GetVolatilityFields returns field-level change frequencies, aggregated across clusters, sorted by count.
func (s *Server) GetVolatilityFields(cluster, group, kind, ns, name string, maxCommits, limit int) ([]FieldVolatilityResult, error) {
	fieldMap := make(map[string]struct{ count, total int })

	for _, clusterName := range s.clusterNames {
		if cluster != "" && clusterName != cluster {
			continue
		}
		cq := s.clusters[clusterName]
		ci := cq.ChangeIndex()
		repo := cq.GitRepo()
		if ci == nil || repo == nil {
			continue
		}

		events, _ := ci.Query(nil, nil, clusterName, group, kind, ns, name, "", 1<<30, 0)
		if len(events) == 0 {
			continue
		}

		entries, err := gitindex.FieldVolatilityFromRepo(repo, events, cq.SubDir(), maxCommits)
		if err != nil {
			continue
		}
		for _, e := range entries {
			v := fieldMap[e.Field]
			v.count += e.Count
			v.total += e.Total
			fieldMap[e.Field] = v
		}
	}

	result := make([]FieldVolatilityResult, 0, len(fieldMap))
	for field, v := range fieldMap {
		ratio := 0.0
		if v.total > 0 {
			ratio = float64(v.count) / float64(v.total)
		}
		result = append(result, FieldVolatilityResult{
			Field: field,
			Count: v.count,
			Total: v.total,
			Ratio: ratio,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Field < result[j].Field
	})

	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}
