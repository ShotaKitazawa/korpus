package gitindex

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
)

// ChangeType describes what happened to a resource in a commit.
type ChangeType string

const (
	Added    ChangeType = "added"
	Modified ChangeType = "modified"
	Deleted  ChangeType = "deleted"
)

// ChangeEvent represents one resource change recorded in the git history.
type ChangeEvent struct {
	Timestamp  time.Time
	SHA        string
	Cluster    string
	Group      string
	Kind       string
	Namespace  string
	Name       string
	ChangeType ChangeType
}

// VolatilityEntry aggregates change counts for a single resource identity.
type VolatilityEntry struct {
	Cluster   string
	Group     string
	Kind      string
	Namespace string
	Name      string
	Count     int
	Total     int // total distinct commits examined
}

// ChangeIndex holds ChangeEvents sorted ascending by Timestamp.
type ChangeIndex struct {
	events []ChangeEvent
}

// kindRef pairs a git cat-file object name with the event that needs its kind.
type kindRef struct {
	objectName string // "<commit-sha>:<filepath>"
	eventIdx   int
}

// BuildChangeIndex walks the git log for up to retentionDays using exec-based git commands
// (avoiding go-git's Tree.Diff which causes Lstat storms on the pack filesystem).
// workDir is the root of the git working tree.
// The repo parameter is accepted for API compatibility but unused.
func BuildChangeIndex(_ *git.Repository, workDir, clusterName, subDir string, retentionDays int) (*ChangeIndex, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)

	args := []string{
		"log",
		"--name-status",
		"--pretty=format:COMMIT %H %aI",
		"--diff-filter=ADM",
		"--no-renames",
		"--since=" + cutoff.Format(time.RFC3339),
	}
	if subDir != "" {
		args = append(args, "--", subDir)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log --name-status: %w", err)
	}

	prefix := subDir
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	var events []ChangeEvent
	var kindRefs []kindRef

	var curSHA string
	var curTime time.Time

	for _, raw := range strings.Split(string(out), "\n") {
		line := strings.TrimRight(raw, "\r")
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "COMMIT ") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			curSHA = fields[1]
			t, _ := time.Parse(time.RFC3339, fields[2])
			curTime = t.UTC()
			continue
		}

		if curSHA == "" || len(line) < 2 || line[1] != '\t' {
			continue
		}
		status := line[0]
		if status != 'A' && status != 'M' && status != 'D' {
			continue
		}
		filePath := line[2:]
		if !strings.HasSuffix(filePath, ".yaml") {
			continue
		}
		if prefix != "" && !strings.HasPrefix(filePath, prefix) {
			continue
		}
		relPath := strings.TrimPrefix(filePath, prefix)
		group, _, namespace, _, name, ok := ParseResourcePath(relPath)
		if !ok {
			continue
		}

		var ct ChangeType
		switch status {
		case 'A':
			ct = Added
		case 'D':
			ct = Deleted
		case 'M':
			ct = Modified
		}

		idx := len(events)
		events = append(events, ChangeEvent{
			Timestamp:  curTime,
			SHA:        curSHA,
			Cluster:    clusterName,
			Group:      group,
			Namespace:  namespace,
			Name:       name,
			ChangeType: ct,
		})
		// Queue kind lookup for Added/Modified (file exists at this commit).
		// Deleted files have Kind="" since they are gone from the commit tree.
		if ct != Deleted {
			kindRefs = append(kindRefs, kindRef{
				objectName: curSHA + ":" + filePath,
				eventIdx:   idx,
			})
		}
	}

	// Batch-read kinds via a single `git cat-file --batch` process.
	kindMap := batchReadKinds(workDir, kindRefs)
	for _, kr := range kindRefs {
		events[kr.eventIdx].Kind = kindMap[kr.objectName]
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.Before(events[j].Timestamp)
	})
	return &ChangeIndex{events: events}, nil
}

// batchReadKinds feeds "<commit>:<path>" object names into git cat-file --batch and
// returns a map from object name to Kubernetes kind. Duplicates are deduplicated.
// Output is streamed so only one file's content is in memory at a time.
func batchReadKinds(workDir string, refs []kindRef) map[string]string {
	if len(refs) == 0 {
		return nil
	}

	// Build deduplicated input; track insertion order to match output positions.
	seen := make(map[string]struct{}, len(refs))
	var sb strings.Builder
	var ordered []string
	for _, r := range refs {
		if _, ok := seen[r.objectName]; ok {
			continue
		}
		seen[r.objectName] = struct{}{}
		sb.WriteString(r.objectName)
		sb.WriteByte('\n')
		ordered = append(ordered, r.objectName)
	}

	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(sb.String())

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	if err := cmd.Start(); err != nil {
		return nil
	}
	defer cmd.Wait() //nolint:errcheck

	// Stream cat-file output: one entry per input object, in order.
	// Each entry: "<sha> blob <size>\n<content>\n"
	// or:         "<name> missing\n"  for unknown objects.
	// Reading one file at a time keeps peak memory at O(max_file_size).
	kindMap := make(map[string]string, len(ordered))
	r := bufio.NewReader(stdout)
	for _, objName := range ordered {
		header, err := r.ReadString('\n')
		if err != nil {
			break
		}
		parts := strings.Fields(strings.TrimRight(header, "\r\n"))
		if len(parts) < 3 || parts[1] != "blob" {
			// "missing" or unexpected type — no content follows.
			continue
		}
		size, _ := strconv.Atoi(parts[2])

		// Read only the first 512 bytes needed for kind extraction, discard the rest.
		const kindWindow = 512
		window := kindWindow
		if size < window {
			window = size
		}
		buf := make([]byte, window)
		if _, err := io.ReadFull(r, buf); err != nil {
			break
		}
		if size > window {
			if _, err := io.CopyN(io.Discard, r, int64(size-window)); err != nil {
				break
			}
		}
		// Consume trailing newline after content.
		if b, err := r.ReadByte(); err == nil && b != '\n' {
			r.UnreadByte() //nolint:errcheck
		}

		kindMap[objName] = kindFromYAML(buf)
	}
	return kindMap
}

// kindFromYAML extracts the Kubernetes kind field from YAML content.
// Only the first 512 bytes are examined since kind is always near the top.
func kindFromYAML(content []byte) string {
	limit := 512
	if len(content) < limit {
		limit = len(content)
	}
	for _, line := range bytes.Split(content[:limit], []byte("\n")) {
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, []byte("kind:")) {
			return string(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("kind:"))))
		}
	}
	return ""
}

// Len returns the number of change events in the index.
func (ci *ChangeIndex) Len() int { return len(ci.events) }

// Query returns a filtered, paginated slice of ChangeEvents (newest first) with total count.
// All filter parameters are optional (empty string / nil pointer = no filter).
func (ci *ChangeIndex) Query(
	since, until *time.Time,
	cluster, group, kind, namespace, name string,
	ct ChangeType,
	limit, offset int,
) ([]ChangeEvent, int) {
	var matched []ChangeEvent
	for _, e := range ci.events {
		if since != nil && e.Timestamp.Before(*since) {
			continue
		}
		if until != nil && e.Timestamp.After(*until) {
			continue
		}
		if cluster != "" && e.Cluster != cluster {
			continue
		}
		if group != "" && !strings.EqualFold(e.Group, group) {
			continue
		}
		if kind != "" && !strings.EqualFold(e.Kind, kind) {
			continue
		}
		if namespace != "" && e.Namespace != namespace {
			continue
		}
		if name != "" && e.Name != name {
			continue
		}
		if ct != "" && e.ChangeType != ct {
			continue
		}
		matched = append(matched, e)
	}

	total := len(matched)
	if offset >= total {
		return nil, total
	}
	end := offset + limit
	if end > total {
		end = total
	}

	// Return in reverse (newest first): matched is ascending, so reverse the slice.
	page := make([]ChangeEvent, 0, end-offset)
	for i := total - 1 - offset; i >= total-end; i-- {
		page = append(page, matched[i])
	}
	return page, total
}

// Volatility returns per-resource change counts within the most recent maxCommits
// that match the optional filters. maxCommits <= 0 means no limit.
func (ci *ChangeIndex) Volatility(cluster, group, kind, namespace, name string, maxCommits int) []VolatilityEntry {
	type key struct{ cluster, group, kind, ns, name string }

	commitSeen := make(map[string]struct{})
	counts := make(map[key]map[string]struct{})

	for i := len(ci.events) - 1; i >= 0; i-- {
		e := ci.events[i]
		if cluster != "" && e.Cluster != cluster {
			continue
		}
		if group != "" && !strings.EqualFold(e.Group, group) {
			continue
		}
		if kind != "" && !strings.EqualFold(e.Kind, kind) {
			continue
		}
		if namespace != "" && e.Namespace != namespace {
			continue
		}
		if name != "" && e.Name != name {
			continue
		}
		if maxCommits > 0 {
			commitSeen[e.SHA] = struct{}{}
			if len(commitSeen) > maxCommits {
				break
			}
		}
		k := key{e.Cluster, e.Group, e.Kind, e.Namespace, e.Name}
		if counts[k] == nil {
			counts[k] = make(map[string]struct{})
		}
		counts[k][e.SHA] = struct{}{}
	}

	total := len(commitSeen)
	result := make([]VolatilityEntry, 0, len(counts))
	for k, shas := range counts {
		result = append(result, VolatilityEntry{
			Cluster:   k.cluster,
			Group:     k.group,
			Kind:      k.kind,
			Namespace: k.ns,
			Name:      k.name,
			Count:     len(shas),
			Total:     total,
		})
	}
	return result
}

// ParseResourcePath parses a subDir-relative path (without the subDir prefix) and returns
// (group, version, namespace, resource, name, ok).
//
//	4 parts: group/version/resource/name.yaml          (cluster-scoped)
//	6 parts: group/version/namespaces/ns/resource/name.yaml
func ParseResourcePath(relPath string) (group, version, namespace, resource, name string, ok bool) {
	relPath = strings.TrimSuffix(relPath, ".yaml")
	parts := strings.Split(relPath, "/")
	switch len(parts) {
	case 4:
		return parts[0], parts[1], "", parts[2], parts[3], true
	case 6:
		if parts[2] != "namespaces" {
			return "", "", "", "", "", false
		}
		return parts[0], parts[1], parts[3], parts[4], parts[5], true
	}
	return "", "", "", "", "", false
}
