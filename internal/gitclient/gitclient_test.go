package gitclient

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupBareRepo creates a local bare git repo with one initial commit.
func setupBareRepo(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()

	bareDir := filepath.Join(tmpDir, "remote.git")
	require.NoError(t, exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run())

	// Create a temporary working dir to push the initial commit
	workDir := filepath.Join(tmpDir, "init-work")
	cmds := [][]string{
		{"git", "clone", "file://" + bareDir, workDir},
		{"git", "-C", workDir, "config", "user.email", "test@test.com"},
		{"git", "-C", workDir, "config", "user.name", "Test"},
	}
	for _, c := range cmds {
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

func TestClone(t *testing.T) {
	bareDir := setupBareRepo(t)
	cloneDir := t.TempDir()

	client, err := Clone(context.Background(), "file://"+bareDir, "main", "", "", cloneDir, 1)
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.FileExists(t, filepath.Join(cloneDir, "README.md"))
}

func TestIsClean_Clean(t *testing.T) {
	bareDir := setupBareRepo(t)
	cloneDir := t.TempDir()
	client, err := Clone(context.Background(), "file://"+bareDir, "main", "", "", cloneDir, 1)
	require.NoError(t, err)

	clean, err := client.IsClean()
	require.NoError(t, err)
	assert.True(t, clean)
}

func TestIsClean_Dirty(t *testing.T) {
	bareDir := setupBareRepo(t)
	cloneDir := t.TempDir()
	client, err := Clone(context.Background(), "file://"+bareDir, "main", "", "", cloneDir, 1)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(cloneDir, "new.yaml"), []byte("data: 1"), 0o644))

	clean, err := client.IsClean()
	require.NoError(t, err)
	assert.False(t, clean)
}

func TestCommitAndPush(t *testing.T) {
	bareDir := setupBareRepo(t)
	cloneDir := t.TempDir()
	client, err := Clone(context.Background(), "file://"+bareDir, "main", "", "", cloneDir, 1)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(cloneDir, "backup.yaml"), []byte("data: 1"), 0o644))

	err = client.CommitAndPush("bot", "bot@test.com", "backup: 2024-01-01T00:00:00Z")
	require.NoError(t, err)

	clean, err := client.IsClean()
	require.NoError(t, err)
	assert.True(t, clean)
}

func TestCommitAndPush_EmptyRemote(t *testing.T) {
	tmpDir := t.TempDir()
	bareDir := filepath.Join(tmpDir, "remote.git")
	require.NoError(t, exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run())

	cloneDir := t.TempDir()
	client, err := Clone(context.Background(), "file://"+bareDir, "main", "", "", cloneDir, 1)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(cloneDir, "backup.yaml"), []byte("data: 1"), 0o644))
	require.NoError(t, client.CommitAndPush("bot", "bot@test.com", "initial backup"))

	// Verify the commit reached the bare repo on the expected branch.
	out, err := exec.Command("git", "-C", bareDir, "log", "--oneline", "main").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "initial backup")
}

func setupRepoWithHistory(t *testing.T) (string, *Client) {
	t.Helper()
	bareDir := setupBareRepo(t)
	cloneDir := t.TempDir()

	// full clone (depth=0) to allow history traversal
	client, err := Clone(context.Background(), "file://"+bareDir, "main", "", "", cloneDir, 0)
	require.NoError(t, err)

	// commit 1 — create the file
	require.NoError(t, os.WriteFile(filepath.Join(cloneDir, "pods.yaml"), []byte("v1"), 0o644))
	require.NoError(t, client.CommitAndPush("bot", "bot@test.com", "backup: v1"))

	// commit 2 — update the file
	require.NoError(t, os.WriteFile(filepath.Join(cloneDir, "pods.yaml"), []byte("v2"), 0o644))
	require.NoError(t, client.CommitAndPush("bot", "bot@test.com", "backup: v2"))

	return cloneDir, client
}

func TestRepo(t *testing.T) {
	_, client := setupRepoWithHistory(t)
	assert.NotNil(t, client.Repo())
}

func TestLoadToken(t *testing.T) {
	t.Run("static token", func(t *testing.T) {
		c := &Client{token: "static"}
		assert.Equal(t, "static", c.loadToken())
	})

	t.Run("tokenFile overrides token", func(t *testing.T) {
		f, err := os.CreateTemp("", "token-*")
		require.NoError(t, err)
		t.Cleanup(func() { os.Remove(f.Name()) })
		_, err = f.WriteString("file-token\n")
		require.NoError(t, err)
		require.NoError(t, f.Close())

		c := &Client{token: "static", tokenFile: f.Name()}
		assert.Equal(t, "file-token", c.loadToken())
	})

	t.Run("tokenFile updated between calls", func(t *testing.T) {
		f, err := os.CreateTemp("", "token-*")
		require.NoError(t, err)
		t.Cleanup(func() { os.Remove(f.Name()) })
		_, err = f.WriteString("token-v1")
		require.NoError(t, err)
		require.NoError(t, f.Close())

		c := &Client{tokenFile: f.Name()}
		assert.Equal(t, "token-v1", c.loadToken())

		require.NoError(t, os.WriteFile(f.Name(), []byte("token-v2"), 0o600))
		assert.Equal(t, "token-v2", c.loadToken())
	})

	t.Run("tokenFile missing falls back to token", func(t *testing.T) {
		c := &Client{token: "fallback", tokenFile: "/nonexistent/token"}
		assert.Equal(t, "fallback", c.loadToken())
	})
}

func TestFileAtCommit(t *testing.T) {
	_, client := setupRepoWithHistory(t)

	// Collect commit SHAs via repo log (newest first).
	iter, err := client.Repo().Log(&git.LogOptions{Order: git.LogOrderCommitterTime})
	require.NoError(t, err)
	defer iter.Close()
	var shas []string
	for {
		c, err := iter.Next()
		if err != nil {
			break
		}
		shas = append(shas, c.Hash.String())
	}
	require.GreaterOrEqual(t, len(shas), 2, "expected at least 2 commits")

	// shas[0] = newest (v2), shas[1] = previous (v1)
	content, err := client.FileAtCommit("pods.yaml", shas[0])
	require.NoError(t, err)
	assert.Equal(t, "v2", content)

	content, err = client.FileAtCommit("pods.yaml", shas[1])
	require.NoError(t, err)
	assert.Equal(t, "v1", content)
}
