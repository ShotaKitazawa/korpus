package gitclient

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

// HistoryEntry represents a single commit that touched a file.
type HistoryEntry struct {
	SHA       string    `json:"sha"`
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message"`
}

// Client wraps a go-git repository.
type Client struct {
	repo      *git.Repository
	token     string
	tokenFile string
}

// loadToken returns the effective token: reads tokenFile on each call if set, falls back to token.
func (c *Client) loadToken() string {
	if c.tokenFile == "" {
		return c.token
	}
	data, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return c.token
	}
	return strings.TrimSpace(string(data))
}

// Clone clones repoURL into dir. depth=1 produces a shallow clone; depth=0 fetches full history.
// tokenFile, if non-empty, is read before each git operation to support token rotation.
func Clone(ctx context.Context, repoURL, branch, token, tokenFile, dir string, depth int) (*Client, error) {
	c := &Client{token: token, tokenFile: tokenFile}
	opts := &git.CloneOptions{
		URL:           repoURL,
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		Depth:         depth,
		SingleBranch:  true,
	}
	if tok := c.loadToken(); tok != "" {
		opts.Auth = &http.BasicAuth{Username: "x-token", Password: tok}
	}
	repo, err := git.PlainCloneContext(ctx, dir, false, opts)
	if err != nil {
		return nil, fmt.Errorf("git clone: %w", err)
	}
	c.repo = repo
	return c, nil
}

// IsClean returns true when the working tree has no changes.
func (c *Client) IsClean() (bool, error) {
	wt, err := c.repo.Worktree()
	if err != nil {
		return false, err
	}
	status, err := wt.Status()
	if err != nil {
		return false, err
	}
	return status.IsClean(), nil
}

// Pull fetches and fast-forwards the current branch.
func (c *Client) Pull() error {
	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}
	opts := &git.PullOptions{SingleBranch: true}
	if tok := c.loadToken(); tok != "" {
		opts.Auth = &http.BasicAuth{Username: "x-token", Password: tok}
	}
	err = wt.Pull(opts)
	if err == git.NoErrAlreadyUpToDate {
		return nil
	}
	return err
}

// FileHistory returns up to n commits that touched relPath (relative to the repo root).
func (c *Client) FileHistory(relPath string, n int) ([]HistoryEntry, error) {
	iter, err := c.repo.Log(&git.LogOptions{
		PathFilter: func(p string) bool { return p == relPath },
		Order:      git.LogOrderCommitterTime,
	})
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	defer iter.Close()

	var entries []HistoryEntry
	for len(entries) < n {
		commit, err := iter.Next()
		if err != nil {
			break
		}
		entries = append(entries, HistoryEntry{
			SHA:       commit.Hash.String(),
			Timestamp: commit.Author.When.UTC(),
			Message:   commit.Message,
		})
	}
	return entries, nil
}

// FileAtCommit returns the content of relPath at the given commit SHA.
func (c *Client) FileAtCommit(relPath, sha string) (string, error) {
	commit, err := c.repo.CommitObject(plumbing.NewHash(sha))
	if err != nil {
		return "", fmt.Errorf("resolve commit %s: %w", sha, err)
	}
	f, err := commit.File(relPath)
	if err != nil {
		return "", fmt.Errorf("file %s at %s: %w", relPath, sha, err)
	}
	contents, err := f.Contents()
	if err != nil {
		return "", fmt.Errorf("read contents: %w", err)
	}
	return contents, nil
}

// CommitAndPush stages all changes, creates a commit, and pushes.
func (c *Client) CommitAndPush(name, email, message string) error {
	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}
	if _, err := wt.Add("."); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	_, err = wt.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  name,
			Email: email,
			When:  time.Now().UTC(),
		},
	})
	if err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	pushOpts := &git.PushOptions{}
	if tok := c.loadToken(); tok != "" {
		pushOpts.Auth = &http.BasicAuth{Username: "x-token", Password: tok}
	}
	if err := c.repo.Push(pushOpts); err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("git push: %w", err)
	}
	return nil
}
