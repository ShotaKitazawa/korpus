package main

import (
	"context"
	"errors"
	"strings"
	"time"

	api "github.com/ShotaKitazawa/korpus/internal/api"
	"github.com/ShotaKitazawa/korpus/internal/gitindex"
	"github.com/ShotaKitazawa/korpus/internal/index"
	"github.com/ShotaKitazawa/korpus/internal/query"
)

type apiHandler struct {
	states       map[string]*ClusterState
	clusterNames []string
	q            *query.Server
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
	return h.q.ListClusters(), nil
}

func (h *apiHandler) ListGVKs(_ context.Context, params api.ListGVKsParams) ([]api.GVKInfo, error) {
	gvks, err := h.q.ListGVKs(params.Cluster.Or(""), params.Namespace.Or(""))
	if err != nil {
		return nil, err
	}
	result := make([]api.GVKInfo, len(gvks))
	for i, g := range gvks {
		result[i] = api.GVKInfo{Group: g.Group, Version: g.Version, Kind: g.Kind}
	}
	return result, nil
}

func (h *apiHandler) ListNamespaces(_ context.Context, params api.ListNamespacesParams) ([]string, error) {
	return h.q.ListNamespaces(params.Cluster.Or(""))
}

func (h *apiHandler) GetResource(_ context.Context, params api.GetResourceParams) (api.GetResourceRes, error) {
	data, err := h.q.GetResource(params.Cluster, params.Group, params.Kind, params.Namespace.Or(""), params.Name)
	if err != nil {
		if errors.Is(err, query.ErrClusterNotFound) || errors.Is(err, query.ErrNotFound) {
			return &api.GetResourceNotFound{}, nil
		}
		return nil, err
	}
	return &api.GetResourceOK{Data: strings.NewReader(string(data))}, nil
}

func (h *apiHandler) GetDiff(_ context.Context, params api.GetDiffParams) (api.GetDiffRes, error) {
	result, err := h.q.GetDiff(params.Cluster, params.Group, params.Kind, params.Namespace.Or(""), params.Name, params.From, params.To)
	if err != nil {
		if errors.Is(err, query.ErrClusterNotFound) || errors.Is(err, query.ErrNotFound) {
			return &api.GetDiffNotFound{}, nil
		}
		return &api.GetDiffBadRequest{}, nil
	}
	return &api.DiffResult{Before: result.Before, After: result.After}, nil
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
		result, err := h.q.GetHistoricalSnapshot(params.Datetime.Value, cluster, group, kind, ns, name, limit, offset)
		if err != nil {
			return nil, err
		}
		page := &api.SnapshotPage{
			Items:  toSnapshotResources(result.Items),
			Total:  result.Total,
			Offset: offset,
			Limit:  limit,
		}
		if result.CommitSHA != "" {
			page.CommitSha.SetTo(result.CommitSHA)
			page.CommitTime.SetTo(result.CommitTime)
		} else {
			page.CommitSha.SetToNull()
			page.CommitTime.SetToNull()
		}
		return page, nil
	}

	result, err := h.q.GetCurrentSnapshot(cluster, group, kind, ns, name, cel, nil, limit, offset)
	if err != nil {
		return &api.GetSnapshotBadRequest{}, nil
	}
	page := &api.SnapshotPage{
		Items:  toSnapshotResources(result.Items),
		Total:  result.Total,
		Offset: offset,
		Limit:  limit,
	}
	page.CommitSha.SetToNull()
	page.CommitTime.SetToNull()
	return page, nil
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

	var changeType string
	if params.ChangeType.IsSet() {
		changeType = string(params.ChangeType.Value)
	}

	limit := params.Limit.Or(50)
	offset := params.Offset.Or(0)
	if limit <= 0 {
		limit = 50
	}

	events, total, err := h.q.GetHistory(
		params.Cluster.Or(""), params.Group.Or(""), params.Kind.Or(""),
		params.Namespace.Or(""), params.Name.Or(""),
		changeType, since, until, limit, offset,
	)
	if err != nil {
		return nil, err
	}

	items := make([]api.ChangeEvent, len(events))
	for i, e := range events {
		items[i] = toAPIChangeEvent(e)
	}
	return &api.HistoryPage{Items: items, Total: total, Offset: offset, Limit: limit}, nil
}

func (h *apiHandler) GetVolatility(_ context.Context, params api.GetVolatilityParams) (*api.VolatilityPage, error) {
	limit := params.Limit.Or(50)
	offset := params.Offset.Or(0)
	if limit <= 0 {
		limit = 50
	}

	results, total, err := h.q.GetVolatility(
		params.Cluster.Or(""), params.Group.Or(""), params.Kind.Or(""),
		params.Namespace.Or(""), params.Name.Or(""),
		params.Commits.Or(50), params.Threshold.Or(0.0),
		limit, offset,
	)
	if err != nil {
		return nil, err
	}

	items := make([]api.VolatilityEntry, len(results))
	for i, r := range results {
		items[i] = api.VolatilityEntry{
			Cluster:   r.Cluster,
			Group:     r.Group,
			Kind:      r.Kind,
			Namespace: r.Namespace,
			Name:      r.Name,
			Count:     r.Count,
			Total:     r.Total,
			Ratio:     r.Ratio,
		}
	}
	return &api.VolatilityPage{Items: items, Total: total, Offset: offset, Limit: limit}, nil
}

func (h *apiHandler) GetVolatilityFields(_ context.Context, params api.GetVolatilityFieldsParams) (api.GetVolatilityFieldsRes, error) {
	results, err := h.q.GetVolatilityFields(
		params.Cluster.Or(""), params.Group, params.Kind,
		params.Namespace.Or(""), params.Name.Or(""),
		params.Commits.Or(50), 0,
	)
	if err != nil {
		return nil, err
	}

	out := make(api.GetVolatilityFieldsOKApplicationJSON, len(results))
	for i, r := range results {
		out[i] = api.FieldVolatilityEntry{Field: r.Field, Count: r.Count, Total: r.Total, Ratio: r.Ratio}
	}
	return &out, nil
}

var _ api.Handler = (*apiHandler)(nil)

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
