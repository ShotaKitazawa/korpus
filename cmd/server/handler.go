package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	api "github.com/ShotaKitazawa/korpus/internal/api"
	"github.com/ShotaKitazawa/korpus/internal/config"
	"github.com/ShotaKitazawa/korpus/internal/index"
)

type apiHandler struct {
	cfg          *config.ServerConfig
	states       map[string]*ClusterState
	clusterNames []string
	logger       *slog.Logger
}

func toAPIResourceMeta(r index.ResourceMeta) api.ResourceMeta {
	m := api.ResourceMeta{
		Cluster:   r.Cluster,
		Kind:      r.Kind,
		Name:      r.Name,
		Namespace: r.Namespace,
	}
	if r.Labels == nil {
		m.Labels.SetToNull()
	} else {
		m.Labels.SetTo(api.ResourceMetaLabels(r.Labels))
	}
	if r.CreationTimestamp != "" {
		m.CreationTimestamp.SetTo(r.CreationTimestamp)
	}
	return m
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
	result := make([]api.ClusterStatus, 0, len(h.cfg.Spec.Clusters))
	for _, c := range h.cfg.Spec.Clusters {
		s := h.states[c.Name].status()
		cs := api.ClusterStatus{
			Name:          c.Name,
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

func (h *apiHandler) ListKinds(_ context.Context, params api.ListKindsParams) ([]string, error) {
	cluster := params.Cluster.Or("")
	ns := params.Namespace.Or("")
	seen := make(map[string]struct{})
	for _, st := range resolveStates(cluster, h.states) {
		for _, k := range st.idx.Kinds(ns) {
			seen[k] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for k := range seen {
		result = append(result, k)
	}
	sort.Strings(result)
	return result, nil
}

func (h *apiHandler) ListNamespaces(_ context.Context, params api.ListNamespacesParams) ([]string, error) {
	cluster := params.Cluster.Or("")
	seen := make(map[string]struct{})
	for _, st := range resolveStates(cluster, h.states) {
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

func (h *apiHandler) ListResources(_ context.Context, params api.ListResourcesParams) ([]api.ResourceMeta, error) {
	cluster := params.Cluster.Or("")
	kind := params.Kind.Or("")
	ns := params.Namespace.Or("")
	labelSel := parseLabelSelector(params.Labels.Or(""))
	var result []api.ResourceMeta
	for _, st := range resolveStates(cluster, h.states) {
		for _, r := range st.idx.List(kind, ns, labelSel) {
			result = append(result, toAPIResourceMeta(r))
		}
	}
	return result, nil
}

func (h *apiHandler) GetResource(_ context.Context, params api.GetResourceParams) (api.GetResourceRes, error) {
	state, ok := h.states[params.Cluster]
	if !ok {
		return &api.GetResourceNotFound{}, nil
	}
	meta, ok := state.idx.Get(params.Kind, params.Namespace, params.Name)
	if !ok {
		return &api.GetResourceNotFound{}, nil
	}
	data, err := os.ReadFile(meta.FilePath)
	if err != nil {
		return nil, err
	}
	return &api.GetResourceOK{Data: strings.NewReader(string(data))}, nil
}

func (h *apiHandler) GetResourceHistory(_ context.Context, params api.GetResourceHistoryParams) (api.GetResourceHistoryRes, error) {
	n := params.N.Or(20)
	state, ok := h.states[params.Cluster]
	if !ok {
		return &api.GetResourceHistoryNotFound{}, nil
	}
	meta, ok := state.idx.Get(params.Kind, params.Namespace, params.Name)
	if !ok {
		return &api.GetResourceHistoryNotFound{}, nil
	}
	relPath := state.relPath(meta.FilePath)
	if relPath == "" {
		return nil, errCannotDetermineGitPath
	}
	entries, err := state.history(relPath, n)
	if err != nil {
		return nil, err
	}
	result := make(api.GetResourceHistoryOKApplicationJSON, len(entries))
	for i, e := range entries {
		result[i] = api.HistoryEntry{
			Sha:       e.SHA,
			Timestamp: e.Timestamp,
			Message:   e.Message,
		}
	}
	return &result, nil
}

func (h *apiHandler) GetResourceDiff(_ context.Context, params api.GetResourceDiffParams) (api.GetResourceDiffRes, error) {
	state, ok := h.states[params.Cluster]
	if !ok {
		return &api.GetResourceDiffNotFound{}, nil
	}
	meta, ok := state.idx.Get(params.Kind, params.Namespace, params.Name)
	if !ok {
		return &api.GetResourceDiffNotFound{}, nil
	}
	relPath := state.relPath(meta.FilePath)
	if relPath == "" {
		return nil, errCannotDetermineGitPath
	}
	before, err := state.fileAt(relPath, params.From)
	if err != nil {
		return &api.GetResourceDiffBadRequest{}, nil
	}
	after, err := state.fileAt(relPath, params.To)
	if err != nil {
		return &api.GetResourceDiffBadRequest{}, nil
	}
	return &api.DiffResult{Before: before, After: after}, nil
}

func (h *apiHandler) QueryResources(_ context.Context, params api.QueryResourcesParams) (api.QueryResourcesRes, error) {
	cluster := params.Cluster.Or("")
	ns := params.Namespace.Or("")
	labelSel := parseLabelSelector(params.Labels.Or(""))
	expr := params.Q.Or("")
	var result api.QueryResourcesOKApplicationJSON
	for _, st := range resolveStates(cluster, h.states) {
		res, err := st.idx.Query(params.Kind, ns, labelSel, expr)
		if err != nil {
			return &api.QueryResourcesBadRequest{}, nil
		}
		for _, r := range res {
			result = append(result, toAPIResourceMeta(r))
		}
	}
	return &result, nil
}

var errCannotDetermineGitPath = fmt.Errorf("cannot determine git path")

// ensure apiHandler implements the ogen Handler interface at compile time.
var _ api.Handler = (*apiHandler)(nil)
