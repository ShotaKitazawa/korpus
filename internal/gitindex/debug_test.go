package gitindex

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ShotaKitazawa/korpus/internal/gitclient"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestDebugChangeIndex(t *testing.T) {
	tmpDir := t.TempDir()
	bareDir := filepath.Join(tmpDir, "remote.git")
	workDir := filepath.Join(tmpDir, "init-work")
	workDir2 := filepath.Join(tmpDir, "clone")

	exec.Command("git", "init", "--bare", "-b", "main", bareDir).Run()
	exec.Command("git", "clone", "file://"+bareDir, workDir).Run()
	exec.Command("git", "-C", workDir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", workDir, "config", "user.name", "Test").Run()
	os.WriteFile(filepath.Join(workDir, "README.md"), []byte("init"), 0o644)
	exec.Command("git", "-C", workDir, "add", ".").Run()
	exec.Command("git", "-C", workDir, "commit", "-m", "init").Run()
	exec.Command("git", "-C", workDir, "push").Run()

	client, err := gitclient.Clone(context.Background(), "file://"+bareDir, "main", "", "", workDir2, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Check what's in workDir2 before writing
	t.Logf("workDir2: %s", workDir2)
	entries, _ := os.ReadDir(workDir2)
	for _, e := range entries {
		t.Logf("  workDir2 entry: %s (isDir=%v)", e.Name(), e.IsDir())
	}

	podPath := filepath.Join(workDir2, "core", "v1", "namespaces", "default", "pods")
	if err := os.MkdirAll(podPath, 0o755); err != nil {
		t.Fatal("MkdirAll:", err)
	}
	t.Logf("podPath: %s", podPath)

	podV1 := "apiVersion: v1\nkind: Pod\nmetadata:\n  name: my-pod\n  namespace: default\n"
	fullPath := filepath.Join(podPath, "my-pod.yaml")
	if err := os.WriteFile(fullPath, []byte(podV1), 0o644); err != nil {
		t.Fatal("WriteFile:", err)
	}
	t.Logf("wrote file to: %s", fullPath)

	// Check what's in workDir2 after writing
	filepath.WalkDir(workDir2, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(workDir2, path)
		if rel != "." && rel != ".git" && !filepath.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			t.Logf("  file: %s", rel)
		}
		return nil
	})

	if err := client.CommitAndPush("bot", "bot@test.com", "backup: v1"); err != nil {
		t.Fatal("commit v1:", err)
	}

	// Verify commit has the right file paths
	repo := client.Repo()
	iter, err := repo.Log(&gogit.LogOptions{Order: gogit.LogOrderCommitterTime})
	if err != nil {
		t.Fatal("Log:", err)
	}
	defer iter.Close()

	iter.ForEach(func(c *object.Commit) error {
		t.Logf("commit: %s msg=%q parents=%d", c.Hash.String()[:8], c.Message[:min3(20, len(c.Message))], c.NumParents())
		tree, err := c.Tree()
		if err != nil {
			return nil
		}
		tree.Files().ForEach(func(f *object.File) error {
			t.Logf("  tree file: %s", f.Name)
			return nil
		})
		return nil
	})
}

func min3(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestFilepathHasPrefix(t *testing.T) {
	fmt.Println(filepath.HasPrefix("/a/b", "/a"))
}
