package churn

import (
	"fmt"
	"log/slog"
	"math"
	"strings"

	"github.com/go-git/go-git/v5"
)

// Entry represents a resource's churn statistics over an analyzed window.
type Entry struct {
	Resource string
	Count    int
	Total    int
}

// Report collects churn data for the last n commits in repoPath under subDir.
// Returns all resources that appear in at least one commit, along with the
// total number of commits analyzed.
func Report(repoPath string, n int, subDir string) ([]Entry, int, error) {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return nil, 0, fmt.Errorf("open repo: %w", err)
	}
	iter, err := repo.Log(&git.LogOptions{Order: git.LogOrderCommitterTime})
	if err != nil {
		return nil, 0, fmt.Errorf("git log: %w", err)
	}
	defer iter.Close()

	var commits [][]string
	for len(commits) < n {
		commit, err := iter.Next()
		if err != nil {
			break
		}
		stats, err := commit.Stats()
		if err != nil {
			continue
		}
		var files []string
		for _, s := range stats {
			files = append(files, s.Name)
		}
		if len(files) > 0 {
			commits = append(commits, files)
		}
	}

	total := len(commits)
	if total == 0 {
		return nil, 0, nil
	}

	counts := make(map[string]int)
	for _, files := range commits {
		seen := make(map[string]struct{})
		for _, f := range files {
			res := extractResource(f, subDir)
			if res == "" {
				continue
			}
			if _, ok := seen[res]; !ok {
				seen[res] = struct{}{}
				counts[res]++
			}
		}
	}

	entries := make([]Entry, 0, len(counts))
	for res, count := range counts {
		entries = append(entries, Entry{Resource: res, Count: count, Total: total})
	}
	return entries, total, nil
}

// Analyze inspects the last n commits in repoPath and warns about high-churn resources.
// A resource is flagged if it appears in at least threshold fraction of the inspected commits.
func Analyze(repoPath string, n int, subDir string, threshold float64, logger *slog.Logger) error {
	entries, total, err := Report(repoPath, n, subDir)
	if err != nil {
		return err
	}
	if total == 0 {
		return nil
	}
	minCount := int(math.Ceil(float64(total) * threshold))
	for _, e := range entries {
		if e.Count >= minCount {
			logger.Warn("high-churn resource detected",
				"resource", e.Resource,
				"changed", fmt.Sprintf("%d/%d", e.Count, e.Total),
				"hint", "consider adding to config.yaml excludes",
			)
		}
	}
	return nil
}

// extractResource derives a resource identifier from a file path.
// Expected forms:
//
//	<subDir>/<group>/<version>/<resource>/<name>.yaml            → <group>/<resource>
//	<subDir>/<group>/<version>/namespaces/<ns>/<resource>/<name>.yaml → <group>/<resource>
func extractResource(path, subDir string) string {
	rel := strings.TrimPrefix(path, subDir+"/")
	if rel == path {
		return "" // not under subDir
	}
	parts := strings.Split(rel, "/")
	switch len(parts) {
	case 4:
		// <group>/<version>/<resource>/<name>.yaml
		return parts[0] + "/" + parts[2]
	case 6:
		// <group>/<version>/namespaces/<ns>/<resource>/<name>.yaml
		if parts[2] == "namespaces" {
			return parts[0] + "/" + parts[4]
		}
	}
	return ""
}
