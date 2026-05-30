package churn

import (
	"fmt"
	"log/slog"
	"math"
	"os/exec"
	"strings"
)

const commitMarker = "---KORPUS-COMMIT---"

// Analyze inspects the last n commits in repoPath and warns about high-churn resources.
// A resource is flagged if it appears in at least threshold fraction of the inspected commits.
func Analyze(repoPath string, n int, subDir string, threshold float64, logger *slog.Logger) error {
	out, err := exec.Command("git", "-C", repoPath, "log",
		fmt.Sprintf("-n%d", n),
		"--pretty=format:"+commitMarker,
		"--name-only",
	).Output()
	if err != nil {
		return fmt.Errorf("git log: %w", err)
	}

	commitBlocks := strings.Split(string(out), commitMarker)
	// The first element before the first marker is empty; skip it.
	var commits [][]string
	for _, block := range commitBlocks {
		var files []string
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			files = append(files, line)
		}
		if len(files) > 0 {
			commits = append(commits, files)
		}
	}

	total := len(commits)
	if total == 0 {
		return nil
	}

	// Count how many commits changed each resource.
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

	minCount := int(math.Ceil(float64(total) * threshold))
	for res, count := range counts {
		if count >= minCount {
			logger.Warn("high-churn resource detected",
				"resource", res,
				"changed", fmt.Sprintf("%d/%d", count, total),
				"hint", "consider adding to config.yaml excludes",
			)
		}
	}
	return nil
}

// extractResource derives a resource identifier from a file path.
// Expected forms:
//
//	<subDir>/cluster-wide/<resource>.yaml   → <resource>
//	<subDir>/namespaced/<ns>/<resource>.yaml → <resource>
func extractResource(path, subDir string) string {
	rel := strings.TrimPrefix(path, subDir+"/")
	if rel == path {
		return "" // not under subDir
	}
	parts := strings.SplitN(rel, "/", 3)
	switch parts[0] {
	case "cluster-wide":
		if len(parts) >= 2 {
			return strings.TrimSuffix(parts[1], ".yaml")
		}
	case "namespaced":
		if len(parts) == 3 {
			return strings.TrimSuffix(parts[2], ".yaml")
		}
	}
	return ""
}
