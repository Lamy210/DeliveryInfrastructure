以下は、これまでに検討したアーキテクチャ（**Cloud Run（Go）＋ Cloudflare（Workers/R2/Queues）＋ Neon（Serverless Postgres）＋ Upstash（Serverless Redis）＋（必要に応じて）Karrio コンテナ**）を反映した**システム要件定義書**です。
※個人／小規模運用を想定しつつ、将来のスケールに耐える前提で数値・根拠を明示しています。

---

# タスク分析

* 国内外配送を**最小コスト**で運用し、**需要増に自動追随**できること。
* **Go製API**を中核に、配送・追跡・通知・決済・住所検証・為替・関税計算を**マイクロ化**。
* 常時稼働が必要な処理以外は**サーバレス（スケールtoゼロ）**へ寄せて固定費を最小化。

# 計画・コーディング（実現方針の要約）

* 同期系（REST/GraphQL）は **Cloud Run（Go, 高並行）**。
* 非同期系（追跡ポーリング／通知キュー／PDF生成）は **Cloudflare Queues + Workers**。
* ストレージは **R2（イグレス無料）** に集約。
* データは **Neon Postgres（自動サスペンド）** と **Upstash Redis（従量）**。
* キャリア連携は **Karrio**（自社ホスト）＋各社API（DHL／ヤマト／17TRACK 等）。

---

# 1. システム概要

## 1.1 目的と背景

* 国内（ヤマト）および海外（DHL）配送を、**単一のバックエンド**で扱い、**レート見積→ラベル発行→集荷→追跡→通知**を自動化する。
* **個人〜小規模**が現実的に維持できる**最小コスト運用**を第一目的とし、将来の事業拡張に備える。

## 1.2 対象ユーザー

* 運用者（個人事業主／小規模チームの管理者・CS）
* 購買ユーザー（EC顧客）
* 外部連携（EC/OMS/WMS など）の技術担当

## 1.3 提供価値

* 複数キャリアを**統一API/UI**で運用
* 国際配送時の**関税・税の事前提示（任意連携）**
* **通知（SES/SMS）**と**追跡**の一元化
* **コスパ最優先**のサーバレス構成で**アイドル時コストを極小化**

---

# 2. 機能要件

## 2.1 必須機能一覧

* ${レート見積}: キャリア横断の送料・ETA取得（DHL/ヤマト）
* ${ラベル発行}: 出荷ラベルPDF生成・保管・署名URL提供（R2 配信）
* ${集荷依頼}: DHL/ヤマトへのピックアップスケジュール
* ${追跡統合}: 追跡番号登録、ステータスWebhook受信（17TRACK/Karrio Trackers）
* ${住所検証}: 国内＝日本郵便API、海外＝HERE Geocoding & Search
* ${通知}: 出荷／配達予定／例外／配達完了のメール（SES）＋重要イベントSMS（Twilio）
* ${決済連動}: Stripe（カード／Wallet／コンビニ）による支払確認フック
* ${為替換算}: exchangerate.host による通貨換算（キャッシュ）
* ${管理UI}: Karrioダッシュボードによるオペレーション（ラベル、追跡、ログ）
* ${イベント監査}: API呼び出しログ、Webhook履歴、ジョブ履歴

## 2.2 オプション機能

* ${関税・税計算}: Zonos / SimplyDuty / Avalara のいずれか
* ${返品ラベル発行}: 指定ルールで返送ラベル生成
* ${DPS/サンクションチェック}: OFAC/BIS CSL のスクリーン（手動承認フロー）
* ${在庫連携}: Shopify/Odoo/NetSuite 等の在庫引当・戻入

## 2.3 ユーザーインターフェース要件

* 管理UI：Karrio ダッシュボード（Web、PCブラウザ最適化）
* 購買UI：既存EC（外部）または簡易フロント（Workers/Pages）で

  * 住所入力：郵便番号→補完（国内）、海外はサジェスト
  * 配送オプション比較：料金/ETA/関税税額（任意）を同時提示
  * 通知同意／連絡先入力（メール必須・SMS任意）

## 2.4 データ処理要件

* 入力検証：住所・氏名・電話・郵便番号（国別ルール）
* 変換：住所正規化（国内/海外）、重量・寸法単位統一（g/cm）
* 永続：注文／出荷／ラベル／追跡イベント（PIIは最小化しトークン化）
* 非同期：追跡ポーリング、PDF生成、関税計算、メール送信はキュー処理

---

# 3. 非機能要件（NFR）

## 3.1 パフォーマンス

* ${応答時間（P95）}: 300ms 以下（APIゲートウェイ；キャッシュヒット時 150ms 以下）
* ${スループット目標}: 200 req/s（短時間ピーク）／長時間平均 5 req/s
* ${コールドスタート目標}: Cloud Run 1秒未満（最小インスタンス=0、需要次第で1を検討）
* ${バッチ遅延}: 非同期ジョブのキュー待ち平均 < 2s、P95 < 10s

## 3.2 セキュリティ要件

* 通信：全経路 TLS（CF→Run、Run→各API、Webhook 受信）
* 認証/認可：OIDC（運用者）、API Key／HMAC（対外API）
* 秘匿情報：Secret Manager / Workers Secrets、KMS相当で暗号化保管
* データ保護：個人情報最小化、ラベルPDFは署名URL＋期限付与
* ログ：PIIマスキング、監査用イベントストア（改ざん防止策／WORM相当の運用）

## 3.3 可用性要件

* ${可用性}: 99.9%（月間ダウンタイム目安 < 43分）
* ${RPO/RTO}: RPO ≤ 15分、RTO ≤ 60分（Neon スナップショット＋R2/Backups）
* 障害時運用：Queue 残積のドレイン／リトライ（指数バックオフ）／手動再送

---

# 4. インフラ要件

## 4.1 サーバー構成（代表）

* ${サーバー種別/Compute}

  * **Cloud Run（Go）**：API Gateway/オーケストレーション

    * `vCPU: 0.5–1.0`、`Memory: 512–1024MiB`、`concurrency: 80–120`
  * **Cloud Run（Karrio コンテナ）** *or* **低価格VM**（常時稼働が必要な場合のみ）
  * **Cloudflare Workers**：軽量API、Webhook受信、署名URL生成
  * **Cloudflare Queues**：非同期実行のトリガ
* ${データ}

  * **Neon Serverless Postgres**：オートスケール/サスペンド、最小CU開始
  * **Upstash Redis**：短TTLキャッシュ、レート制限、軽キュー
* ${ストレージ}

  * **Cloudflare R2**：ラベルPDF/静的配信（イグレス無料）

## 4.2 ネットワーク構成

* 外縁：Cloudflare（DNS/WAF/CDN）→ Workers/Pages → Cloud Run（mTLS/HTTPS）
* egress：Run→外部API（DHL/ヤマト/17TRACK/HERE/SES/Twilio/Stripe/FX/Duties）
* Webhook：17TRACK/決済/関税計算→Workers 経由→Queues or Run
* 管理：運用者は OIDC でダッシュボードへ（IP allowlist オプション）

### 参考トポロジ（Mermaid）

```mermaid
flowchart LR
  CF[Cloudflare (DNS/WAF/CDN)] --> WK[Workers/Pages]
  WK --> CR[Cloud Run (Go API)]
  CR <--> PG[(Neon Postgres)]
  CR <--> RD[(Upstash Redis)]
  CR <--> R2[(R2 Object Storage)]
  WK --> Q[Cloudflare Queues]
  Q --> WK
  subgraph External APIs
    DHL[DHL API]:::e
    YMT[ヤマトB2 Cloud]:::e
    T17[17TRACK]:::e
    HERE[HERE Geo]:::e
    SES[AWS SES]:::e
    TWI[Twilio]:::e
    STR[Stripe]:::e
    FX[exchangerate.host]:::e
    DUT[Zonos/SimplyDuty]:::e
  end
  CR <--> DHL & YMT & T17 & HERE & SES & TWI & STR & FX & DUT
  classDef e fill:#f5f5f5,stroke:#aaa,stroke-width:1px;
```

## 4.3 ストレージ要件

* ラベルPDF/インボイス：R2（オブジェクト1–2MB、保存 13ヶ月）
* メタデータ：Postgres（取引・出荷・イベント）
* キャッシュ：Redis（TTL 1–10分、ホットキー対策）

## 4.4 バックアップ方針

* Postgres：Neon の自動スナップショット＋日次エクスポート（R2）
* 設定/Secrets：IaC（Terraform/Pulumi）とバージョン管理、Secrets は KMS 暗号化
* 監査ログ：R2へ週次アーカイブ、保存 13ヶ月

---

# 5. ライブラリ / フレームワーク / 言語

## 5.1 使用技術スタック

* 言語：Go ${1.22.x}
* Web FW：**Gin** *or* **chi**（軽量・高並行）
* データアクセス：`pgx` + `sqlc`（型安全）
* キャッシュ：Upstash Redis SDK（HTTP）
* メトリクス：`prometheus/client_golang`、ログは構造化（zap/logrus）
* プロファイリング：`pprof` ＋（任意）Parca Agent
* コンテナ：Distroless ベース（最小攻撃面）
* IaC：Terraform or Pulumi（GCP/Cloudflare/Neon/Upstash 設定）
* Karrio：公式コンテナ（自社ホスト）

## 5.2 バージョン要件（初期）

* Go 1.22 / Gin 1.10+ *or* chi v5 / pgx v5 / sqlc v1.27+
* Cloud Run（`asia-northeast1`）、Workers（Paid）、R2、Queues、Neon（最新安定）、Upstash（Free/入門プラン）

## 5.3 依存関係

* 外部API：DHL、ヤマトB2 Cloud、17TRACK、HERE、AWS SES、Twilio、Stripe、exchangerate.host、（任意）Zonos/SimplyDuty
* 認証/鍵：OIDC プロバイダ、各種 API キー／シークレット（Secret Manager/Workers Secrets）

---

# 6. その他

## 6.1 考慮事項

* **コスト最適化**：

  * Cloud Run `concurrency` 高設定 → インスタンス数削減
  * 非同期は **Queues/Workers** に徹底オフロード（$0.40/100万オペ級）
  * R2 で**イグレス無料**配信、Neon/Upstashで**アイドルコスト抑制**
* **スケール戦略**：

  * まずサーバレスを起点、P95/コストが閾値超なら**常時処理のみ** T2D/T2A/Gravitonへ移し**CUD/Savings**適用
* **セキュリティ**：PII 最小・期限付き署名URL・Webhook 署名検証・秘密鍵ローテーション

## 6.2 前提条件

* ドメイン取得・Cloudflare 管理
* 対象キャリアの利用契約（ヤマトは**個人事業主以上**）
* 決済・SMS・地理APIの利用規約順守（KYC 済み）

## 6.3 制約事項

* 一部API（ヤマト/DHL 等）は**サービス停止/レート制限**に依存
* Cloud Run の**リクエスト外 CPU 制限**（長時間処理はキュー必須）
* 個人運用のため**SLA はベンダ依存**、自前SLAはベストエフォート

---

## 技術選定の根拠（要約）

* **Cloud Run（Go）**：100ms 課金＋スケール to 0 → **アイドル時ほぼ ¥0**。Go は高速・低メモリで**コスパ良**。
* **Cloudflare（Workers/R2/Queues）**：**R2 イグレス無料**で配信コスト抑止、Queues が**超低単価**、Workers で軽量処理をエッジ実行。
* **Neon/Upstash**：サーバレス DB/Redis で**固定費→従量化**、小規模時の**総額最小**。
* **Karrio**：複数キャリアの差異吸収、**自社ホスト**で**運用コストと自由度を両立**。
* **Go エコシステム（Gin/chi, pgx/sqlc, distroless）**：**高効率＋攻撃面最小**で、**性能/セキュリティ/コスト**のバランスが最良。

---

## 運用手順（DB初期化・SQLテスト・Webhook受信）

### DB 初期化（Neon / ローカル Postgres 共通）
- `DATABASE_URL` を環境変数に設定（例: `postgres://user:pass@host:port/dbname`）
- スキーマ適用と拡張作成: `make db-init`
- 既存スキーマへの再適用（マイグレーション相当）: `make db-migrate`

### Docker で Postgres を起動（推奨ローカル開発）
- 初回のみ: `.env.example` を参照して `.env` を作成
  - 例: `cp .env.example .env`（必要に応じてパスワード等を編集）
- コンテナ起動: `make docker-up`
- 状態確認: `make docker-ps` / `make docker-logs`
- 接続文字列（例）: `DATABASE_URL=postgres://delivery:delivery@localhost:5433/delivery?sslmode=disable`
- スキーマ適用: `DATABASE_URL=... make db-init`
- テスト: `DATABASE_URL=... make sql-test`
- リセット（データ消去）: `make docker-reset`

### SQL スキーマテスト（v2仕様）
- インデックス・ユニーク制約・外部キーの `CASCADE / SET NULL` を検証
- 実行: `make db-test` または `make sql-test`（内部で `psql -v ON_ERROR_STOP=1 -f db/tests/test_schema.sql`）
- 注記: `db/tests/test_schema.sql` は Postgres 方言指定（`-- dialect: postgresql`）済みで `CREATE TEMPORARY TABLE` を使用

### Webhook 受信仕様（最小構成）
- エンドポイント: `POST /webhooks/{source}`（例: `dummy`, `karrio`）
- 署名検証: `HMAC-SHA256`（ヘッダ `X-Signature` に16進文字列、`sha256=`プリフィックス対応）
- シークレット: 環境変数 `DUMMY_WEBHOOK_SECRET` / `KARRIO_WEBHOOK_SECRET`
- 正常時: `tracking_events` にイベント挿入／`trackers.last_event_at` と `status` を更新
- 例（curl）:
  - `SECRET=...; BODY='{"code":"ABC123","status":"in_transit","occurred_at":"","location":{"country":"US"}}'`
  - `SIG=$(printf "%s" "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -binary | xxd -p -c 256)`
  - `curl -XPOST http://localhost:8080/webhooks/dummy -H "Content-Type: application/json" -H "X-Signature: $SIG" -d "$BODY"`

### API 実行時のDB接続（DATABASE_URL）
- APIは起動時に`DATABASE_URL`でPostgresへ接続し、`Ping`で疎通確認を行います。
 - 例: `export DATABASE_URL=postgres://delivery:delivery@localhost:5433/delivery`
- 起動: `PORT=8080 go run ./cmd/api`
- `DATABASE_URL`未設定や接続不可の場合は、起動時に明示的なエラーログで終了します（秘密情報はログに出力しません）。

#### 重複イベントの扱い（Idempotency）
- 以下が同一と判定されるイベントは、2回目以降の挿入をスキップ
  - `tracker_id` が一致（`code`で解決）
  - `occurred_at` が一致
  - `status` と `description` が一致（DBでは `COALESCE(..., ''::text)` により NULL は空文字同一として比較）
- 実装: ユニークインデックス `idx_tracking_events_dedupe`（`tracker_id, occurred_at, COALESCE(status,''), COALESCE(description,'')`）でDBレイヤーの重複排除を保証
- 同一イベントでも `trackers.status` と `last_event_at` の更新は必要に応じて維持（最新状態を反映）

#### エラー応答形式（JSON）
- 形式: `{"error": {"code": string, "message": string}}`
 - 例: `{"error": {"code": "invalid_signature_format", "message": "invalid signature format"}}`
- 主なエラーコード: `missing_signature`, `invalid_signature_format`, `signature_mismatch`, `invalid_json`, `invalid_request`, `invalid_occurred_at`, `db_error`

#### リクエストID（相関識別子）
- 全レスポンスに `X-Request-ID` を付与（未指定時は自動生成）
- ログとエラー応答の突合用に使用し、サポート時の調査を簡易化

#### プロバイダ正規化レイヤー（Normalizer）
- 目的: プロバイダ毎のペイロード差異（フィールド名や構造）を吸収し、共通の `TrackerEventRequest` にマッピング
- キーのフォールバック例:
  - `code`: `code` / `tracking_number` / `tracker_code` / `tracking_code` / `id`
  - `status`: `status` / `event.status` / `tracking_status`
  - `description`: `description` / `event.description` / `message` / `event.message`
  - `occurred_at`: `occurred_at` / `event.occurred_at` / `event_time` / `timestamp`
  - `location`: `location` / `event.location` / `address` / `place`（オブジェクトはそのままJSON格納）
- 対応: `dummy`/`karrio` は現状同一のデフォルト正規化で処理（将来的に専用実装に差し替え可能）

### Go テストの実行
- 単体テスト・統合テストをまとめて実行: `make go-test`
- DB接続が必要な統合テストは `DATABASE_URL` 未設定時に自動スキップ（出荷作成・追跡・Webhookなど）

### レート推定プロバイダの切替
- 環境変数 `RATE_PROVIDER` により `/rates` の推定ロジックを選択
- 指定例: `RATE_PROVIDER=karrio`（現状は `dummy` と等価なスタブ動作）

---

### 付録：初期キャパシティ＆SLO（推奨）

* 同時接続目安：`concurrency=100` × `min-instances=0`（需要時自動増）
* API SLO：可用性 99.9%、P95 300ms、エラー率 < 0.3%
* バックアップ：日次スナップ＋週次フル、保存 13ヶ月
* コストガード：月次上限アラート、単価変動（R2操作/Queuesオペ）監視、Budgets & Alerts 設定

---

必要なら、この要件に合わせた**Terraform/Pulumi テンプレート**、**Cloud Run 用 Dockerfile（distroless）**、**Workers/Queues/R2 初期化スクリプト**、**Neon/Upstash スキーマ＆接続テンプレ**、**SLO ダッシュボード（Grafana JSON）**をそのまま配布できる形でご用意します。
