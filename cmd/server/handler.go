package main

import (
	"context"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"sigs.k8s.io/yaml"

	api "github.com/ShotaKitazawa/korpus/internal/api"
	"github.com/ShotaKitazawa/korpus/internal/gitindex"
	"github.com/ShotaKitazawa/korpus/internal/index"
)

type apiHandler struct {
	states       map[string]*ClusterState
	clusterNames []string
}

// resolveStates returns the ClusterStates to query.
// If cluster is empty, all states are returned in the order of clusterNames.
func (h *apiHandler) resolveStates(cluster string) []*ClusterState {
	if cluster != "" {
		if st, ok := h.states[cluster]; ok {
			return []*ClusterState{st}
		}
		return nil
	}
	result := make([]*ClusterState, 0, len(h.clusterNames))
	for _, name := range h.clusterNames {
		result = append(result, h.states[name])
	}
	return result
}

func (h *apiHandler) Healthz(_ context.Context) (api.HealthzRes, error) {
	for _, st := range h.states {
		if !st.ready() {
			return &api.HealthzServiceUnavailable{}, nil
		}
	}
	return &api.HealthzOK{}, nil
}

func (h *apiHandler) GetStatus(_ context.Context) (*api.StatusResponse, error) {
	result := make([]api.ClusterStatus, 0, len(h.clusterNames))
	for _, name := range h.clusterNames {
		s := h.states[name].status()
		cs := api.ClusterStatus{
			Name:          name,
			LastPullErr:   s.LastPullErr,
			ResourceCount: s.ResourceCount,
		}
		if s.LastPullAt != nil {
			cs.LastPullAt.SetTo(*s.LastPullAt)
		} else {
			cs.LastPullAt.SetToNull()
		}
		result = append(result, cs)
	}
	return &api.StatusResponse{Clusters: result}, nil
}

func (h *apiHandler) ListClusters(_ context.Context) ([]string, error) {
	return h.clusterNames, nil
}

func (h *apiHandler) ListGVKs(_ context.Context, params api.ListGVKsParams) ([]api.GVKInfo, error) {
	cluster := params.Cluster.Or("")
	ns := params.Namespace.Or("")
	type key struct{ g, v, k string }
	seen := make(map[key]struct{})
	for _, st := range h.resolveStates(cluster) {
		for _, gvk := range st.idx.GVKs(ns) {
			seen[key{gvk.Group, gvk.Version, gvk.Kind}] = struct{}{}
		}
	}
	result := make([]api.GVKInfo, 0, len(seen))
	for k := range seen {
		result = append(result, api.GVKInfo{Group: k.g, Version: k.v, Kind: k.k})
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

func (h *apiHandler) ListNamespaces(_ context.Context, params api.ListNamespacesParams) ([]string, error) {
	cluster := params.Cluster.Or("")
	seen := make(map[string]struct{})
	for _, st := range h.resolveStates(cluster) {
		for _, ns := range st.idx.Namespaces() {
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

func (h *apiHandler) GetResource(_ context.Context, params api.GetResourceParams) (api.GetResourceRes, error) {
	st, ok := h.states[params.Cluster]
	if !ok {
		return &api.GetResourceNotFound{}, nil
	}
	meta, ok := st.idx.Get(params.Group, params.Kind, params.Namespace.Or(""), params.Name)
	if !ok {
		return &api.GetResourceNotFound{}, nil
	}
	data, err := os.ReadFile(meta.FilePath)
	if err != nil {
		return nil, err
	}
	return &api.GetResourceOK{Data: strings.NewReader(string(data))}, nil
}

func (h *apiHandler) GetDiff(_ context.Context, params api.GetDiffParams) (api.GetDiffRes, error) {
	st, ok := h.states[params.Cluster]
	if !ok {
		return &api.GetDiffNotFound{}, nil
	}
	ns := params.Namespace.Or("")
	meta, ok := st.idx.Get(params.Group, params.Kind, ns, params.Name)
	if !ok {
		// Resource not in current index — search historical commit.
		found, err := findFileInCommit(st, params.Group, ns, params.Name, params.From)
		if err != nil || found == "" {
			return &api.GetDiffNotFound{}, nil
		}
		meta.FilePath = st.workDir + "/" + found
	}

	relPath := st.relPath(meta.FilePath)
	if relPath == "" {
		return &api.GetDiffBadRequest{}, nil
	}
	before, err := st.fileAt(relPath, params.From)
	if err != nil {
		return &api.GetDiffBadRequest{}, nil
	}
	after, err := st.fileAt(relPath, params.To)
	if err != nil {
		return &api.GetDiffBadRequest{}, nil
	}
	return &api.DiffResult{Before: before, After: after}, nil
}

// findFileInCommit searches a historical commit for a resource file matching group/ns/name.
func findFileInCommit(st *ClusterState, group, ns, name, sha string) (string, error) {
	st.mu.RLock()
	gc := st.gc
	subDir := st.subDir
	st.mu.RUnlock()
	if gc == nil {
		return "", nil
	}
	commit, err := gc.Repo().CommitObject(plumbing.NewHash(sha))
	if err != nil {
		return "", err
	}
	prefix := subDir + "/"
	if subDir == "" {
		prefix = ""
	}
	return gitindex.FindResourcePathInCommit(commit, prefix, group, ns, name)
}

func (h *apiHandler) GetSnapshot(_ context.Context, params api.GetSnapshotParams) (api.GetSnapshotRes, error) {
	cluster := params.Cluster.Or("")
	group := params.Group.Or("")
	kind := params.Kind.Or("")
	ns := params.Namespace.Or("")
	name := params.Name.Or("")
	cel := params.Cel.Or("")
	limit := params.Limit.Or(50)
	offset := params.Offset.Or(0)
	if limit <= 0 {
		limit = 50
	}

	if params.Datetime.IsSet() {
		if cel != "" {
			return &api.GetSnapshotBadRequest{}, nil
		}
		return h.snapshotAt(params.Datetime.Value, cluster, group, kind, ns, name, limit, offset)
	}

	// Current state from in-memory index.
	var all []index.ResourceMeta
	for _, st := range h.resolveStates(cluster) {
		var results []index.ResourceMeta
		var err error
		if cel != "" {
			results, err = st.idx.Query(group, kind, ns, parseLabelSelector(""), cel)
			if err != nil {
				return &api.GetSnapshotBadRequest{}, nil
			}
		} else {
			results = st.idx.List(group, kind, ns, nil)
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
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	page := &api.SnapshotPage{
		Items:  toSnapshotResources(all[start:end]),
		Total:  total,
		Offset: offset,
		Limit:  limit,
	}
	page.CommitSha.SetToNull()
	page.CommitTime.SetToNull()
	return page, nil
}

func (h *apiHandler) snapshotAt(t time.Time, cluster, group, kind, ns, name string, limit, offset int) (api.GetSnapshotRes, error) {
	// Collect snapshots from each requested cluster.
	var all []api.SnapshotResource

	for _, clusterName := range h.clusterNames {
		if cluster != "" && clusterName != cluster {
			continue
		}
		st := h.states[clusterName]

		st.mu.RLock()
		commitIdx := st.commitIdx
		gc := st.gc
		subDir := st.subDir
		st.mu.RUnlock()

		if commitIdx == nil || gc == nil {
			continue
		}

		ref, ok := commitIdx.FindBefore(t)
		if !ok {
			continue
		}

		commit, err := gc.Repo().CommitObject(plumbing.NewHash(ref.SHA))
		if err != nil {
			continue
		}

		resources, err := resourcesAtCommit(commit, clusterName, subDir, group, kind, ns, name)
		if err != nil {
			continue
		}
		all = append(all, resources...)
	}

	total := len(all)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	// Use the commit ref from the first matching cluster for metadata.
	var commitSHA string
	var commitTime time.Time
	if len(h.clusterNames) > 0 {
		for _, clusterName := range h.clusterNames {
			if cluster != "" && clusterName != cluster {
				continue
			}
			st := h.states[clusterName]
			st.mu.RLock()
			ci := st.commitIdx
			st.mu.RUnlock()
			if ci != nil {
				if ref, ok := ci.FindBefore(t); ok {
					commitSHA = ref.SHA
					commitTime = ref.Time
					break
				}
			}
		}
	}

	page := &api.SnapshotPage{
		Items:  all[start:end],
		Total:  total,
		Offset: offset,
		Limit:  limit,
	}
	if commitSHA != "" {
		page.CommitSha.SetTo(commitSHA)
		page.CommitTime.SetTo(commitTime)
	} else {
		page.CommitSha.SetToNull()
		page.CommitTime.SetToNull()
	}
	return page, nil
}

// resourcesAtCommit walks the git tree at the given commit and returns matching SnapshotResources.
func resourcesAtCommit(commit *object.Commit, clusterName, subDir, group, kind, ns, name string) ([]api.SnapshotResource, error) {
	tree, err := commit.Tree()
	if err != nil {
		return nil, err
	}

	prefix := subDir + "/"
	if subDir == "" {
		prefix = ""
	}

	var results []api.SnapshotResource
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

		// Read file content to get kind and labels.
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

		meta, _ := raw["metadata"].(map[string]any)
		var labels map[string]string
		var creationTimestamp string
		if meta != nil {
			if rawLabels, ok := meta["labels"].(map[string]any); ok {
				labels = make(map[string]string, len(rawLabels))
				for lk, lv := range rawLabels {
					if s, ok := lv.(string); ok {
						labels[lk] = s
					}
				}
			}
			creationTimestamp, _ = meta["creationTimestamp"].(string)
		}

		r := api.SnapshotResource{
			Cluster:   clusterName,
			Group:     g,
			Kind:      k,
			Namespace: fileNS,
			Name:      n,
		}
		if labels == nil {
			r.Labels.SetToNull()
		} else {
			r.Labels.SetTo(api.SnapshotResourceLabels(labels))
		}
		if creationTimestamp != "" {
			r.CreationTimestamp.SetTo(creationTimestamp)
		}
		results = append(results, r)
		return nil
	})
	return results, err
}

func (h *apiHandler) GetHistory(_ context.Context, params api.GetHistoryParams) (*api.HistoryPage, error) {
	var since, until *time.Time
	if params.Since.IsSet() {
		t := params.Since.Value
		since = &t
	}
	if params.Until.IsSet() {
		t := params.Until.Value
		until = &t
	}

	cluster := params.Cluster.Or("")
	group := params.Group.Or("")
	kind := params.Kind.Or("")
	ns := params.Namespace.Or("")
	name := params.Name.Or("")
	limit := params.Limit.Or(50)
	offset := params.Offset.Or(0)
	if limit <= 0 {
		limit = 50
	}

	var ct gitindex.ChangeType
	if params.ChangeType.IsSet() {
		ct = gitindex.ChangeType(string(params.ChangeType.Value))
	}

	// Aggregate across clusters.
	var allEvents []gitindex.ChangeEvent
	for _, clusterName := range h.clusterNames {
		if cluster != "" && clusterName != cluster {
			continue
		}
		st := h.states[clusterName]
		st.mu.RLock()
		ci := st.changeIdx
		st.mu.RUnlock()
		if ci == nil {
			continue
		}
		events, _ := ci.Query(since, until, clusterName, group, kind, ns, name, ct, 1<<30, 0)
		allEvents = append(allEvents, events...)
	}

	// Sort merged result newest-first.
	sort.Slice(allEvents, func(i, j int) bool {
		return allEvents[i].Timestamp.After(allEvents[j].Timestamp)
	})

	total := len(allEvents)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	items := make([]api.ChangeEvent, 0, end-start)
	for _, e := range allEvents[start:end] {
		items = append(items, toAPIChangeEvent(e))
	}
	return &api.HistoryPage{
		Items:  items,
		Total:  total,
		Offset: offset,
		Limit:  limit,
	}, nil
}

func (h *apiHandler) GetVolatility(_ context.Context, params api.GetVolatilityParams) (*api.VolatilityPage, error) {
	cluster := params.Cluster.Or("")
	group := params.Group.Or("")
	kind := params.Kind.Or("")
	ns := params.Namespace.Or("")
	name := params.Name.Or("")
	commits := params.Commits.Or(50)
	threshold := params.Threshold.Or(0.0)
	limit := params.Limit.Or(50)
	offset := params.Offset.Or(0)
	if limit <= 0 {
		limit = 50
	}

	var all []gitindex.VolatilityEntry
	for _, clusterName := range h.clusterNames {
		if cluster != "" && clusterName != cluster {
			continue
		}
		st := h.states[clusterName]
		st.mu.RLock()
		ci := st.changeIdx
		st.mu.RUnlock()
		if ci == nil {
			continue
		}
		all = append(all, ci.Volatility(clusterName, group, kind, ns, name, commits)...)
	}

	// Filter by threshold and convert.
	var filtered []api.VolatilityEntry
	for _, e := range all {
		ratio := 0.0
		if e.Total > 0 {
			ratio = float64(e.Count) / float64(e.Total)
		}
		if ratio < threshold {
			continue
		}
		filtered = append(filtered, api.VolatilityEntry{
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

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Ratio != filtered[j].Ratio {
			return filtered[i].Ratio > filtered[j].Ratio
		}
		return filtered[i].Name < filtered[j].Name
	})

	total := len(filtered)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	return &api.VolatilityPage{
		Items:  filtered[start:end],
		Total:  total,
		Offset: offset,
		Limit:  limit,
	}, nil
}

func (h *apiHandler) GetVolatilityFields(_ context.Context, params api.GetVolatilityFieldsParams) (api.GetVolatilityFieldsRes, error) {
	cluster := params.Cluster.Or("")
	group := params.Group
	kind := params.Kind
	ns := params.Namespace.Or("")
	name := params.Name.Or("")
	commits := params.Commits.Or(50)

	var allEntries []gitindex.FieldVolatilityEntry

	for _, clusterName := range h.clusterNames {
		if cluster != "" && clusterName != cluster {
			continue
		}
		st := h.states[clusterName]
		st.mu.RLock()
		ci := st.changeIdx
		gc := st.gc
		subDir := st.subDir
		st.mu.RUnlock()

		if ci == nil || gc == nil {
			continue
		}

		events, _ := ci.Query(nil, nil, clusterName, group, kind, ns, name, "", 1<<30, 0)
		if len(events) == 0 {
			continue
		}

		entries, err := gitindex.FieldVolatilityFromRepo(gc.Repo(), events, subDir, commits)
		if err != nil {
			continue
		}
		allEntries = append(allEntries, entries...)
	}

	if len(allEntries) == 0 {
		result := api.GetVolatilityFieldsOKApplicationJSON{}
		return &result, nil
	}

	// Merge entries from multiple clusters.
	fieldMap := make(map[string]struct{ count, total int })
	for _, e := range allEntries {
		v := fieldMap[e.Field]
		v.count += e.Count
		v.total += e.Total
		fieldMap[e.Field] = v
	}

	result := make(api.GetVolatilityFieldsOKApplicationJSON, 0, len(fieldMap))
	for field, v := range fieldMap {
		ratio := 0.0
		if v.total > 0 {
			ratio = float64(v.count) / float64(v.total)
		}
		result = append(result, api.FieldVolatilityEntry{
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
	return &result, nil
}

// toSnapshotResources converts index.ResourceMeta slice to api.SnapshotResource slice.
func toSnapshotResources(resources []index.ResourceMeta) []api.SnapshotResource {
	result := make([]api.SnapshotResource, len(resources))
	for i, r := range resources {
		sr := api.SnapshotResource{
			Cluster:   r.Cluster,
			Group:     r.Group,
			Kind:      r.Kind,
			Name:      r.Name,
			Namespace: r.Namespace,
		}
		if r.Labels == nil {
			sr.Labels.SetToNull()
		} else {
			sr.Labels.SetTo(api.SnapshotResourceLabels(r.Labels))
		}
		if r.CreationTimestamp != "" {
			sr.CreationTimestamp.SetTo(r.CreationTimestamp)
		}
		result[i] = sr
	}
	return result
}

func toAPIChangeEvent(e gitindex.ChangeEvent) api.ChangeEvent {
	return api.ChangeEvent{
		Timestamp:  e.Timestamp,
		Sha:        e.SHA,
		Cluster:    e.Cluster,
		Group:      e.Group,
		Kind:       e.Kind,
		Namespace:  e.Namespace,
		Name:       e.Name,
		ChangeType: api.ChangeEventChangeType(string(e.ChangeType)),
	}
}

var _ api.Handler = (*apiHandler)(nil)

// parseLabelSelector parses "key=value,key2=value2" into a map.
func parseLabelSelector(s string) map[string]string {
	if s == "" {
		return nil
	}
	result := make(map[string]string)
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		result[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return result
}
