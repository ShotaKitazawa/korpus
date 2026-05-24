package gitclient

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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

	client, err := Clone(context.Background(), "file://"+bareDir, "main", "", cloneDir)
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.FileExists(t, filepath.Join(cloneDir, "README.md"))
}

func TestIsClean_Clean(t *testing.T) {
	bareDir := setupBareRepo(t)
	cloneDir := t.TempDir()
	client, err := Clone(context.Background(), "file://"+bareDir, "main", "", cloneDir)
	require.NoError(t, err)

	clean, err := client.IsClean()
	require.NoError(t, err)
	assert.True(t, clean)
}

func TestIsClean_Dirty(t *testing.T) {
	bareDir := setupBareRepo(t)
	cloneDir := t.TempDir()
	client, err := Clone(context.Background(), "file://"+bareDir, "main", "", cloneDir)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(cloneDir, "new.yaml"), []byte("data: 1"), 0o644))

	clean, err := client.IsClean()
	require.NoError(t, err)
	assert.False(t, clean)
}

func TestCommitAndPush(t *testing.T) {
	bareDir := setupBareRepo(t)
	cloneDir := t.TempDir()
	client, err := Clone(context.Background(), "file://"+bareDir, "main", "", cloneDir)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(cloneDir, "backup.yaml"), []byte("data: 1"), 0o644))

	err = client.CommitAndPush("bot", "bot@test.com", "backup: 2024-01-01T00:00:00Z")
	require.NoError(t, err)

	clean, err := client.IsClean()
	require.NoError(t, err)
	assert.True(t, clean)
}
