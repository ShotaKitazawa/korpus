package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

func mustLoadKorpus(t *testing.T, content string) *KorpusConfig {
	t.Helper()
	cfg, err := LoadKorpus(writeTempConfig(t, content))
	require.NoError(t, err)
	return cfg
}

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
		ResolveExcludeFieldsForObject(cfg, "deployments", "apps", "", ""))
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
	fields := ResolveExcludeFieldsForObject(cfg, "nodes", "", "", "")
	assert.Contains(t, fields, "metadata.resourceVersion")
	assert.Contains(t, fields, "status")
	assert.Contains(t, fields, "metadata.generation")
	// deployments only get wildcard
	assert.Equal(t, []string{"metadata.resourceVersion", "status"},
		ResolveExcludeFieldsForObject(cfg, "deployments", "apps", "", ""))
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
	fields := ResolveExcludeFieldsForObject(cfg, "applications", "argoproj.io", "", "")
	assert.Contains(t, fields, "metadata.resourceVersion")
	assert.Contains(t, fields, "status.reconciledAt")
	// unrelated resources are unaffected
	assert.NotContains(t, ResolveExcludeFieldsForObject(cfg, "deployments", "apps", "", ""), "status.reconciledAt")
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
	fields := ResolveExcludeFieldsForObject(cfg, "applications", "argoproj.io", "", "")
	assert.NotContains(t, fields, "status.reconciledAt")
}

// Object-filter rules only apply when namespace/name match; unmatched objects skip them.
func TestResolveExcludeFields_ObjectFilterNotAppliedWithoutMatch(t *testing.T) {
	cfg := &KorpusConfig{
		Spec: KorpusSpec{
			Backup: BackupConfig{
				Rules: []RuleConfig{
					{Resource: "*", ExcludeFields: []string{"metadata.resourceVersion"}},
					{Resource: "cronjobs.batch", Namespace: "kube-system", Name: "backup-manifests",
						ExcludeFields: []string{"status.active"}},
				},
			},
		},
	}
	// other-cron does not match the object-filter rule → only wildcard fields apply
	assert.Equal(t, []string{"metadata.resourceVersion"},
		ResolveExcludeFieldsForObject(cfg, "cronjobs", "batch", "kube-system", "other-cron"))
}

// Per-object excludeFields are unioned with wildcard and type-level fields.
func TestResolveExcludeFields_ObjectFilterApplied(t *testing.T) {
	cfg := &KorpusConfig{
		Spec: KorpusSpec{
			Backup: BackupConfig{
				Rules: []RuleConfig{
					{Resource: "*", ExcludeFields: []string{"metadata.resourceVersion"}},
					{Resource: "cronjobs.batch", ExcludeFields: []string{"metadata.generation"}},
					{Resource: "cronjobs.batch", Namespace: "dns-system", Name: "record-syncer",
						ExcludeFields: []string{"status.active", "status.lastScheduleTime", "status.lastSuccessfulTime"}},
				},
			},
		},
	}
	fields := ResolveExcludeFieldsForObject(cfg, "cronjobs", "batch", "dns-system", "record-syncer")
	assert.Contains(t, fields, "metadata.resourceVersion")  // from wildcard
	assert.Contains(t, fields, "metadata.generation")       // from type-level
	assert.Contains(t, fields, "status.active")             // from object-level
	assert.Contains(t, fields, "status.lastScheduleTime")   // from object-level
	assert.Contains(t, fields, "status.lastSuccessfulTime") // from object-level

	// other CronJobs do not get the object-level fields
	other := ResolveExcludeFieldsForObject(cfg, "cronjobs", "batch", "kube-system", "backup-manifests")
	assert.Contains(t, other, "metadata.resourceVersion")
	assert.Contains(t, other, "metadata.generation")
	assert.NotContains(t, other, "status.active")
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

func makeUnstructured(resource, namespace, name string, ownerKind string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetNamespace(namespace)
	u.SetName(name)
	if ownerKind != "" {
		trueVal := true
		u.SetOwnerReferences([]metav1.OwnerReference{{Kind: ownerKind, Controller: &trueVal}})
	}
	return u
}

func TestLoadKorpus_CEL_InvalidExpression(t *testing.T) {
	cfg := sampleKorpusConfig + `      - resource: pods
        excludeIf: undeclaredVar == "foo"
`
	_, err := LoadKorpus(writeTempConfig(t, cfg))
	assert.ErrorContains(t, err, "excludeIf")
}

func TestLoadKorpus_CEL_SyntaxError(t *testing.T) {
	cfg := sampleKorpusConfig + `      - resource: pods
        excludeIf: "hasOwnerKind("
`
	_, err := LoadKorpus(writeTempConfig(t, cfg))
	assert.ErrorContains(t, err, "excludeIf")
}

func TestIsCELObjectExcluded_BasicTrue(t *testing.T) {
	cfg := mustLoadKorpus(t, sampleKorpusConfig+`      - resource: jobs.batch
        excludeIf: hasOwnerKind("CronJob")
`)
	item := makeUnstructured("jobs", "default", "my-job", "CronJob")
	excluded, err := IsCELObjectExcluded(cfg, "jobs", "batch", item)
	require.NoError(t, err)
	assert.True(t, excluded)
}

func TestIsCELObjectExcluded_BasicFalse(t *testing.T) {
	cfg := mustLoadKorpus(t, sampleKorpusConfig+`      - resource: jobs.batch
        excludeIf: hasOwnerKind("CronJob")
`)
	item := makeUnstructured("jobs", "default", "my-job", "") // no owner
	excluded, err := IsCELObjectExcluded(cfg, "jobs", "batch", item)
	require.NoError(t, err)
	assert.False(t, excluded)
}

func TestIsCELObjectExcluded_ResourcePreFilter(t *testing.T) {
	cfg := mustLoadKorpus(t, sampleKorpusConfig+`      - resource: jobs.batch
        excludeIf: hasOwnerKind("CronJob")
`)
	// Rule targets "jobs.batch" — pods are not affected
	item := makeUnstructured("pods", "default", "my-pod", "CronJob")
	excluded, err := IsCELObjectExcluded(cfg, "pods", "", item)
	require.NoError(t, err)
	assert.False(t, excluded)
}

func TestIsCELObjectExcluded_WildcardResource(t *testing.T) {
	cfg := mustLoadKorpus(t, sampleKorpusConfig+`      - resource: "*"
        excludeIf: isBeingDeleted()
`)
	item := makeUnstructured("pods", "default", "my-pod", "")
	now := metav1.Now()
	item.SetDeletionTimestamp(&now)

	excluded, err := IsCELObjectExcluded(cfg, "pods", "", item)
	require.NoError(t, err)
	assert.True(t, excluded)
}

func TestIsCELObjectExcluded_NamespacePreFilter(t *testing.T) {
	cfg := mustLoadKorpus(t, sampleKorpusConfig+`      - resource: pods
        namespace: ci
        excludeIf: isGenerated()
`)
	generated := makeUnstructured("pods", "ci", "runner-abc", "")
	generated.SetGenerateName("runner-")

	otherNS := makeUnstructured("pods", "default", "runner-abc", "")
	otherNS.SetGenerateName("runner-")

	excl, err := IsCELObjectExcluded(cfg, "pods", "", generated)
	require.NoError(t, err)
	assert.True(t, excl)

	excl, err = IsCELObjectExcluded(cfg, "pods", "", otherNS)
	require.NoError(t, err)
	assert.False(t, excl) // different namespace → rule does not apply
}

func TestIsCELObjectExcluded_NoRules(t *testing.T) {
	cfg := mustLoadKorpus(t, sampleKorpusConfig) // no excludeIf rules
	item := makeUnstructured("pods", "default", "my-pod", "Job")
	excluded, err := IsCELObjectExcluded(cfg, "pods", "", item)
	require.NoError(t, err)
	assert.False(t, excluded)
}

// excludeIf rules must not affect IsExcluded (resource-type exclusion).
func TestIsExcluded_ExcludeIfRuleIgnored(t *testing.T) {
	cfg := mustLoadKorpus(t, sampleKorpusConfig+`      - resource: pods
        excludeIf: hasOwnerKind("Job")
`)
	// pods are not excluded at the resource-type level
	assert.False(t, IsExcluded(cfg, "pods", ""))
}

// excludeIf rules must not contribute excludeFields.
func TestResolveExcludeFields_ExcludeIfRuleIgnored(t *testing.T) {
	cfg := mustLoadKorpus(t, sampleKorpusConfig+`      - resource: pods
        excludeIf: hasOwnerKind("Job")
        excludeFields:
          - status.phase
`)
	// The excludeFields on a CEL rule should not be applied
	fields := ResolveExcludeFieldsForObject(cfg, "pods", "", "default", "my-pod")
	assert.NotContains(t, fields, "status.phase")
}

// disableBuiltinExcludes does not suppress user CEL rules.
func TestIsCELObjectExcluded_NotAffectedByDisableBuiltin(t *testing.T) {
	yaml := `
apiVersion: korpus.io/v1alpha1
kind: KorpusConfig
spec:
  git:
    repo: https://github.com/example/backup
    branch: main
    author:
      name: bot
      email: bot@example.com
  backup:
    disableBuiltinExcludes: true
    rules:
      - resource: jobs.batch
        excludeIf: hasOwnerKind("CronJob")
`
	cfg := mustLoadKorpus(t, yaml)
	item := makeUnstructured("jobs", "default", "my-job", "CronJob")
	excluded, err := IsCELObjectExcluded(cfg, "jobs", "batch", item)
	require.NoError(t, err)
	assert.True(t, excluded) // user CEL rules are unaffected by disableBuiltinExcludes
}

func TestIsBuiltinObjectExcluded(t *testing.T) {
	cfg := mustLoadKorpus(t, sampleKorpusConfig)

	ownerRef := func(kind string) []metav1.OwnerReference {
		return []metav1.OwnerReference{{Kind: kind}}
	}

	// CronJob-owned Job is excluded
	assert.True(t, IsBuiltinObjectExcluded(cfg, "jobs", "batch", ownerRef("CronJob")))
	// Job-owned Pod is excluded
	assert.True(t, IsBuiltinObjectExcluded(cfg, "pods", "", ownerRef("Job")))
	// Deployment-owned Pod is NOT excluded
	assert.False(t, IsBuiltinObjectExcluded(cfg, "pods", "", ownerRef("ReplicaSet")))
	// Manual Job (no owner) is NOT excluded
	assert.False(t, IsBuiltinObjectExcluded(cfg, "jobs", "batch", nil))
	// disableBuiltinExcludes bypasses this check
	cfgDisabled := mustLoadKorpus(t, sampleKorpusConfig+`    disableBuiltinExcludes: true
`)
	assert.False(t, IsBuiltinObjectExcluded(cfgDisabled, "jobs", "batch", ownerRef("CronJob")))
}
