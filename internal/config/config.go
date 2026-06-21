package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/ShotaKitazawa/korpus/internal/defaults"
	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

type TypeMeta struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
}

// KorpusConfig is the configuration for the korpus backup daemon.
type KorpusConfig struct {
	TypeMeta `yaml:",inline"`
	Spec     KorpusSpec `yaml:"spec"`
}

type KorpusSpec struct {
	Git    GitConfig    `yaml:"git"`
	Backup BackupConfig `yaml:"backup"`
}

// ServerConfig is the configuration for the server viewer.
type ServerConfig struct {
	TypeMeta `yaml:",inline"`
	Spec     ServerSpec `yaml:"spec"`
}

type ServerSpec struct {
	Addr         string          `yaml:"addr"`
	PullInterval string          `yaml:"pullInterval"`
	Clusters     []ClusterConfig `yaml:"clusters"`
	Index        IndexConfig     `yaml:"index"`
	OIDC         *OIDCConfig     `yaml:"oidc"`
}

type OIDCConfig struct {
	Issuer   string `yaml:"issuer"`
	Audience string `yaml:"audience"`
	ClientID string `yaml:"clientId"`
}

// PullIntervalDuration parses PullInterval into a time.Duration.
func (s *ServerSpec) PullIntervalDuration() time.Duration {
	d, _ := time.ParseDuration(s.PullInterval)
	return d
}

type ClusterConfig struct {
	Name string    `yaml:"name"`
	Git  GitConfig `yaml:"git"`
}

type GitConfig struct {
	Repo      string       `yaml:"repo"`
	Branch    string       `yaml:"branch"`
	SubDir    string       `yaml:"subDir"`
	Token     string       `yaml:"token"`
	TokenFile string       `yaml:"tokenFile"`
	Author    AuthorConfig `yaml:"author"`
}

type AuthorConfig struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

type BackupConfig struct {
	Schedule               string       `yaml:"schedule"`
	DisableBuiltinExcludes bool         `yaml:"disableBuiltinExcludes"`
	Rules                  []RuleConfig `yaml:"rules"`
}

// RuleConfig configures backup behaviour for a resource type or a specific object.
//
// resource: "resource.group", "resource" (core), or "*" (all resources).
// namespace/name: when set, the rule applies only to matching objects.
// excludeFields is additive — all matching rules (wildcard + specific) are unioned.
// resource: "*" is only meaningful for excludeFields; it is ignored by IsExcluded.
type RuleConfig struct {
	Resource      string   `yaml:"resource"`
	Namespace     string   `yaml:"namespace,omitempty"`
	Name          string   `yaml:"name,omitempty"`
	Exclude       bool     `yaml:"exclude"`
	ExcludeFields []string `yaml:"excludeFields,omitempty"`
}

func (rc *RuleConfig) hasObjectFilter() bool {
	return rc.Namespace != "" || rc.Name != ""
}

type IndexConfig struct {
	Fields      []string `yaml:"fields"`
	HistoryDays int      `yaml:"historyDays"`
}

var envVarRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func expandEnvStrict(s string) (string, error) {
	var missing []string
	result := envVarRe.ReplaceAllStringFunc(s, func(m string) string {
		name := m[2 : len(m)-1]
		v, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("undefined env vars in config: %s", strings.Join(missing, ", "))
	}
	return result, nil
}

func readAndExpand(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	expanded, err := expandEnvStrict(string(raw))
	if err != nil {
		return nil, fmt.Errorf("config envsubst: %w", err)
	}
	return []byte(expanded), nil
}

// LoadKorpus reads and validates a KorpusConfig file.
func LoadKorpus(path string) (*KorpusConfig, error) {
	data, err := readAndExpand(path)
	if err != nil {
		return nil, err
	}
	var cfg KorpusConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Spec.Git.Repo == "" {
		return nil, fmt.Errorf("spec.git.repo is required")
	}
	if cfg.Spec.Git.Branch == "" {
		cfg.Spec.Git.Branch = "main"
	}
	if cfg.Spec.Backup.Schedule == "" {
		cfg.Spec.Backup.Schedule = "*/10 * * * *"
	}
	if _, err := cron.ParseStandard(cfg.Spec.Backup.Schedule); err != nil {
		return nil, fmt.Errorf("invalid spec.backup.schedule %q: %w", cfg.Spec.Backup.Schedule, err)
	}
	return &cfg, nil
}

// LoadServer reads and validates a ServerConfig file.
func LoadServer(path string) (*ServerConfig, error) {
	data, err := readAndExpand(path)
	if err != nil {
		return nil, err
	}
	var cfg ServerConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if len(cfg.Spec.Clusters) == 0 {
		return nil, fmt.Errorf("spec.clusters must be non-empty")
	}
	for i, c := range cfg.Spec.Clusters {
		if c.Name == "" {
			return nil, fmt.Errorf("spec.clusters[%d].name is required", i)
		}
		if c.Git.Repo == "" {
			return nil, fmt.Errorf("spec.clusters[%d].git.repo is required", i)
		}
		if cfg.Spec.Clusters[i].Git.Branch == "" {
			cfg.Spec.Clusters[i].Git.Branch = "main"
		}
	}
	if cfg.Spec.Addr == "" {
		cfg.Spec.Addr = ":8080"
	}
	if cfg.Spec.PullInterval == "" {
		cfg.Spec.PullInterval = "10m"
	}
	if _, err := time.ParseDuration(cfg.Spec.PullInterval); err != nil {
		return nil, fmt.Errorf("invalid spec.pullInterval %q: %w", cfg.Spec.PullInterval, err)
	}
	if len(cfg.Spec.Index.Fields) == 0 {
		cfg.Spec.Index.Fields = []string{"metadata.labels", "metadata.creationTimestamp"}
	}
	if cfg.Spec.Index.HistoryDays == 0 {
		cfg.Spec.Index.HistoryDays = 30
	}
	if o := cfg.Spec.OIDC; o != nil {
		if o.Issuer == "" {
			return nil, fmt.Errorf("spec.oidc.issuer is required when oidc is configured")
		}
		if o.Audience == "" {
			return nil, fmt.Errorf("spec.oidc.audience is required when oidc is configured")
		}
		if o.ClientID == "" {
			return nil, fmt.Errorf("spec.oidc.clientId is required when oidc is configured")
		}
	}
	return &cfg, nil
}

// resourceKey builds the lookup key in "resource.group" or "resource" form.
func resourceKey(resource, group string) string {
	if group == "" {
		return resource
	}
	return resource + "." + group
}

// IsExcluded reports whether a resource type should be skipped entirely.
// User rules take precedence over built-in exclusions, so setting exclude: false
// on a built-in resource re-enables backup for that resource type.
// Rules with resource: "*" or an object filter (namespace/name) are ignored here.
func IsExcluded(cfg *KorpusConfig, resource, group string) bool {
	key := resourceKey(resource, group)
	for _, rc := range cfg.Spec.Backup.Rules {
		if rc.Resource == "*" || rc.hasObjectFilter() {
			continue
		}
		if rc.Resource == key || rc.Resource == resource {
			return rc.Exclude
		}
	}
	if !cfg.Spec.Backup.DisableBuiltinExcludes {
		for _, r := range defaults.BuiltinExcludeResources {
			if r == key || r == resource {
				return true
			}
		}
	}
	return false
}

// IsObjectExcluded reports whether a specific object should be skipped.
// Called per-item after listing, complementing the GVR-level IsExcluded.
func IsObjectExcluded(cfg *KorpusConfig, resource, group, namespace, name string) bool {
	key := resourceKey(resource, group)
	for _, rc := range cfg.Spec.Backup.Rules {
		if !rc.hasObjectFilter() {
			continue
		}
		if rc.Resource != "*" && rc.Resource != key && rc.Resource != resource {
			continue
		}
		if (rc.Namespace == "" || rc.Namespace == namespace) &&
			(rc.Name == "" || rc.Name == name) {
			return rc.Exclude
		}
	}
	return false
}

// ResolveExcludeFields returns the field paths to strip for a given resource.
// All matching rules contribute additively: wildcard ("*") rules are unioned with
// resource-specific rules. Built-in field exclusions are appended last unless
// disableBuiltinExcludes is true.
func ResolveExcludeFields(cfg *KorpusConfig, resource, group string) []string {
	key := resourceKey(resource, group)
	var fields []string
	for _, rc := range cfg.Spec.Backup.Rules {
		if rc.hasObjectFilter() {
			continue
		}
		if rc.Resource == "*" || rc.Resource == key || rc.Resource == resource {
			fields = append(fields, rc.ExcludeFields...)
		}
	}
	if !cfg.Spec.Backup.DisableBuiltinExcludes {
		for k, builtinFields := range defaults.BuiltinExcludeFields {
			if k == key || k == resource {
				fields = append(fields, builtinFields...)
				break
			}
		}
	}
	return fields
}
