# ADR-0002: server コンポーネントの追加

## Status

Accepted

## Context

korpus は K8s リソースを git リポジトリへバックアップするデーモンだが、
バックアップ内容を閲覧・検索する手段がなかった。また AI ツールから
バックアップデータへアクセスする手段も存在しなかった。

## Decision

`server` バイナリを追加する。backup repo を N 分ごとに git pull し、
以下の 3 つの責務を 1 プロセスで担う。

1. **フロントエンド提供**: React + Vite で構築した SPA を embed.FS で同梱しサーブ
2. **インデックス管理**: git pull 後に YAML を走査し in-memory index を更新
3. **MCP サーバ**: HTTP SSE トランスポートで K8s リソースをクエリ可能にする

### 採用しない方式

- **GitHub Actions によるインデックス生成**: backup repo 側にワークフローの追加が必要になり、ユーザーへの導入コストが高い
- **Informer ベース**: ADR-0001 と同じ理由（このクラスタ規模では List-based polling で十分）
- **stdio MCP**: HTTP SSE のみ対応する。将来必要になった段階で追加する

## Consequences

### 変更

- `internal/config`: envsubst 対応（全フィールドで `${VAR}` を許容）。`GitConfig.Token()` メソッドを廃止し `Token string` フィールドに移行（破壊的変更）
- `manifests/`: base + overlay 構成に再編。korpus のみ・server のみ・両方の 3 通りの適用が可能
- `Dockerfile` → `Dockerfile.korpus` に改名。`Dockerfile.server` を新設

### 受け入れるトレードオフ

- config.yaml に `git.token: ${GIT_TOKEN}` の明示的な記述が必要になる（既存ユーザーへの破壊的変更）
- server は backup repo の N 分遅れのスナップショットを提供する（pull 間隔分の staleness）
