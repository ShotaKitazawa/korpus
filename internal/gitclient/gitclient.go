package gitclient

import (
	"context"
	"fmt"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

// Client wraps a go-git repository.
type Client struct {
	repo  *git.Repository
	token string
}

// Clone performs a shallow clone (depth=1) into dir.
func Clone(ctx context.Context, repoURL, branch, token, dir string) (*Client, error) {
	opts := &git.CloneOptions{
		URL:           repoURL,
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		Depth:         1,
		SingleBranch:  true,
	}
	if token != "" {
		opts.Auth = &http.BasicAuth{Username: "x-token", Password: token}
	}
	repo, err := git.PlainCloneContext(ctx, dir, false, opts)
	if err != nil {
		return nil, fmt.Errorf("git clone: %w", err)
	}
	return &Client{repo: repo, token: token}, nil
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
	if c.token != "" {
		opts.Auth = &http.BasicAuth{Username: "x-token", Password: c.token}
	}
	err = wt.Pull(opts)
	if err == git.NoErrAlreadyUpToDate {
		return nil
	}
	return err
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
	if c.token != "" {
		pushOpts.Auth = &http.BasicAuth{Username: "x-token", Password: c.token}
	}
	if err := c.repo.Push(pushOpts); err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("git push: %w", err)
	}
	return nil
}
