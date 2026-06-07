# ADR-0005: 履歴・変動分析 API の全面刷新

## Status

Accepted

## Context

ADR-0002 で導入した server コンポーネントは、現時点のリソース一覧とその CEL クエリに
特化した設計だった。その後の運用で以下の要件が顕在化した。

- **過去状態の参照**: 「昨日の 15 時時点での Pod 一覧を知りたい」のような
  時刻指定スナップショットが取れない
- **リソース別変更履歴**: あるリソースがいつ・どのように変わったかをたどれない
- **変動頻度ランキング**: どのリソースが最も頻繁に変更されているかを把握できない
- **フィールド単位の変動分析**: 変更が多い場合に「どのフィールドが変わっているか」を
  特定できない
- **AI エージェント向けの操作性**: MCP ツールが `list_resources` / `query_resources`
  の 2 本しかなく、LLM が自律的にクラスタの変化を探索するには不十分

また、変動分析の実装（`internal/churn`）が `exec.Command("git", ...)` に依存しており、
go-git と二重管理になっていた。

## Decision

### 新パッケージ `internal/gitindex`

go-git ベースの 3 つのインデックスを新設し、起動時および pull 後に再構築する。

#### `CommitIndex`（`commit.go`）

全コミットを `CommitRef{Time, SHA}` の時刻昇順スライスとして保持する。
`FindBefore(t time.Time)` でバイナリサーチし、`datetime` 指定スナップショットの
git tree walk の起点コミットを O(log n) で取得する。

#### `ChangeIndex`（`change.go`）

コミットの差分から `ChangeEvent` を生成してスライスに保持する。

```go
type ChangeEvent struct {
    Timestamp  time.Time
    SHA, Cluster, Group, Kind, Namespace, Name string
    ChangeType ChangeType  // "added" | "modified" | "deleted"
}
```

構築時のファイルパス解析には `ParseResourcePath` を使用する。

```
group/version/resource/name.yaml                           (cluster-scoped)
group/version/namespaces/<ns>/resource/name.yaml           (namespaced)
```

`retentionDays`（設定の `historyDays`、デフォルト 30）を超えた古いコミットは
打ち切り、インデックスサイズを一定に保つ。

`Query()` で since/until・cluster/group/kind/namespace/name・changeType による
フィルタと limit/offset ページングを提供する。

`Volatility()` は `ChangeEvent` を resource 単位で GROUP BY カウントし、
`ratio = count / maxCommits` と `VolatilityEntry` のスライスを返す。

#### `FieldVolatility`（`fields.go`）

指定リソースの `ChangeEvent` を最大 `maxCommits` 件遡り、各コミットの
before/after YAML blob を go-git から直接読み取って再帰 diff する。
変更されたフィールドのドット区切りパス（例: `spec.replicas`）を
出現回数と ratio でランク付けして返す。

#### go-git の実装上の注意点

- `change.Files()` が返す `object.File.Name` はベース名のみ（ゴ-git の仕様）。
  パス全体は `change.From.Name` / `change.To.Name` から取得する。
- `repo.Log(nil)` はパニックする。常に `&git.LogOptions{Order: git.LogOrderCommitterTime}` を渡す。

### `internal/churn` の削除

`ChangeIndex.Volatility` が `churn` の機能をすべて包含するため削除する。
`cmd/korpus/main.go` の `churn.Analyze()` 呼び出しも除去する。

### API の全面刷新

旧エンドポイント群（`/api/resources`, `/api/query`, `/api/churn`）を廃止し、
以下に置き換える（破壊的変更）。

| エンドポイント | ロジック |
|---|---|
| `GET /api/groups` | `idx.Groups()` → `[]string` |
| `GET /api/kinds?group=` | `idx.Kinds(group)` → `KindInfo[]` |
| `GET /api/snapshot` datetime なし | `idx.Query(...)` + CEL（現在状態） |
| `GET /api/snapshot` datetime あり | `CommitIndex.FindBefore` → git tree walk。`cel=` と併用不可（400） |
| `GET /api/resource` | `idx.Get(...)` → ファイル読み取り |
| `GET /api/history` | `ChangeIndex.Query(...)` |
| `GET /api/diff` | git の 2 コミット間で blob を読み before/after 文字列を返す |
| `GET /api/volatility` | `ChangeIndex.Volatility(...)` → threshold フィルタ → ratio 降順 |
| `GET /api/volatility/fields` | `FieldVolatility(repo, events, subDir, commits)` |

`group` を新たなフィルタ次元として全エンドポイントに追加する。
K8s の core group（空文字）は git ファイルパス上では `"core"` として扱われるため、
API でも `group=core` で参照する。

### `ClusterState` への `commitIdx`・`changeIdx` 追加

```go
type ClusterState struct {
    idx       *index.Index
    commitIdx *gitindex.CommitIndex
    changeIdx *gitindex.ChangeIndex
    subDir    string
    // ...
}
```

pull 成功ごとに `rebuildIndexes()` で 3 インデックスを一括再構築する。

### MCP ツールの拡充

旧 5 本（`list_clusters`, `list_namespaces`, `list_resources`, `get_resource`, `query_resources`）を廃止し、以下 10 本に置き換える。

| ツール名 | 対応 API |
|---|---|
| `list_clusters` | `/api/clusters` |
| `list_groups` | `/api/groups` |
| `list_kinds` | `/api/kinds` |
| `list_namespaces` | `/api/namespaces` |
| `get_resource` | `/api/resource` |
| `get_snapshot` | `/api/snapshot` |
| `get_history` | `/api/history` |
| `get_diff` | `/api/diff` |
| `get_volatility` | `/api/volatility` |
| `get_volatility_fields` | `/api/volatility/fields` |

### フロントエンド

- `SnapshotResource`（旧 `ResourceMeta`）・`ChangeEvent`・`VolatilityEntry`・`KindInfo`・`DiffResult` に型を更新
- `App.tsx`: `/api/snapshot` に統合、`group` フィルタ追加、URL sync に `selGroup`
- `KindSelect.tsx`: `KindInfo[]` を受け取り `group/kind` 形式で表示
- `ResourceDetail.tsx`: History → `/api/history`、Diff → `/api/diff`（クエリパラメータ方式）
- `ChurnView.tsx`: `/api/churn` → `/api/volatility`

## Consequences

### 変更ファイル

| ファイル / パッケージ | 内容 |
|---|---|
| `internal/gitindex/` | 新規パッケージ（commit.go / change.go / fields.go） |
| `internal/churn/` | **削除**（gitindex に置き換え） |
| `internal/config/config.go` | `HistoryDays int` 追加（デフォルト 30） |
| `internal/gitclient/gitclient.go` | `Repo() *git.Repository` 追加 |
| `internal/index/index.go` | `Groups()` / `Kinds(group)` 追加、`Build` の subDir 引数追加 |
| `openapi.yaml` | 全書き直し（新スキーマ・エンドポイント） |
| `cmd/server/handler.go` | 全書き直し |
| `cmd/server/main.go` | `ClusterState` 拡張、MCP ツール全書き直し |
| `cmd/server/frontend/src/` | 型・API 呼び出しを新仕様に更新 |
| `cmd/korpus/main.go` | `churn.Analyze()` 呼び出し削除 |

### 受け入れるトレードオフ

- **破壊的変更**: 旧 `/api/resources` / `/api/query` / `/api/churn` は廃止される。
  既存のクライアント・MCP 接続は再設定が必要
- **ChangeIndex のメモリ消費**: `retentionDays` 内の全コミット差分をメモリに保持する。
  変更頻度が高いクラスタ・長い `retentionDays` では増大する。デフォルト 30 日で
  数千コミット規模なら数十 MB 程度の想定
- **`FieldVolatility` のオンデマンドコスト**: git blob を都度読み取るため、
  `commits` 数が多い場合にレスポンスが遅くなりうる。キャッシュはしない
- **`cel=` と `datetime=` の排他**: datetime 指定時に git tree walk するため、
  インメモリ CEL 評価が使えない。将来的には tree walk + CEL 評価の組み合わせも
  実装可能だが、初期実装では 400 エラーとする
