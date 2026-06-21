package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleKorpusConfig = `
apiVersion: korpus.io/v1alpha1
kind: KorpusConfig
spec:
  git:
    repo: https://github.com/example/backup
    branch: main
    subDir: cluster
    author:
      name: bot
      email: bot@example.com
  backup:
    rules:
      - resource: "*"
        excludeFields:
          - metadata.resourceVersion
          - status
      - resource: ciliumidentities.cilium.io
        exclude: true
      - resource: nodes
        excludeFields:
          - metadata.resourceVersion
`

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "config*.yaml")
	require.NoError(t, err)
	t.Cleanup(func() { os.Remove(f.Name()) })
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}

func TestLoadKorpus_EnvSubst(t *testing.T) {
	t.Setenv("MY_REPO", "https://github.com/example/repo")
	path := writeTempConfig(t, "kind: KorpusConfig\nspec:\n  git:\n    repo: ${MY_REPO}\n")
	cfg, err := LoadKorpus(path)
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/example/repo", cfg.Spec.Git.Repo)
}

func TestLoadKorpus_EnvSubst_MissingVar(t *testing.T) {
	path := writeTempConfig(t, "kind: KorpusConfig\nspec:\n  git:\n    repo: ${UNDEFINED_VAR_XYZ}\n")
	_, err := LoadKorpus(path)
	assert.ErrorContains(t, err, "UNDEFINED_VAR_XYZ")
}

func TestLoadKorpus(t *testing.T) {
	path := writeTempConfig(t, sampleKorpusConfig)
	cfg, err := LoadKorpus(path)
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/example/backup", cfg.Spec.Git.Repo)
	assert.Equal(t, "main", cfg.Spec.Git.Branch)
	assert.Equal(t, "bot", cfg.Spec.Git.Author.Name)
	require.Len(t, cfg.Spec.Backup.Rules, 3)
	assert.Equal(t, "*", cfg.Spec.Backup.Rules[0].Resource)
	assert.Equal(t, []string{"metadata.resourceVersion", "status"}, cfg.Spec.Backup.Rules[0].ExcludeFields)
}

func TestLoadKorpus_DefaultBranch(t *testing.T) {
	path := writeTempConfig(t, "kind: KorpusConfig\nspec:\n  git:\n    repo: https://github.com/example/backup\n")
	cfg, err := LoadKorpus(path)
	require.NoError(t, err)
	assert.Equal(t, "main", cfg.Spec.Git.Branch)
}

func TestLoadKorpus_MissingRepo(t *testing.T) {
	path := writeTempConfig(t, "kind: KorpusConfig\nspec:\n  git:\n    branch: main\n")
	_, err := LoadKorpus(path)
	assert.Error(t, err)
}

func TestLoadServer_Clusters(t *testing.T) {
	yaml := `
apiVersion: korpus.io/v1alpha1
kind: ServerConfig
spec:
  clusters:
    - name: prod
      git:
        repo: https://github.com/org/k8s-prod.git
    - name: staging
      git:
        repo: https://github.com/org/k8s-all.git
        branch: staging
`
	path := writeTempConfig(t, yaml)
	cfg, err := LoadServer(path)
	require.NoError(t, err)
	require.Len(t, cfg.Spec.Clusters, 2)
	assert.Equal(t, "prod", cfg.Spec.Clusters[0].Name)
	assert.Equal(t, "main", cfg.Spec.Clusters[0].Git.Branch) // default applied
	assert.Equal(t, "staging", cfg.Spec.Clusters[1].Name)
	assert.Equal(t, "staging", cfg.Spec.Clusters[1].Git.Branch)
	assert.Equal(t, ":8080", cfg.Spec.Addr)
	assert.Equal(t, "10m", cfg.Spec.PullInterval)
}

func TestLoadServer_Clusters_EnvSubst(t *testing.T) {
	t.Setenv("PROD_TOKEN", "secret-token")
	yaml := `
kind: ServerConfig
spec:
  clusters:
    - name: prod
      git:
        repo: https://github.com/org/k8s-prod.git
        token: ${PROD_TOKEN}
`
	path := writeTempConfig(t, yaml)
	cfg, err := LoadServer(path)
	require.NoError(t, err)
	assert.Equal(t, "secret-token", cfg.Spec.Clusters[0].Git.Token)
}

func TestLoadServer_Clusters_TokenFile(t *testing.T) {
	yaml := `
kind: ServerConfig
spec:
  clusters:
    - name: prod
      git:
        repo: https://github.com/org/k8s-prod.git
        tokenFile: /var/run/secrets/git-token
`
	path := writeTempConfig(t, yaml)
	cfg, err := LoadServer(path)
	require.NoError(t, err)
	assert.Equal(t, "/var/run/secrets/git-token", cfg.Spec.Clusters[0].Git.TokenFile)
	assert.Empty(t, cfg.Spec.Clusters[0].Git.Token)
}

func TestLoadServer_EmptyClusters(t *testing.T) {
	path := writeTempConfig(t, "kind: ServerConfig\nspec:\n  addr: \":8080\"\n")
	_, err := LoadServer(path)
	assert.ErrorContains(t, err, "clusters")
}

func TestLoadServer_MissingClusterName(t *testing.T) {
	yaml := `
kind: ServerConfig
spec:
  clusters:
    - git:
        repo: https://github.com/org/repo.git
`
	path := writeTempConfig(t, yaml)
	_, err := LoadServer(path)
	assert.ErrorContains(t, err, "name")
}

func TestLoadServer_MissingClusterRepo(t *testing.T) {
	yaml := `
kind: ServerConfig
spec:
  clusters:
    - name: prod
      git:
        branch: main
`
	path := writeTempConfig(t, yaml)
	_, err := LoadServer(path)
	assert.ErrorContains(t, err, "repo")
}

func TestIsExcluded_BuiltinSecrets(t *testing.T) {
	cfg := &KorpusConfig{}
	assert.True(t, IsExcluded(cfg, "secrets", ""))
	assert.True(t, IsExcluded(cfg, "events", ""))
	assert.True(t, IsExcluded(cfg, "events", "events.k8s.io"))
}

func TestIsExcluded_UserConfigured(t *testing.T) {
	cfg := &KorpusConfig{
		Spec: KorpusSpec{
			Backup: BackupConfig{
				Rules: []RuleConfig{
					{Resource: "ciliumidentities.cilium.io", Exclude: true},
				},
			},
		},
	}
	assert.True(t, IsExcluded(cfg, "ciliumidentities", "cilium.io"))
	assert.False(t, IsExcluded(cfg, "deployments", "apps"))
}

func TestIsExcluded_DisableBuiltin(t *testing.T) {
	cfg := &KorpusConfig{
		Spec: KorpusSpec{
			Backup: BackupConfig{
				DisableBuiltinExcludes: true,
			},
		},
	}
	assert.False(t, IsExcluded(cfg, "secrets", ""))
}

// User rule with exclude: false overrides a builtin exclusion.
func TestIsExcluded_UserOverridesBuiltin(t *testing.T) {
	cfg := &KorpusConfig{
		Spec: KorpusSpec{
			Backup: BackupConfig{
				Rules: []RuleConfig{
					{Resource: "secrets", Exclude: false},
				},
			},
		},
	}
	assert.False(t, IsExcluded(cfg, "secrets", ""))
}

// resource: "*" rules are ignored by IsExcluded.
func TestIsExcluded_WildcardIgnored(t *testing.T) {
	cfg := &KorpusConfig{
		Spec: KorpusSpec{
			Backup: BackupConfig{
				Rules: []RuleConfig{
					{Resource: "*", Exclude: true},
				},
			},
		},
	}
	// builtin secrets should still be excluded; wildcard does not trigger IsExcluded
	assert.True(t, IsExcluded(cfg, "secrets", ""))
	// non-builtin resource should NOT be excluded by the wildcard rule
	assert.False(t, IsExcluded(cfg, "deployments", "apps"))
}

func TestIsObjectExcluded_ByName(t *testing.T) {
	cfg := &KorpusConfig{
		Spec: KorpusSpec{
			Backup: BackupConfig{
				Rules: []RuleConfig{
					{Resource: "cronjobs.batch", Namespace: "kube-system", Name: "backup-manifests", Exclude: true},
				},
			},
		},
	}
	assert.True(t, IsObjectExcluded(cfg, "cronjobs", "batch", "kube-system", "backup-manifests"))
	assert.False(t, IsObjectExcluded(cfg, "cronjobs", "batch", "kube-system", "other-cron"))
	assert.False(t, IsObjectExcluded(cfg, "cronjobs", "batch", "default", "backup-manifests"))
}

func TestIsObjectExcluded_NamespaceOnly(t *testing.T) {
	cfg := &KorpusConfig{
		Spec: KorpusSpec{
			Backup: BackupConfig{
				Rules: []RuleConfig{
					{Resource: "pods", Namespace: "dev", Exclude: true},
				},
			},
		},
	}
	assert.True(t, IsObjectExcluded(cfg, "pods", "", "dev", "any-pod"))
	assert.False(t, IsObjectExcluded(cfg, "pods", "", "prod", "any-pod"))
}

func TestIsObjectExcluded_NoMatch(t *testing.T) {
	cfg := &KorpusConfig{}
	assert.False(t, IsObjectExcluded(cfg, "deployments", "apps", "default", "my-app"))
}

func TestResolveExcludeFields_WildcardOnly(t *testing.T) {
	cfg := &KorpusConfig{
		Spec: KorpusSpec{
			Backup: BackupConfig{
				Rules: []RuleConfig{
					{Resource: "*", ExcludeFields: []string{"metadata.resourceVersion", "status"}},
				},
			},
		},
	}
	assert.Equal(t, []string{"metadata.resourceVersion", "status"},
		ResolveExcludeFields(cfg, "deployments", "apps"))
}

func TestResolveExcludeFields_WildcardAndSpecific(t *testing.T) {
	cfg := &KorpusConfig{
		Spec: KorpusSpec{
			Backup: BackupConfig{
				Rules: []RuleConfig{
					{Resource: "*", ExcludeFields: []string{"metadata.resourceVersion", "status"}},
					{Resource: "nodes", ExcludeFields: []string{"metadata.generation"}},
				},
			},
		},
	}
	// nodes gets wildcard + specific (union)
	fields := ResolveExcludeFields(cfg, "nodes", "")
	assert.Contains(t, fields, "metadata.resourceVersion")
	assert.Contains(t, fields, "status")
	assert.Contains(t, fields, "metadata.generation")
	// deployments only get wildcard
	assert.Equal(t, []string{"metadata.resourceVersion", "status"},
		ResolveExcludeFields(cfg, "deployments", "apps"))
}

func TestResolveExcludeFields_BuiltinAppended(t *testing.T) {
	cfg := &KorpusConfig{
		Spec: KorpusSpec{
			Backup: BackupConfig{
				Rules: []RuleConfig{
					{Resource: "*", ExcludeFields: []string{"metadata.resourceVersion"}},
				},
			},
		},
	}
	// applications.argoproj.io gets builtin status.reconciledAt appended
	fields := ResolveExcludeFields(cfg, "applications", "argoproj.io")
	assert.Contains(t, fields, "metadata.resourceVersion")
	assert.Contains(t, fields, "status.reconciledAt")
	// unrelated resources are unaffected
	assert.NotContains(t, ResolveExcludeFields(cfg, "deployments", "apps"), "status.reconciledAt")
}

func TestResolveExcludeFields_BuiltinDisabled(t *testing.T) {
	cfg := &KorpusConfig{
		Spec: KorpusSpec{
			Backup: BackupConfig{
				DisableBuiltinExcludes: true,
				Rules: []RuleConfig{
					{Resource: "*", ExcludeFields: []string{"metadata.resourceVersion"}},
				},
			},
		},
	}
	fields := ResolveExcludeFields(cfg, "applications", "argoproj.io")
	assert.NotContains(t, fields, "status.reconciledAt")
}

// Object-filter rules are excluded from field resolution.
func TestResolveExcludeFields_ObjectFilterIgnored(t *testing.T) {
	cfg := &KorpusConfig{
		Spec: KorpusSpec{
			Backup: BackupConfig{
				Rules: []RuleConfig{
					{Resource: "*", ExcludeFields: []string{"metadata.resourceVersion"}},
					{Resource: "cronjobs.batch", Namespace: "kube-system", Name: "backup-manifests", Exclude: true},
				},
			},
		},
	}
	// object-filter rule must not contribute excludeFields
	assert.Equal(t, []string{"metadata.resourceVersion"},
		ResolveExcludeFields(cfg, "cronjobs", "batch"))
}

func baseServerYAML() string {
	return `
kind: ServerConfig
spec:
  clusters:
    - name: prod
      git:
        repo: https://github.com/org/k8s-prod.git
`
}

func TestLoadServer_OIDC_Valid(t *testing.T) {
	yaml := baseServerYAML() + `  oidc:
    issuer: https://example.auth0.com/
    audience: https://api.example.com
    clientId: abc123
`
	path := writeTempConfig(t, yaml)
	cfg, err := LoadServer(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Spec.OIDC)
	assert.Equal(t, "https://example.auth0.com/", cfg.Spec.OIDC.Issuer)
	assert.Equal(t, "https://api.example.com", cfg.Spec.OIDC.Audience)
	assert.Equal(t, "abc123", cfg.Spec.OIDC.ClientID)
}

func TestLoadServer_OIDC_Disabled(t *testing.T) {
	path := writeTempConfig(t, baseServerYAML())
	cfg, err := LoadServer(path)
	require.NoError(t, err)
	assert.Nil(t, cfg.Spec.OIDC)
}

func TestLoadServer_OIDC_MissingIssuer(t *testing.T) {
	yaml := baseServerYAML() + `  oidc:
    audience: https://api.example.com
    clientId: abc123
`
	path := writeTempConfig(t, yaml)
	_, err := LoadServer(path)
	assert.ErrorContains(t, err, "issuer")
}

func TestLoadServer_OIDC_MissingAudience(t *testing.T) {
	yaml := baseServerYAML() + `  oidc:
    issuer: https://example.auth0.com/
    clientId: abc123
`
	path := writeTempConfig(t, yaml)
	_, err := LoadServer(path)
	assert.ErrorContains(t, err, "audience")
}

func TestLoadServer_OIDC_MissingClientID(t *testing.T) {
	yaml := baseServerYAML() + `  oidc:
    issuer: https://example.auth0.com/
    audience: https://api.example.com
`
	path := writeTempConfig(t, yaml)
	_, err := LoadServer(path)
	assert.ErrorContains(t, err, "clientId")
}
