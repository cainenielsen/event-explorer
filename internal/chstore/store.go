// Package chstore is the ClickHouse data access layer: schema migration and
// the handful of queries the API needs (search, get-by-id, stats).
package chstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"eventquery/internal/chquery"
	"eventquery/internal/model"
)

// ErrNotFound is returned by GetByID when no event matches the id.
var ErrNotFound = errors.New("event not found")

// Store wraps a ClickHouse connection scoped to one database.
type Store struct {
	conn     driver.Conn
	database string
}

// New wraps an already-open, already-migrated connection.
func New(conn driver.Conn, database string) *Store {
	return &Store{conn: conn, database: database}
}

func (s *Store) table() string {
	return s.database + ".events"
}

// Close closes the underlying connection.
func (s *Store) Close() error {
	return s.conn.Close()
}

// allowedSortColumns whitelists what SearchParams.Sort may reference, since
// it's interpolated directly into the ORDER BY clause (query parameters
// can't be used for identifiers).
var allowedSortColumns = map[string]string{
	"created_at": "created_at",
	"event_type": "event_type",
	"source":     "source",
}

// SearchParams are the inputs to Search, already validated/defaulted by the
// API layer's Huma input struct.
type SearchParams struct {
	Query    string
	Sort     string
	Order    string
	Page     int
	PageSize int
}

// SearchResult is one page of matching events plus the total match count.
type SearchResult struct {
	Items    []model.Event
	Total    uint64
	Page     int
	PageSize int
	TookMS   int64
}

// Search translates a Lucene-style query into SQL and returns one sorted,
// paginated page of matching events along with the total match count.
//
// The total comes from a `count() OVER ()` window rather than a second
// COUNT(*) query: ClickHouse has to materialize every matching row to
// satisfy the window function regardless, so folding total-count into the
// same query trades nothing for one fewer round trip — a good trade given
// the whole point of the query language is to narrow a huge table down to a
// small matching subset (see design notes).
func (s *Store) Search(ctx context.Context, p SearchParams) (*SearchResult, error) {
	where, args, err := chquery.Translate(p.Query)
	if err != nil {
		return nil, err
	}

	sortCol, ok := allowedSortColumns[p.Sort]
	if !ok {
		sortCol = "created_at"
	}
	order := "DESC"
	if strings.EqualFold(p.Order, "asc") {
		order = "ASC"
	}

	pageSize := p.PageSize
	if pageSize <= 0 || pageSize > 500 {
		pageSize = 50
	}
	page := max(p.Page, 1)
	offset := (page - 1) * pageSize

	query := fmt.Sprintf(`
		SELECT
			toString(event_id) AS event_id,
			created_at,
			event_type,
			source,
			body,
			attributes,
			count() OVER () AS total_matched
		FROM %s
		WHERE %s
		ORDER BY %s %s, event_id
		LIMIT %d OFFSET %d`,
		s.table(), where, sortCol, order, pageSize, offset,
	)

	start := time.Now()
	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := &SearchResult{Page: page, PageSize: pageSize}
	for rows.Next() {
		var e model.Event
		var total uint64
		if err := rows.Scan(
			&e.EventID, &e.CreatedAt, &e.EventType, &e.Source, &e.Body, &e.Attributes, &total,
		); err != nil {
			return nil, fmt.Errorf("scan event row: %w", err)
		}
		result.Total = total
		result.Items = append(result.Items, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search rows: %w", err)
	}
	result.TookMS = time.Since(start).Milliseconds()
	return result, nil
}

// GetByID fetches a single event by its UUID string.
func (s *Store) GetByID(ctx context.Context, id string) (*model.Event, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, fmt.Errorf("%w: invalid event id", ErrNotFound)
	}

	query := fmt.Sprintf(`
		SELECT toString(event_id), created_at, event_type, source, body, attributes
		FROM %s
		WHERE event_id = ?
		LIMIT 1`,
		s.table(),
	)

	var e model.Event
	row := s.conn.QueryRow(ctx, query, id)
	if err := row.Scan(&e.EventID, &e.CreatedAt, &e.EventType, &e.Source, &e.Body, &e.Attributes); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get event: %w", err)
	}
	return &e, nil
}

// FacetCount is one value/count pair, e.g. one event_type and how many
// events have it.
type FacetCount struct {
	Value string `json:"value"`
	Count uint64 `json:"count"`
}

// Stats is the dataset-wide summary shown on the search page: total row
// count, top event types/sources, and the time range covered.
type Stats struct {
	TotalEvents uint64       `json:"total_events"`
	EventTypes  []FacetCount `json:"event_types"`
	Sources     []FacetCount `json:"sources"`
	EarliestAt  time.Time    `json:"earliest_at"`
	LatestAt    time.Time    `json:"latest_at"`
}

// Stats gathers the dataset summary. The total row count deliberately comes
// from system.parts rather than SELECT count() FROM events: it's the
// "zero-I/O metadata lookup" from the design notes, reading cached partition
// metadata instead of touching the table at all.
func (s *Store) Stats(ctx context.Context) (*Stats, error) {
	stats := &Stats{}

	totalRow := s.conn.QueryRow(ctx,
		`SELECT ifNull(sum(rows), 0) FROM system.parts WHERE database = ? AND table = 'events' AND active = 1`,
		s.database,
	)
	if err := totalRow.Scan(&stats.TotalEvents); err != nil {
		return nil, fmt.Errorf("total row count: %w", err)
	}

	rangeRow := s.conn.QueryRow(ctx, fmt.Sprintf(`SELECT min(created_at), max(created_at) FROM %s`, s.table()))
	if err := rangeRow.Scan(&stats.EarliestAt, &stats.LatestAt); err != nil {
		return nil, fmt.Errorf("time range: %w", err)
	}

	var err error
	stats.EventTypes, err = s.topValues(ctx, "event_type")
	if err != nil {
		return nil, err
	}
	stats.Sources, err = s.topValues(ctx, "source")
	if err != nil {
		return nil, err
	}

	return stats, nil
}

func (s *Store) topValues(ctx context.Context, column string) ([]FacetCount, error) {
	// column is one of the two hardcoded call sites above, never user input.
	query := fmt.Sprintf(`SELECT %s, count() AS c FROM %s GROUP BY %s ORDER BY c DESC LIMIT 15`, column, s.table(), column)
	rows, err := s.conn.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("top values for %s: %w", column, err)
	}
	defer func() { _ = rows.Close() }()

	var out []FacetCount
	for rows.Next() {
		var f FacetCount
		if err := rows.Scan(&f.Value, &f.Count); err != nil {
			return nil, fmt.Errorf("scan facet: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// PrepareEventBatch opens a batch insert against the events table for
// the seeder to append rows to. Columns are explicit (rather than relying
// on table column order) so the seeder stays correct if the schema grows.
func (s *Store) PrepareEventBatch(ctx context.Context) (driver.Batch, error) {
	query := fmt.Sprintf(
		"INSERT INTO %s (event_id, created_at, event_type, source, body, attributes)",
		s.table(),
	)
	return s.conn.PrepareBatch(ctx, query)
}
