package gitclient

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

// Client wraps a go-git repository.
type Client struct {
	repo      *git.Repository
	branch    string
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
// If the remote is empty, a local repo is initialised with the remote configured so that the
// first CommitAndPush will bootstrap the repository.
func Clone(ctx context.Context, repoURL, branch, token, tokenFile, dir string, depth int) (*Client, error) {
	c := &Client{branch: branch, token: token, tokenFile: tokenFile}
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
		if !errors.Is(err, transport.ErrEmptyRemoteRepository) {
			return nil, fmt.Errorf("git clone: %w", err)
		}
		repo, err = git.PlainInit(dir, false)
		if err != nil {
			return nil, fmt.Errorf("git init: %w", err)
		}
		// Point HEAD at the configured branch so the first commit lands on the right branch.
		if err = repo.Storer.SetReference(plumbing.NewSymbolicReference(
			plumbing.HEAD, plumbing.NewBranchReferenceName(branch),
		)); err != nil {
			return nil, fmt.Errorf("set HEAD: %w", err)
		}
		if _, err = repo.CreateRemote(&gitconfig.RemoteConfig{
			Name: "origin",
			URLs: []string{repoURL},
		}); err != nil {
			return nil, fmt.Errorf("create remote: %w", err)
		}
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
	// Nothing to pull if the local repo has no commits yet (bootstrapping empty remote).
	if _, err := c.repo.Head(); errors.Is(err, plumbing.ErrReferenceNotFound) {
		return nil
	}
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

// Repo returns the underlying git.Repository for use by gitindex.
func (c *Client) Repo() *git.Repository { return c.repo }

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

	pushOpts := &git.PushOptions{
		// Use an explicit refspec so that pushing to a freshly-initialised local repo
		// (bootstrapping an empty remote) works without a tracking branch configured.
		RefSpecs: []gitconfig.RefSpec{
			gitconfig.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", c.branch, c.branch)),
		},
	}
	if tok := c.loadToken(); tok != "" {
		pushOpts.Auth = &http.BasicAuth{Username: "x-token", Password: tok}
	}
	if err := c.repo.Push(pushOpts); err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("git push: %w", err)
	}
	return nil
}
