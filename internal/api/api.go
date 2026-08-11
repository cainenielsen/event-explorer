// Package api wires the HTTP surface: three read endpoints over the
// ClickHouse-backed event store, registered with Huma v2 so they get
// request validation and an OpenAPI spec for free.
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"eventquery/internal/chstore"
)

// Register adds the events API's operations to a Huma API instance. Every
// path is registered under /api so it can share a router with the embedded
// frontend without colliding.
func Register(humaAPI huma.API, store *chstore.Store) {
	huma.Register(humaAPI, huma.Operation{
		OperationID: "search-events",
		Method:      http.MethodGet,
		Path:        "/api/events",
		Summary:     "Search events",
		Description: "Filter, sort, and paginate the events table using a Lucene-style query language.",
		Tags:        []string{"events"},
	}, searchHandler(store))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "get-event",
		Method:      http.MethodGet,
		Path:        "/api/events/{id}",
		Summary:     "Get one event by id",
		Tags:        []string{"events"},
	}, getEventHandler(store))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "get-stats",
		Method:      http.MethodGet,
		Path:        "/api/stats",
		Summary:     "Dataset summary",
		Description: "Total row count (read from part metadata, not scanned) plus top event types and sources.",
		Tags:        []string{"events"},
	}, statsHandler(store))

	huma.Register(humaAPI, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/api/health",
		Summary:     "Health check",
		Tags:        []string{"meta"},
	}, healthHandler())
}

func searchHandler(store *chstore.Store) func(context.Context, *SearchInput) (*SearchOutput, error) {
	return func(ctx context.Context, in *SearchInput) (*SearchOutput, error) {
		res, err := store.Search(ctx, chstore.SearchParams{
			Query:    in.Query,
			Sort:     in.Sort,
			Order:    in.Order,
			Page:     in.Page,
			PageSize: in.PageSize,
		})
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}

		items := make([]EventDTO, 0, len(res.Items))
		for _, e := range res.Items {
			items = append(items, toDTO(e))
		}

		out := &SearchOutput{}
		out.Body = SearchOutputBody{
			Items:    items,
			Total:    res.Total,
			Page:     res.Page,
			PageSize: res.PageSize,
			TookMs:   res.TookMS,
		}
		return out, nil
	}
}

func getEventHandler(store *chstore.Store) func(context.Context, *GetEventInput) (*GetEventOutput, error) {
	return func(ctx context.Context, in *GetEventInput) (*GetEventOutput, error) {
		e, err := store.GetByID(ctx, in.ID)
		if err != nil {
			if errors.Is(err, chstore.ErrNotFound) {
				return nil, huma.Error404NotFound("event not found")
			}
			return nil, huma.Error500InternalServerError(err.Error())
		}
		out := &GetEventOutput{Body: toDTO(*e)}
		return out, nil
	}
}

func statsHandler(store *chstore.Store) func(context.Context, *struct{}) (*StatsOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*StatsOutput, error) {
		stats, err := store.Stats(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		out := &StatsOutput{}
		out.Body = StatsOutputBody{
			TotalEvents: stats.TotalEvents,
			EventTypes:  stats.EventTypes,
			Sources:     stats.Sources,
			EarliestAt:  stats.EarliestAt,
			LatestAt:    stats.LatestAt,
		}
		return out, nil
	}
}

func healthHandler() func(context.Context, *struct{}) (*HealthOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*HealthOutput, error) {
		out := &HealthOutput{}
		out.Body.Status = "ok"
		return out, nil
	}
}
