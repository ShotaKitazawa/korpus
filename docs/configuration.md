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

    # Fields stripped from every resource before committing.
    # A per-resource excludeFields list replaces this entirely — it is not merged.
    defaultExcludeFields:
      - metadata.resourceVersion
      - metadata.managedFields
      - metadata.generation
      - metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"]

    # Set to true to disable the built-in exclude list (see below)
    disableBuiltinExcludes: false

    resources:
      # Skip a resource entirely
      - match: events
        exclude: true

      # Override excluded fields for a specific resource
      # (replaces defaultExcludeFields for this resource)
      - match: configmaps
        excludeFields:
          - metadata.resourceVersion
          - metadata.managedFields
```

**Built-in excluded resources:** `secrets`, `events`, `leases.coordination.k8s.io`, `endpointslices.discovery.k8s.io`, `componentstatuses`, and transient cert-manager resources.

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
