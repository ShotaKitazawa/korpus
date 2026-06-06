package index

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"sigs.k8s.io/yaml"
)

// KindInfo pairs an API group with a resource kind.
type KindInfo struct {
	Group string `json:"group"`
	Kind  string `json:"kind"`
}

// ResourceMeta holds the identifying metadata of a single K8s resource.
type ResourceMeta struct {
	Cluster           string            `json:"cluster"`
	Group             string            `json:"group"`
	Kind              string            `json:"kind"`
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	Labels            map[string]string `json:"labels"`
	CreationTimestamp string            `json:"creationTimestamp,omitempty"`
	FilePath          string            `json:"-"`
	IndexedFields     map[string]any    `json:"-"`
}

// Index is a thread-safe in-memory index of K8s resources.
type Index struct {
	cluster   string
	mu        sync.RWMutex
	resources []ResourceMeta
	fields    []string
	celEnv    *cel.Env
	celCache  sync.Map
}

// New returns an empty Index for the given cluster.
func New(cluster string, fields []string) *Index {
	env, _ := cel.NewEnv(cel.Variable("object", cel.DynType))
	return &Index{cluster: cluster, fields: fields, celEnv: env}
}

// Build walks dir, parses every .yaml file, and rebuilds the index.
// Group is extracted from the first path component relative to dir
// (e.g. dir/apps/v1/namespaces/default/pods/mypod.yaml → group "apps").
// Falls back to parsing apiVersion when path structure is not recognisable.
func (idx *Index) Build(dir string) error {
	var result []ResourceMeta
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, doc := range splitYAMLDocs(data) {
			var raw map[string]any
			if err := yaml.Unmarshal(doc, &raw); err != nil || raw == nil {
				continue
			}
			kind, _ := raw["kind"].(string)
			if kind == "" {
				continue
			}
			meta, _ := raw["metadata"].(map[string]any)
			if meta == nil {
				continue
			}
			name, _ := meta["name"].(string)
			if name == "" {
				continue
			}
			namespace, _ := meta["namespace"].(string)
			creationTimestamp, _ := meta["creationTimestamp"].(string)

			rawLabels, _ := meta["labels"].(map[string]any)
			labels := make(map[string]string, len(rawLabels))
			for k, v := range rawLabels {
				if s, ok := v.(string); ok {
					labels[k] = s
				}
			}
			if len(labels) == 0 {
				labels = nil
			}

			group := groupFromPath(path, dir)
			if group == "" {
				group = groupFromAPIVersion(raw)
			}

			indexedFields := make(map[string]any, len(idx.fields))
			for _, field := range idx.fields {
				if v := getNestedField(raw, field); v != nil {
					indexedFields[field] = v
				}
			}

			result = append(result, ResourceMeta{
				Cluster:           idx.cluster,
				Group:             group,
				Kind:              kind,
				Name:              name,
				Namespace:         namespace,
				Labels:            labels,
				CreationTimestamp: creationTimestamp,
				FilePath:          path,
				IndexedFields:     indexedFields,
			})
		}
		return nil
	})
	if err != nil {
		return err
	}

	idx.mu.Lock()
	idx.resources = result
	idx.mu.Unlock()
	return nil
}

// groupFromPath extracts the API group from the first path component relative to dir.
func groupFromPath(absPath, dir string) string {
	rel := strings.TrimPrefix(filepath.ToSlash(absPath), filepath.ToSlash(dir)+"/")
	if rel == filepath.ToSlash(absPath) {
		return ""
	}
	parts := strings.SplitN(rel, "/", 2)
	if len(parts) < 2 {
		return ""
	}
	return parts[0]
}

// groupFromAPIVersion derives a group string from the YAML apiVersion field.
// "v1" → "core"; "apps/v1" → "apps"; anything else → split on last "/".
func groupFromAPIVersion(raw map[string]any) string {
	av, _ := raw["apiVersion"].(string)
	if av == "" {
		return ""
	}
	if !strings.Contains(av, "/") {
		return "core"
	}
	return av[:strings.LastIndex(av, "/")]
}

// Groups returns the sorted unique list of API groups in the index.
func (idx *Index) Groups() []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	seen := make(map[string]struct{})
	for _, r := range idx.resources {
		seen[r.Group] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for g := range seen {
		result = append(result, g)
	}
	sort.Strings(result)
	return result
}

// Kinds returns the sorted unique list of KindInfo in the index,
// optionally filtered by namespace and/or group.
func (idx *Index) Kinds(namespace, group string) []KindInfo {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	type key struct{ g, k string }
	seen := make(map[key]struct{})
	for _, r := range idx.resources {
		if namespace != "" && r.Namespace != namespace {
			continue
		}
		if group != "" && !strings.EqualFold(r.Group, group) {
			continue
		}
		seen[key{r.Group, r.Kind}] = struct{}{}
	}
	result := make([]KindInfo, 0, len(seen))
	for k := range seen {
		result = append(result, KindInfo{Group: k.g, Kind: k.k})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Group != result[j].Group {
			return result[i].Group < result[j].Group
		}
		return result[i].Kind < result[j].Kind
	})
	return result
}

// Namespaces returns the sorted unique list of namespaces in the index.
func (idx *Index) Namespaces() []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	seen := make(map[string]struct{})
	for _, r := range idx.resources {
		if r.Namespace != "" {
			seen[r.Namespace] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for ns := range seen {
		result = append(result, ns)
	}
	sort.Strings(result)
	return result
}

// List returns resources matching group, kind, namespace, and/or labels.
// Empty string / nil means "any".
func (idx *Index) List(group, kind, namespace string, labelSel map[string]string) []ResourceMeta {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	var result []ResourceMeta
	for _, r := range idx.resources {
		if group != "" && !strings.EqualFold(r.Group, group) {
			continue
		}
		if kind != "" && !strings.EqualFold(r.Kind, kind) {
			continue
		}
		if namespace != "" && r.Namespace != namespace {
			continue
		}
		if !matchLabels(r.Labels, labelSel) {
			continue
		}
		result = append(result, r)
	}
	return result
}

func matchLabels(labels map[string]string, sel map[string]string) bool {
	for k, v := range sel {
		got, ok := labels[k]
		if !ok {
			return false
		}
		if v != "" && got != v {
			return false
		}
	}
	return true
}

// Query evaluates a CEL expression against resources, optionally filtered by group/kind/namespace.
// kind is required when expr is non-empty; otherwise kind may be empty.
func (idx *Index) Query(group, kind, namespace string, labelSel map[string]string, expr string) ([]ResourceMeta, error) {
	idx.mu.RLock()
	candidates := make([]ResourceMeta, 0)
	for _, r := range idx.resources {
		if group != "" && !strings.EqualFold(r.Group, group) {
			continue
		}
		if kind != "" && !strings.EqualFold(r.Kind, kind) {
			continue
		}
		if namespace != "" && r.Namespace != namespace {
			continue
		}
		if !matchLabels(r.Labels, labelSel) {
			continue
		}
		candidates = append(candidates, r)
	}
	idx.mu.RUnlock()

	if expr == "" {
		return candidates, nil
	}

	prog, err := idx.getOrCompile(expr)
	if err != nil {
		return nil, fmt.Errorf("CEL compile: %w", err)
	}

	var result []ResourceMeta
	for _, r := range candidates {
		match, err := idx.evalResource(prog, r)
		if err != nil {
			return nil, err
		}
		if match {
			result = append(result, r)
		}
	}
	return result, nil
}

// Get returns the ResourceMeta for the given group/kind/namespace/name, if present.
func (idx *Index) Get(group, kind, namespace, name string) (ResourceMeta, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	for _, r := range idx.resources {
		if group != "" && !strings.EqualFold(r.Group, group) {
			continue
		}
		if strings.EqualFold(r.Kind, kind) && r.Namespace == namespace && r.Name == name {
			return r, true
		}
	}
	return ResourceMeta{}, false
}

func (idx *Index) getOrCompile(expr string) (cel.Program, error) {
	if v, ok := idx.celCache.Load(expr); ok {
		return v.(cel.Program), nil
	}
	ast, issues := idx.celEnv.Compile(expr)
	if issues.Err() != nil {
		return nil, issues.Err()
	}
	prog, err := idx.celEnv.Program(ast)
	if err != nil {
		return nil, err
	}
	idx.celCache.Store(expr, prog)
	return prog, nil
}

func (idx *Index) evalResource(prog cel.Program, r ResourceMeta) (bool, error) {
	objMap := buildObjectMap(r)
	out, _, evalErr := prog.Eval(map[string]any{"object": objMap})

	needsFallback := false
	if evalErr != nil {
		if strings.Contains(evalErr.Error(), "no such key") {
			needsFallback = true
		} else {
			return false, fmt.Errorf("CEL eval: %w", evalErr)
		}
	} else if types.IsError(out) {
		needsFallback = true
	}

	if needsFallback {
		full, err := loadFullDoc(r)
		if err != nil {
			return false, fmt.Errorf("load full doc: %w", err)
		}
		out, _, err = prog.Eval(map[string]any{"object": full})
		if err != nil {
			return false, fmt.Errorf("CEL eval (full): %w", err)
		}
		if types.IsError(out) {
			return false, fmt.Errorf("CEL eval error: %s", out)
		}
	}
	return out == types.True, nil
}

func buildObjectMap(r ResourceMeta) map[string]any {
	doc := map[string]any{
		"kind": r.Kind,
		"metadata": map[string]any{
			"name":      r.Name,
			"namespace": r.Namespace,
		},
	}
	for path, value := range r.IndexedFields {
		setNestedField(doc, path, value)
	}
	return doc
}

func setNestedField(m map[string]any, path string, value any) {
	parts := strings.SplitN(path, ".", 2)
	if len(parts) == 1 {
		m[path] = value
		return
	}
	child, ok := m[parts[0]].(map[string]any)
	if !ok {
		child = make(map[string]any)
		m[parts[0]] = child
	}
	setNestedField(child, parts[1], value)
}

func getNestedField(m map[string]any, path string) any {
	parts := strings.SplitN(path, ".", 2)
	v, ok := m[parts[0]]
	if !ok {
		return nil
	}
	if len(parts) == 1 {
		return v
	}
	child, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return getNestedField(child, parts[1])
}

func loadFullDoc(r ResourceMeta) (map[string]any, error) {
	data, err := os.ReadFile(r.FilePath)
	if err != nil {
		return nil, err
	}
	for _, doc := range splitYAMLDocs(data) {
		var raw map[string]any
		if err := yaml.Unmarshal(doc, &raw); err != nil || raw == nil {
			continue
		}
		kind, _ := raw["kind"].(string)
		meta, _ := raw["metadata"].(map[string]any)
		if meta == nil {
			continue
		}
		name, _ := meta["name"].(string)
		namespace, _ := meta["namespace"].(string)
		if strings.EqualFold(kind, r.Kind) && name == r.Name && namespace == r.Namespace {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("document not found in %s", r.FilePath)
}

func splitYAMLDocs(data []byte) [][]byte {
	var docs [][]byte
	for _, part := range strings.Split(string(data), "\n---") {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			docs = append(docs, []byte(trimmed))
		}
	}
	return docs
}
