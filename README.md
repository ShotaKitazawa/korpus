<div align="center">
  <img src="assets/icon.svg" width="80" alt="korpus">
  <h1>korpus</h1>
  <p>A persistent, CEL-queryable Information Base for your Kubernetes cluster — git-backed and accessible to AI agents via MCP.</p>
</div>

---

korpus snapshots every Kubernetes resource as YAML and commits it to Git, stripping runtime noise (`resourceVersion`, `managedFields`, `generation`) before each commit so `git log` / `git diff` reflects real configuration changes — not constant churn. The result is a queryable record of cluster state that works without the Kubernetes API.

korpus consists of two binaries:

- **korpus** — backup daemon that maintains the Information Base.
- **server** — read-only viewer that periodically pulls the backup repo and serves a React SPA, a REST API, and an MCP server over Streamable HTTP, making the Information Base queryable with CEL expressions and accessible to AI agents.

## Quickstart

Create `korpus.yaml` for the backup daemon:

```yaml
apiVersion: korpus.io/v1alpha1
kind: KorpusConfig
spec:
  git:
    repo: https://github.com/your-org/k8s-backup.git
    branch: main
    subDir: cluster
    token: ${GIT_TOKEN}
    author:
      name: korpus-bot
      email: korpus@example.com
```

Create `server.yaml` for the viewer:

```yaml
apiVersion: korpus.io/v1alpha1
kind: ServerConfig
spec:
  clusters:
    - name: default
      git:
        repo: https://github.com/your-org/k8s-backup.git
        branch: main
        subDir: cluster
        token: ${GIT_TOKEN}
```

Run the backup daemon:

```bash
GIT_TOKEN=<token> docker run --rm \
  -v ~/.kube:/root/.kube:ro \
  -v $(pwd)/korpus.yaml:/korpus.yaml \
  ghcr.io/shotakitazawa/korpus:latest --config /korpus.yaml
```

Run the viewer:

```bash
GIT_TOKEN=<token> docker run --rm \
  -p 8080:8080 \
  -v $(pwd)/server.yaml:/server.yaml \
  ghcr.io/shotakitazawa/korpus-server:latest --config /server.yaml
```

Open http://localhost:8080. For full configuration options, see [docs/configuration.md](docs/configuration.md).

## Usage

### korpus (backup daemon)

```
korpus [-config <path>]   default: config.yaml
```

Runs on the schedule defined in `backup.schedule` (default: every 10 minutes). Exposes `/healthz` and `/metrics` (Prometheus).

**Output layout:**

```
<subDir>/
└── $API_GROUP/          (core group uses "core")
    └── $VERSION/
        ├── $RESOURCE/
        │   └── $NAME.yaml
        └── namespaces/
            └── $NAMESPACE/
                └── $RESOURCE/
                    └── $NAME.yaml
```

### server (viewer)

```
server [-config <path>]   default: config.yaml
```

Pulls the backup repo every `server.pullInterval` (default: `10m`) and rebuilds the in-memory index. Serves:

| Endpoint | Description |
|---|---|
| `GET /` | React SPA |
| `GET /healthz` | Health check |
| `GET /api/clusters` | Cluster names |
| `GET /api/groups` | API groups |
| `GET /api/kinds` | Resource kinds |
| `GET /api/namespaces` | Namespaces |
| `GET /api/snapshot` | Resource list (CEL-filterable, point-in-time) |
| `GET /api/resource` | Raw YAML of a single resource |
| `GET /api/history` | Change history for a resource |
| `GET /api/diff` | YAML diff between two commits |
| `GET /api/volatility` | Resources ranked by change frequency |
| `GET /api/volatility/fields` | Field-level change frequency |
| `POST /mcp` | MCP server (Streamable HTTP) |

For full parameter reference, see [openapi.yaml](openapi.yaml).

**CEL expression examples** (used in `cel=` parameter of `/api/snapshot`):

```
object.spec.replicas > 1
object.metadata.labels["app"] == "nginx"
object.status.phase == "Running"
```

**MCP tools:** `list_clusters`, `list_gvks`, `list_namespaces`, `get_resource`, `get_snapshot`, `get_history`, `get_diff`, `get_volatility`, `get_volatility_fields`

**Connecting via MCP:**

Claude Code:
```bash
claude mcp add --transport http korpus http://localhost:8080/mcp
```

Claude Desktop (`claude_desktop_config.json`):
```json
{
  "mcpServers": {
    "korpus": {
      "url": "http://localhost:8080/mcp"
    }
  }
}
```

When OIDC is enabled, the same command works — Claude Code handles authentication automatically via OAuth 2.0 with Dynamic Client Registration. See [docs/dcr-flow.md](docs/dcr-flow.md) for details.

If your OIDC provider does not support DCR, pass a pre-obtained bearer token instead:

```bash
claude mcp add --transport http korpus http://localhost:8080/mcp \
  --header "Authorization: Bearer <token>"
```

## Deploy to Kubernetes

Apply the latest release manifest:

```bash
kubectl apply -f https://github.com/ShotaKitazawa/korpus/releases/latest/download/install.yaml
```

Or build from source with kustomize:

```bash
# Both korpus + server
kustomize build manifests/ | kubectl apply -f -

# korpus only
kustomize build manifests/korpus | kubectl apply -f -

# server only
kustomize build manifests/server | kubectl apply -f -
```

Edit `manifests/base/configmap.yaml` to set your Git repository and token before applying.
