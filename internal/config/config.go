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
	Schedule               string           `yaml:"schedule"`
	DefaultExcludeFields   []string         `yaml:"defaultExcludeFields"`
	DisableBuiltinExcludes bool             `yaml:"disableBuiltinExcludes"`
	Resources              []ResourceConfig `yaml:"resources"`
}

type ResourceConfig struct {
	Match         string   `yaml:"match"`
	Exclude       bool     `yaml:"exclude"`
	ExcludeFields []string `yaml:"excludeFields"`
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

// resourceKey builds the match key in "resource.group" or "resource" form.
func resourceKey(resource, group string) string {
	if group == "" {
		return resource
	}
	return resource + "." + group
}

// IsExcluded reports whether a resource should be skipped entirely.
func IsExcluded(cfg *KorpusConfig, resource, group string) bool {
	key := resourceKey(resource, group)
	if !cfg.Spec.Backup.DisableBuiltinExcludes {
		for _, r := range defaults.BuiltinExcludeResources {
			if r == key || r == resource {
				return true
			}
		}
	}
	for _, rc := range cfg.Spec.Backup.Resources {
		if rc.Match == key || rc.Match == resource {
			return rc.Exclude
		}
	}
	return false
}

// ResolveExcludeFields returns the field paths to strip for a given resource.
// A per-resource excludeFields completely replaces defaultExcludeFields.
func ResolveExcludeFields(cfg *KorpusConfig, resource, group string) []string {
	key := resourceKey(resource, group)
	for _, rc := range cfg.Spec.Backup.Resources {
		if rc.Match == key || rc.Match == resource {
			if rc.ExcludeFields != nil {
				return rc.ExcludeFields
			}
		}
	}
	return cfg.Spec.Backup.DefaultExcludeFields
}
