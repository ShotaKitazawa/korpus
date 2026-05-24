# ADR-0001: ワンショット実行から常駐プロセスへの移行

## Status

Accepted

## Context

### 現行アーキテクチャ

korpus は Kubernetes CronJob として 10 分ごとに起動し、全リソースを List → sanitize → git commit+push して終了するワンショットプロセスである。

### 移行の動機

以下の 3 点を理由に常駐プロセス化を決定した。

**1. 決められた時刻での実行（wall-clock scheduling）**
Kubernetes CronJob も cron 式を使うが、Pod 起動オーバーヘッドにより実際の実行時刻がスケジュール時刻からずれることがある。常駐プロセス内で cron スケジューラを使うことで、プロセス起動タイミングに依存せず決められた時刻に実行できる。

**2. ヘルスチェック・メトリクスエンドポイントの提供**
HTTP エンドポイントを常時公開することで、livenessProbe による自動再起動と Prometheus 等による継続的な監視（バックアップ成否・実行時間等）が可能になる。CronJob のジョブ履歴は代替にならない。

**3. コンテナ起動コストの排除**
CronJob ではポーリングのたびにコンテナ起動・git clone・TLS ハンドシェイクが発生する。常駐プロセスではこれらの初期化コストが 1 回で済む。

## Decision

**常駐プロセス化する。ただし、リソース取得には client-go Informer を使用しない。**

現行と同様の `discovery.ListPreferredResources` + `dynamic.Interface.List` によるポーリングを、プロセス内 cron スケジューラでループさせる実装とする。

### client-go Informer を採用しない理由

Informer はオブジェクトをプロセスのメモリ上にキャッシュし続ける設計であり、クラスタ規模に比例してメモリ消費が増大する。また標準 Informer にはキャッシュ退避機構がなく、メモリ上限を超えないよう動作させることは構造上困難である。

対象クラスタの規模は数ノード・数十 namespace・Pod 数百未満程度であり、分単位の List ポーリングによる API 負荷は無視できる水準である。List ベースのポーリングで機能・性能の要件を満たせるため、Informer が生む複雑性を受け入れる必要がない。

将来クラスタ規模が拡大し List ポーリングの API 負荷が問題として顕在化した段階で、Informer への移行を改めて検討する。

## Consequences

### 変更が必要なもの

- `main()` の線形フローをプロセス内 cron スケジューラによるループに置き換える
- エラーを `os.Exit` で終了する代わりに、致命的エラーと再試行可能エラーを分類する
- SIGTERM/SIGINT に対する graceful shutdown を実装する
- SIGKILL 等による異常終了後の tmpDir・git worktree 残骸を起動時にクリアする
- HTTP サーバ（ヘルスチェック・メトリクス）を goroutine で起動する
- Kubernetes マニフェストを CronJob から Deployment に変更する（`strategy: Recreate`、`replicas: 1`）

### 受け入れるトレードオフ

- CronJob が提供していた K8s ネイティブのジョブ履歴・Events による実行監査が失われる（メトリクスで代替）
- `replicas: 1` + `strategy: Recreate` を運用上の制約として維持する必要がある
