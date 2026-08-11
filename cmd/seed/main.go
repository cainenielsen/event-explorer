// Command seed populates the events table with synthetic data (1,000,000
// rows by default) for the demo. See internal/seedgen for what gets
// generated and why.
package main

import (
	"context"
	"flag"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"eventquery/internal/chstore"
	"eventquery/internal/config"
	"eventquery/internal/flatten"
	"eventquery/internal/seedgen"
)

func main() {
	total := flag.Int("count", 1_000_000, "number of events to generate")
	batchSize := flag.Int("batch", 5000, "rows per insert batch")
	workers := flag.Int("workers", runtime.NumCPU(), "concurrent insert workers")
	personPool := flag.Int("persons", 5000, "size of the shared person-id pool (creates cross-event-type overlaps)")
	lookbackDays := flag.Int("lookback-days", 90, "spread created_at over this many past days")
	seed := flag.Int64("seed", time.Now().UnixNano(), "base random seed")
	flag.Parse()

	if *workers < 1 {
		*workers = 1
	}

	ctx := context.Background()
	chCfg := config.ClickHouseFromEnv()
	log.Printf("connecting to clickhouse at %s (database %q)...", chCfg.Addr, chCfg.Database)
	conn, err := chstore.Open(ctx, chCfg)
	if err != nil {
		log.Fatalf("clickhouse: %v", err)
	}
	store := chstore.New(conn, chCfg.Database)
	defer func() { _ = store.Close() }()

	log.Printf("seeding %d events across %d worker(s) (batch size %d, person pool %d, lookback %dd)...",
		*total, *workers, *batchSize, *personPool, *lookbackDays)

	var produced int64
	start := time.Now()

	rowsPerWorker := *total / *workers
	remainder := *total % *workers

	var wg sync.WaitGroup
	errCh := make(chan error, *workers)

	for w := 0; w < *workers; w++ {
		n := rowsPerWorker
		if w < remainder {
			n++
		}
		if n == 0 {
			continue
		}
		workerSeed := *seed + int64(w)*7919 // distinct but deterministic per worker
		wg.Add(1)
		go func(n int, workerSeed int64) {
			defer wg.Done()
			if err := seedWorker(ctx, store, n, *batchSize, *personPool, *lookbackDays, workerSeed, &produced); err != nil {
				errCh <- err
			}
		}(n, workerSeed)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
progress:
	for {
		select {
		case <-done:
			break progress
		case <-ticker.C:
			p := atomic.LoadInt64(&produced)
			elapsed := time.Since(start).Seconds()
			rate := float64(p) / elapsed
			log.Printf("  %d/%d rows inserted (%.0f rows/sec)", p, *total, rate)
		}
	}

	close(errCh)
	for err := range errCh {
		if err != nil {
			log.Fatalf("seed worker failed: %v", err)
		}
	}

	elapsed := time.Since(start)
	rate := float64(*total) / elapsed.Seconds()
	log.Printf("done: %d events in %s (%.0f rows/sec)", *total, elapsed.Round(time.Millisecond), rate)
}

// seedWorker generates and inserts n rows in batches of batchSize, using its
// own Generator (seeded independently) and its own batch insert so workers
// never share mutable state.
func seedWorker(ctx context.Context, store *chstore.Store, n, batchSize, personPool, lookbackDays int, seed int64, produced *int64) error {
	gen := seedgen.New(seed, personPool, lookbackDays)

	inserted := 0
	for inserted < n {
		thisBatch := batchSize
		if remaining := n - inserted; remaining < thisBatch {
			thisBatch = remaining
		}

		batch, err := store.PrepareEventBatch(ctx)
		if err != nil {
			return err
		}

		for i := 0; i < thisBatch; i++ {
			ev, err := gen.Next()
			if err != nil {
				_ = batch.Close()
				return err
			}
			attrs, err := flatten.Flatten(ev.Body)
			if err != nil {
				_ = batch.Close()
				return err
			}
			if err := batch.Append(uuid.New(), ev.CreatedAt, ev.EventType, ev.Source, string(ev.Body), attrs); err != nil {
				_ = batch.Close()
				return err
			}
		}

		if err := batch.Send(); err != nil {
			return err
		}
		_ = batch.Close()

		inserted += thisBatch
		atomic.AddInt64(produced, int64(thisBatch))
	}
	return nil
}
