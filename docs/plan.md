以下は、これまで合意したアーキテクチャ（Cloud Run〔Go〕＋ Cloudflare〔Workers/R2/Queues〕＋ Neon Postgres ＋ Upstash Redis ＋ Karrio コンテナ）を前提にした**システム詳細設計書**です。
※図はすべて**ASCII**で記述し、四角等の図形が崩れないよう固定幅で表示できる形式にしています。

────────────────────────────────────

# 1. システム構成図とコンポーネント間の連携フロー

## 1.1 物理/論理アーキテクチャ

```
+--------------------+         +---------------------------+
|  End Users (Web)   |  HTTPS  |  Cloudflare (DNS/WAF/CDN)|
+--------------------+-------> +---------------------------+
                                   |           |             \
                                   |           |              \
                                   v           v               v
                           +---------------+  +-------------------+
                           | Workers/Pages |  |   R2 Object Store |
                           +-------+-------+  +---------+---------+
                                   |                        ^
                     (web/static & edge APIs)               |
                                   v                        |
+---------------------+   HTTPS   +---------------------+   |
| External Services   |<----------|  Cloud Run (Go API) |---+
| (DHL/Yamato/17Track |          /|  gw/api, auth,     |\
|  HERE/SES/Twilio/   |<---------  |  orchestrations    | \   signed URLs
|  Stripe/FX/Duties)  |  Webhooks  +-----+-----+--------+  \
+---------------------+                   |     |            \
                                          |     |             \
                                          |     | gRPC/HTTP     \
                                          |     v                v
                                   +------+-----+-----+   +-----+------+
                                   | Cloud Run      |    | Cloudflare  |
                                   |  Karrio API    |    |  Queues     |
                                   | (container)    |    +-----+-------+
                                   +------+---------+          |
                                          |                    |
                                          |  REST/GraphQL      v  dequeue
                                          |             +------+------+
                                          |             |  Workers    |
                                          |             |  (Jobs)     |
                                          |             +------+------+
                                          |                    |
                                          v                    |
                        +-----------------+----+       +-------+-------+
                        | Neon Postgres        |       | Upstash Redis |
                        | (auto-suspend/scale) |       | cache/ratelmt |
                        +----------------------+       +---------------+
```

## 1.2 主フロー（時系列・要点）

### A) チェックアウト→出荷作成

```
UI(Workers/Pages)
  -> Go API(/rates)  -> HERE/JP-Address + cache
                     -> Karrio rate shopping (DHL/Yamato)
  <- 最安/最速候補返却
UIで選択 -> Go API(/shipments)
         -> Karrio: create shipment + buy label
         -> PDF to R2 (PUT, signed URL)
         -> Queues.enqueue("notify:shipment.created")
```

### B) 集荷/追跡/通知

```
Go API(/pickups) -> Karrio pickup (DHL/Yamato)
17TRACK webhook -> Workers(webhook) -> Queues.enqueue("tracking.update")
Queues -> Workers(jobs) -> Postgres保存 -> SES/Twilio通知 -> UIへPush(SSE/Webhook)
```

### C) 決済・関税

```
UI -> Stripe   (成功) -> Stripe webhook -> Workers -> Go API
UI -> Duties(Zonos/SimplyDuty) 事前見積 -> UI表示 -> 注文確定
```

### D) ユーザ連携（外部アカウント/サービス間連携）

* OIDC/OAuth2（Google/Apple/GitHub）で**ログイン連携**
* 「キャリア連携」画面で**DHL/Yamato**のアカウント資格情報を**暗号化保管**
* 「通知連携」画面で **SES/Twilio** の送信元設定・検証
* 「追跡連携」画面で **17TRACK** の API Key 設定
* サービス間は **Webhook + 署名検証**／**HMAC**／**mTLS（必要時）**

────────────────────────────────────

# 2. データベーススキーマ設計（Neon Postgres）

## 2.1 エンティティ一覧

* `users` … 認証・権限・外部ID連携
* `orgs` … 事業体（個人事業主も含む）
* `carriers` … 使用するキャリア種別（DHL/YAMATO/…）
* `carrier_accounts` … キャリア資格情報（暗号化）
* `orders` … 受注（外部ECから同期も可）
* `shipments` … 出荷（Karrio連携）
* `labels` … ラベルメタ（PDFはR2に保存）
* `pickups` … 集荷依頼
* `trackers` … 追跡（イベントは別テーブル）
* `tracking_events` … 追跡イベント
* `notifications` … 通知履歴（メール/SMS）
* `webhooks` … 受信/送信Webhookログ
* `fx_rates` … 為替キャッシュ
* ` duties_quotes` … 関税・税見積（任意）

## 2.2 テーブル定義（抜粋）

```sql
-- users
CREATE TABLE users (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  email           CITEXT UNIQUE NOT NULL,
  password_hash   TEXT,                     -- 外部IdPのみのユーザはNULL
  role            TEXT NOT NULL CHECK (role IN ('owner','admin','operator','viewer')),
  oidc_sub        TEXT,                     -- OIDC subject (外部連携ID)
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- carrier_accounts (暗号化はアプリ層/pgcrypto想定)
CREATE TABLE carrier_accounts (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  carrier_code    TEXT NOT NULL,            -- 'DHL','YAMATO','17TRACK' etc
  credentials_enc BYTEA NOT NULL,           -- KMS/アプリ鍵で暗号化済みblob
  display_name    TEXT,
  is_active       BOOLEAN NOT NULL DEFAULT true,
  created_by      UUID REFERENCES users(id),
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- orders
CREATE TABLE orders (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  external_id     TEXT,                      -- EC側の注文ID
  customer_email  CITEXT,
  ship_to_json    JSONB NOT NULL,            -- 正規化済み住所
  total_amount    NUMERIC(12,2),
  currency        CHAR(3) NOT NULL,
  status          TEXT NOT NULL CHECK (status IN ('pending','paid','fulfilled','canceled')),
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- shipments
CREATE TABLE shipments (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  order_id        UUID REFERENCES orders(id),
  carrier_account_id UUID REFERENCES carrier_accounts(id),
  carrier_code    TEXT NOT NULL,             -- DHL/YAMATO/...
  service_code    TEXT,                      -- キャリアのサービス種別
  parcel_json     JSONB NOT NULL,            -- 重量/寸法/個口等
  ship_to_json    JSONB NOT NULL,
  ship_from_json  JSONB NOT NULL,
  karrio_shipment_id TEXT,                   -- Karrio側ID
  status          TEXT NOT NULL CHECK (status IN ('draft','label_purchased','in_transit','delivered','exception','returned')),
  cost_amount     NUMERIC(12,2),
  currency        CHAR(3) NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- labels
CREATE TABLE labels (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  shipment_id     UUID NOT NULL REFERENCES shipments(id) ON DELETE CASCADE,
  format          TEXT NOT NULL,             -- 'PDF','ZPL'
  r2_key          TEXT NOT NULL,             -- R2 object key
  signed_url_exp  TIMESTAMPTZ,               -- 署名URLの有効期限（表示用）
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- trackers
CREATE TABLE trackers (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  shipment_id     UUID NOT NULL REFERENCES shipments(id) ON DELETE CASCADE,
  tracking_number TEXT NOT NULL,
  carrier_code    TEXT NOT NULL,
  status          TEXT NOT NULL,             -- 最新サマリ
  last_event_at   TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(tracking_number, carrier_code)
);

-- tracking_events
CREATE TABLE tracking_events (
  id              BIGSERIAL PRIMARY KEY,
  tracker_id      UUID NOT NULL REFERENCES trackers(id) ON DELETE CASCADE,
  code            TEXT NOT NULL,             -- event code
  description     TEXT,
  location        TEXT,
  event_at        TIMESTAMPTZ NOT NULL,
  raw_json        JSONB,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

## 2.3 リレーションシップ図（ER, ASCII）

```
[orgs] 1---n [users]
  |
  +--1---n [carrier_accounts] --n---1--> [shipments]
  |                                        |
  |                                        +--1---n [labels]
  |                                        |
  |                                        +--1---1 [trackers] --1---n [tracking_events]
  |
  +--1---n [orders] --1---n--> [shipments]
```

インデックス推奨：

* `shipments(org_id, created_at DESC)`
* `trackers(tracking_number, carrier_code)`
* `tracking_events(tracker_id, event_at DESC)`

────────────────────────────────────

# 3. API 仕様（Go API ゲートウェイ）

## 3.1 共通

* Base URL: `https://api.example.com/v1`
* Auth: Bearer JWT（OIDC）／API Key（サーバ間）
* Headers: `Content-Type: application/json`
* エラー形式（共通）:

```json
{ "error": { "code": "RESOURCE_NOT_FOUND", "message": "shipment not found", "id": "req_abc123" } }
```

* 主なエラーコード: `BAD_REQUEST`, `UNAUTHORIZED`, `FORBIDDEN`, `NOT_FOUND`, `CONFLICT`, `RATE_LIMITED`, `INTERNAL`

## 3.2 エンドポイント（抜粋）

### GET /rates

* 概要: 発送元/先・荷姿からキャリア横断レート見積
* Req:

```json
{ "from": { "country":"JP","postal":"1500001" },
  "to":   { "country":"US","postal":"94043" },
  "parcel": { "weight_g":800, "length_cm":20, "width_cm":15, "height_cm":10 },
  "options": { "carrier_codes":["DHL","YAMATO"] } }
```

* Res:

```json
{ "rates": [
    {"carrier":"DHL","service":"EXPRESS","eta_days":3,"amount":2100,"currency":"JPY"},
    {"carrier":"YAMATO","service":"TA-Q-BIN","eta_days":2,"amount":1200,"currency":"JPY"}
] }
```

### POST /shipments

* 概要: 出荷作成＋ラベル購入（Karrio連携）
* Req:

```json
{ "order_id":"...", "carrier_account_id":"...", "rate": {"carrier":"YAMATO","service":"TA-Q-BIN"},
  "from": {...}, "to": {...}, "parcel": {...} }
```

* Res:

```json
{ "shipment_id":"...", "status":"label_purchased",
  "label": {"format":"PDF","url":"https://r2.example.com/signed/...","expires_at":"..."},
  "tracking": {"number":"ABC123456","carrier":"YAMATO"} }
```

### POST /pickups

* 概要: 集荷依頼
* Req: `{ "shipment_id":"...", "date":"2025-10-05", "time_window":"14:00-16:00" }`
* Res: `{ "pickup_id":"...", "status":"scheduled" }`

### POST /trackers

* 概要: 手動で追跡監視を開始（外部で作成した送り状用）
* Req: `{ "carrier":"DHL","tracking_number":"JD123..." }`
* Res: `{ "tracker_id":"...", "status":"in_transit" }`

### POST /webhooks/stripe|17track|karrio

* 概要: 各サービスのWebhook受信（Workers -> Go API も可）
* 認可: 署名検証（Secret/HMAC）

### GET /shipments/:id, GET /trackers/:id/events

* 詳細取得。`If-None-Match` を受け ETag キャッシュ対応。

## 3.3 サービス間連携（外部API）

* Karrio: REST/GraphQL。`Authorization: Token <API_KEY>`
* DHL/Yamato: Karrio経由／直呼びを選択可能（障害回避の二経路）
* 17TRACK: Tracking登録＆Webhook
* HERE: 住所検証/ジオコーディング（UIサジェスト）
* SES/Twilio: 通知送信（Queues -> Workers から呼ぶ）
* Stripe: 決済完了 → Webhook で注文状態遷移
* FX/Duties: 為替キャッシュ/関税見積（Queues経由で非同期化推奨）

────────────────────────────────────

# 4. ユーザーインターフェース

## 4.1 画面遷移図（ASCII）

```
[ログイン/SSO] --成功--> [ダッシュボード]
     |                       |
     |                       +--> [出荷作成] --> [レート比較] --> [ラベル発行結果]
     |                       |                                   |
     |                       |                                   +--> [集荷設定]
     |                       |
     |                       +--> [追跡一覧] --> [追跡詳細(タイムライン)]
     |                       |
     |                       +--> [連携設定]
     |                             |--> [キャリア連携(DHL/YAMATO)]
     |                             |--> [通知連携(SES/Twilio)]
     |                             |--> [追跡連携(17TRACK)]
     |                             \--> [課金/Stripe]
     |
     \--(初回のみ)--> [組織設定/住所/倉庫設定]
```

## 4.2 ワイヤーフレーム（主要画面）

### ダッシュボード

```
+--------------------------------------------------------------+
|  ヘッダ: ロゴ | 出荷作成 | 追跡 | 連携設定 | ユーザ          |
+--------------------------------------------------------------+
|  KPI: 今日の出荷数 | 配達完了 | 例外 | 平均配達日数       |
+--------------------------------------------------------------+
|  最近の出荷                                           [検索] |
|  ----------------------------------------------------------- |
|  #/日時     顧客       キャリア  状態       追跡番号        |
|  1023 ...   Sato       YAMATO   in_transit  ABC123...       |
|  1022 ...   Chan       DHL      delivered   JD123...        |
+--------------------------------------------------------------+
```

### 出荷作成 → レート比較 → ラベル発行

```
[From住所入力][To住所入力][荷姿入力(重量/寸法)][見積ボタン]
  -> レート一覧:
  +--------------------------------------------+
  | YAMATO TA-Q-BIN   ETA:2日  ¥1,200 [選択]  |
  | DHL EXPRESS       ETA:3日  ¥2,100 [選択]  |
  +--------------------------------------------+
[選択] -> [ラベル発行] -> "PDF を取得" (署名URL)
[集荷スケジュール] [追跡を開始]
```

### 追跡詳細（タイムライン）

```
+-------------------------------+
|  追跡番号: ABC123 / YAMATO    |
+-------------------------------+
|  状態: In Transit             |
|  2025-10-05 09:10 Tokyo   集荷|
|  2025-10-05 12:40 Tokyo   出発|
|  2025-10-06 08:15 Osaka   到着|
|  ...                          |
+-------------------------------+
[再通知] [問題報告]
```

### 連携設定

```
+---------------------------------------------+
| キャリア連携                                |
|  [DHL] ClientID/Secret  [保存] [接続テスト] |
|  [YAMATO] APIキー/顧客番号 [保存][テスト]    |
+---------------------------------------------+
| 通知連携                                    |
|  SES: 送信ドメイン/SMTP 資格情報 [検証]     |
|  Twilio: SID/AuthToken/SenderID  [検証]     |
+---------------------------------------------+
| 追跡連携                                    |
|  17TRACK: API Key [保存][テスト]            |
+---------------------------------------------+
```

────────────────────────────────────

# 5. セキュリティ要件と対策方針

* **通信保護**: すべて TLS。Cloudflare WAF/Rate Limit/Firewall Rules 有効化
* **認証/認可**:

  * 管理UI: OIDC（Google/Apple/GitHub）。RBAC（owner/admin/operator/viewer）
  * API: Bearer JWT（短命トークン）／HMAC（webhook）
* **秘密情報**:

  * `carrier_accounts.credentials_enc` はアプリでAES-GCM暗号化（KMSのラップ鍵/バージョン管理）
  * Secrets は Cloud Run / Workers Secrets に保存。定期ローテーション
* **データ保護**:

  * PII最小化、削除リクエスト対応（論理削除 + 定期物理削除ジョブ）
  * ラベルPDFは R2 の**短期署名URL**のみ配布、直リンク無し
* **Webhook保護**:

  * すべて**署名検証（HMAC）**、**タイムスタンプの許容ウィンドウ**、**リプレイ防止**
* **監査/ログ**:

  * 操作ログ（who/when/what）
  * 追跡イベントは改ざん検出目的で**連番ID＋ハッシュ鎖**(任意)
* **脆弱性管理**:

  * 依存ライブラリの SCA、Distroless で攻撃面縮小、コンテナ署名（cosign）

────────────────────────────────────

# 6. パフォーマンス要件と最適化方針

* **SLO**: P95 300ms、エラー率 < 0.3%、可用性 99.9%
* **Cloud Run 最適化**:

  * `concurrency=80~120`（まず高め→負荷試験で調整）
  * request-based 課金前提、長処理は**Queues/Workers**へオフロード
  * Go: `GOMAXPROCS` 自動、pprof常時、ホットパスを `sqlc/pgx`＋`prepared statements`
* **キャッシュ戦略**:

  * Upstash: レート見積/為替/住所の短TTL（30–300秒）
  * HTTP ETag/If-None-Matchで GET 結果の再利用
* **DB最適化**:

  * Neon: コネクションプーリング（短TTL）、N+1回避、適切な複合INDEX
  * 大量追跡イベントは**日別パーティション** or **時系列テーブル**化を検討
* **I/O最適化**:

  * PDF/画像は**R2直PUT**→**署名URL**で配信（イグレス無料、回数のみ意識）
* **バックプレッシャ**:

  * Queues のリトライ（指数バックオフ・DLQ）、Workers で同時実行上限
* **監視**:

  * `prometheus/client_golang` メトリクス（RT/CPU/Mem/GC/Queue lag）
  * 予算ガード：クラウドの Budgets & Alerts、有料操作（R2 ClassA/B, Queues ops）監視

────────────────────────────────────

# 付録A: サービス連携（ユーザ連携 & サービス間連携）フロー

## A.1 ユーザ連携（SSO）

```
[ユーザ] -> [UI] -> [OIDC 認可エンドポイント]
   <- code -> [Go API /oauth/callback] -> token交換 -> セッション確立
users.oidc_sub に外部IDを保持／ロール付与
```

## A.2 キャリア/外部サービス連携（アカウントリンク）

```
[連携設定画面] -> 資格情報入力 -> Go API(/integrations/:service)
  -> 検証API呼び出し -> OKなら AES-GCM で暗号化保存 (carrier_accounts)
  -> 「接続済み」表示
```

## A.3 Webhook連携（受信/送信）

```
外部 -> Workers(webhook endpoint)
  - 署名検証/HMAC
  - 正常なら Queues.enqueue
Queues -> Workers(jobs)
  - Go API 呼出 or DB 更新/通知/再計算
```

────────────────────────────────────

# 付録B: OpenAPI（ミニスケルトン）

```yaml
openapi: 3.0.3
info: { title: Shipping Hub API, version: "1.0.0" }
servers: [{ url: https://api.example.com/v1 }]
paths:
  /rates:
    get:
      parameters:
        - in: query; name: from_postal; schema: { type: string }
        - in: query; name: to_postal; schema: { type: string }
      responses:
        "200": { description: OK }
  /shipments:
    post:
      requestBody: { content: { application/json: { schema: { $ref: "#/components/schemas/CreateShipment" }}}}
      responses: { "200": { description: OK } }
components:
  schemas:
    CreateShipment:
      type: object
      required: [from, to, parcel, rate]
      properties:
        from: { type: object }
        to: { type: object }
        parcel: { type: object }
        rate: { type: object }
```

────────────────────────────────────

# 付録C: キュー種別とペイロード

```
Queue: notify:shipment.created
{ "shipment_id":"...", "email":"...", "locale":"ja-JP" }

Queue: tracking.update
{ "tracker_id":"...", "event":{...}, "severity":"normal|exception" }

Queue: duties.quote
{ "order_id":"...", "items":[{"hs":"...","qty":1,"value":1000,"currency":"JPY"}] }
```

────────────────────────────────────

# 付録D: 例外・障害ハンドリング

* 17TRACK/DHL/Yamato API 一時障害 → **再試行(指数バックオフ)**，DLQへ退避
* R2 操作失敗 → 一時URL再発行／整合性監査ジョブ
* Stripe Webhook 重複 → `event_id` の冪等チェック
* DB ロック/タイムアウト → 再試行ポリシ／粒度の細かいトランザクション

────────────────────────────────────
