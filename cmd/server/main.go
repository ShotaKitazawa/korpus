package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	_ "net/http/pprof"

	"github.com/felixge/fgprof"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"embed"

	git "github.com/go-git/go-git/v5"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	api "github.com/ShotaKitazawa/korpus/internal/api"
	"github.com/ShotaKitazawa/korpus/internal/config"
	"github.com/ShotaKitazawa/korpus/internal/gitclient"
	"github.com/ShotaKitazawa/korpus/internal/gitindex"
	"github.com/ShotaKitazawa/korpus/internal/index"
	oidcmw "github.com/ShotaKitazawa/korpus/internal/oidc"
	"github.com/ShotaKitazawa/korpus/internal/query"
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

// --- query.ClusterQuerier implementation ---

func (s *ClusterState) Index() *index.Index { return s.idx }

func (s *ClusterState) CommitIndex() *gitindex.CommitIndex {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.commitIdx
}

func (s *ClusterState) ChangeIndex() *gitindex.ChangeIndex {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.changeIdx
}

func (s *ClusterState) FileAt(relPath, sha string) (string, error) {
	return s.fileAt(relPath, sha)
}

func (s *ClusterState) RelPath(absPath string) string {
	return s.relPath(absPath)
}

func (s *ClusterState) GitRepo() *git.Repository {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.gc == nil {
		return nil
	}
	return s.gc.Repo()
}

func (s *ClusterState) SubDir() string { return s.subDir }

func (s *ClusterState) WorkDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workDir
}

// --- lifecycle methods ---

func (s *ClusterState) setGit(gc *gitclient.Client, workDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gc = gc
	s.workDir = workDir
}

func (s *ClusterState) rebuildIndexes(dir, clusterName string, historyDays int) error {
	logger := slog.Default()

	t0 := time.Now()
	if err := s.idx.Build(dir); err != nil {
		return err
	}
	logger.Info("index.Build done", "cluster", clusterName, "elapsed", time.Since(t0).Round(time.Millisecond))

	s.mu.RLock()
	gc := s.gc
	subDir := s.subDir
	workDir := s.workDir
	s.mu.RUnlock()
	if gc == nil {
		return fmt.Errorf("git client not ready")
	}
	repo := gc.Repo()

	t1 := time.Now()
	commitIdx, err := gitindex.BuildCommitIndex(repo)
	if err != nil {
		return fmt.Errorf("build commit index: %w", err)
	}
	logger.Info("BuildCommitIndex done", "cluster", clusterName, "elapsed", time.Since(t1).Round(time.Millisecond))

	t2 := time.Now()
	changeIdx, err := gitindex.BuildChangeIndex(repo, workDir, clusterName, subDir, historyDays)
	if err != nil {
		return fmt.Errorf("build change index: %w", err)
	}
	logger.Info("BuildChangeIndex done", "cluster", clusterName, "elapsed", time.Since(t2).Round(time.Millisecond))

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
	pprofAddr := flag.String("pprof-addr", "", "address for pprof debug server (e.g. :6060); disabled when empty")
	enableFgprof := flag.Bool("fgprof", false, "add /debug/fgprof wall-clock profile endpoint (requires --pprof-addr)")
	flag.Parse()

	logger := slog.Default()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if *pprofAddr != "" {
		if *enableFgprof {
			http.DefaultServeMux.Handle("/debug/fgprof", fgprof.Handler())
		}
		go func() {
			logger.Info("pprof server starting", "addr", *pprofAddr)
			if err := http.ListenAndServe(*pprofAddr, http.DefaultServeMux); err != nil {
				logger.Error("pprof server", "err", err)
			}
		}()
	}

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

			t0 := time.Now()
			gc, err := gitclient.Clone(ctx, c.Git.Repo, c.Git.Branch, c.Git.Token, c.Git.TokenFile, workDir, 0)
			if err != nil {
				logger.Error("git clone", "cluster", c.Name, "err", err)
				return
			}
			logger.Info("git clone done", "cluster", c.Name, "elapsed", time.Since(t0).Round(time.Millisecond))
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
		rmURL := protectedResourceMetadataURL(cfg.Spec.OIDC.Audience)
		oidcMiddleware, err = oidcmw.New(ctx, cfg.Spec.OIDC.Issuer, cfg.Spec.OIDC.Audience, rmURL)
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

	// Build the shared query server used by both REST and MCP.
	clusters := make(map[string]query.ClusterQuerier, len(states))
	for name, st := range states {
		clusters[name] = st
	}
	q := query.New(clusters, clusterNames)

	ogenSrv, err := api.NewServer(&apiHandler{
		states:       states,
		clusterNames: clusterNames,
		q:            q,
	})
	if err != nil {
		panic(fmt.Sprintf("create api server: %v", err))
	}

	// Always-public routes.
	mux.Handle("/healthz", ogenSrv)
	wkPath := "/.well-known/oauth-protected-resource"
	if cfg.Spec.OIDC != nil {
		wkPath = protectedResourceWellKnownPath(cfg.Spec.OIDC.Audience)
	}
	mux.HandleFunc(wkPath, oauthProtectedResourceHandler(cfg))
	mux.HandleFunc("/auth-config", authConfigHandler(cfg))

	// Protected routes (JWT middleware when OIDC is configured).
	mux.Handle("/api/", maybeProtect(oidcMW, ogenSrv))
	mux.Handle("/mcp", maybeProtect(oidcMW, buildMCPServer(q)))

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

func buildMCPServer(q *query.Server) http.Handler {
	s := mcpserver.NewMCPServer("korpus", "2.0.0")

	s.AddTool(mcp.NewTool("list_clusters",
		mcp.WithDescription("List all available cluster names"),
	), func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(mustJSON(q.ListClusters())), nil
	})

	s.AddTool(mcp.NewTool("list_gvks",
		mcp.WithDescription("List available GVKs (Group/Version/Kind), optionally filtered by cluster or namespace"),
		mcp.WithString("cluster", mcp.Description("Cluster name (optional)")),
		mcp.WithString("namespace", mcp.Description("Namespace filter (optional)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		cluster, _ := args["cluster"].(string)
		ns, _ := args["namespace"].(string)
		result, err := q.ListGVKs(cluster, ns)
		if err != nil {
			return mcp.NewToolResultText(err.Error()), nil
		}
		return mcp.NewToolResultText(mustJSON(result)), nil
	})

	s.AddTool(mcp.NewTool("list_namespaces",
		mcp.WithDescription("List namespaces, optionally filtered by cluster"),
		mcp.WithString("cluster", mcp.Description("Cluster name (optional)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		cluster, _ := args["cluster"].(string)
		result, err := q.ListNamespaces(cluster)
		if err != nil {
			return mcp.NewToolResultText(err.Error()), nil
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
		data, err := q.GetResource(cluster, group, kind, ns, name)
		if err != nil {
			if errors.Is(err, query.ErrClusterNotFound) {
				return mcp.NewToolResultText("cluster not found"), nil
			}
			if errors.Is(err, query.ErrNotFound) {
				return mcp.NewToolResultText("not found"), nil
			}
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
		datetimeStr, _ := args["datetime"].(string)
		limit := 50
		if lv, ok := args["limit"].(float64); ok && lv > 0 {
			limit = int(lv)
		}
		offset := 0
		if ov, ok := args["offset"].(float64); ok && ov >= 0 {
			offset = int(ov)
		}

		if datetimeStr != "" {
			if cel != "" {
				return mcp.NewToolResultText("cel filter is not supported with datetime"), nil
			}
			t, err := time.Parse(time.RFC3339, datetimeStr)
			if err != nil {
				return mcp.NewToolResultText("invalid datetime: " + err.Error()), nil
			}
			result, err := q.GetHistoricalSnapshot(t, cluster, group, kind, ns, name, limit, offset)
			if err != nil {
				return mcp.NewToolResultText(err.Error()), nil
			}
			return mcp.NewToolResultText(mustJSON(result.Items)), nil
		}

		result, err := q.GetCurrentSnapshot(cluster, group, kind, ns, name, cel, nil, limit, offset)
		if err != nil {
			return mcp.NewToolResultText("cel error: " + err.Error()), nil
		}
		return mcp.NewToolResultText(mustJSON(result.Items)), nil
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
		mcp.WithNumber("offset", mcp.Description("Pagination offset (default 0)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		cluster, _ := args["cluster"].(string)
		group, _ := args["group"].(string)
		kind, _ := args["kind"].(string)
		ns, _ := args["namespace"].(string)
		name, _ := args["name"].(string)
		changeType, _ := args["changeType"].(string)
		limit := 50
		if lv, ok := args["limit"].(float64); ok && lv > 0 {
			limit = int(lv)
		}
		offset := 0
		if ov, ok := args["offset"].(float64); ok && ov >= 0 {
			offset = int(ov)
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
		events, _, err := q.GetHistory(cluster, group, kind, ns, name, changeType, since, until, limit, offset)
		if err != nil {
			return mcp.NewToolResultText(err.Error()), nil
		}
		return mcp.NewToolResultText(mustJSON(events)), nil
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
		result, err := q.GetDiff(cluster, group, kind, ns, name, fromSHA, toSHA)
		if err != nil {
			if errors.Is(err, query.ErrClusterNotFound) {
				return mcp.NewToolResultText("cluster not found"), nil
			}
			if errors.Is(err, query.ErrNotFound) {
				return mcp.NewToolResultText("not found"), nil
			}
			return mcp.NewToolResultText(err.Error()), nil
		}
		return mcp.NewToolResultText(mustJSON(map[string]string{"before": result.Before, "after": result.After})), nil
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
		mcp.WithNumber("offset", mcp.Description("Pagination offset (default 0)")),
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
		offset := 0
		if ov, ok := args["offset"].(float64); ok && ov >= 0 {
			offset = int(ov)
		}
		result, _, err := q.GetVolatility(cluster, group, kind, ns, name, commits, threshold, limit, offset)
		if err != nil {
			return mcp.NewToolResultText(err.Error()), nil
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
		mcp.WithNumber("limit", mcp.Description("Maximum results (default 0 = all)")),
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
		limit := 0
		if lv, ok := args["limit"].(float64); ok && lv > 0 {
			limit = int(lv)
		}
		result, err := q.GetVolatilityFields(cluster, group, kind, ns, name, commits, limit)
		if err != nil {
			return mcp.NewToolResultText(err.Error()), nil
		}
		return mcp.NewToolResultText(mustJSON(result)), nil
	})

	return mcpserver.NewStreamableHTTPServer(s)
}

// protectedResourceWellKnownPath returns the /.well-known/oauth-protected-resource path
// for the given audience URI, per RFC 9728 §3 well-known URI construction.
func protectedResourceWellKnownPath(audience string) string {
	u, err := url.Parse(audience)
	if err != nil || u.Host == "" {
		return "/.well-known/oauth-protected-resource"
	}
	p := strings.TrimSuffix(u.Path, "/")
	return "/.well-known/oauth-protected-resource" + p
}

// protectedResourceMetadataURL returns the full URL of the well-known document,
// used in WWW-Authenticate challenges per RFC 9728 §5.1.
func protectedResourceMetadataURL(audience string) string {
	u, err := url.Parse(audience)
	if err != nil || u.Host == "" {
		return ""
	}
	p := strings.TrimSuffix(u.Path, "/")
	return u.Scheme + "://" + u.Host + "/.well-known/oauth-protected-resource" + p
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
