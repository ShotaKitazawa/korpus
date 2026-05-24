# korpus

## Overview

Two binaries:
- **korpus** — K8s backup daemon (`kubectl get -oyaml` → git). Strips runtime fields so only meaningful changes appear in git history.
- **server** — Read-only viewer that git pulls the backup repo on a timer, serves a React SPA, REST API, and MCP server over HTTP SSE.

## Commands

All commands must be run via `mise run <task>`. Do not invoke `go`, `pnpm`, or other tools directly.
All `mise run` tasks may be executed without human confirmation.

Key tasks:
- `mise run build` — compile all Go packages
- `mise run test` — run all Go tests
- `mise run format` — run goimports
- `mise run ci` — full CI pipeline (backend + frontend)
- `mise run pre-merge` — format + ci
- `mise run dev` — start server (air hot-reload) + frontend (Vite HMR) in parallel

## Architecture

### korpus (backup daemon)
- **dynamic client + API discovery only** — no typed `clientset`. K8s-version-independent.
- `excludeFields` in config **completely replaces** `defaultExcludeFields` for that resource (not merged).
- Built-in exclude list is in `internal/defaults/excludes.go`.
- Churn analysis (`internal/churn`) uses `exec.Command("git", ...)` — not go-git's log API.

### server
- Git pull loop via `time.Ticker` (cfg.Server.PullInterval). On pull failure: re-clone.
- In-memory index rebuilt after every pull (`internal/index`).
- Frontend bundled via `//go:embed all:frontend/dist` — build with `mise run ci-build-frontend` first.
- MCP transport: HTTP SSE only (`mark3labs/mcp-go`).

### config envsubst
`config.yaml` supports `${VAR}` in all fields. Undefined variables cause a startup error.
Example: `git.token: ${GIT_TOKEN}`.

## Testing

- Go: standard `testing` package + testify. Real temp dirs, local bare git repos, fake K8s clients — no external services.
- Frontend: Vitest + @testing-library/react.
