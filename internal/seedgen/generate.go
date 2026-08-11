// Package seedgen generates synthetic events for the demo dataset.
//
// The interesting bit isn't the field lists below, it's the shared person-id
// pool: different event types name "the person" with a different JSON key
// (owner_id, eater_id, buyer_id, ...) but draw that value from the same
// pool most of the time. That reproduces the exact scenario the design
// notes call out — "a book event's owner_id might match a lunch event's
// eater_id" — so a free-text search for one id turns up a small, scattered
// handful of events across many event types and keys, which is the thing
// the tokenbf_v1 haystack index and the bare-term query syntax exist for.
package seedgen

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"time"
)

// Generated is one not-yet-flattened synthetic event.
type Generated struct {
	EventType string
	Source    string
	CreatedAt time.Time
	Body      []byte
}

type template struct {
	eventType string
	fields    func(r *rand.Rand, g *Generator) map[string]any
}

// Generator produces a stream of synthetic events from a seeded RNG. It is
// not safe for concurrent use; the seeder gives each worker goroutine its
// own Generator with a distinct seed instead.
type Generator struct {
	rng       *rand.Rand
	personIDs []string
	sources   []string
	templates []template
	lookback  time.Duration
}

// New builds a Generator. personPoolSize controls how many distinct shared
// person ids exist to create cross-event-type overlaps; lookbackDays spreads
// created_at over that many past days ending now.
func New(seed int64, personPoolSize, lookbackDays int) *Generator {
	r := rand.New(rand.NewSource(seed))

	ids := make([]string, personPoolSize)
	for i := range ids {
		ids[i] = fmt.Sprintf("usr_%05d", i+1)
	}

	g := &Generator{
		rng:       r,
		personIDs: ids,
		sources:   []string{"web", "mobile", "api", "partner-a", "partner-b"},
		lookback:  time.Duration(lookbackDays) * 24 * time.Hour,
	}
	g.templates = g.buildTemplates()
	return g
}

// Next produces one synthetic event.
func (g *Generator) Next() (Generated, error) {
	tmpl := g.templates[g.rng.Intn(len(g.templates))]
	fields := tmpl.fields(g.rng, g)

	body, err := json.Marshal(fields)
	if err != nil {
		return Generated{}, fmt.Errorf("marshal %s event: %w", tmpl.eventType, err)
	}

	return Generated{
		EventType: tmpl.eventType,
		Source:    choice(g.rng, g.sources),
		CreatedAt: g.randomTime(),
		Body:      body,
	}, nil
}

// person returns a person id. 70% of the time it's drawn from the shared
// pool (creating the cross-event-type overlaps the query language demo
// relies on); 30% of the time it's a one-off id unique to this event.
func (g *Generator) person() string {
	if g.rng.Float64() < 0.7 {
		return g.personIDs[g.rng.Intn(len(g.personIDs))]
	}
	return fmt.Sprintf("usr_%d", g.rng.Int63())
}

func (g *Generator) randomTime() time.Time {
	offset := time.Duration(g.rng.Int63n(int64(g.lookback)))
	return time.Now().UTC().Add(-offset)
}

func choice[T any](r *rand.Rand, options []T) T {
	return options[r.Intn(len(options))]
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

func randomIP(r *rand.Rand) string {
	return fmt.Sprintf("%d.%d.%d.%d", r.Intn(256), r.Intn(256), r.Intn(256), r.Intn(256))
}
