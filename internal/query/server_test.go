package query_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ShotaKitazawa/korpus/internal/gitclient"
	"github.com/ShotaKitazawa/korpus/internal/gitindex"
	"github.com/ShotaKitazawa/korpus/internal/index"
	"github.com/ShotaKitazawa/korpus/internal/query"
)

// stubCluster implements query.ClusterQuerier for testing.
type stubCluster struct {
	idx       *index.Index
	commitIdx *gitindex.CommitIndex
	changeIdx *gitindex.ChangeIndex
	repo      *git.Repository
	subDir    string
	workDir   string
	gc        *gitclient.Client
}

func (s *stubCluster) Index() *index.Index                { return s.idx }
func (s *stubCluster) CommitIndex() *gitindex.CommitIndex { return s.commitIdx }
func (s *stubCluster) ChangeIndex() *gitindex.ChangeIndex { return s.changeIdx }
func (s *stubCluster) SubDir() string                     { return s.subDir }
func (s *stubCluster) WorkDir() string                    { return s.workDir }
func (s *stubCluster) GitRepo() *git.Repository           { return s.repo }

func (s *stubCluster) RelPath(absPath string) string {
	if s.workDir == "" {
		return ""
	}
	rel := absPath
	prefix := s.workDir + string(filepath.Separator)
	if len(absPath) > len(prefix) && absPath[:len(prefix)] == prefix {
		rel = filepath.ToSlash(absPath[len(prefix):])
	}
	if rel == absPath {
		return ""
	}
	return rel
}

func (s *stubCluster) FileAt(relPath, sha string) (string, error) {
	return s.gc.FileAtCommit(relPath, sha)
}

// writeFixtures creates YAML files in the backup-daemon directory structure under dir.
func writeFixtures(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"core/v1/namespaces/default/pods/my-pod.yaml": `apiVersion: v1
kind: Pod
metadata:
  name: my-pod
  namespace: default
  labels:
    app: my-app
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
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
}

// buildIndexOnly builds an index-only stub (no git).
func buildIndexOnly(t *testing.T, clusterName string) *stubCluster {
	t.Helper()
	dir := t.TempDir()
	writeFixtures(t, dir)
	idx := index.New(clusterName, []string{"metadata.labels"})
	require.NoError(t, idx.Build(dir))
	return &stubCluster{idx: idx}
}

// setupBareRepo initialises a bare git repo with an initial commit and returns the path.
func setupBareRepo(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "remote.git")
	require.NoError(t, exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run())
	workDir := filepath.Join(tmp, "init-work")
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

// buildGitStub builds a stub backed by a real git repo with two commits.
func buildGitStub(t *testing.T, clusterName string) *stubCluster {
	t.Helper()
	bareDir := setupBareRepo(t)
	workDir := t.TempDir()

	gc, err := gitclient.Clone(context.Background(), "file://"+bareDir, "main", "", "", workDir, 0)
	require.NoError(t, err)

	// Commit v1.
	podPath := filepath.Join(workDir, "core", "v1", "namespaces", "default", "pods")
	require.NoError(t, os.MkdirAll(podPath, 0o755))
	podV1 := "apiVersion: v1\nkind: Pod\nmetadata:\n  name: my-pod\n  namespace: default\n"
	require.NoError(t, os.WriteFile(filepath.Join(podPath, "my-pod.yaml"), []byte(podV1), 0o644))
	require.NoError(t, gc.CommitAndPush("bot", "bot@test.com", "backup: v1"))

	// Commit v2.
	podV2 := podV1 + "spec:\n  dnsPolicy: ClusterFirst\n"
	require.NoError(t, os.WriteFile(filepath.Join(podPath, "my-pod.yaml"), []byte(podV2), 0o644))
	require.NoError(t, gc.CommitAndPush("bot", "bot@test.com", "backup: v2"))

	idx := index.New(clusterName, nil)
	require.NoError(t, idx.Build(workDir))

	commitIdx, err := gitindex.BuildCommitIndex(gc.Repo())
	require.NoError(t, err)
	changeIdx, err := gitindex.BuildChangeIndex(gc.Repo(), workDir, clusterName, "", 30)
	require.NoError(t, err)

	return &stubCluster{
		idx:       idx,
		commitIdx: commitIdx,
		changeIdx: changeIdx,
		repo:      gc.Repo(),
		workDir:   workDir,
		gc:        gc,
	}
}

func newServer(clusters map[string]query.ClusterQuerier, names []string) *query.Server {
	return query.New(clusters, names)
}

// --- ListClusters ---

func TestListClusters(t *testing.T) {
	q := newServer(map[string]query.ClusterQuerier{
		"a": buildIndexOnly(t, "a"),
		"b": buildIndexOnly(t, "b"),
	}, []string{"a", "b"})
	assert.Equal(t, []string{"a", "b"}, q.ListClusters())
}

// --- ListGVKs ---

func TestListGVKs_Sorted(t *testing.T) {
	q := newServer(map[string]query.ClusterQuerier{
		"c": buildIndexOnly(t, "c"),
	}, []string{"c"})

	gvks, err := q.ListGVKs("", "")
	require.NoError(t, err)
	require.Len(t, gvks, 3)
	// Should be sorted: apps before core (by group), Node < Pod within core.
	assert.Equal(t, "apps", gvks[0].Group)
	assert.Equal(t, "core", gvks[1].Group)
	assert.Equal(t, "core", gvks[2].Group)
	// Within core: Node before Pod alphabetically.
	assert.True(t, gvks[1].Kind < gvks[2].Kind)
}

func TestListGVKs_FilterByNamespace(t *testing.T) {
	q := newServer(map[string]query.ClusterQuerier{
		"c": buildIndexOnly(t, "c"),
	}, []string{"c"})

	gvks, err := q.ListGVKs("", "default")
	require.NoError(t, err)
	require.Len(t, gvks, 1)
	assert.Equal(t, "Pod", gvks[0].Kind)
}

func TestListGVKs_DeduplicatedAcrossClusters(t *testing.T) {
	q := newServer(map[string]query.ClusterQuerier{
		"a": buildIndexOnly(t, "a"),
		"b": buildIndexOnly(t, "b"),
	}, []string{"a", "b"})

	gvks, err := q.ListGVKs("", "")
	require.NoError(t, err)
	// Both clusters have the same GVKs — should be deduplicated.
	assert.Len(t, gvks, 3)
}

// --- ListNamespaces ---

func TestListNamespaces_Sorted(t *testing.T) {
	q := newServer(map[string]query.ClusterQuerier{
		"c": buildIndexOnly(t, "c"),
	}, []string{"c"})

	nss, err := q.ListNamespaces("")
	require.NoError(t, err)
	assert.Equal(t, []string{"default", "kube-system"}, nss)
}

// --- GetResource ---

func TestGetResource_Found(t *testing.T) {
	q := newServer(map[string]query.ClusterQuerier{
		"c": buildIndexOnly(t, "c"),
	}, []string{"c"})

	data, err := q.GetResource("c", "core", "Pod", "default", "my-pod")
	require.NoError(t, err)
	assert.Contains(t, string(data), "my-pod")
}

func TestGetResource_ClusterNotFound(t *testing.T) {
	q := newServer(map[string]query.ClusterQuerier{}, []string{})
	_, err := q.GetResource("nonexistent", "core", "Pod", "", "x")
	assert.ErrorIs(t, err, query.ErrClusterNotFound)
}

func TestGetResource_NotFound(t *testing.T) {
	q := newServer(map[string]query.ClusterQuerier{
		"c": buildIndexOnly(t, "c"),
	}, []string{"c"})
	_, err := q.GetResource("c", "core", "Pod", "default", "nonexistent")
	assert.ErrorIs(t, err, query.ErrNotFound)
}

// --- GetCurrentSnapshot ---

func TestGetCurrentSnapshot_All(t *testing.T) {
	q := newServer(map[string]query.ClusterQuerier{
		"c": buildIndexOnly(t, "c"),
	}, []string{"c"})

	result, err := q.GetCurrentSnapshot("", "", "", "", "", "", nil, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, result.Total)
	assert.Len(t, result.Items, 3)
}

func TestGetCurrentSnapshot_Pagination(t *testing.T) {
	q := newServer(map[string]query.ClusterQuerier{
		"c": buildIndexOnly(t, "c"),
	}, []string{"c"})

	r1, err := q.GetCurrentSnapshot("", "", "", "", "", "", nil, 2, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, r1.Total)
	assert.Len(t, r1.Items, 2)

	r2, err := q.GetCurrentSnapshot("", "", "", "", "", "", nil, 2, 2)
	require.NoError(t, err)
	assert.Equal(t, 3, r2.Total)
	assert.Len(t, r2.Items, 1)
}

func TestGetCurrentSnapshot_OffsetBeyondTotal(t *testing.T) {
	q := newServer(map[string]query.ClusterQuerier{
		"c": buildIndexOnly(t, "c"),
	}, []string{"c"})

	result, err := q.GetCurrentSnapshot("", "", "", "", "", "", nil, 10, 100)
	require.NoError(t, err)
	assert.Equal(t, 3, result.Total)
	assert.Len(t, result.Items, 0)
}

// --- GetDiff ---

func TestGetDiff_Success(t *testing.T) {
	stub := buildGitStub(t, "c")
	q := newServer(map[string]query.ClusterQuerier{"c": stub}, []string{"c"})

	events, _, err := q.GetHistory("c", "core", "Pod", "default", "my-pod", "", nil, nil, 10, 0)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(events), 2)

	from := events[len(events)-1].SHA
	to := events[0].SHA
	result, err := q.GetDiff("c", "core", "Pod", "default", "my-pod", from, to)
	require.NoError(t, err)
	assert.NotEmpty(t, result.Before)
	assert.NotEmpty(t, result.After)
	assert.NotEqual(t, result.Before, result.After)
}

func TestGetDiff_ClusterNotFound(t *testing.T) {
	q := newServer(map[string]query.ClusterQuerier{}, []string{})
	_, err := q.GetDiff("x", "core", "Pod", "", "y", "sha1", "sha2")
	assert.ErrorIs(t, err, query.ErrClusterNotFound)
}

// --- GetHistory ---

func TestGetHistory_SortedNewestFirst(t *testing.T) {
	stub := buildGitStub(t, "c")
	q := newServer(map[string]query.ClusterQuerier{"c": stub}, []string{"c"})

	events, total, err := q.GetHistory("c", "core", "Pod", "default", "my-pod", "", nil, nil, 50, 0)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, 1)
	for i := 1; i < len(events); i++ {
		assert.True(t, !events[i].Timestamp.After(events[i-1].Timestamp), "events should be newest-first")
	}
}

func TestGetHistory_Pagination(t *testing.T) {
	stub := buildGitStub(t, "c")
	q := newServer(map[string]query.ClusterQuerier{"c": stub}, []string{"c"})

	all, total, err := q.GetHistory("c", "", "", "", "", "", nil, nil, 50, 0)
	require.NoError(t, err)
	require.GreaterOrEqual(t, total, 2)

	page, _, err := q.GetHistory("c", "", "", "", "", "", nil, nil, 1, 0)
	require.NoError(t, err)
	require.Len(t, page, 1)
	assert.Equal(t, all[0].SHA, page[0].SHA)
}

// --- GetVolatility ---

func TestGetVolatility_SortedByRatioDesc(t *testing.T) {
	stub := buildGitStub(t, "c")
	q := newServer(map[string]query.ClusterQuerier{"c": stub}, []string{"c"})

	results, _, err := q.GetVolatility("c", "", "", "", "", 50, 0.0, 50, 0)
	require.NoError(t, err)
	for i := 1; i < len(results); i++ {
		assert.True(t, results[i].Ratio <= results[i-1].Ratio, "should be ratio descending")
	}
}

func TestGetVolatility_ThresholdFilters(t *testing.T) {
	stub := buildGitStub(t, "c")
	q := newServer(map[string]query.ClusterQuerier{"c": stub}, []string{"c"})

	all, _, err := q.GetVolatility("c", "", "", "", "", 50, 0.0, 50, 0)
	require.NoError(t, err)

	high, _, err := q.GetVolatility("c", "", "", "", "", 50, 0.99, 50, 0)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(high), len(all))
}

// --- GetHistoricalSnapshot ---

func TestGetHistoricalSnapshot_CommitMetadata(t *testing.T) {
	stub := buildGitStub(t, "c")
	q := newServer(map[string]query.ClusterQuerier{"c": stub}, []string{"c"})

	result, err := q.GetHistoricalSnapshot(time.Now().Add(time.Hour), "c", "", "", "", "", 50, 0)
	require.NoError(t, err)
	assert.NotEmpty(t, result.CommitSHA)
	assert.False(t, result.CommitTime.IsZero())
	assert.GreaterOrEqual(t, result.Total, 1)
}

// --- GetVolatilityFields ---

func TestGetVolatilityFields_SortedByCountDesc(t *testing.T) {
	stub := buildGitStub(t, "c")
	q := newServer(map[string]query.ClusterQuerier{"c": stub}, []string{"c"})

	results, err := q.GetVolatilityFields("c", "core", "Pod", "", "", 50, 0)
	require.NoError(t, err)
	for i := 1; i < len(results); i++ {
		assert.True(t, results[i].Count <= results[i-1].Count, "should be count descending")
	}
}

func TestGetVolatilityFields_LimitApplied(t *testing.T) {
	stub := buildGitStub(t, "c")
	q := newServer(map[string]query.ClusterQuerier{"c": stub}, []string{"c"})

	all, err := q.GetVolatilityFields("c", "core", "Pod", "", "", 50, 0)
	require.NoError(t, err)
	if len(all) < 2 {
		t.Skip("not enough fields to test limit")
	}

	limited, err := q.GetVolatilityFields("c", "core", "Pod", "", "", 50, 1)
	require.NoError(t, err)
	assert.Len(t, limited, 1)
}
