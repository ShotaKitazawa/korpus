package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"embed"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	api "github.com/ShotaKitazawa/korpus/internal/api"
	"github.com/ShotaKitazawa/korpus/internal/config"
	"github.com/ShotaKitazawa/korpus/internal/gitclient"
	"github.com/ShotaKitazawa/korpus/internal/gitindex"
	"github.com/ShotaKitazawa/korpus/internal/index"
	oidcmw "github.com/ShotaKitazawa/korpus/internal/oidc"
)

//go:embed all:frontend/dist
var frontendDist embed.FS

// ClusterState bundles the in-memory index, git indexes, and pull status for one cluster.
type ClusterState struct {
	idx    *index.Index
	subDir string

	mu            sync.RWMutex
	gc            *gitclient.Client
	workDir       string
	commitIdx     *gitindex.CommitIndex
	changeIdx     *gitindex.ChangeIndex
	lastPullAt    *time.Time
	lastPullErr   string
	resourceCount int
}

func (s *ClusterState) setGit(gc *gitclient.Client, workDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gc = gc
	s.workDir = workDir
}

func (s *ClusterState) rebuildIndexes(dir, clusterName string, historyDays int) error {
	if err := s.idx.Build(dir); err != nil {
		return err
	}
	s.mu.RLock()
	gc := s.gc
	subDir := s.subDir
	s.mu.RUnlock()
	if gc == nil {
		return fmt.Errorf("git client not ready")
	}
	repo := gc.Repo()
	commitIdx, err := gitindex.BuildCommitIndex(repo)
	if err != nil {
		return fmt.Errorf("build commit index: %w", err)
	}
	changeIdx, err := gitindex.BuildChangeIndex(repo, clusterName, subDir, historyDays)
	if err != nil {
		return fmt.Errorf("build change index: %w", err)
	}
	s.mu.Lock()
	s.commitIdx = commitIdx
	s.changeIdx = changeIdx
	s.mu.Unlock()
	return nil
}

func (s *ClusterState) recordPull(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.lastPullAt = &now
	if err != nil {
		s.lastPullErr = err.Error()
	} else {
		s.lastPullErr = ""
		s.resourceCount = len(s.idx.List("", "", "", nil))
	}
}

func (s *ClusterState) status() clusterStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cs := clusterStatus{
		LastPullErr:   s.lastPullErr,
		ResourceCount: s.resourceCount,
	}
	if s.lastPullAt != nil {
		t := *s.lastPullAt
		cs.LastPullAt = &t
	}
	return cs
}

func (s *ClusterState) ready() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastPullAt != nil && s.lastPullErr == ""
}

// relPath converts an absolute file path into a repo-relative path using forward slashes.
func (s *ClusterState) relPath(absPath string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rel := strings.TrimPrefix(absPath, s.workDir+string(filepath.Separator))
	if rel == absPath {
		return ""
	}
	return filepath.ToSlash(rel)
}

func (s *ClusterState) fileAt(relPath, sha string) (string, error) {
	s.mu.RLock()
	gc := s.gc
	s.mu.RUnlock()
	if gc == nil {
		return "", fmt.Errorf("cluster not ready")
	}
	return gc.FileAtCommit(relPath, sha)
}

type clusterStatus struct {
	Name          string     `json:"name"`
	LastPullAt    *time.Time `json:"lastPullAt"`
	LastPullErr   string     `json:"lastPullErr"`
	ResourceCount int        `json:"resourceCount"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	logger := slog.Default()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	cfg, err := config.LoadServer(*configPath)
	if err != nil {
		logger.Error("load config", "err", err)
		os.Exit(1)
	}

	clusterNames := make([]string, 0, len(cfg.Spec.Clusters))
	for _, c := range cfg.Spec.Clusters {
		clusterNames = append(clusterNames, c.Name)
	}

	states := make(map[string]*ClusterState, len(cfg.Spec.Clusters))
	for _, c := range cfg.Spec.Clusters {
		states[c.Name] = &ClusterState{
			idx:    index.New(c.Name, cfg.Spec.Index.Fields),
			subDir: c.Git.SubDir,
		}
	}

	// Start one pull goroutine per cluster.
	for _, clusterCfg := range cfg.Spec.Clusters {
		c := clusterCfg
		state := states[c.Name]
		go func() {
			workDir, err := os.MkdirTemp("", "korpus-server-"+c.Name+"-*")
			if err != nil {
				logger.Error("create work dir", "cluster", c.Name, "err", err)
				return
			}
			defer os.RemoveAll(workDir)

			gc, err := gitclient.Clone(ctx, c.Git.Repo, c.Git.Branch, c.Git.Token, c.Git.TokenFile, workDir, 0)
			if err != nil {
				logger.Error("git clone", "cluster", c.Name, "err", err)
				return
			}
			state.setGit(gc, workDir)

			indexDir := filepath.Join(workDir, c.Git.SubDir)
			if buildErr := state.rebuildIndexes(indexDir, c.Name, cfg.Spec.Index.HistoryDays); buildErr != nil {
				logger.Warn("index build", "cluster", c.Name, "err", buildErr)
				state.recordPull(buildErr)
			} else {
				state.recordPull(nil)
			}

			ticker := time.NewTicker(cfg.Spec.PullIntervalDuration())
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					pullErr := gc.Pull()
					if pullErr != nil {
						logger.Warn("pull failed, re-cloning", "cluster", c.Name, "err", pullErr)
						os.RemoveAll(workDir)
						workDir, err = os.MkdirTemp("", "korpus-server-"+c.Name+"-*")
						if err != nil {
							logger.Error("create work dir on re-clone", "cluster", c.Name, "err", err)
							state.recordPull(err)
							continue
						}
						gc, err = gitclient.Clone(ctx, c.Git.Repo, c.Git.Branch, c.Git.Token, c.Git.TokenFile, workDir, 0)
						if err != nil {
							logger.Error("re-clone failed", "cluster", c.Name, "err", err)
							state.recordPull(err)
							continue
						}
						state.setGit(gc, workDir)
					}
					indexDir = filepath.Join(workDir, c.Git.SubDir)
					if buildErr := state.rebuildIndexes(indexDir, c.Name, cfg.Spec.Index.HistoryDays); buildErr != nil {
						logger.Warn("index rebuild", "cluster", c.Name, "err", buildErr)
						state.recordPull(buildErr)
					} else {
						state.recordPull(nil)
					}
				}
			}
		}()
	}

	var oidcMiddleware *oidcmw.Middleware
	if cfg.Spec.OIDC != nil {
		oidcMiddleware, err = oidcmw.New(ctx, cfg.Spec.OIDC.Issuer, cfg.Spec.OIDC.Audience)
		if err != nil {
			logger.Error("initialize oidc middleware", "err", err)
			os.Exit(1)
		}
	}

	mux := buildMux(ctx, cfg, states, clusterNames, logger, oidcMiddleware)
	srv := &http.Server{Addr: cfg.Spec.Addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server", "err", err)
		}
	}()

	logger.Info("started", "addr", cfg.Spec.Addr, "pullInterval", cfg.Spec.PullInterval, "clusters", len(cfg.Spec.Clusters))

	<-ctx.Done()
	logger.Info("shutting down")
	srv.Shutdown(context.Background()) //nolint:errcheck
}

func maybeProtect(mw *oidcmw.Middleware, h http.Handler) http.Handler {
	if mw != nil {
		return mw.Handler(h)
	}
	return h
}

func buildMux(ctx context.Context, cfg *config.ServerConfig, states map[string]*ClusterState, clusterNames []string, logger *slog.Logger, oidcMW *oidcmw.Middleware) http.Handler {
	_ = ctx
	mux := http.NewServeMux()

	ogenSrv, err := api.NewServer(&apiHandler{
		states:       states,
		clusterNames: clusterNames,
	})
	if err != nil {
		panic(fmt.Sprintf("create api server: %v", err))
	}

	// Always-public routes.
	mux.Handle("/healthz", ogenSrv)
	mux.HandleFunc("/.well-known/oauth-protected-resource", oauthProtectedResourceHandler(cfg))
	mux.HandleFunc("/auth-config", authConfigHandler(cfg))

	// Protected routes (JWT middleware when OIDC is configured).
	mux.Handle("/api/", maybeProtect(oidcMW, ogenSrv))
	mux.Handle("/mcp", maybeProtect(oidcMW, buildMCPServer(states, clusterNames)))

	// Frontend SPA.
	distFS, err := fs.Sub(frontendDist, "frontend/dist")
	if err != nil {
		logger.Error("frontend embed", "err", err)
	} else {
		fileServer := http.FileServer(http.FS(distFS))
		mux.Handle("/assets/", fileServer)
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				f, err := distFS.Open(r.URL.Path[1:])
				if err == nil {
					f.Close()
					fileServer.ServeHTTP(w, r)
					return
				}
			}
			http.ServeFileFS(w, r, distFS, "index.html")
		})
	}

	return mux
}

func buildMCPServer(states map[string]*ClusterState, clusterNames []string) http.Handler {
	s := mcpserver.NewMCPServer("korpus", "2.0.0")

	resolveStates := func(cluster string) []*ClusterState {
		if cluster != "" {
			if st, ok := states[cluster]; ok {
				return []*ClusterState{st}
			}
			return nil
		}
		out := make([]*ClusterState, 0, len(clusterNames))
		for _, name := range clusterNames {
			out = append(out, states[name])
		}
		return out
	}

	s.AddTool(mcp.NewTool("list_clusters",
		mcp.WithDescription("List all available cluster names"),
	), func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(mustJSON(clusterNames)), nil
	})

	s.AddTool(mcp.NewTool("list_gvks",
		mcp.WithDescription("List available GVKs (Group/Version/Kind), optionally filtered by cluster or namespace"),
		mcp.WithString("cluster", mcp.Description("Cluster name (optional)")),
		mcp.WithString("namespace", mcp.Description("Namespace filter (optional)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		cluster, _ := args["cluster"].(string)
		ns, _ := args["namespace"].(string)
		type gvkInfo struct {
			Group   string `json:"group"`
			Version string `json:"version"`
			Kind    string `json:"kind"`
		}
		type key struct{ g, v, k string }
		seen := make(map[key]struct{})
		var result []gvkInfo
		for _, st := range resolveStates(cluster) {
			for _, gvk := range st.idx.GVKs(ns) {
				k := key{gvk.Group, gvk.Version, gvk.Kind}
				if _, ok := seen[k]; !ok {
					seen[k] = struct{}{}
					result = append(result, gvkInfo{Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind})
				}
			}
		}
		return mcp.NewToolResultText(mustJSON(result)), nil
	})

	s.AddTool(mcp.NewTool("list_namespaces",
		mcp.WithDescription("List namespaces, optionally filtered by cluster"),
		mcp.WithString("cluster", mcp.Description("Cluster name (optional)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		cluster, _ := args["cluster"].(string)
		seen := make(map[string]struct{})
		for _, st := range resolveStates(cluster) {
			for _, ns := range st.idx.Namespaces() {
				seen[ns] = struct{}{}
			}
		}
		result := make([]string, 0, len(seen))
		for ns := range seen {
			result = append(result, ns)
		}
		return mcp.NewToolResultText(mustJSON(result)), nil
	})

	s.AddTool(mcp.NewTool("get_resource",
		mcp.WithDescription("Get the YAML content of a specific K8s resource"),
		mcp.WithString("cluster", mcp.Required(), mcp.Description("Cluster name")),
		mcp.WithString("group", mcp.Required(), mcp.Description("API group (use 'core' for core resources)")),
		mcp.WithString("kind", mcp.Required(), mcp.Description("Resource kind")),
		mcp.WithString("namespace", mcp.Description("Namespace (empty for cluster-scoped resources)")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Resource name")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		cluster, _ := args["cluster"].(string)
		group, _ := args["group"].(string)
		kind, _ := args["kind"].(string)
		ns, _ := args["namespace"].(string)
		name, _ := args["name"].(string)
		st, ok := states[cluster]
		if !ok {
			return mcp.NewToolResultText("cluster not found"), nil
		}
		meta, ok := st.idx.Get(group, kind, ns, name)
		if !ok {
			return mcp.NewToolResultText("not found"), nil
		}
		data, err := os.ReadFile(meta.FilePath)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	s.AddTool(mcp.NewTool("get_snapshot",
		mcp.WithDescription("Get a snapshot of K8s resources at a point in time. Omit datetime for current state. CEL filtering only works without datetime."),
		mcp.WithString("datetime", mcp.Description("RFC3339 datetime for historical snapshot (optional)")),
		mcp.WithString("cluster", mcp.Description("Cluster name (optional)")),
		mcp.WithString("group", mcp.Description("API group filter (optional)")),
		mcp.WithString("kind", mcp.Description("Resource kind filter (optional)")),
		mcp.WithString("namespace", mcp.Description("Namespace filter (optional)")),
		mcp.WithString("name", mcp.Description("Resource name filter (optional)")),
		mcp.WithString("cel", mcp.Description("CEL expression filter (only valid without datetime)")),
		mcp.WithNumber("limit", mcp.Description("Maximum results to return (default 50)")),
		mcp.WithNumber("offset", mcp.Description("Pagination offset (default 0)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		cluster, _ := args["cluster"].(string)
		group, _ := args["group"].(string)
		kind, _ := args["kind"].(string)
		ns, _ := args["namespace"].(string)
		name, _ := args["name"].(string)
		cel, _ := args["cel"].(string)
		limit := 50
		if lv, ok := args["limit"].(float64); ok && lv > 0 {
			limit = int(lv)
		}

		var all []index.ResourceMeta
		for _, st := range resolveStates(cluster) {
			var results []index.ResourceMeta
			var err error
			if cel != "" {
				results, err = st.idx.Query(group, kind, ns, nil, cel)
				if err != nil {
					return mcp.NewToolResultText("cel error: " + err.Error()), nil
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
		return mcp.NewToolResultText(mustJSON(all[:min(limit, len(all))])), nil
	})

	s.AddTool(mcp.NewTool("get_history",
		mcp.WithDescription("Get the change history for K8s resources"),
		mcp.WithString("since", mcp.Description("RFC3339 start time (optional)")),
		mcp.WithString("until", mcp.Description("RFC3339 end time (optional)")),
		mcp.WithString("cluster", mcp.Description("Cluster name (optional)")),
		mcp.WithString("group", mcp.Description("API group filter (optional)")),
		mcp.WithString("kind", mcp.Description("Resource kind filter (optional)")),
		mcp.WithString("namespace", mcp.Description("Namespace filter (optional)")),
		mcp.WithString("name", mcp.Description("Resource name filter (optional)")),
		mcp.WithString("changeType", mcp.Description("Change type: added, modified, or deleted (optional)")),
		mcp.WithNumber("limit", mcp.Description("Maximum results (default 50)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		cluster, _ := args["cluster"].(string)
		group, _ := args["group"].(string)
		kind, _ := args["kind"].(string)
		ns, _ := args["namespace"].(string)
		name, _ := args["name"].(string)
		ct := gitindex.ChangeType(func() string { s, _ := args["changeType"].(string); return s }())
		limit := 50
		if lv, ok := args["limit"].(float64); ok && lv > 0 {
			limit = int(lv)
		}
		var since, until *time.Time
		if sv, ok := args["since"].(string); ok && sv != "" {
			if t, err := time.Parse(time.RFC3339, sv); err == nil {
				since = &t
			}
		}
		if uv, ok := args["until"].(string); ok && uv != "" {
			if t, err := time.Parse(time.RFC3339, uv); err == nil {
				until = &t
			}
		}

		var allEvents []gitindex.ChangeEvent
		for _, clusterName := range clusterNames {
			if cluster != "" && clusterName != cluster {
				continue
			}
			st := states[clusterName]
			st.mu.RLock()
			ci := st.changeIdx
			st.mu.RUnlock()
			if ci == nil {
				continue
			}
			events, _ := ci.Query(since, until, clusterName, group, kind, ns, name, ct, 1<<30, 0)
			allEvents = append(allEvents, events...)
		}
		if len(allEvents) > limit {
			allEvents = allEvents[:limit]
		}
		return mcp.NewToolResultText(mustJSON(allEvents)), nil
	})

	s.AddTool(mcp.NewTool("get_diff",
		mcp.WithDescription("Get the before/after YAML for a resource between two commits"),
		mcp.WithString("cluster", mcp.Required(), mcp.Description("Cluster name")),
		mcp.WithString("group", mcp.Required(), mcp.Description("API group")),
		mcp.WithString("kind", mcp.Required(), mcp.Description("Resource kind")),
		mcp.WithString("namespace", mcp.Description("Namespace (empty for cluster-scoped)")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Resource name")),
		mcp.WithString("from", mcp.Required(), mcp.Description("From commit SHA")),
		mcp.WithString("to", mcp.Required(), mcp.Description("To commit SHA")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		cluster, _ := args["cluster"].(string)
		group, _ := args["group"].(string)
		kind, _ := args["kind"].(string)
		ns, _ := args["namespace"].(string)
		name, _ := args["name"].(string)
		fromSHA, _ := args["from"].(string)
		toSHA, _ := args["to"].(string)
		st, ok := states[cluster]
		if !ok {
			return mcp.NewToolResultText("cluster not found"), nil
		}
		meta, ok := st.idx.Get(group, kind, ns, name)
		if !ok {
			return mcp.NewToolResultText("not found"), nil
		}
		relPath := st.relPath(meta.FilePath)
		if relPath == "" {
			return mcp.NewToolResultText("cannot determine git path"), nil
		}
		before, err := st.fileAt(relPath, fromSHA)
		if err != nil {
			return mcp.NewToolResultText("from: " + err.Error()), nil
		}
		after, err := st.fileAt(relPath, toSHA)
		if err != nil {
			return mcp.NewToolResultText("to: " + err.Error()), nil
		}
		return mcp.NewToolResultText(mustJSON(map[string]string{"before": before, "after": after})), nil
	})

	s.AddTool(mcp.NewTool("get_volatility",
		mcp.WithDescription("Get the most frequently changing K8s resources, ranked by change ratio"),
		mcp.WithString("cluster", mcp.Description("Cluster name (optional)")),
		mcp.WithString("group", mcp.Description("API group filter (optional)")),
		mcp.WithString("kind", mcp.Description("Resource kind filter (optional)")),
		mcp.WithString("namespace", mcp.Description("Namespace filter (optional)")),
		mcp.WithString("name", mcp.Description("Resource name filter (optional)")),
		mcp.WithNumber("commits", mcp.Description("Number of recent commits to analyze (default 50)")),
		mcp.WithNumber("threshold", mcp.Description("Minimum change ratio to include 0.0–1.0 (default 0.0)")),
		mcp.WithNumber("limit", mcp.Description("Maximum results (default 50)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		cluster, _ := args["cluster"].(string)
		group, _ := args["group"].(string)
		kind, _ := args["kind"].(string)
		ns, _ := args["namespace"].(string)
		name, _ := args["name"].(string)
		commits := 50
		if cv, ok := args["commits"].(float64); ok && cv > 0 {
			commits = int(cv)
		}
		threshold := 0.0
		if tv, ok := args["threshold"].(float64); ok {
			threshold = tv
		}
		limit := 50
		if lv, ok := args["limit"].(float64); ok && lv > 0 {
			limit = int(lv)
		}

		type entry struct {
			Cluster   string  `json:"cluster"`
			Group     string  `json:"group"`
			Kind      string  `json:"kind"`
			Namespace string  `json:"namespace"`
			Name      string  `json:"name"`
			Count     int     `json:"count"`
			Total     int     `json:"total"`
			Ratio     float64 `json:"ratio"`
		}
		var result []entry
		for _, clusterName := range clusterNames {
			if cluster != "" && clusterName != cluster {
				continue
			}
			st := states[clusterName]
			st.mu.RLock()
			ci := st.changeIdx
			st.mu.RUnlock()
			if ci == nil {
				continue
			}
			for _, e := range ci.Volatility(clusterName, group, kind, ns, name, commits) {
				ratio := 0.0
				if e.Total > 0 {
					ratio = float64(e.Count) / float64(e.Total)
				}
				if ratio < threshold {
					continue
				}
				result = append(result, entry{
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
		if len(result) > limit {
			result = result[:limit]
		}
		return mcp.NewToolResultText(mustJSON(result)), nil
	})

	s.AddTool(mcp.NewTool("get_volatility_fields",
		mcp.WithDescription("Get field-level change frequencies for a specific resource kind"),
		mcp.WithString("cluster", mcp.Description("Cluster name (optional)")),
		mcp.WithString("group", mcp.Required(), mcp.Description("API group")),
		mcp.WithString("kind", mcp.Required(), mcp.Description("Resource kind")),
		mcp.WithString("namespace", mcp.Description("Namespace filter (optional)")),
		mcp.WithString("name", mcp.Description("Resource name filter (optional)")),
		mcp.WithNumber("commits", mcp.Description("Number of recent commits to analyze (default 50)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		cluster, _ := args["cluster"].(string)
		group, _ := args["group"].(string)
		kind, _ := args["kind"].(string)
		ns, _ := args["namespace"].(string)
		name, _ := args["name"].(string)
		commits := 50
		if cv, ok := args["commits"].(float64); ok && cv > 0 {
			commits = int(cv)
		}

		var allEntries []gitindex.FieldVolatilityEntry
		for _, clusterName := range clusterNames {
			if cluster != "" && clusterName != cluster {
				continue
			}
			st := states[clusterName]
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
		return mcp.NewToolResultText(mustJSON(allEntries)), nil
	})

	return mcpserver.NewStreamableHTTPServer(s)
}

// oauthProtectedResourceHandler serves RFC9728 metadata when OIDC is configured.
func oauthProtectedResourceHandler(cfg *config.ServerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.Spec.OIDC == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"resource":                 cfg.Spec.OIDC.Audience,
			"authorization_servers":    []string{cfg.Spec.OIDC.Issuer},
			"bearer_methods_supported": []string{"header"},
		})
	}
}

// authConfigHandler serves OIDC configuration to the SPA frontend.
func authConfigHandler(cfg *config.ServerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if cfg.Spec.OIDC == nil {
			json.NewEncoder(w).Encode(map[string]any{"enabled": false}) //nolint:errcheck
			return
		}
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"enabled":  true,
			"issuer":   cfg.Spec.OIDC.Issuer,
			"clientId": cfg.Spec.OIDC.ClientID,
			"audience": cfg.Spec.OIDC.Audience,
		})
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
