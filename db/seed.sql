-- Seed data for local/dev/testing environments
-- Carriers
INSERT INTO carriers (code, name) VALUES
  ('DHL','DHL Express'),
  ('YAMATO','Yamato Transport')
ON CONFLICT (code) DO NOTHING;

-- Demo org and user
INSERT INTO orgs (id, name) VALUES
  (gen_random_uuid(), 'demo')
ON CONFLICT DO NOTHING;

-- Fetch demo org id
WITH o AS (
  SELECT id FROM orgs WHERE name = 'demo' LIMIT 1
)
INSERT INTO users (id, org_id, email, role)
SELECT gen_random_uuid(), o.id, 'owner@example.com', 'owner' FROM o
ON CONFLICT (email) DO NOTHING;

-- Sample order
WITH o AS (
  SELECT id FROM orgs WHERE name = 'demo' LIMIT 1
)
INSERT INTO orders (id, org_id, external_id, customer_email, ship_to_json, total_amount, currency, status)
SELECT gen_random_uuid(), o.id, 'ORDER-1001', 'customer@example.com',
       '{"country":"JP","postal":"1500001","city":"Shibuya","line1":"Jinnan 1-1"}'::jsonb,
       1200.00, 'JPY', 'paid' FROM o
ON CONFLICT DO NOTHING;

-- Sample carrier account (placeholder encrypted blob)
WITH o AS (
  SELECT id FROM orgs WHERE name = 'demo' LIMIT 1
)
INSERT INTO carrier_accounts (id, org_id, carrier_code, credentials_enc, display_name)
SELECT gen_random_uuid(), o.id, 'DHL', '\xDEADBEEF', 'DHL Demo' FROM o
ON CONFLICT DO NOTHING;

-- Sample shipment + label + tracker + event
WITH o AS (
  SELECT id FROM orgs WHERE name = 'demo' LIMIT 1
), ord AS (
  SELECT id FROM orders WHERE external_id = 'ORDER-1001' LIMIT 1
), ca AS (
  SELECT id FROM carrier_accounts WHERE display_name = 'DHL Demo' LIMIT 1
)
INSERT INTO shipments (
  id, org_id, order_id, carrier_account_id, carrier_code, service_code,
  parcel_json, ship_to_json, ship_from_json, karrio_shipment_id, status,
  cost_amount, currency
)
SELECT gen_random_uuid(), o.id, ord.id, ca.id, 'DHL', 'EXPRESS',
       '{"weight_g":500,"length_cm":20,"width_cm":15,"height_cm":8}'::jsonb,
       '{"country":"US","postal":"94043","city":"Mountain View","line1":"1600 Amphitheatre"}'::jsonb,
       '{"country":"JP","postal":"1500001","city":"Shibuya","line1":"Jinnan 1-1"}'::jsonb,
       'karrio_demo_001', 'label_purchased', 1800.00, 'JPY'
FROM o, ord, ca
ON CONFLICT DO NOTHING;

-- Insert label
WITH s AS (
  SELECT id FROM shipments WHERE karrio_shipment_id = 'karrio_demo_001' LIMIT 1
)
INSERT INTO labels (id, shipment_id, format, r2_key)
SELECT gen_random_uuid(), s.id, 'PDF', 'labels/demo/karrio_demo_001.pdf' FROM s
ON CONFLICT DO NOTHING;

-- Insert tracker and event
WITH o AS (
  SELECT id FROM orgs WHERE name = 'demo' LIMIT 1
), s AS (
  SELECT id FROM shipments WHERE karrio_shipment_id = 'karrio_demo_001' LIMIT 1
)
INSERT INTO trackers (id, org_id, shipment_id, tracking_number, carrier_code, status)
SELECT gen_random_uuid(), o.id, s.id, 'TN123456789', 'DHL', 'in_transit' FROM o, s
ON CONFLICT DO NOTHING;

WITH t AS (
  SELECT id FROM trackers WHERE tracking_number = 'TN123456789' AND carrier_code = 'DHL' LIMIT 1
)
INSERT INTO tracking_events (tracker_id, code, description, location, event_at, raw_json)
SELECT t.id, 'PU', 'Picked up', 'Tokyo', now(), '{"status":"picked_up"}'::jsonb FROM t;
-- DeliveryInfrastructure Seed Data (PostgreSQL)
-- Idempotent inserts using INSERT ... SELECT ... WHERE NOT EXISTS

-- Carriers
INSERT INTO carriers (code, name, service_region)
SELECT 'ups', 'UPS', 'us'
WHERE NOT EXISTS (SELECT 1 FROM carriers WHERE code = 'ups');

INSERT INTO carriers (code, name, service_region)
SELECT 'fedex', 'FedEx', 'us'
WHERE NOT EXISTS (SELECT 1 FROM carriers WHERE code = 'fedex');

INSERT INTO carriers (code, name, service_region)
SELECT 'dhl', 'DHL', 'global'
WHERE NOT EXISTS (SELECT 1 FROM carriers WHERE code = 'dhl');

-- Demo Org
INSERT INTO orgs (slug, name)
SELECT 'demo', 'Demo Org'
WHERE NOT EXISTS (SELECT 1 FROM orgs WHERE slug = 'demo');

-- Demo User
INSERT INTO users (org_id, email, name, role)
SELECT (SELECT id FROM orgs WHERE slug = 'demo'), 'demo@example.com', 'Demo User', 'admin'
WHERE NOT EXISTS (
  SELECT 1 FROM users WHERE org_id = (SELECT id FROM orgs WHERE slug = 'demo') AND email = 'demo@example.com'
);

-- Sample Order
INSERT INTO orders (org_id, external_order_id, customer_email, shipping_address, billing_address, status)
SELECT (SELECT id FROM orgs WHERE slug = 'demo'), 'ORDER-001', 'customer@example.com',
       CAST('{}' AS JSONB), CAST('{}' AS JSONB), 'new'
WHERE NOT EXISTS (
  SELECT 1 FROM orders WHERE org_id = (SELECT id FROM orgs WHERE slug = 'demo') AND external_order_id = 'ORDER-001'
);

-- Sample Carrier Account (UPS)
INSERT INTO carrier_accounts (org_id, carrier_id, external_account_id, credentials_encrypted)
SELECT (SELECT id FROM orgs WHERE slug = 'demo'), (SELECT id FROM carriers WHERE code = 'ups'), 'acct_demo', NULL
WHERE NOT EXISTS (
  SELECT 1 FROM carrier_accounts
  WHERE org_id = (SELECT id FROM orgs WHERE slug = 'demo')
    AND carrier_id = (SELECT id FROM carriers WHERE code = 'ups')
    AND external_account_id = 'acct_demo'
);

-- Sample Shipment linked to ORDER-001
INSERT INTO shipments (
  org_id, order_id, carrier_account_id, status,
  rate_currency, rate_amount, ship_to, ship_from, package, metadata
)
SELECT
  (SELECT id FROM orgs WHERE slug = 'demo'),
  (SELECT id FROM orders WHERE external_order_id = 'ORDER-001' AND org_id = (SELECT id FROM orgs WHERE slug = 'demo')),
  (SELECT id FROM carrier_accounts WHERE external_account_id = 'acct_demo' AND org_id = (SELECT id FROM orgs WHERE slug = 'demo')),
  'created', 'USD', 9.99,
  CAST('{"country":"US"}' AS JSONB),
  CAST('{"country":"US"}' AS JSONB),
  CAST('{"weight_oz":16}' AS JSONB),
  CAST('{}' AS JSONB)
WHERE NOT EXISTS (
  SELECT 1 FROM shipments
  WHERE order_id = (SELECT id FROM orders WHERE external_order_id = 'ORDER-001' AND org_id = (SELECT id FROM orgs WHERE slug = 'demo'))
);

-- Label for Shipment
INSERT INTO labels (shipment_id, document_url, format, size, cost, currency, metadata)
SELECT
  (SELECT id FROM shipments WHERE order_id = (SELECT id FROM orders WHERE external_order_id = 'ORDER-001' AND org_id = (SELECT id FROM orgs WHERE slug = 'demo'))),
  'https://example.com/label/ORDER-001.pdf', 'pdf', '4x6', 0.00, 'USD', CAST('{}' AS JSONB)
WHERE NOT EXISTS (
  SELECT 1 FROM labels
  WHERE shipment_id = (SELECT id FROM shipments WHERE order_id = (SELECT id FROM orders WHERE external_order_id = 'ORDER-001' AND org_id = (SELECT id FROM orgs WHERE slug = 'demo')))
);

-- Tracker for Shipment
INSERT INTO trackers (shipment_id, carrier_tracking_code, status, last_event_at, metadata)
SELECT
  (SELECT id FROM shipments WHERE order_id = (SELECT id FROM orders WHERE external_order_id = 'ORDER-001' AND org_id = (SELECT id FROM orgs WHERE slug = 'demo'))),
  'TRACK123', 'in_transit', NOW(), CAST('{}' AS JSONB)
WHERE NOT EXISTS (
  SELECT 1 FROM trackers WHERE carrier_tracking_code = 'TRACK123'
);

-- Tracking Event for Tracker
INSERT INTO tracking_events (tracker_id, occurred_at, status, description, location, raw)
SELECT
  (SELECT id FROM trackers WHERE carrier_tracking_code = 'TRACK123'),
  NOW(), 'picked_up', 'Package picked up', CAST('{}' AS JSONB), CAST('{}' AS JSONB)
WHERE NOT EXISTS (
  SELECT 1 FROM tracking_events WHERE tracker_id = (SELECT id FROM trackers WHERE carrier_tracking_code = 'TRACK123')
);