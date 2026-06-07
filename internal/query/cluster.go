package query

import (
	git "github.com/go-git/go-git/v5"

	"github.com/ShotaKitazawa/korpus/internal/gitindex"
	"github.com/ShotaKitazawa/korpus/internal/index"
)

// ClusterQuerier is the read interface over a single cluster's git-backed state.
// cmd/server.ClusterState implements this via accessor methods.
type ClusterQuerier interface {
	Index() *index.Index
	CommitIndex() *gitindex.CommitIndex
	ChangeIndex() *gitindex.ChangeIndex
	FileAt(relPath, sha string) (string, error)
	RelPath(absPath string) string
	GitRepo() *git.Repository
	SubDir() string
	WorkDir() string
}
