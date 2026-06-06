package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ShotaKitazawa/korpus/internal/config"
	"github.com/ShotaKitazawa/korpus/internal/gitclient"
	"github.com/ShotaKitazawa/korpus/internal/index"
)

func writeFixtureYAMLs(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pod-default.yaml"), []byte(`kind: Pod
metadata:
  name: my-pod
  namespace: default
  labels:
    app: my-app
spec:
  dnsPolicy: ClusterFirst
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "deploy-kube-system.yaml"), []byte(`kind: Deployment
metadata:
  name: coredns
  namespace: kube-system
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "node.yaml"), []byte(`kind: Node
metadata:
  name: node-1
`), 0o644))
}

func buildTestState(t *testing.T) *ClusterState {
	t.Helper()
	dir := t.TempDir()
	writeFixtureYAMLs(t, dir)
	idx := index.New("test-cluster", []string{"metadata.labels"})
	require.NoError(t, idx.Build(dir))
	state := &ClusterState{idx: idx}
	state.recordPull(nil)
	return state
}

func buildTestServer(t *testing.T, states map[string]*ClusterState) *httptest.Server {
	t.Helper()
	cfg := &config.ServerConfig{
		Spec: config.ServerSpec{
			Clusters: []config.ClusterConfig{{Name: "test-cluster"}},
		},
	}
	ts := httptest.NewServer(buildMux(t.Context(), cfg, states, slog.Default(), nil))
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

	podV1 := `kind: Pod
metadata:
  name: my-pod
  namespace: default
  labels:
    app: my-app
`
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "pod-default.yaml"), []byte(podV1), 0o644))
	require.NoError(t, client.CommitAndPush("bot", "bot@test.com", "backup: v1"))

	podV2 := podV1 + "spec:\n  dnsPolicy: ClusterFirst\n"
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "pod-default.yaml"), []byte(podV2), 0o644))
	require.NoError(t, client.CommitAndPush("bot", "bot@test.com", "backup: v2"))

	idx := index.New("test-cluster", []string{"metadata.labels"})
	require.NoError(t, idx.Build(workDir))

	state := &ClusterState{idx: idx}
	state.setGit(client, workDir)
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

func TestKinds_All(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/api/kinds")
	require.NoError(t, err)
	defer resp.Body.Close()

	var kinds []string
	decodeJSON(t, resp, &kinds)
	assert.Equal(t, []string{"Deployment", "Node", "Pod"}, kinds)
}

func TestKinds_ByNamespace(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/api/kinds?namespace=default")
	require.NoError(t, err)
	defer resp.Body.Close()

	var kinds []string
	decodeJSON(t, resp, &kinds)
	assert.Equal(t, []string{"Pod"}, kinds)
}

type resourcePage struct {
	Items  []index.ResourceMeta `json:"items"`
	Total  int                  `json:"total"`
	Offset int                  `json:"offset"`
	Limit  int                  `json:"limit"`
}

func TestResources_All(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/api/resources")
	require.NoError(t, err)
	defer resp.Body.Close()

	var page resourcePage
	decodeJSON(t, resp, &page)
	assert.Equal(t, 3, page.Total)
	assert.Len(t, page.Items, 3)
}

func TestResources_Pagination(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/api/resources?offset=0&limit=2")
	require.NoError(t, err)
	defer resp.Body.Close()

	var page resourcePage
	decodeJSON(t, resp, &page)
	assert.Equal(t, 3, page.Total)
	assert.Len(t, page.Items, 2)
	assert.Equal(t, 0, page.Offset)
	assert.Equal(t, 2, page.Limit)
}

func TestResources_ByKind(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/api/resources?kind=Pod")
	require.NoError(t, err)
	defer resp.Body.Close()

	var page resourcePage
	decodeJSON(t, resp, &page)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "my-pod", page.Items[0].Name)
}

func TestResources_ByLabel(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/api/resources?labels=app=my-app")
	require.NoError(t, err)
	defer resp.Body.Close()

	var page resourcePage
	decodeJSON(t, resp, &page)
	assert.Len(t, page.Items, 1)
}

func TestResources_LabelKeyOnly(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/api/resources?labels=app")
	require.NoError(t, err)
	defer resp.Body.Close()

	var page resourcePage
	decodeJSON(t, resp, &page)
	assert.Len(t, page.Items, 1)
}

func TestResourceDetail(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/api/resources/test-cluster/Pod/default/my-pod")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/plain")
}

func TestResourceDetail_NotFound(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	resp, err := http.Get(ts.URL + "/api/resources/test-cluster/Pod/default/nonexistent")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestQuery_CEL(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	q := url.QueryEscape(`object.metadata.labels["app"]=="my-app"`)
	resp, err := http.Get(ts.URL + "/api/query?kind=Pod&q=" + q)
	require.NoError(t, err)
	defer resp.Body.Close()

	var page resourcePage
	decodeJSON(t, resp, &page)
	assert.Len(t, page.Items, 1)
}

func TestQuery_KindRequired(t *testing.T) {
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": buildTestState(t)})

	q := url.QueryEscape(`object.metadata.name=="my-pod"`)
	resp, err := http.Get(ts.URL + "/api/query?q=" + q)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
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

	resp, err := http.Get(ts.URL + "/api/resources/test-cluster/Pod/default/my-pod/history")
	require.NoError(t, err)
	defer resp.Body.Close()

	var entries []struct {
		SHA     string `json:"sha"`
		Message string `json:"message"`
	}
	decodeJSON(t, resp, &entries)
	require.Len(t, entries, 2)
	assert.NotEmpty(t, entries[0].SHA)
	assert.NotEmpty(t, entries[0].Message)
}

func TestDiff(t *testing.T) {
	state := buildTestStateWithGit(t)
	ts := buildTestServer(t, map[string]*ClusterState{"test-cluster": state})

	histResp, err := http.Get(ts.URL + "/api/resources/test-cluster/Pod/default/my-pod/history")
	require.NoError(t, err)
	var entries []struct {
		SHA string `json:"sha"`
	}
	decodeJSON(t, histResp, &entries)
	histResp.Body.Close()
	require.Len(t, entries, 2)
	// entries[0]=v2 (latest), entries[1]=v1 (older)
	from, to := entries[1].SHA, entries[0].SHA

	diffResp, err := http.Get(ts.URL + "/api/resources/test-cluster/Pod/default/my-pod/diff?from=" + from + "&to=" + to)
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
