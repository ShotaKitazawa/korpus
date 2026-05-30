package churn

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractResource(t *testing.T) {
	cases := []struct {
		path   string
		subDir string
		want   string
	}{
		{"cluster/apps/v1/deployments/foo.yaml", "cluster", "apps/deployments"},
		{"cluster/core/v1/namespaces/default/pods/bar.yaml", "cluster", "core/pods"},
		{"cluster/core/v1/namespaces/kube-system/configmaps/cm.yaml", "cluster", "core/configmaps"},
		{"cluster/core/v1/nodes/node-1.yaml", "cluster", "core/nodes"},
		{"other/core/v1/nodes/node-1.yaml", "cluster", ""},              // wrong subDir
		{"cluster/apps/v1/foo.yaml", "cluster", ""},                      // too few parts (3)
		{"cluster/apps/v1/namespaces/default/pods.yaml", "cluster", ""},  // 5 parts, missing name
		{"cluster/apps/v1/cluster/default/pods/foo.yaml", "cluster", ""}, // 6 parts, not namespaces
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, extractResource(tc.path, tc.subDir), tc.path)
	}
}

func TestAnalyze_HighChurn(t *testing.T) {
	subDir := "backup"
	// 3 commits all touching the same deployment → should warn
	commitFiles := [][]string{
		{subDir + "/apps/v1/deployments/my-deploy.yaml"},
		{subDir + "/apps/v1/deployments/my-deploy.yaml"},
		{subDir + "/apps/v1/deployments/my-deploy.yaml"},
	}

	var warned []string
	logger := slog.New(&captureHandler{fn: func(_ string, attrs []slog.Attr) {
		for _, a := range attrs {
			if a.Key == "resource" {
				warned = append(warned, a.Value.String())
			}
		}
	}})

	dir := makeTestRepo(t, subDir, commitFiles)
	require.NoError(t, Analyze(dir, 3, subDir, 1.0, logger))
	assert.Contains(t, warned, "apps/deployments")
}

func TestAnalyze_NoChurn(t *testing.T) {
	subDir := "backup"
	// Each commit touches a different resource
	commitFiles := [][]string{
		{subDir + "/apps/v1/deployments/deploy-a.yaml"},
		{subDir + "/core/v1/pods/pod-a.yaml"},
		{subDir + "/core/v1/nodes/node-1.yaml"},
	}

	var warned []string
	logger := slog.New(&captureHandler{fn: func(_ string, attrs []slog.Attr) {
		for _, a := range attrs {
			if a.Key == "resource" {
				warned = append(warned, a.Value.String())
			}
		}
	}})

	dir := makeTestRepo(t, subDir, commitFiles)
	require.NoError(t, Analyze(dir, 3, subDir, 1.0, logger))
	assert.Empty(t, warned)
}

func TestAnalyze_PartialThreshold(t *testing.T) {
	subDir := "backup"
	// 3 commits, deployments appears in 2 of 3 → flagged at threshold=0.5, not at 1.0
	commitFiles := [][]string{
		{subDir + "/apps/v1/deployments/deploy-a.yaml"},
		{subDir + "/apps/v1/deployments/deploy-a.yaml"},
		{subDir + "/core/v1/pods/pod-a.yaml"},
	}

	warned := func(threshold float64) []string {
		var out []string
		logger := slog.New(&captureHandler{fn: func(_ string, attrs []slog.Attr) {
			for _, a := range attrs {
				if a.Key == "resource" {
					out = append(out, a.Value.String())
				}
			}
		}})
		dir := makeTestRepo(t, subDir, commitFiles)
		require.NoError(t, Analyze(dir, 3, subDir, threshold, logger))
		return out
	}

	assert.Contains(t, warned(0.5), "apps/deployments")
	assert.Empty(t, warned(1.0))
}

// makeTestRepo creates a git repo where each commitFiles[i] is staged and committed.
func makeTestRepo(t *testing.T, subDir string, commitFiles [][]string) string {
	t.Helper()
	dir := t.TempDir()
	for _, c := range [][]string{
		{"git", "init", dir},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
	} {
		require.NoError(t, exec.Command(c[0], c[1:]...).Run())
	}
	for i, files := range commitFiles {
		for _, f := range files {
			full := filepath.Join(dir, f)
			require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
			require.NoError(t, os.WriteFile(full, []byte{byte('a' + i)}, 0o644))
		}
		require.NoError(t, exec.Command("git", "-C", dir, "add", ".").Run())
		require.NoError(t, exec.Command("git", "-C", dir, "commit", "-m", "backup").Run())
	}
	return dir
}

// captureHandler is a minimal slog.Handler for test assertions.
type captureHandler struct {
	fn func(msg string, attrs []slog.Attr)
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	var attrs []slog.Attr
	r.Attrs(func(a slog.Attr) bool { attrs = append(attrs, a); return true })
	h.fn(r.Message, attrs)
	return nil
}
func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler      { return h }
