# 実装戦略（優先順位ベース）

## 1. 要件定義と優先順位（Value/Effort + 依存）
- 事業価値/緊急性・技術複雑性・依存関係で評価し、WSJFで毎週更新。
- 高優先（順に着手）
  - 出荷・ラベル（POST /shipments）
  - 追跡（GET /trackers、イベント取り込み、Webhook）
  - 見積（GET /rates）
  - セキュリティ/監査（暗号化、HMAC、監査ログ）
- 次点：ピックアップ、住所検証、通貨換算、通知拡張、管理UI。

## 2. 開発スケジュール（2週間スプリント）
- Sprint 0（基盤・1週）：API骨格、DB接続、CI/CD、観測、`db-init/seed/test`。
- Sprint 1（出荷/ラベル・2週）：`POST /shipments` を実用化、レート連携の土台。
- Sprint 2（追跡/Webhook・2週）：トラッカー登録、イベント取り込み、Webhook配信/再送。
- Sprint 3（セキュリティ/監査・1-2週）：AES-GCM+KMS、監査ログ、基本UI。
- Sprint 4（ピックアップ/FX/硬化・2週）：`fx_rates`更新、コスト最適化、SLO整備。

## 3. リソースと期間見積（概算）
- Backend 2名、Platform 1名、QA 1名。
- Sprint毎の人日：S0(14)、S1(22〜24)、S2(22〜24)、S3(16〜18)、S4(18〜20)。
- 前提：キャリアSandbox、Cloud Run/Workers/R2/Neon/Upstash準備済。

## 4. 進捗確認と見直しの仕組み
- 毎週WSJF再採点、スプリント中盤レビュー、ステアリングでKPI/SLI/SLO確認。
- 指標：ラベル発行成功率、追跡イベント遅延、Webhook成功率、API p95、キュー滞留、コスト/千リクエスト。
- ガバナンス：DoR/DoD（仕様・テスト・観測・セキュリティチェック）。

## 5. リスクと対応策
- 外部連携遅延：スタブ/バックオフ/タイムアウト、フィーチャーフラグ。
- 非同期重複・順序：IDEMPキー、イベントバージョン、DLQ監視。
- コスト暴走：並列度・TTL・レートリミット、予算アラート。
- セキュリティ/PII：AES-GCM+KMS、PII最小化、監査ログ、鍵ローテーション。
- 可観測性不足：構造化ログ、トレース、相関ID、p95/P99監視。

## 今すぐの着手
- `POST /shipments` と `GET /rates` は実装済み（ダミー）。
- 次に `GET /trackers/{code}` を追加し、追跡の参照を提供。
- Karrio連携・R2署名URL・Webhook/HMACは後続で導入。