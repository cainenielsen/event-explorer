// Command server runs the events query API and serves the embedded
// frontend from a single binary.
package main

import (
	"context"
	"log"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"eventquery/internal/api"
	"eventquery/internal/chstore"
	"eventquery/internal/config"
	"eventquery/web"
)

func main() {
	ctx := context.Background()

	chCfg := config.ClickHouseFromEnv()
	log.Printf("connecting to clickhouse at %s (database %q)...", chCfg.Addr, chCfg.Database)
	conn, err := chstore.Open(ctx, chCfg)
	if err != nil {
		log.Fatalf("clickhouse: %v", err)
	}
	store := chstore.New(conn, chCfg.Database)
	defer func() { _ = store.Close() }()
	log.Print("clickhouse ready, schema migrated")

	router := chi.NewMux()
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)

	humaCfg := huma.DefaultConfig("Events Query API", "1.0.0")
	humaCfg.Info.Description = "Search a huge ClickHouse-backed events table using a Lucene-style query language. " +
		"See /docs for interactive API docs."
	humaAPI := humachi.New(router, humaCfg)
	api.Register(humaAPI, store)

	// Everything that isn't an API route or the generated docs falls
	// through to the embedded single-page frontend.
	router.Handle("/*", http.FileServer(http.FS(web.FS())))

	addr := config.HTTPAddr()
	log.Printf("listening on %s (UI at http://localhost%s, API docs at http://localhost%s/docs)", addr, addr, addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatal(err)
	}
}
