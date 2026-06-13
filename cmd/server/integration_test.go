package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ShotaKitazawa/korpus/internal/config"
	"github.com/ShotaKitazawa/korpus/internal/gitclient"
	"github.com/ShotaKitazawa/korpus/internal/gitindex"
	"github.com/ShotaKitazawa/korpus/internal/index"
)

// writeFixtureDir creates YAML files in the proper backup-daemon directory structure:
//
//	<dir>/core/v1/namespaces/default/pods/my-pod.yaml
//	<dir>/apps/v1/namespaces/kube-system/deployments/coredns.yaml
//	<dir>/core/v1/nodes/node-1.yaml
func writeFixtureDir(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"core/v1/namespaces/default/pods/my-pod.yaml": `apiVersion: v1
kind: Pod
metadata:
  name: my-pod
  namespace: default
  labels:
    app: my-app
spec:
  dnsPolicy: ClusterFirst
`,
		"apps/v1/namespaces/kube-system/deployments/coredns.yaml": `apiVersion: apps/v1
kind: Deployment
metadata:
  name: coredns
  namespace: kube-system
`,
		"core/v1/nodes/node-1.yaml": `apiVersion: v1
kind: Node
metadata:
  name: node-1
`,
	}
	for relPath, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(relPath))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
}

func buildTestState(t *testing.T) *ClusterState {
	t.Helper()
	dir := t.TempDir()
	writeFixtureDir(t, dir)
	idx := index.New("test-cluster", []string{"metadata.labels"})
	require.NoError(t, idx.Build(dir))
	state := &ClusterState{idx: idx, subDir: ""}
	state.recordPull(nil)
	return state
}

func buildTestServer(t *testing.T, states map[string]*ClusterState) *httptest.Server {
	t.Helper()
	clusterNames := make([]string, 0, len(states))
	for name := range states {
		clusterNames = append(clusterNames, name)
	}
	cfg := &config.ServerConfig{
		Spec: config.ServerSpec{
			Clusters: func() []config.ClusterConfig {
				cs := make([]config.ClusterConfig, 0, len(clusterNames))
				for _, n := range clusterNames {
					cs = append(cs, config.ClusterConfig{Name: n})
				}
				return cs
			}(),
		},
	}
	ts := httptest.NewServer(buildMux(context.Background(), cfg, states, clusterNames, nil, nil))
	t.Cleanup(ts.Close)
	return ts
}

func setupBareRepoForTest(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	bareDir := filepath.Join(tmpDir, "remote.git")
	require.NoError(t, exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run())
	workDir := filepath.Join(tmpDir, "init-work")
	for _, c := range [][]string{
		{"git", "clone", "file://" + bareDir, workDir},
		{"git", "-C", workDir, "config", "user.email", "test@test.com"},
		{"git", "-C", workDir, "config", "user.name", "Test"},
	} {
		require.NoError(t, exec.Command(c[0], c[1:]...).Run())
	}
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "README.md"), []byte("init"), 0o644))
	for _, c := range [][]string{
		{"git", "-C", workDir, "add", "."},
		{"git", "-C", workDir, "commit", "-m", "init"},
		{"git", "-C", workDir, "push"},
	} {
		require.NoError(t, exec.Command(c[0], c[1:]...).Run())
	}
	return bareDir
}

func buildTestStateWithGit(t *testing.T) *ClusterState {
	t.Helper()
	bareDir := setupBareRepoForTest(t)
	workDir := t.TempDir()

	client, err := gitclient.Clone(context.Background(), "file://"+bareDir, "main", "", "", workDir, 0)
	require.NoError(t, err)

	// Commit v1 of pod in proper directory structure.
	podPath := filepath.Join(workDir, "core", "v1", "namespaces", "default", "pods")
	require.NoError(t, os.MkdirAll(podPath, 0o755))
	podV1 := `apiVersion: v1
kind: Pod
metadata:
  name: my-pod
  namespace: default
  labels:
    app: my-app
`
	require.NoError(t, os.WriteFile(filepath.Join(podPath, "my-pod.yaml"), []byte(podV1), 0o644))
	require.NoError(t, client.CommitAndPush("bot", "bot@test.com", "backup: v1"))

	// Commit v2 with spec added.
	podV2 := podV1 + "spec:\n  dnsPolicy: ClusterFirst\n"
	require.NoError(t, os.WriteFile(filepath.Join(podPath, "my-pod.yaml"), []byte(podV2), 0o644))
	require.NoError(t, client.CommitAndPush("bot", "bot@test.com", "backup: v2"))

	idx := index.New("test-cluster", []string{"metadata.labels"})
	require.NoError(t, idx.Build(workDir))

	commitIdx, err := gitindex.BuildCommitIndex(client.Repo())
	require.NoError(t, err)
	changeIdx, err := gitindex.BuildChangeIndex(client.Repo(), workDir, "test-cluster", "", 30)
	require.NoError(t, err)

	state := &ClusterState{idx: idx, subDir: ""}
	state.setGit(client, workDir)
	state.mu.Lock()
	state.commitIdx = commitIdx
	state.changeIdx = changeIdx
	state.mu.Unlock()
	state.recordPull(nil)
	return state
}

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(v))
}

func TestHealthz_Ready(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHealthz_NotReady(t *testing.T) {
	dir := t.TempDir()
	idx := index.New("test-cluster", []string{"metadata.labels"})
	require.NoError(t, idx.Build(dir))
	notReady := &ClusterState{idx: idx} // no recordPull → lastPullAt == nil
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": notReady})

	resp, err := http.Get(ts.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestStatus(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/api/status")
	require.NoError(t, err)
	defer resp.Body.Close()

	var result struct {
		Clusters []struct {
			Name          string `json:"name"`
			ResourceCount int    `json:"resourceCount"`
		} `json:"clusters"`
	}
	decodeJSON(t, resp, &result)
	require.Len(t, result.Clusters, 1)
	assert.Equal(t, "test-cluster", result.Clusters[0].Name)
	assert.Equal(t, 3, result.Clusters[0].ResourceCount)
}

func TestClusters(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/api/clusters")
	require.NoError(t, err)
	defer resp.Body.Close()

	var clusters []string
	decodeJSON(t, resp, &clusters)
	assert.Equal(t, []string{"test-cluster"}, clusters)
}

func TestNamespaces(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	for _, path := range []string{"/api/namespaces", "/api/namespaces?cluster=test-cluster"} {
		resp, err := http.Get(ts.URL + path)
		require.NoError(t, err)
		var nss []string
		decodeJSON(t, resp, &nss)
		resp.Body.Close()
		assert.ElementsMatch(t, []string{"default", "kube-system"}, nss, "path=%s", path)
	}
}

func TestGVKs_All(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/api/gvks")
	require.NoError(t, err)
	defer resp.Body.Close()

	var gvks []struct {
		Group   string `json:"group"`
		Version string `json:"version"`
		Kind    string `json:"kind"`
	}
	decodeJSON(t, resp, &gvks)
	require.Len(t, gvks, 3)
}

func TestGVKs_ByNamespace(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/api/gvks?namespace=default")
	require.NoError(t, err)
	defer resp.Body.Close()

	var gvks []struct {
		Group   string `json:"group"`
		Version string `json:"version"`
		Kind    string `json:"kind"`
	}
	decodeJSON(t, resp, &gvks)
	require.Len(t, gvks, 1)
	assert.Equal(t, "Pod", gvks[0].Kind)
	assert.Equal(t, "v1", gvks[0].Version)
}

func TestSnapshot_All(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/api/snapshot")
	require.NoError(t, err)
	defer resp.Body.Close()

	var page struct {
		Items  []any `json:"items"`
		Total  int   `json:"total"`
		Offset int   `json:"offset"`
		Limit  int   `json:"limit"`
	}
	decodeJSON(t, resp, &page)
	assert.Equal(t, 3, page.Total)
	assert.Len(t, page.Items, 3)
}

func TestSnapshot_Pagination(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/api/snapshot?limit=2&offset=0")
	require.NoError(t, err)
	defer resp.Body.Close()

	var page struct {
		Items  []any `json:"items"`
		Total  int   `json:"total"`
		Offset int   `json:"offset"`
		Limit  int   `json:"limit"`
	}
	decodeJSON(t, resp, &page)
	assert.Equal(t, 3, page.Total)
	assert.Len(t, page.Items, 2)
	assert.Equal(t, 2, page.Limit)
}

func TestSnapshot_ByKind(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/api/snapshot?kind=Pod")
	require.NoError(t, err)
	defer resp.Body.Close()

	var page struct {
		Items []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"items"`
		Total int `json:"total"`
	}
	decodeJSON(t, resp, &page)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "my-pod", page.Items[0].Name)
}

func TestSnapshot_CEL_BadRequestWithDatetime(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/api/snapshot?datetime=2024-01-01T00:00:00Z&cel=object.metadata.name==%22x%22")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestResource_Get(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/api/resource?cluster=test-cluster&group=core&kind=Pod&namespace=default&name=my-pod")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/plain")
}

func TestResource_NotFound(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/api/resource?cluster=test-cluster&group=core&kind=Pod&namespace=default&name=nonexistent")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestParseLabelSelector(t *testing.T) {
	tests := []struct {
		input    string
		expected map[string]string
	}{
		{"app=nginx,env=prod", map[string]string{"app": "nginx", "env": "prod"}},
		{"app", map[string]string{"app": ""}},
		{"", nil},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, parseLabelSelector(tt.input), "input=%q", tt.input)
	}
}

func TestHistory(t *testing.T) {
	state := buildTestStateWithGit(t)
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": state})

	resp, err := http.Get(ts.URL + "/api/history?cluster=test-cluster&group=core&kind=Pod&namespace=default&name=my-pod")
	require.NoError(t, err)
	defer resp.Body.Close()

	var page struct {
		Items []struct {
			SHA        string `json:"sha"`
			ChangeType string `json:"changeType"`
		} `json:"items"`
		Total int `json:"total"`
	}
	decodeJSON(t, resp, &page)
	assert.GreaterOrEqual(t, page.Total, 1)
	require.NotEmpty(t, page.Items)
	assert.NotEmpty(t, page.Items[0].SHA)
}

func TestDiff(t *testing.T) {
	state := buildTestStateWithGit(t)
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": state})

	// Get history to obtain commit SHAs.
	histResp, err := http.Get(ts.URL + "/api/history?cluster=test-cluster&group=core&kind=Pod&namespace=default&name=my-pod")
	require.NoError(t, err)
	var histPage struct {
		Items []struct {
			SHA string `json:"sha"`
		} `json:"items"`
	}
	decodeJSON(t, histResp, &histPage)
	histResp.Body.Close()
	require.GreaterOrEqual(t, len(histPage.Items), 2, "need at least 2 events for diff")

	// newest first: items[0]=latest, items[1]=older
	from := histPage.Items[1].SHA
	to := histPage.Items[0].SHA

	diffResp, err := http.Get(ts.URL + "/api/diff?cluster=test-cluster&group=core&kind=Pod&namespace=default&name=my-pod&from=" + from + "&to=" + to)
	require.NoError(t, err)
	defer diffResp.Body.Close()

	var diff struct {
		Before string `json:"before"`
		After  string `json:"after"`
	}
	require.Equal(t, http.StatusOK, diffResp.StatusCode)
	require.NoError(t, json.NewDecoder(diffResp.Body).Decode(&diff))
	assert.NotEmpty(t, diff.Before)
	assert.NotEmpty(t, diff.After)
}
