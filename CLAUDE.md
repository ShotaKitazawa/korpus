# korpus

## Overview

Two binaries:
- **korpus** — K8s backup daemon (`kubectl get -oyaml` → git). Strips runtime fields so only meaningful changes appear in git history.
- **server** — Read-only viewer that git pulls the backup repo on a timer, serves a React SPA, REST API, and MCP server over HTTP SSE.

## Commands

**All commands must be run via `mise run <task>`.** Never invoke `go`, `pnpm`, `goimports`, `oxfmt`, or any other tool directly — always use the mise task equivalents below.
All `mise run` tasks may be executed without human confirmation.

Key tasks:
- `mise run build` — compile all Go packages
- `mise run test` — run all Go tests
- `mise run format` — run goimports (Go)
- `mise run format-frontend` — run oxfmt (TypeScript/TSX)
- `mise run generate` — regenerate ogen + openapi-typescript artifacts (run after editing `openapi.yaml`)
- `mise run ci` — full CI pipeline (backend + frontend)
- `mise run pre-merge` — generate + format + ci

**Workflow for schema changes:**
1. Edit `openapi.yaml`
2. `mise run generate`
3. Implement handlers / update frontend
4. `mise run format-frontend` (after writing TSX — oxfmt may reformat)
5. `mise run ci`
- `mise run dev` — start server (air hot-reload) + frontend (Vite HMR) in parallel

## Architecture

### korpus (backup daemon)
- **dynamic client + API discovery only** — no typed `clientset`. K8s-version-independent.
- `backup.rules[]` is the single place for all exclusion config. `resource: "*"` applies to all resources. `excludeFields` is always additive (all matching rules are unioned). See `docs/configuration.md`.
- Built-in exclude list is in `internal/defaults/excludes.go`.
- Churn analysis (`internal/churn`) uses `exec.Command("git", ...)` — not go-git's log API.

### server
- Git pull loop via `time.Ticker` (cfg.Server.PullInterval). On pull failure: re-clone.
- In-memory index rebuilt after every pull (`internal/index`).
- Frontend bundled via `//go:embed all:frontend/dist` — build with `mise run ci-build-frontend` first.
- MCP transport: HTTP SSE only (`mark3labs/mcp-go`).

### API スキーマ (OpenAPI-first)
`openapi.yaml` が single source of truth。変更フロー:
1. `openapi.yaml` を編集
2. `mise run generate` → `internal/api/` (ogen) と `src/gen/api.d.ts` (openapi-typescript) を再生成
3. `cmd/server/handler.go` にハンドラ実装を追加/修正

**生成ファイルを直接編集しない。**

### config envsubst
`config.yaml` supports `${VAR}` in all fields. Undefined variables cause a startup error.
Example: `git.token: ${GIT_TOKEN}`.

## Testing

- Go: standard `testing` package + testify. Real temp dirs, local bare git repos, fake K8s clients — no external services.
- Frontend: Vitest + @testing-library/react.
