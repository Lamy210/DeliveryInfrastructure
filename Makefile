# DeliveryInfrastructure Makefile for Postgres setup and tests

DB_URL ?= $(DATABASE_URL)

.PHONY: db-init db-migrate db-seed db-test sql-test db-drop

db-init:
	@[ -n "$(DB_URL)" ] || (echo "Set DATABASE_URL environment variable"; exit 1)
	psql -v ON_ERROR_STOP=1 "$(DB_URL)" -c 'CREATE EXTENSION IF NOT EXISTS citext;';
	psql -v ON_ERROR_STOP=1 "$(DB_URL)" -c 'CREATE EXTENSION IF NOT EXISTS pgcrypto;';
	psql -v ON_ERROR_STOP=1 "$(DB_URL)" -f db/schema.sql

db-migrate:
	@[ -n "$(DB_URL)" ] || (echo "Set DATABASE_URL environment variable"; exit 1)
	psql -v ON_ERROR_STOP=1 "$(DB_URL)" -f db/schema.sql

db-seed:
	@[ -n "$(DB_URL)" ] || (echo "Set DATABASE_URL environment variable"; exit 1)
	psql -v ON_ERROR_STOP=1 "$(DB_URL)" -f db/seed.sql

db-test:
	@[ -n "$(DB_URL)" ] || (echo "Set DATABASE_URL environment variable"; exit 1)
	psql -v ON_ERROR_STOP=1 "$(DB_URL)" -f db/tests/test_schema.sql

sql-test: db-test

db-drop:
	@[ -n "$(DB_URL)" ] || (echo "Set DATABASE_URL environment variable"; exit 1)
	psql -v ON_ERROR_STOP=1 "$(DB_URL)" -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"
	psql -v ON_ERROR_STOP=1 "$(DB_URL)" -c 'CREATE EXTENSION IF NOT EXISTS citext;'
	psql -v ON_ERROR_STOP=1 "$(DB_URL)" -c 'CREATE EXTENSION IF NOT EXISTS pgcrypto;'

.PHONY: run
run:
	go run ./cmd/api

.PHONY: go-test test
go-test:
	go test ./...

test: db-test go-test

.PHONY: docker-up docker-down docker-logs docker-ps docker-reset
docker-up:
	docker compose up -d

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f postgres

docker-ps:
	docker compose ps

docker-reset:
	docker compose down -v