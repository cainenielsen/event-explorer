// Package model holds the shapes shared between the seeder, the store layer,
// and the API — the on-disk row and its JSON-facing representation.
package model

import "time"

// Event is one row of the events table. EventID/CreatedAt/EventType/Source
// are real typed columns; Body is the raw JSON payload exactly as received;
// Attributes is Body flattened to dot-notation keys -> stringified values,
// which is what the query language actually filters against (see
// internal/flatten and internal/chquery for why).
type Event struct {
	EventID    string            `json:"event_id"`
	CreatedAt  time.Time         `json:"created_at"`
	EventType  string            `json:"event_type"`
	Source     string            `json:"source"`
	Body       string            `json:"body"`
	Attributes map[string]string `json:"attributes"`
}
