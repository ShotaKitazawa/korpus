# ADR-0003: リソースクエリ言語とインデックス戦略

## Status

Accepted

## Context

server コンポーネント（ADR-0002）では、バックアップ済み K8s リソースを検索する
手段が必要になった。当初の実装は kind / name / namespace / labels の 4 フィールドに
対する部分文字列マッチのみで、任意フィールドへのクエリができなかった。

検討した要件:
- `spec.nodeName` や `spec.replicas` など、任意フィールドの値でフィルタしたい
- AI ツール（MCP 経由）から構造化クエリを投げたい
- メモリ消費を抑えたい（クラスタ規模によっては数千リソース）

## Decision

### クエリ言語: CEL（Common Expression Language）

以下の選択肢を比較した。

| 選択肢 | 理由 |
|---|---|
| Field Selector | K8s ネイティブだが等値・不等値のみ。数値比較・`contains` が書けない |
| JSONPath | `k8s.io/client-go/util/jsonpath` は既存依存だが、filter 式が非公式サポートで不完全 |
| jq | 表現力は高いが K8s エコシステム外。外部ライブラリが必要 |
| **CEL** | K8s 1.26+ で ValidatingAdmissionPolicy・CRD validation に採用。現在の K8s 標準クエリ言語。型安全・比較・論理演算すべて対応 |

CEL を採用する。クエリ例:

```
object.spec.replicas > 1
object.metadata.labels["app"] == "nginx" && object.spec.nodeName.startsWith("worker")
object.status.phase == "Running"
```

### インデックス戦略: ハイブリッド（設定可能フィールドインデックス + lazy disk load）

以下の 3 案を比較した。

| 案 | 内容 | 問題点 |
|---|---|---|
| A: 都度 disk load | クエリのたびに全件 ReadFile | O(n) I/O で MCP 利用時に顕著に遅い |
| B: フルドキュメント in-memory | 全 YAML を `map[string]any` で保持 | 5,000 件 × 5KB ≈ 25MB。メモリ消費が大きい |
| **C: ハイブリッド** | 設定フィールドをインデックス化。未インデックスフィールドは lazy load | メモリを抑えつつ頻用フィールドのクエリを高速化 |

**Option C を採用する。**

`kind` / `name` / `namespace` は API の pre-filter パラメータと対応するフィールドとして
常にインデックスされる（設定不要）。それ以外のフィールドはすべて `index.fields` で制御する。

`index.fields` のデフォルト値:

```yaml
server:
  index:
    fields:
      # metadata.labels を削除するとラベルクエリで disk I/O が発生する
      - metadata.labels
      - metadata.creationTimestamp
```

ユーザーはデフォルトを上書きして任意のフィールドを追加できる:

```yaml
server:
  index:
    fields:
      - metadata.labels
      - metadata.creationTimestamp
      - spec.nodeName
      - spec.replicas
      - status.phase
```

CEL 評価時のフォールバック戦略:
1. インデックス済みフィールドで CEL を評価する
2. CEL が "no such key" エラーを返した場合（参照フィールドが未インデックス）、
   ディスクからフルドキュメントを読み込んで再評価する

これにより AST 解析なしでインデックスヒット／フォールバックを自動判定できる。

### API 設計

CEL クエリは既存の `GET /api/resources`（メタデータ一覧）と分離し、
専用エンドポイント `GET /api/query` を設ける。

```
GET /api/query?kind=Pod&namespace=default&q=object.spec.nodeName=="worker1"
```

| パラメータ | 必須 | 説明 |
|---|---|---|
| `kind` | **必須** | インデックスのスキャン対象を決定する。意図しない全件スキャンを防ぐ |
| `namespace` | 任意 | 指定することで lazy-load fallback の disk I/O スコープを namespace 内に限定できる。指定なし = 全 namespace |
| `q` | 任意 | CEL 式。省略時は kind/namespace フィルタのみ |

`name` は独立パラメータとして持たない。1 件特定が目的の場合は
`GET /api/resources/{kind}/{namespace}/{name}` を使う。

`namespace` を CEL 式内（`object.metadata.namespace == "x"`）ではなくパラメータで
指定すべき理由: CEL 式に含めると、未インデックスフィールドへのクエリで
namespace pre-filter が効かず、kind 内全リソースへの disk I/O が発生しうる。

MCP ツール `query_resources(kind, namespace?, expr)` も同じ仕様で追加する。

### インデックスのライフタイム

インデックス全体を git pull 成功ごとに `index.Build()` で**全件アトミック再構築**する。
個別フィールドの TTL は存在しない。staleness は `server.pullInterval` と同一であり、
ADR-0002 で受け入れ済みのトレードオフ。

CEL コンパイル済みプログラムは式文字列をキーに `sync.Map` でキャッシュする。
インデックス再構築とは独立しており TTL なし（同一式は同一結果）。

## Consequences

### 変更

- `internal/config`: `ServerConfig` に `Index IndexConfig` フィールド追加。
  `IndexConfig.Fields []string` でインデックス対象フィールドを指定する。
  未設定時のデフォルト値は `[metadata.labels, metadata.creationTimestamp]`
- `internal/index`: `ResourceMeta` に `IndexedFields map[string]any` 追加。
  `Build()` で設定フィールドを抽出。`Search()` を廃止し `Query(kind, namespace, expr)` を新設
- `cmd/server/main.go`: `GET /api/query` エンドポイント追加。`/api/search` は廃止。
  MCP ツール `query_resources` 追加
- `go.mod`: `google.golang.org/cel-go` 追加

### 受け入れるトレードオフ

- `kind` 必須のため、全リソース横断クエリは kind ごとに複数回呼び出す必要がある
- 未インデックスフィールドへのクエリは `namespace` 未指定時に kind 内全件の disk I/O が発生しうる。`namespace` 指定を推奨する
- 配列フィールド（`spec.containers[*].image` 等）は初期実装ではインデックス対象外とし、
  lazy load に委ねる
