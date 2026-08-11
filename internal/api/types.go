package api

import (
	"encoding/json"
	"time"

	"eventquery/internal/chstore"
	"eventquery/internal/model"
)

// EventDTO is an event as it goes out over the wire. Body is re-embedded as
// raw JSON (not a JSON-encoded string) so API consumers get the original
// payload shape back untouched.
type EventDTO struct {
	EventID    string            `json:"event_id" doc:"Event UUID"`
	CreatedAt  time.Time         `json:"created_at"`
	EventType  string            `json:"event_type"`
	Source     string            `json:"source"`
	Body       json.RawMessage   `json:"body" doc:"Original JSON payload, unmodified"`
	Attributes map[string]string `json:"attributes" doc:"Body flattened to dot-notation key -> stringified value; this is what the query language filters against"`
}

func toDTO(e model.Event) EventDTO {
	return EventDTO{
		EventID:    e.EventID,
		CreatedAt:  e.CreatedAt,
		EventType:  e.EventType,
		Source:     e.Source,
		Body:       json.RawMessage(e.Body),
		Attributes: e.Attributes,
	}
}

// SearchInput is the /events query string.
type SearchInput struct {
	Query    string `query:"q" doc:"Lucene-style query. Examples: owner_id:usr_42 · type:purchase AND source:web · amount:>100 · \"usr_42\" (bare term = search every field)"`
	Sort     string `query:"sort" enum:"created_at,event_type,source" default:"created_at"`
	Order    string `query:"order" enum:"asc,desc" default:"desc"`
	Page     int    `query:"page" default:"1" minimum:"1" doc:"1-indexed page number"`
	PageSize int    `query:"page_size" default:"50" minimum:"1" maximum:"500"`
}

type SearchOutputBody struct {
	Items    []EventDTO `json:"items"`
	Total    uint64     `json:"total" doc:"Total events matching the query, not just this page"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	TookMs   int64      `json:"took_ms"`
}

type SearchOutput struct {
	Body SearchOutputBody
}

type GetEventInput struct {
	ID string `path:"id" doc:"Event UUID"`
}

type GetEventOutput struct {
	Body EventDTO
}

type StatsOutputBody struct {
	TotalEvents uint64               `json:"total_events"`
	EventTypes  []chstore.FacetCount `json:"event_types"`
	Sources     []chstore.FacetCount `json:"sources"`
	EarliestAt  time.Time            `json:"earliest_at"`
	LatestAt    time.Time            `json:"latest_at"`
}

type StatsOutput struct {
	Body StatsOutputBody
}

type HealthOutput struct {
	Body struct {
		Status string `json:"status" example:"ok"`
	}
}
