package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/robfig/cron/v3"
	k8sdiscovery "k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/ShotaKitazawa/korpus/internal/config"
	"github.com/ShotaKitazawa/korpus/internal/discovery"
	"github.com/ShotaKitazawa/korpus/internal/fetcher"
	"github.com/ShotaKitazawa/korpus/internal/gitclient"
	"github.com/ShotaKitazawa/korpus/internal/sanitizer"
	"github.com/ShotaKitazawa/korpus/internal/store"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type backupMetrics struct {
	runsTotal       *prometheus.CounterVec
	durationSeconds prometheus.Histogram
	lastSuccessTime prometheus.Gauge
}

func newBackupMetrics() *backupMetrics {
	m := &backupMetrics{
		runsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "korpus_backup_runs_total",
			Help: "Total number of backup runs.",
		}, []string{"result"}),
		durationSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "korpus_backup_duration_seconds",
			Help:    "Duration of backup runs in seconds.",
			Buckets: prometheus.DefBuckets,
		}),
		lastSuccessTime: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "korpus_backup_last_success_timestamp_seconds",
			Help: "Unix timestamp of the last successful backup.",
		}),
	}
	prometheus.MustRegister(m.runsTotal, m.durationSeconds, m.lastSuccessTime)
	return m
}

type runner struct {
	cfg       *config.KorpusConfig
	dc        *k8sdiscovery.DiscoveryClient
	dynClient dynamic.Interface
	gc        *gitclient.Client
	workDir   string
	metrics   *backupMetrics
	logger    *slog.Logger
}

func (r *runner) init(ctx context.Context) error {
	dir, err := os.MkdirTemp("", "korpus-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	gc, err := gitclient.Clone(ctx, r.cfg.Spec.Git.Repo, r.cfg.Spec.Git.Branch, r.cfg.Spec.Git.Token, r.cfg.Spec.Git.TokenFile, dir, 1)
	if err != nil {
		os.RemoveAll(dir)
		return fmt.Errorf("git clone: %w", err)
	}
	r.workDir = dir
	r.gc = gc
	return nil
}

func (r *runner) reset(ctx context.Context) error {
	if r.workDir != "" {
		os.RemoveAll(r.workDir)
		r.workDir = ""
		r.gc = nil
	}
	return r.init(ctx)
}

func (r *runner) run() {
	ctx := context.Background()
	start := time.Now()

	if err := r.runOnce(ctx); err != nil {
		r.logger.Error("backup failed", "err", err)
		r.metrics.runsTotal.WithLabelValues("failure").Inc()
	} else {
		r.metrics.runsTotal.WithLabelValues("success").Inc()
		r.metrics.lastSuccessTime.SetToCurrentTime()
	}
	r.metrics.durationSeconds.Observe(time.Since(start).Seconds())
}

func (r *runner) runOnce(ctx context.Context) error {
	if err := r.gc.Pull(); err != nil {
		r.logger.Warn("git pull failed, re-cloning", "err", err)
		if err := r.reset(ctx); err != nil {
			return fmt.Errorf("re-clone after pull failure: %w", err)
		}
	}

	resources, err := discovery.ListPreferredResources(r.dc)
	if err != nil {
		r.logger.Warn("discovery partial error", "err", err)
	}

	subDir := filepath.Join(r.workDir, r.cfg.Spec.Git.SubDir)
	currPaths := make(map[string]struct{})

	for _, gvr := range resources {
		if config.IsExcluded(r.cfg, gvr.Resource, gvr.Group) {
			r.logger.Debug("skipping excluded resource", "resource", gvr.Resource, "group", gvr.Group)
			continue
		}

		items, err := fetcher.ListAll(ctx, r.dynClient, gvr)
		if err != nil {
			r.logger.Warn("list resource", "resource", gvr.Resource, "err", err)
			continue
		}

		apiGroup := gvr.Group
		if apiGroup == "" {
			apiGroup = "core"
		}
		for _, item := range items {
			if config.IsObjectExcluded(r.cfg, gvr.Resource, gvr.Group,
				item.GetNamespace(), item.GetName()) ||
				config.IsBuiltinObjectExcluded(r.cfg, gvr.Resource, gvr.Group,
					item.GetOwnerReferences()) {
				r.logger.Debug("skipping excluded object",
					"resource", gvr.Resource, "namespace", item.GetNamespace(), "name", item.GetName())
				continue
			}
			fields := config.ResolveExcludeFieldsForObject(r.cfg, gvr.Resource, gvr.Group,
				item.GetNamespace(), item.GetName())
			sanitizer.DeleteFields(item.Object, fields)
			name := item.GetName()
			var path string
			if gvr.Namespaced {
				path = filepath.Join(subDir, apiGroup, gvr.Version,
					"namespaces", item.GetNamespace(), gvr.Resource, name+".yaml")
			} else {
				path = filepath.Join(subDir, apiGroup, gvr.Version,
					gvr.Resource, name+".yaml")
			}
			if err := writeSinglePath(path, item); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
			currPaths[path] = struct{}{}
		}
	}

	if err := store.CleanObsolete(subDir, currPaths); err != nil {
		return fmt.Errorf("clean obsolete: %w", err)
	}

	clean, err := r.gc.IsClean()
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if clean {
		r.logger.Info("no changes, skipping push")
		return nil
	}

	msg := "backup: " + time.Now().UTC().Format(time.RFC3339)
	if err := r.gc.CommitAndPush(r.cfg.Spec.Git.Author.Name, r.cfg.Spec.Git.Author.Email, msg); err != nil {
		r.logger.Warn("git commit+push failed, re-cloning", "err", err)
		if resetErr := r.reset(ctx); resetErr != nil {
			return fmt.Errorf("re-clone after push failure: %w", resetErr)
		}
		return fmt.Errorf("git commit+push: %w", err)
	}
	r.logger.Info("backup committed", "message", msg)

	return nil
}

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	addr := flag.String("addr", ":8080", "HTTP server address")
	flag.Parse()

	logger := slog.Default()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	cfg, err := config.LoadKorpus(*configPath)
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	k8sCfg, err := loadK8sConfig()
	if err != nil {
		slog.Error("load k8s config", "err", err)
		os.Exit(1)
	}

	dc, err := k8sdiscovery.NewDiscoveryClientForConfig(k8sCfg)
	if err != nil {
		slog.Error("create discovery client", "err", err)
		os.Exit(1)
	}

	dynClient, err := dynamic.NewForConfig(k8sCfg)
	if err != nil {
		slog.Error("create dynamic client", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("/metrics", promhttp.Handler())
	srv := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server", "err", err)
		}
	}()
	defer srv.Shutdown(context.Background()) //nolint:errcheck

	r := &runner{
		cfg:       cfg,
		dc:        dc,
		dynClient: dynClient,
		metrics:   newBackupMetrics(),
		logger:    logger,
	}
	if err := r.init(ctx); err != nil {
		slog.Error("init runner", "err", err)
		os.Exit(1)
	}
	defer os.RemoveAll(r.workDir)

	c := cron.New()
	if _, err := c.AddFunc(cfg.Spec.Backup.Schedule, r.run); err != nil {
		slog.Error("register cron job", "err", err)
		os.Exit(1)
	}
	c.Start()
	defer c.Stop()

	logger.Info("started", "schedule", cfg.Spec.Backup.Schedule, "addr", *addr)
	<-ctx.Done()
	logger.Info("shutting down")
}

func writeSinglePath(path string, item unstructured.Unstructured) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, err := store.WriteSingle(path, item)
	return err
}

func loadK8sConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, nil).ClientConfig()
}
