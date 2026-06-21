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
```

**Built-in excluded resources:** `secrets`, `events`, `leases.coordination.k8s.io`, `endpointslices.discovery.k8s.io`, `componentstatuses`, and transient cert-manager / Cilium / metrics-server resources.

**Built-in excluded fields** (always appended, unless `disableBuiltinExcludes: true`): per-resource timestamp noise such as `status.reconciledAt` (ArgoCD Applications), `status.lastResync` (Grafana Operator), and `status.conditions[*].lastHeartbeatTime` (Nodes).

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
