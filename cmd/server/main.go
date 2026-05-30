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
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"embed"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/ShotaKitazawa/korpus/internal/config"
	"github.com/ShotaKitazawa/korpus/internal/gitclient"
	"github.com/ShotaKitazawa/korpus/internal/index"
)

//go:embed all:frontend/dist
var frontendDist embed.FS

// ClusterState bundles the in-memory index and the git client for one cluster.
type ClusterState struct {
	idx *index.Index

	mu      sync.RWMutex
	gc      *gitclient.Client
	workDir string
}

func (s *ClusterState) setGit(gc *gitclient.Client, workDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gc = gc
	s.workDir = workDir
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

func (s *ClusterState) history(relPath string, n int) ([]gitclient.HistoryEntry, error) {
	s.mu.RLock()
	gc := s.gc
	s.mu.RUnlock()
	if gc == nil {
		return nil, fmt.Errorf("cluster not ready")
	}
	return gc.FileHistory(relPath, n)
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

	states := make(map[string]*ClusterState, len(cfg.Spec.Clusters))
	for _, c := range cfg.Spec.Clusters {
		states[c.Name] = &ClusterState{
			idx: index.New(c.Name, cfg.Spec.Index.Fields),
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

			gc, err := gitclient.Clone(ctx, c.Git.Repo, c.Git.Branch, c.Git.Token, workDir, 0)
			if err != nil {
				logger.Error("git clone", "cluster", c.Name, "err", err)
				return
			}
			state.setGit(gc, workDir)

			indexDir := filepath.Join(workDir, c.Git.SubDir)
			if err := state.idx.Build(indexDir); err != nil {
				logger.Warn("index build", "cluster", c.Name, "err", err)
			}

			ticker := time.NewTicker(cfg.Spec.PullIntervalDuration())
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := gc.Pull(); err != nil {
						logger.Warn("pull failed, re-cloning", "cluster", c.Name, "err", err)
						os.RemoveAll(workDir)
						workDir, err = os.MkdirTemp("", "korpus-server-"+c.Name+"-*")
						if err != nil {
							logger.Error("create work dir on re-clone", "cluster", c.Name, "err", err)
							continue
						}
						gc, err = gitclient.Clone(ctx, c.Git.Repo, c.Git.Branch, c.Git.Token, workDir, 0)
						if err != nil {
							logger.Error("re-clone failed", "cluster", c.Name, "err", err)
							continue
						}
						state.setGit(gc, workDir)
					}
					indexDir = filepath.Join(workDir, c.Git.SubDir)
					if err := state.idx.Build(indexDir); err != nil {
						logger.Warn("index rebuild", "cluster", c.Name, "err", err)
					}
				}
			}
		}()
	}

	mux := buildMux(cfg, states, logger)
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

// resolveStates returns the states to query. If cluster is empty, all states are returned.
func resolveStates(cluster string, states map[string]*ClusterState) []*ClusterState {
	if cluster == "" {
		result := make([]*ClusterState, 0, len(states))
		for _, st := range states {
			result = append(result, st)
		}
		return result
	}
	if st, ok := states[cluster]; ok {
		return []*ClusterState{st}
	}
	return nil
}

func buildMux(cfg *config.ServerConfig, states map[string]*ClusterState, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	// Cluster names list (stable order)
	clusterNames := make([]string, 0, len(cfg.Spec.Clusters))
	for _, c := range cfg.Spec.Clusters {
		clusterNames = append(clusterNames, c.Name)
	}

	// API
	mux.HandleFunc("GET /api/clusters", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, clusterNames)
	})

	mux.HandleFunc("GET /api/kinds", func(w http.ResponseWriter, r *http.Request) {
		cluster := r.URL.Query().Get("cluster")
		ns := r.URL.Query().Get("namespace")
		seen := make(map[string]struct{})
		for _, st := range resolveStates(cluster, states) {
			for _, k := range st.idx.Kinds(ns) {
				seen[k] = struct{}{}
			}
		}
		result := make([]string, 0, len(seen))
		for k := range seen {
			result = append(result, k)
		}
		sort.Strings(result)
		jsonResponse(w, result)
	})

	mux.HandleFunc("GET /api/namespaces", func(w http.ResponseWriter, r *http.Request) {
		cluster := r.URL.Query().Get("cluster")
		seen := make(map[string]struct{})
		for _, st := range resolveStates(cluster, states) {
			for _, ns := range st.idx.Namespaces() {
				seen[ns] = struct{}{}
			}
		}
		result := make([]string, 0, len(seen))
		for ns := range seen {
			result = append(result, ns)
		}
		sort.Strings(result)
		jsonResponse(w, result)
	})

	mux.HandleFunc("GET /api/resources", func(w http.ResponseWriter, r *http.Request) {
		cluster := r.URL.Query().Get("cluster")
		kind := r.URL.Query().Get("kind")
		ns := r.URL.Query().Get("namespace")
		var result []index.ResourceMeta
		for _, st := range resolveStates(cluster, states) {
			result = append(result, st.idx.List(kind, ns)...)
		}
		jsonResponse(w, result)
	})

	mux.HandleFunc("GET /api/resources/{cluster}/{kind}/{namespace}/{name}", func(w http.ResponseWriter, r *http.Request) {
		cluster := r.PathValue("cluster")
		kind := r.PathValue("kind")
		ns := r.PathValue("namespace")
		name := r.PathValue("name")
		state, ok := states[cluster]
		if !ok {
			http.NotFound(w, r)
			return
		}
		meta, ok := state.idx.Get(kind, ns, name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(meta.FilePath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(data) //nolint:errcheck
	})

	mux.HandleFunc("GET /api/resources/{cluster}/{kind}/{namespace}/{name}/history", func(w http.ResponseWriter, r *http.Request) {
		cluster := r.PathValue("cluster")
		kind := r.PathValue("kind")
		ns := r.PathValue("namespace")
		name := r.PathValue("name")
		n := 20
		if nStr := r.URL.Query().Get("n"); nStr != "" {
			if parsed, err := strconv.Atoi(nStr); err == nil && parsed > 0 {
				n = parsed
			}
		}
		state, ok := states[cluster]
		if !ok {
			http.NotFound(w, r)
			return
		}
		meta, ok := state.idx.Get(kind, ns, name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		relPath := state.relPath(meta.FilePath)
		if relPath == "" {
			http.Error(w, "cannot determine git path", http.StatusInternalServerError)
			return
		}
		entries, err := state.history(relPath, n)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResponse(w, entries)
	})

	mux.HandleFunc("GET /api/resources/{cluster}/{kind}/{namespace}/{name}/diff", func(w http.ResponseWriter, r *http.Request) {
		cluster := r.PathValue("cluster")
		kind := r.PathValue("kind")
		ns := r.PathValue("namespace")
		name := r.PathValue("name")
		fromSHA := r.URL.Query().Get("from")
		toSHA := r.URL.Query().Get("to")
		if fromSHA == "" || toSHA == "" {
			http.Error(w, "from and to are required", http.StatusBadRequest)
			return
		}
		state, ok := states[cluster]
		if !ok {
			http.NotFound(w, r)
			return
		}
		meta, ok := state.idx.Get(kind, ns, name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		relPath := state.relPath(meta.FilePath)
		if relPath == "" {
			http.Error(w, "cannot determine git path", http.StatusInternalServerError)
			return
		}
		before, err := state.fileAt(relPath, fromSHA)
		if err != nil {
			http.Error(w, "from: "+err.Error(), http.StatusInternalServerError)
			return
		}
		after, err := state.fileAt(relPath, toSHA)
		if err != nil {
			http.Error(w, "to: "+err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResponse(w, map[string]string{"before": before, "after": after})
	})

	mux.HandleFunc("GET /api/query", func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		if kind == "" {
			http.Error(w, "kind is required", http.StatusBadRequest)
			return
		}
		cluster := r.URL.Query().Get("cluster")
		ns := r.URL.Query().Get("namespace")
		expr := r.URL.Query().Get("q")
		var result []index.ResourceMeta
		for _, st := range resolveStates(cluster, states) {
			res, err := st.idx.Query(kind, ns, expr)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			result = append(result, res...)
		}
		jsonResponse(w, result)
	})

	// MCP
	mux.Handle("/mcp", buildMCPServer(states, clusterNames))

	// Frontend (SPA)
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
	s := mcpserver.NewMCPServer("korpus", "1.0.0")

	s.AddTool(mcp.NewTool("list_clusters",
		mcp.WithDescription("List all cluster names"),
	), func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(mustJSON(clusterNames)), nil
	})

	s.AddTool(mcp.NewTool("list_kinds",
		mcp.WithDescription("List all resource kinds, optionally filtered by cluster and/or namespace"),
		mcp.WithString("cluster", mcp.Description("Cluster name (optional, omit for all clusters)")),
		mcp.WithString("namespace", mcp.Description("Namespace to filter by (optional)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		cluster, _ := args["cluster"].(string)
		ns, _ := args["namespace"].(string)
		seen := make(map[string]struct{})
		for _, st := range resolveStates(cluster, states) {
			for _, k := range st.idx.Kinds(ns) {
				seen[k] = struct{}{}
			}
		}
		result := make([]string, 0, len(seen))
		for k := range seen {
			result = append(result, k)
		}
		return mcp.NewToolResultText(mustJSON(result)), nil
	})

	s.AddTool(mcp.NewTool("list_namespaces",
		mcp.WithDescription("List all namespaces, optionally filtered by cluster"),
		mcp.WithString("cluster", mcp.Description("Cluster name (optional, omit for all clusters)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		cluster, _ := args["cluster"].(string)
		seen := make(map[string]struct{})
		for _, st := range resolveStates(cluster, states) {
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

	s.AddTool(mcp.NewTool("list_resources",
		mcp.WithDescription("List K8s resources, optionally filtered by cluster, kind and/or namespace"),
		mcp.WithString("cluster", mcp.Description("Cluster name (optional, omit for all clusters)")),
		mcp.WithString("kind", mcp.Description("Resource kind (e.g. Pod, Deployment)")),
		mcp.WithString("namespace", mcp.Description("Namespace to filter by")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		cluster, _ := args["cluster"].(string)
		kind, _ := args["kind"].(string)
		ns, _ := args["namespace"].(string)
		var result []index.ResourceMeta
		for _, st := range resolveStates(cluster, states) {
			result = append(result, st.idx.List(kind, ns)...)
		}
		return mcp.NewToolResultText(mustJSON(result)), nil
	})

	s.AddTool(mcp.NewTool("get_resource",
		mcp.WithDescription("Get the YAML content of a specific K8s resource"),
		mcp.WithString("cluster", mcp.Required(), mcp.Description("Cluster name")),
		mcp.WithString("kind", mcp.Required(), mcp.Description("Resource kind")),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Namespace (empty for cluster-scoped)")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Resource name")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		cluster, _ := args["cluster"].(string)
		kind, _ := args["kind"].(string)
		ns, _ := args["namespace"].(string)
		name, _ := args["name"].(string)
		state, ok := states[cluster]
		if !ok {
			return mcp.NewToolResultText("cluster not found"), nil
		}
		meta, ok := state.idx.Get(kind, ns, name)
		if !ok {
			return mcp.NewToolResultText("not found"), nil
		}
		data, err := os.ReadFile(meta.FilePath)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	s.AddTool(mcp.NewTool("get_resource_history",
		mcp.WithDescription("Get the commit history for a specific K8s resource"),
		mcp.WithString("cluster", mcp.Required(), mcp.Description("Cluster name")),
		mcp.WithString("kind", mcp.Required(), mcp.Description("Resource kind")),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Namespace (empty for cluster-scoped)")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Resource name")),
		mcp.WithNumber("n", mcp.Description("Maximum number of commits to return (default 20)")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		cluster, _ := args["cluster"].(string)
		kind, _ := args["kind"].(string)
		ns, _ := args["namespace"].(string)
		name, _ := args["name"].(string)
		n := 20
		if nv, ok := args["n"].(float64); ok && nv > 0 {
			n = int(nv)
		}
		state, ok := states[cluster]
		if !ok {
			return mcp.NewToolResultText("cluster not found"), nil
		}
		meta, ok := state.idx.Get(kind, ns, name)
		if !ok {
			return mcp.NewToolResultText("not found"), nil
		}
		relPath := state.relPath(meta.FilePath)
		if relPath == "" {
			return mcp.NewToolResultText("cannot determine git path"), nil
		}
		entries, err := state.history(relPath, n)
		if err != nil {
			return mcp.NewToolResultText("error: " + err.Error()), nil
		}
		return mcp.NewToolResultText(mustJSON(entries)), nil
	})

	s.AddTool(mcp.NewTool("get_resource_diff",
		mcp.WithDescription("Get the before/after YAML for a resource between two commits"),
		mcp.WithString("cluster", mcp.Required(), mcp.Description("Cluster name")),
		mcp.WithString("kind", mcp.Required(), mcp.Description("Resource kind")),
		mcp.WithString("namespace", mcp.Required(), mcp.Description("Namespace (empty for cluster-scoped)")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Resource name")),
		mcp.WithString("from", mcp.Required(), mcp.Description("From commit SHA")),
		mcp.WithString("to", mcp.Required(), mcp.Description("To commit SHA")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		cluster, _ := args["cluster"].(string)
		kind, _ := args["kind"].(string)
		ns, _ := args["namespace"].(string)
		name, _ := args["name"].(string)
		fromSHA, _ := args["from"].(string)
		toSHA, _ := args["to"].(string)
		if fromSHA == "" || toSHA == "" {
			return mcp.NewToolResultText("from and to are required"), nil
		}
		state, ok := states[cluster]
		if !ok {
			return mcp.NewToolResultText("cluster not found"), nil
		}
		meta, ok := state.idx.Get(kind, ns, name)
		if !ok {
			return mcp.NewToolResultText("not found"), nil
		}
		relPath := state.relPath(meta.FilePath)
		if relPath == "" {
			return mcp.NewToolResultText("cannot determine git path"), nil
		}
		before, err := state.fileAt(relPath, fromSHA)
		if err != nil {
			return mcp.NewToolResultText("from: " + err.Error()), nil
		}
		after, err := state.fileAt(relPath, toSHA)
		if err != nil {
			return mcp.NewToolResultText("to: " + err.Error()), nil
		}
		return mcp.NewToolResultText(mustJSON(map[string]string{"before": before, "after": after})), nil
	})

	s.AddTool(mcp.NewTool("query_resources",
		mcp.WithDescription("Query K8s resources using a CEL expression. Examples: object.spec.replicas > 1, object.metadata.labels[\"app\"] == \"nginx\""),
		mcp.WithString("cluster", mcp.Description("Cluster name (optional, omit for all clusters)")),
		mcp.WithString("kind", mcp.Required(), mcp.Description("Resource kind (e.g. Pod, Deployment)")),
		mcp.WithString("namespace", mcp.Description("Namespace to filter by (optional)")),
		mcp.WithString("expr", mcp.Description("CEL expression (optional). If omitted, returns all resources of the given kind.")),
	), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		cluster, _ := args["cluster"].(string)
		kind, _ := args["kind"].(string)
		ns, _ := args["namespace"].(string)
		expr, _ := args["expr"].(string)
		var result []index.ResourceMeta
		for _, st := range resolveStates(cluster, states) {
			res, err := st.idx.Query(kind, ns, expr)
			if err != nil {
				return mcp.NewToolResultText("error: " + err.Error()), nil
			}
			result = append(result, res...)
		}
		return mcp.NewToolResultText(mustJSON(result)), nil
	})

	return mcpserver.NewStreamableHTTPServer(s)
}

func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
