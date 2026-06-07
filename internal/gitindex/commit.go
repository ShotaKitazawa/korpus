package gitindex

import (
	"sort"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// CommitRef is a (time, sha) pair used for binary search by datetime.
type CommitRef struct {
	Time time.Time
	SHA  string
}

// CommitIndex is a sorted slice of CommitRef enabling O(log n) datetime lookup.
type CommitIndex struct {
	refs []CommitRef // sorted ascending by Time
}

// BuildCommitIndex walks the repository log and builds a sorted CommitIndex.
func BuildCommitIndex(repo *git.Repository) (*CommitIndex, error) {
	iter, err := repo.Log(&git.LogOptions{Order: git.LogOrderCommitterTime})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var refs []CommitRef
	err = iter.ForEach(func(c *object.Commit) error {
		refs = append(refs, CommitRef{
			Time: c.Author.When.UTC(),
			SHA:  c.Hash.String(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// repo.Log returns newest-first; reverse to oldest-first so SliceStable
	// preserves topological order for equal timestamps — the newest commit among
	// same-second entries ends up at the highest index, which is what FindBefore needs.
	for i, j := 0, len(refs)-1; i < j; i, j = i+1, j-1 {
		refs[i], refs[j] = refs[j], refs[i]
	}
	sort.SliceStable(refs, func(i, j int) bool {
		return refs[i].Time.Before(refs[j].Time)
	})
	return &CommitIndex{refs: refs}, nil
}

// FindBefore returns the latest CommitRef whose Time is <= t.
// Returns false when no such commit exists.
func (ci *CommitIndex) FindBefore(t time.Time) (CommitRef, bool) {
	refs := ci.refs
	lo, hi := 0, len(refs)-1
	result := -1
	for lo <= hi {
		mid := (lo + hi) / 2
		if !refs[mid].Time.After(t) {
			result = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if result == -1 {
		return CommitRef{}, false
	}
	return refs[result], true
}

// All returns a copy of all CommitRefs in ascending time order.
func (ci *CommitIndex) All() []CommitRef {
	out := make([]CommitRef, len(ci.refs))
	copy(out, ci.refs)
	return out
}
