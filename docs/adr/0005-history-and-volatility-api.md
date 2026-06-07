# ADR-0005: server API スキーマの全面刷新

## Status

Accepted

## Context

ADR-0002・0003 で設計した server API は「現時点のリソース一覧と CEL クエリ」に特化しており、以下の要件を満たせなかった。

- **時刻指定スナップショット**: 「あの時点でのクラスタ状態を知りたい」に答えられない
- **リソース変更履歴**: あるリソースがいつ・どう変わったかをたどれない
- **変動頻度分析**: どのリソースが頻繁に変わっているかを把握できない
- **フィールド単位の変動特定**: 変更が多いリソースで「何のフィールドが変わっているか」を絞り込めない

AI エージェント（MCP）が自律的にクラスタの変化を探索するには、これらすべてが必要である。

また既存の `internal/churn` は `exec.Command("git", ...)` に依存しており、同じリポジトリを扱う go-git と二重管理になっていた。

## Decision

**API スキーマを全面刷新し、現在・過去・変化の 3 軸でクラスタ状態を問い合わせられる設計にする。**

### 新 API エンドポイント

旧 `/api/resources`・`/api/query`・`/api/churn` を廃止し、以下に置き換える。

| エンドポイント | 役割 |
|---|---|
| `GET /api/groups` | API グループ一覧 |
| `GET /api/kinds` | リソース種別一覧（`group` で絞り込み可） |
| `GET /api/snapshot` | リソース一覧。`datetime` 省略 = 現在、指定 = git 履歴から再現。`cel=` は `datetime` と排他 |
| `GET /api/resource` | 単一リソースの現在 YAML |
| `GET /api/history` | リソースの変更イベント一覧（since/until でフィルタ） |
| `GET /api/diff` | 2 コミット間の before/after YAML |
| `GET /api/volatility` | 変動頻度ランキング（`threshold` でフィルタ） |
| `GET /api/volatility/fields` | リソース内フィールド単位の変動頻度 |

`group` を全エンドポイントの新フィルタ次元として追加する。K8s core group（空文字）はファイルパス上の慣例に合わせ `"core"` として扱う。

### git 履歴インデックスの導入

時刻指定スナップショット・履歴・変動分析を実現するために、pull 成功ごとに再構築するインメモリインデックスを 2 つ追加する。

**CommitIndex**: 全コミットを時刻昇順に保持し、`datetime` 指定時に対応コミットをバイナリサーチで取得する。

**ChangeIndex**: コミット差分を `ChangeEvent{timestamp, sha, group, kind, namespace, name, changeType}` として保持する。`/api/history` のクエリと `/api/volatility` の集計に使用する。保持期間は `historyDays`（デフォルト 30 日）で制限する。

フィールド単位の分析（`/api/volatility/fields`）はキャッシュせず、リクエスト時に git blob を読んで都度計算する。

### `internal/churn` の廃止

`ChangeIndex` が変動分析機能を包含するため削除する。

### MCP ツールの更新

旧 5 本から 10 本に拡充し、新 API エンドポイントとの 1 対 1 対応とする：
`list_clusters` / `list_groups` / `list_kinds` / `list_namespaces` / `get_resource` / `get_snapshot` / `get_history` / `get_diff` / `get_volatility` / `get_volatility_fields`

## Consequences

### 受け入れるトレードオフ

- **破壊的変更**: 旧エンドポイントを廃止するため、既存クライアントは再設定が必要
- **ChangeIndex のメモリ消費**: 保持期間内のコミット差分をすべてメモリに乗せる。`historyDays` と変更頻度に比例するが、デフォルト 30 日・数千コミット規模で数十 MB 程度の想定
- **`cel=` と `datetime=` の排他**: `datetime` 指定時は git tree walk を使うため、インメモリ CEL 評価と組み合わせられない
- **`/api/volatility/fields` のレイテンシ**: キャッシュしないため、`commits` が多い場合にレスポンスが遅くなりうる
