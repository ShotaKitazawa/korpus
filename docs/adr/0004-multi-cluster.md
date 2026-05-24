# ADR-0004: 複数クラスタサポート

## Status

Accepted

## Context

現在の korpus は単一クラスタのみを対象としており、バックアップデーモンも server も
1クラスタ分の設定しか持てない。複数クラスタを運用する環境では、クラスタごとに
独立したデプロイが必要なうえ、server で複数クラスタのリソースを横断して
クエリする手段がない。

要件:
- 複数クラスタのバックアップを管理したい
- server のビューアで複数クラスタのリソースを一元的に参照・クエリしたい
- Git レイアウトは「クラスタごとに別リポジトリ」「1リポジトリ内のサブディレクトリ」
  の両方を選択できるようにしたい

## Decision

### バックアップデーモン (korpus) ― 変更なし

1デーモン = 1クラスタの設計を維持する。複数クラスタをバックアップするには
複数インスタンスをデプロイする。デーモンの設定スキーマ・動作は変更しない。

### server ― クラスタ概念の導入（破壊的変更あり）

#### 設定スキーマ

`git:` トップレベルキーを廃止し、`server.clusters` リストに移行する。
各クラスタエントリは `name`（識別子）と `git`（既存の git セクションと同構造）を持つ。

```yaml
server:
  addr: ":8080"
  pullInterval: "10m"
  clusters:
    - name: prod
      git:
        repo: https://github.com/org/k8s-prod.git
        branch: main
        token: ${PROD_GIT_TOKEN}
        author:
          name: korpus-bot
          email: korpus@example.com
    - name: staging
      git:
        repo: https://github.com/org/k8s-all.git
        branch: main
        subDir: staging
        token: ${STAGING_GIT_TOKEN}
        author:
          name: korpus-bot
          email: korpus@example.com
  index:
    fields:
      - metadata.labels
      - metadata.creationTimestamp
```

`subDir` を使うことで「別リポジトリ」「同一リポジトリ内サブディレクトリ」
のどちらのレイアウトも同じ設定モデルで表現できる。

旧来の `git:` トップレベルは起動時エラーとする。

#### インデックス設計: クラスタごとの独立インデックス

`ResourceMeta` に `Cluster string` フィールドを追加する。

`Index` はクラスタごとに独立したインスタンスを持ち、server は
`map[clusterName]*Index` で管理する。

- 各クラスタのインデックスは独立したゴルーチンで pull/rebuild される
- クロスクラスタクエリ時はすべてのクラスタインデックスを fan-out して結果をマージ
- `cluster` パラメータを指定した場合は該当クラスタのインデックスのみを参照

この設計により、クラスタ間の pull タイミングが独立し、
1クラスタの git 障害が他クラスタのインデックス更新をブロックしない。

#### Pull ループ

現在のシングルゴルーチン pull ループを廃止し、クラスタごとに
独立した pull ゴルーチンを起動する。pull 間隔は `server.pullInterval` で統一する。

pull 失敗時の再クローン動作（ADR-0002 の既存トレードオフ）は引き続き各クラスタで独立して適用する。

#### API 変更

`cluster` を新たなフィルタ次元として追加する。

| エンドポイント | 変更内容 |
|---|---|
| `GET /api/clusters` | **新規** クラスタ名一覧を返す |
| `GET /api/namespaces` | `cluster=` パラメータ追加（省略 = 全クラスタ） |
| `GET /api/resources` | `cluster=` パラメータ追加（省略 = 全クラスタ） |
| `GET /api/resources/{kind}/{namespace}/{name}` | **廃止** → 下記に置換 |
| `GET /api/resources/{cluster}/{kind}/{namespace}/{name}` | **新規** クラスタをパスに含める |
| `GET /api/query` | `cluster=` パラメータ追加（省略 = 全クラスタ） |

`cluster=` が省略または空文字の場合はすべてのクラスタを対象とする。
レスポンスの `ResourceMeta` には `cluster` フィールドが含まれる。

`GET /api/resources/{kind}/{namespace}/{name}` は1リソースを特定するのに
クラスタが必要なため、クラスタをパスに含める形に変更する。

#### MCP 変更

- `list_namespaces`・`list_resources`・`get_resource`・`query_resources` に
  `cluster` パラメータ（省略可能、省略 = 全クラスタ）を追加する
- `list_clusters` ツールを新規追加する

#### フロントエンド変更

- 既存のナビゲーション（Namespace 選択）の上位に Cluster 選択を追加する
- `selectedCluster = ""` が「全クラスタ」を意味する
- リソース詳細の URL は `/resources/{cluster}/{kind}/{namespace}/{name}` に変更する

## Consequences

### 受け入れるトレードオフ

- **破壊的変更**: 旧 `git:` トップレベルを持つ server 設定は起動不可になる。
  移行は設定ファイルの編集のみで完結するが、ゼロダウンタイムのオンラインマイグレーションはない
- **クロスクラスタクエリのコスト**: `cluster=` 省略時はすべてのクラスタインデックスを
  直列/並列にスキャンするため、クラスタ数に比例してレスポンスが遅くなりうる。
  MCP ツール利用時は `cluster` パラメータを指定することを推奨する
- **pull ゴルーチンの増加**: クラスタ数に比例してゴルーチンが増える。
  現実的なクラスタ数（< 20）では問題にならない想定
