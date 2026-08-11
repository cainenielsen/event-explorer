.PHONY: up down logs seed seed-small build test dev

# Start ClickHouse + the API server.
up:
	docker compose up --build -d

down:
	docker compose down

logs:
	docker compose logs -f

# Seed the full 1,000,000-row demo dataset. Safe to re-run (it just adds
# more rows); drop the clickhouse-data volume first for a clean slate.
seed:
	docker compose --profile seed run --rm seed -count=1000000

# Faster seed for local iteration.
seed-small:
	docker compose --profile seed run --rm seed -count=20000

build:
	go build ./...

test:
	go test ./...

# Run the API server against a locally-running ClickHouse
# (CLICKHOUSE_ADDR=localhost:9000 by default, see internal/config).
dev:
	go run ./cmd/server
