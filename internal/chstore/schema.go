package chstore

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// eventsDDL is the events table. See the README ("How this stays fast")
// for the full mechanism; in short, every index here answers "how do you
// prune granules for a column whose values you didn't know ahead of time":
//
//   - attributes Map(LowCardinality(String), String) is the flattened JSON
//     body (see internal/flatten) — the column arbitrary key:value search
//     actually runs against, populated without the caller declaring keys
//     ahead of time.
//   - idx_attr_keys / idx_attr_values (bloom_filter on mapKeys/mapValues)
//     accelerate `attributes['key'] = 'value'` lookups even when the key is
//     high-cardinality, without a set() index's "too many distinct values,
//     give up" failure mode.
//   - idx_body_tokens (tokenbf_v1 on the raw body) is the needle-in-haystack
//     index: it lets `hasToken(body, 'usr_42')` skip granules that provably
//     don't contain the token, so a free-text search for one user's id
//     across every event type/key stays cheap.
//   - idx_event_type / idx_source (set()) are the low-cardinality case:
//     event_type and source have a handful of distinct values, so a set
//     index skips granules exactly rather than probabilistically.
//   - ORDER BY (created_at, event_id) + PARTITION BY toYYYYMM(created_at)
//     matches the table's dominant access pattern: browse/paginate recent
//     events, optionally filtered.
const eventsDDL = `
CREATE TABLE IF NOT EXISTS %s.events
(
	event_id    UUID DEFAULT generateUUIDv4(),
	created_at  DateTime64(3) DEFAULT now64(3),
	event_type  LowCardinality(String),
	source      LowCardinality(String),
	body        String,
	attributes  Map(LowCardinality(String), String),

	INDEX idx_event_type   event_type           TYPE set(100)        GRANULARITY 4,
	INDEX idx_source       source               TYPE set(100)        GRANULARITY 4,
	INDEX idx_body_tokens  body                 TYPE tokenbf_v1(32768, 3, 0) GRANULARITY 4,
	INDEX idx_attr_keys    mapKeys(attributes)   TYPE bloom_filter(0.01) GRANULARITY 4,
	INDEX idx_attr_values  mapValues(attributes) TYPE bloom_filter(0.01) GRANULARITY 4
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(created_at)
ORDER BY (created_at, event_id)
SETTINGS index_granularity = 8192
`

// Migrate creates the database and events table if they don't already
// exist. It's idempotent and safe to call from both the server and the
// seeder on startup.
func Migrate(ctx context.Context, conn driver.Conn, database string) error {
	if err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", database)); err != nil {
		return fmt.Errorf("create database: %w", err)
	}
	if err := conn.Exec(ctx, fmt.Sprintf(eventsDDL, database)); err != nil {
		return fmt.Errorf("create events table: %w", err)
	}
	return nil
}
