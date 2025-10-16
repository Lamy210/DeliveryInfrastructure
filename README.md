# DeliveryInfrastructure
配送基盤

## 実装計画（概要）
- 重要機能から順に開発：出荷・ラベル・追跡・通知・集荷。
- 同時にDB（Neon Postgres）とテスト環境（psqlベース）を整備。
- Workers/Queues/R2は後続で段階的に統合（署名URL配信・Webhook）。

## 優先タスク（機能／環境）
- 出荷・ラベル・追跡のデータモデルとAPI下支えを実装。
- キャリア資格情報の暗号化保管（アプリ層）と参照フロー整備。
- 追跡イベントの保存・インデックス・監査ログの最小版。
- psqlでのDB初期化／シード／テストの自動化（Makefile）。
- Secrets/OIDC/監視はAPI実装着手後に併走。

## データベース（PostgreSQL / psql）
- スキーマ: `db/schema.sql`（拡張 `citext` / `pgcrypto` 有効化）
- シード: `db/seed.sql`（キャリア・デモ組織・サンプル出荷）
- テスト: `db/tests/test_schema.sql`（制約/インデックス/カスケード検証）

### psql 実行方法（Neon などの接続文字列が必要）
1. `export DATABASE_URL='postgresql://user:pass@host:port/dbname'`
2. 初期化: `make db-init`
3. シード投入: `make db-seed`
4. テスト: `make db-test`

## API クイックスタート
- 事前に `DATABASE_URL` を設定し、`make db-init` と `make db-seed` を完了してください。
- サーバ起動：`make run` または `go run ./cmd/api`
- 設定（暫定）：`RATE_PROVIDER`（現状は `dummy` のみ。将来 `karrio` を予定）

### RATE_PROVIDER の設定例

- ダミープロバイダ（既定）

```
export RATE_PROVIDER=dummy
make run
```

- 今後の拡張（例）：`karrio`
  - 資格情報設定と安全な保管（暗号化）が必要
  - 実装後は `export RATE_PROVIDER=karrio` で切替予定
- ヘルスチェック：`curl -s 'http://localhost:8080/healthz'`
- レート見積（GET）：
  - `curl 'http://localhost:8080/rates?from_country=US&to_country=US&weight_oz=16&carrier_code=ups'`
- 出荷作成（POST）：
  - `curl -X POST 'http://localhost:8080/shipments' -H 'Content-Type: application/json' -d '{
      "org_slug": "demo",
      "order_external_id": "ORDER-001",
      "carrier_code": "ups",
      "rate_currency": "USD",
      "ship_to": {"country":"US"},
      "ship_from": {"country":"US"},
      "package": {"weight_oz": 16},
      "metadata": {}
    }'`

- 追跡参照（GET）：
  - `curl 'http://localhost:8080/trackers/TRACK123'`
  - 応答例：`{ "code":"TRACK123", "status":"in_transit", "last_event_at":"...", "last_event": { ... } }`

- 追跡イベント取り込み（POST）：
  - `curl -X POST 'http://localhost:8080/trackers/TRACK123/events' -H 'Content-Type: application/json' -d '{
      "status": "in_transit",
      "description": "Departed facility",
      "location": {"country":"US"},
      "occurred_at": "2025-01-01T12:00:00Z",
      "raw": {"carrier":"DHL"}
    }'`
  - 応答例：`{ "code":"TRACK123", "status":"in_transit", "occurred_at":"2025-01-01T12:00:00Z" }`

## テスト実行

- Goユニット/統合テストの実行：

```
make go-test
```

- すべてのテスト（SQLテスト + Goテスト）：

```
make test
```

- 統合テストの注意事項：
  - `internal/server/shipments_integration_test.go` は `DATABASE_URL` が未設定の場合は自動でスキップされます。
  - 統合テストを有効にするには、`DATABASE_URL` を設定し、`make db-init && make db-seed` を事前に実行してください。


### トランザクション管理とインデックス設計
- 主要書き込みは `BEGIN; ... COMMIT;`（API層）で一貫性担保。
- 推奨インデックス（v2）を作成済み：
  - `users(org_id, email)` → `idx_users_org_email`
  - `shipments(org_id, status)` → `idx_shipments_org_status`
  - `labels(shipment_id)` → `idx_labels_shipment`
  - `tracking_events(tracker_id, occurred_at)` → `idx_tracking_events_tracker_occurred`
  - 重複防止（冪等性）インデックス：`tracking_events(tracker_id, occurred_at, COALESCE(status,''), COALESCE(description,''))` → `idx_tracking_events_dedupe`
  - 主な一意制約：`carriers(code)`、`orders(org_id, external_order_id)`、`trackers(carrier_tracking_code)`

### エラーレスポンスの標準化（API全体）
- `webhook` に加え、`/trackers/:code` および `/shipments` でも同じ JSON 形式を返します。
- 例：`{"error": {"code": "invalid_request", "message": "code required"}}`
- 主なコード：`invalid_json`、`invalid_request`、`invalid_occurred_at`、`resource_not_found`、`db_error`、`unsupported_source`、`invalid_signature_format`、`signature_mismatch`

## 今後の拡張（抜粋）
- Karrio連携のID保存・再購入/返品ラベルのフロー追加。
- R2署名URL生成のWorkers実装と配布ルート統合。
- Webhook署名検証・監査ログの拡充（PII最小化）。
- sqlc + pgxで型安全DAO生成、Cloud RunへAPI実装展開。
