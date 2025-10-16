-- dialect: postgresql
-- DeliveryInfrastructure Schema Tests (pure SQL)
-- Assumes psql runs with ON_ERROR_STOP=1. Each CHECK constraint will fail
-- if a condition is not met, causing the script to stop with an error.

-- Extensions
CREATE TEMPORARY TABLE test_ext_citext(ok BOOLEAN);
INSERT INTO test_ext_citext(ok)
SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'citext');
ALTER TABLE test_ext_citext ADD CONSTRAINT check_ext_citext CHECK (ok);

CREATE TEMPORARY TABLE test_ext_pgcrypto(ok BOOLEAN);
INSERT INTO test_ext_pgcrypto(ok)
SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pgcrypto');
ALTER TABLE test_ext_pgcrypto ADD CONSTRAINT check_ext_pgcrypto CHECK (ok);

-- Index existence checks (v2)
CREATE TEMPORARY TABLE test_idx_users_org_email(ok BOOLEAN);
INSERT INTO test_idx_users_org_email(ok)
SELECT to_regclass('public.idx_users_org_email') IS NOT NULL;
ALTER TABLE test_idx_users_org_email ADD CONSTRAINT check_idx_users_org_email CHECK (ok);

CREATE TEMPORARY TABLE test_idx_shipments_org_status(ok BOOLEAN);
INSERT INTO test_idx_shipments_org_status(ok)
SELECT to_regclass('public.idx_shipments_org_status') IS NOT NULL;
ALTER TABLE test_idx_shipments_org_status ADD CONSTRAINT check_idx_shipments_org_status CHECK (ok);

CREATE TEMPORARY TABLE test_idx_labels_shipment(ok BOOLEAN);
INSERT INTO test_idx_labels_shipment(ok)
SELECT to_regclass('public.idx_labels_shipment') IS NOT NULL;
ALTER TABLE test_idx_labels_shipment ADD CONSTRAINT check_idx_labels_shipment CHECK (ok);

CREATE TEMPORARY TABLE test_idx_tracking_events(ok BOOLEAN);
INSERT INTO test_idx_tracking_events(ok)
SELECT to_regclass('public.idx_tracking_events_tracker_occurred') IS NOT NULL;
ALTER TABLE test_idx_tracking_events ADD CONSTRAINT check_idx_tracking_events CHECK (ok);

-- Idempotency unique index existence
CREATE TEMPORARY TABLE test_idx_tracking_events_dedupe(ok BOOLEAN);
INSERT INTO test_idx_tracking_events_dedupe(ok)
SELECT to_regclass('public.idx_tracking_events_dedupe') IS NOT NULL;
ALTER TABLE test_idx_tracking_events_dedupe ADD CONSTRAINT check_idx_tracking_events_dedupe CHECK (ok);

-- Unique constraints (default names: <table>_<cols>_key)
CREATE TEMPORARY TABLE test_unique_carriers_code(ok BOOLEAN);
INSERT INTO test_unique_carriers_code(ok)
SELECT to_regclass('public.carriers_code_key') IS NOT NULL;
ALTER TABLE test_unique_carriers_code ADD CONSTRAINT check_unique_carriers_code CHECK (ok);

CREATE TEMPORARY TABLE test_unique_users_org_email(ok BOOLEAN);
INSERT INTO test_unique_users_org_email(ok)
SELECT to_regclass('public.users_org_id_email_key') IS NOT NULL;
ALTER TABLE test_unique_users_org_email ADD CONSTRAINT check_unique_users_org_email CHECK (ok);

CREATE TEMPORARY TABLE test_unique_orders_org_external(ok BOOLEAN);
INSERT INTO test_unique_orders_org_external(ok)
SELECT to_regclass('public.orders_org_id_external_order_id_key') IS NOT NULL;
ALTER TABLE test_unique_orders_org_external ADD CONSTRAINT check_unique_orders_org_external CHECK (ok);

-- Verify unique dedupe prevents exact duplicates (status/description equality, nulls treated as empty)
CREATE TEMPORARY TABLE test_tracking_events_dedupe(ok BOOLEAN);
DO $$
DECLARE
  tid UUID;
  ok BOOLEAN := FALSE;
BEGIN
  -- Create isolated tracker
  INSERT INTO trackers (carrier_tracking_code, status, metadata)
  VALUES ('TMP_DEDUPE_TEST', 'in_transit', '{}'::jsonb)
  ON CONFLICT (carrier_tracking_code) DO NOTHING;

  SELECT id INTO tid FROM trackers WHERE carrier_tracking_code = 'TMP_DEDUPE_TEST';

  -- Insert first event
  INSERT INTO tracking_events (tracker_id, occurred_at, status, description, location, raw)
  VALUES (tid, '2025-01-01T00:00:00Z'::timestamptz, 'in_transit', 'Departed', '{}'::jsonb, '{}'::jsonb);

  -- Attempt duplicate insert; expect unique_violation
  BEGIN
    INSERT INTO tracking_events (tracker_id, occurred_at, status, description, location, raw)
    VALUES (tid, '2025-01-01T00:00:00Z'::timestamptz, 'in_transit', 'Departed', '{}'::jsonb, '{}'::jsonb);
    ok := FALSE; -- should not reach
  EXCEPTION WHEN unique_violation THEN
    ok := TRUE; -- expected
  END;

  INSERT INTO test_tracking_events_dedupe(ok) VALUES (ok);
END $$;
ALTER TABLE test_tracking_events_dedupe ADD CONSTRAINT check_tracking_events_dedupe CHECK (ok);

-- Cascade delete validations (delete org -> cascade users/orders/shipments and nested)
-- Prepare org and related records (idempotent inserts)
INSERT INTO orgs (slug, name)
SELECT 'tmp_test_org_del', 'Tmp Test Org'
WHERE NOT EXISTS (SELECT 1 FROM orgs WHERE slug = 'tmp_test_org_del');

CREATE TEMPORARY TABLE tmp_org AS
SELECT id AS org_id FROM orgs WHERE slug = 'tmp_test_org_del';

INSERT INTO users (org_id, email, name, role)
SELECT (SELECT org_id FROM tmp_org), 'tmp.user@example.com', 'Tmp User', 'member'
WHERE NOT EXISTS (
  SELECT 1 FROM users WHERE org_id = (SELECT org_id FROM tmp_org) AND email = 'tmp.user@example.com'
);

INSERT INTO orders (org_id, external_order_id, customer_email, shipping_address, billing_address, status)
SELECT (SELECT org_id FROM tmp_org), 'TMP-ORDER-1', 'tmp.customer@example.com',
       '{}'::jsonb, '{}'::jsonb, 'new'
WHERE NOT EXISTS (
  SELECT 1 FROM orders WHERE org_id = (SELECT org_id FROM tmp_org) AND external_order_id = 'TMP-ORDER-1'
);

CREATE TEMPORARY TABLE tmp_order AS
SELECT id AS order_id FROM orders WHERE org_id = (SELECT org_id FROM tmp_org) AND external_order_id = 'TMP-ORDER-1';

INSERT INTO shipments (
  org_id, order_id, carrier_account_id, status,
  rate_currency, rate_amount, ship_to, ship_from, package, metadata
)
SELECT
  (SELECT org_id FROM tmp_org),
  (SELECT order_id FROM tmp_order),
  NULL,
  'created', 'USD', 1.23,
  '{"country":"US"}'::jsonb,
  '{"country":"US"}'::jsonb,
  '{"weight_oz":8}'::jsonb,
  '{}'::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM shipments WHERE order_id = (SELECT order_id FROM tmp_order)
);

CREATE TEMPORARY TABLE tmp_shipment AS
SELECT id AS shipment_id FROM shipments WHERE order_id = (SELECT order_id FROM tmp_order);

INSERT INTO labels (shipment_id, document_url, format, size, cost, currency, metadata)
SELECT (SELECT shipment_id FROM tmp_shipment), 'https://example.com/tmp_label.pdf', 'pdf', '4x6', 0.00, 'USD', '{}'::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM labels WHERE shipment_id = (SELECT shipment_id FROM tmp_shipment)
);

INSERT INTO trackers (shipment_id, carrier_tracking_code, status, last_event_at, metadata)
SELECT (SELECT shipment_id FROM tmp_shipment), 'TMPTRACK', 'in_transit', NOW(), '{}'::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM trackers WHERE carrier_tracking_code = 'TMPTRACK'
);

CREATE TEMPORARY TABLE tmp_tracker AS
SELECT id AS tracker_id FROM trackers WHERE carrier_tracking_code = 'TMPTRACK';

INSERT INTO tracking_events (tracker_id, occurred_at, status, description, location, raw)
SELECT (SELECT tracker_id FROM tmp_tracker), NOW(), 'picked_up', 'Tmp pickup', '{}'::jsonb, '{}'::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM tracking_events WHERE tracker_id = (SELECT tracker_id FROM tmp_tracker)
);

-- Delete org and verify cascading
DELETE FROM orgs WHERE id = (SELECT org_id FROM tmp_org);

CREATE TEMPORARY TABLE test_cascade_users(ok BOOLEAN);
INSERT INTO test_cascade_users(ok)
SELECT (SELECT COUNT(*) FROM users WHERE org_id = (SELECT org_id FROM tmp_org)) = 0;
ALTER TABLE test_cascade_users ADD CONSTRAINT check_cascade_users CHECK (ok);

CREATE TEMPORARY TABLE test_cascade_orders(ok BOOLEAN);
INSERT INTO test_cascade_orders(ok)
SELECT (SELECT COUNT(*) FROM orders WHERE id = (SELECT order_id FROM tmp_order)) = 0;
ALTER TABLE test_cascade_orders ADD CONSTRAINT check_cascade_orders CHECK (ok);

CREATE TEMPORARY TABLE test_cascade_shipments(ok BOOLEAN);
INSERT INTO test_cascade_shipments(ok)
SELECT (SELECT COUNT(*) FROM shipments WHERE id = (SELECT shipment_id FROM tmp_shipment)) = 0;
ALTER TABLE test_cascade_shipments ADD CONSTRAINT check_cascade_shipments CHECK (ok);

CREATE TEMPORARY TABLE test_cascade_labels(ok BOOLEAN);
INSERT INTO test_cascade_labels(ok)
SELECT (SELECT COUNT(*) FROM labels WHERE shipment_id = (SELECT shipment_id FROM tmp_shipment)) = 0;
ALTER TABLE test_cascade_labels ADD CONSTRAINT check_cascade_labels CHECK (ok);

CREATE TEMPORARY TABLE test_cascade_trackers(ok BOOLEAN);
INSERT INTO test_cascade_trackers(ok)
SELECT (SELECT COUNT(*) FROM trackers WHERE id = (SELECT tracker_id FROM tmp_tracker)) = 0;
ALTER TABLE test_cascade_trackers ADD CONSTRAINT check_cascade_trackers CHECK (ok);

CREATE TEMPORARY TABLE test_cascade_tracking_events(ok BOOLEAN);
INSERT INTO test_cascade_tracking_events(ok)
SELECT (SELECT COUNT(*) FROM tracking_events WHERE tracker_id = (SELECT tracker_id FROM tmp_tracker)) = 0;
ALTER TABLE test_cascade_tracking_events ADD CONSTRAINT check_cascade_tracking_events CHECK (ok);

-- SET NULL behavior validation for shipments.order_id
INSERT INTO orgs (slug, name)
SELECT 'tmp_test_org_null', 'Tmp Org Null'
WHERE NOT EXISTS (SELECT 1 FROM orgs WHERE slug = 'tmp_test_org_null');

CREATE TEMPORARY TABLE tmp_org_null AS
SELECT id AS org_id FROM orgs WHERE slug = 'tmp_test_org_null';

INSERT INTO orders (org_id, external_order_id, customer_email, shipping_address, billing_address, status)
SELECT (SELECT org_id FROM tmp_org_null), 'TMP-ORDER-NULL', 'tmp.customer@example.com',
       '{}'::jsonb, '{}'::jsonb, 'new'
WHERE NOT EXISTS (
  SELECT 1 FROM orders WHERE org_id = (SELECT org_id FROM tmp_org_null) AND external_order_id = 'TMP-ORDER-NULL'
);

CREATE TEMPORARY TABLE tmp_order_null AS
SELECT id AS order_id FROM orders WHERE org_id = (SELECT org_id FROM tmp_org_null) AND external_order_id = 'TMP-ORDER-NULL';

INSERT INTO shipments (
  org_id, order_id, carrier_account_id, status,
  rate_currency, rate_amount, ship_to, ship_from, package, metadata
)
SELECT
  (SELECT org_id FROM tmp_org_null),
  (SELECT order_id FROM tmp_order_null),
  NULL,
  'created', 'USD', 2.34,
  '{"country":"US"}'::jsonb,
  '{"country":"US"}'::jsonb,
  '{"weight_oz":10}'::jsonb,
  '{}'::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM shipments WHERE order_id = (SELECT order_id FROM tmp_order_null)
);

CREATE TEMPORARY TABLE tmp_shipment_null AS
SELECT id AS shipment_id FROM shipments WHERE order_id = (SELECT order_id FROM tmp_order_null);

-- Delete only the order; shipment should remain with order_id set to NULL
DELETE FROM orders WHERE id = (SELECT order_id FROM tmp_order_null);

CREATE TEMPORARY TABLE test_setnull_shipment_exists(ok BOOLEAN);
INSERT INTO test_setnull_shipment_exists(ok)
SELECT (SELECT COUNT(*) FROM shipments WHERE id = (SELECT shipment_id FROM tmp_shipment_null)) = 1;
ALTER TABLE test_setnull_shipment_exists ADD CONSTRAINT check_setnull_shipment_exists CHECK (ok);

CREATE TEMPORARY TABLE test_setnull_order_id_is_null(ok BOOLEAN);
INSERT INTO test_setnull_order_id_is_null(ok)
SELECT (SELECT order_id IS NULL FROM shipments WHERE id = (SELECT shipment_id FROM tmp_shipment_null));
ALTER TABLE test_setnull_order_id_is_null ADD CONSTRAINT check_setnull_order_id_is_null CHECK (ok);