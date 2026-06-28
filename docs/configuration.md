# Configuration Reference

Both binaries use a Kubernetes-style config file (`apiVersion: korpus.io/v1alpha1`). `${VAR}` placeholders in any config value are expanded from environment variables at startup; undefined variables cause a startup error.

## korpus.yaml (backup daemon)

```yaml
apiVersion: korpus.io/v1alpha1
kind: KorpusConfig
spec:
  git:
    repo: https://github.com/your-org/k8s-backup.git
    branch: main
    subDir: cluster          # optional: subdirectory within the repo
    token: ${GIT_TOKEN}      # or use tokenFile: /path/to/token
    author:
      name: korpus-bot
      email: korpus@example.com

  backup:
    schedule: "*/10 * * * *"

    # Set to true to disable all built-in exclusions (see below).
    disableBuiltinExcludes: false

    # rules defines per-resource backup behaviour.
    # resource: "resource.group", "resource" (core), or "*" (all resources).
    # excludeFields is always additive — all matching rules are unioned.
    rules:
      # Strip fields from every resource (wildcard)
      - resource: "*"
        excludeFields:
          - metadata.resourceVersion
          - metadata.managedFields
          - metadata.generation
          - metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"]

      # Skip a resource type entirely
      - resource: events
        exclude: true

      # Add extra fields to exclude for a specific resource (stacks with "*" above)
      - resource: nodes
        excludeFields:
          - metadata.annotations["node.alpha.kubernetes.io/ttl"]

      # Skip a specific object by namespace + name
      - resource: cronjobs.batch
        namespace: kube-system
        name: backup-manifests
        exclude: true

      # Re-enable a built-in excluded resource (user rules take precedence)
      - resource: secrets
        exclude: false

      # excludeIf: CEL expression evaluated per object; skips the object when true.
      # Compiled at startup — invalid expressions cause a startup error.
      - resource: jobs.batch
        excludeIf: hasOwnerKind("CronJob")  # skip CronJob-generated Jobs

      # namespace/name act as optional pre-filters for excludeIf rules
      - resource: pods
        namespace: ci
        excludeIf: isGenerated()

      # Wildcard applies to every resource type
      - resource: "*"
        excludeIf: isBeingDeleted()
```

**Built-in excluded resources:** `secrets`, `events`, `leases.coordination.k8s.io`, `endpointslices.discovery.k8s.io`, `componentstatuses`, and transient cert-manager / Cilium / metrics-server resources.

**Built-in excluded fields** (always appended, unless `disableBuiltinExcludes: true`): per-resource timestamp noise such as `status.reconciledAt` (ArgoCD Applications), `status.lastResync` (Grafana Operator), and `status.conditions[*].lastHeartbeatTime` (Nodes).

### `excludeIf` — CEL expression for per-object exclusion

`excludeIf` accepts a [CEL](https://cel.dev/) expression that is evaluated for each object. When the expression returns `true`, the object is excluded from the information base. The expression is compiled at startup; a syntax or type error causes a startup error.

Rules with `excludeIf` are handled separately from `exclude` and `excludeFields` rules and do not interact with them.

**Activation variables:**

| Variable | Type | Description |
|---|---|---|
| `resource` | `string` | GVR resource name, e.g. `"pods"` |
| `group` | `string` | API group, e.g. `"batch"`, `""` for core resources |
| `ns` | `string` | Object namespace (empty for cluster-scoped resources) |
| `name` | `string` | Object name |

**Extension functions:**

| Function | Returns | Description |
|---|---|---|
| `hasOwnerKind(kind)` | `bool` | True if any `ownerReference` has `kind == kind` |
| `hasOwnerName(name)` | `bool` | True if any `ownerReference` has `name == name` |
| `isControlled()` | `bool` | True if any `ownerReference` has `controller: true` |
| `isGenerated()` | `bool` | True if `generateName` is non-empty (object created from a template) |
| `hasLabel(key)` | `bool` | True if the label key is present |
| `labelValue(key)` | `string` | Label value, or `""` if absent |
| `hasAnnotation(key)` | `bool` | True if the annotation key is present |
| `annotationValue(key)` | `string` | Annotation value, or `""` if absent |
| `hasFinalizer(key)` | `bool` | True if the finalizer key is present |
| `isBeingDeleted()` | `bool` | True if `deletionTimestamp` is set |

**Runtime evaluation errors** (e.g. unexpected panics in extension logic) cause the object to be **included** and logged as a warning, preserving information-base completeness.

**Examples:**

```yaml
# Exclude CronJob-generated Jobs (same effect as the built-in rule)
- resource: jobs.batch
  excludeIf: hasOwnerKind("CronJob")

# Exclude any object currently being garbage-collected
- resource: "*"
  excludeIf: isBeingDeleted()

# Combine activation variables and functions
- resource: jobs.batch
  excludeIf: |
    group == "batch" &&
    hasOwnerKind("CronJob") &&
    !hasAnnotation("audit.example.com/retain")
```

## server.yaml (viewer)

The server only reads from Git (no commits), so `author` is not needed.

```yaml
apiVersion: korpus.io/v1alpha1
kind: ServerConfig
spec:
  addr: ":8080"
  pullInterval: "10m"
  clusters:
    - name: prod
      git:
        repo: https://github.com/your-org/k8s-prod.git
        branch: main
        token: ${PROD_GIT_TOKEN}
    # Add more clusters as needed:
    # - name: staging
    #   git:
    #     repo: https://github.com/your-org/k8s-all.git
    #     subDir: staging
    #     token: ${STAGING_GIT_TOKEN}
  index:
    fields:
      - metadata.labels
      - metadata.creationTimestamp
      # Add fields here to avoid disk I/O on queries that reference them:
      # - spec.nodeName
      # - spec.replicas
  # Optional: enable OIDC authentication
  # oidc:
  #   issuer: https://your-idp.example.com/
  #   audience: https://korpus.example.com
  #   clientId: <your-client-id>
```
