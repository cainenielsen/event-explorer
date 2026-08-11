# Event Explorer

A small observability-style demo: ClickHouse + Go (Huma v2 API) + a vanilla
HTML/CSS/JS frontend embedded straight into the server binary, built to
search a huge table of arbitrary-JSON events with a Lucene-style query
language — find the small subset of events where `key:value` matches, even
at high value cardinality or nested several levels deep, sorted and
paginated — the way an observability platform lets you search logs, except
these are generic events with a free-form JSON body instead of a log line.

The interesting part isn't the CRUD; it's that a table with no declared
schema for its JSON payload stays fast to filter on *any* key, known or
not, at a million-plus rows on a laptop. [How this stays fast](#how-this-stays-fast)
below goes through the actual mechanism — which ClickHouse features are
doing the work and why, traced through a real query.

## Stack

- **ClickHouse** — storage. One `events` table, `MergeTree`, indexed for
  fast filtered search on both known and unknown JSON keys.
- **Go / [Huma v2](https://github.com/danielgtaylor/huma)** — the API
  (`internal/api`), on a [chi](https://github.com/go-chi/chi) router. Get
  request validation, error handling, and an OpenAPI spec (`/docs`) for
  free.
- **[clickhouse-go/v2](https://github.com/ClickHouse/clickhouse-go)** —
  native-protocol client, used for both queries and batch seeding.
- **[grindlemire/go-lucene](https://github.com/grindlemire/go-lucene)** —
  parses the Lucene-style query string into an AST; `internal/chquery`
  supplies a custom `driver.Dialect` that renders that AST to ClickHouse
  SQL (see below — this is the trickiest and most interesting part of the
  repo).
- **go:embed** — the entire frontend (`web/static/*`) is compiled into the
  server binary (`web/embed.go`); there's no separate static file
  deployment step.
- Vanilla HTML/CSS/JS frontend, no build step, no framework.

## Quick start

Requires Docker (with Compose v2). All commands from this directory:

```sh
make up      # docker compose up --build -d  (clickhouse + api)
make seed    # seed 1,000,000 synthetic events (takes well under a minute)
```

Then open **http://localhost:8080**. API docs (OpenAPI/Swagger UI) are at
**http://localhost:8080/docs**.

```sh
make down    # stop everything
make logs    # tail logs
```

To run against a smaller dataset while iterating: `make seed-small` (20k
rows). Both targets are safe to re-run — they only add rows.

### Running without Docker

```sh
# 1. Start ClickHouse however you like, e.g.:
docker run -d -p 9000:9000 -p 8123:8123 clickhouse/clickhouse-server:24.8

# 2. Run the server (creates the database/table on first connect)
go run ./cmd/server

# 3. Seed it
go run ./cmd/seed -count=1000000
```

Connection settings come from environment variables (see
`internal/config`): `CLICKHOUSE_ADDR` (default `localhost:9000`),
`CLICKHOUSE_DATABASE` (default `eventsdb`), `CLICKHOUSE_USER`,
`CLICKHOUSE_PASSWORD`, `HTTP_ADDR` (default `:8080`).

## The query language

A search box, not a SQL editor. Examples (also clickable in the UI):

| Query                                   | Meaning                                                                                                            |
| ---------------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| `owner_id:usr_00042`                    | events where the JSON body has `owner_id` = that value — works for _any_ key, not just ones declared ahead of time |
| `user.is_active:true`                   | nested JSON fields, dot-notation — `{"user": {"is_active": true}}` flattens to attribute key `user.is_active`      |
| `type:purchase`                         | top-level field match (aliases: `type`→`event_type`, `id`→`event_id`, `time`/`ts`→`created_at`, `src`→`source`)    |
| `type:book AND owner_id:usr_00042`      | boolean `AND`/`OR`/`NOT`, parentheses                                                                              |
| `type:err* OR type:fail*`               | wildcards (`*`, `?`)                                                                                                |
| `amount:>100`                           | numeric comparisons (`>`, `>=`, `<`, `<=`) against a JSON field                                                     |
| `created_at:[2024-01-01 TO 2024-01-02]` | ranges                                                                                                              |
| `"exact phrase"`                        | quoted phrase                                                                                                       |
| `usr_00042` (no field at all)           | **haystack search**: find this value under _any_ key in the JSON body, regardless of what the key is called        |

That last row is the scenario the seeder is deliberately built around: a
`book` event's `owner_id` and a `lunch` event's `eater_id` can hold the same
person, under different keys, in different event types. Searching a bare id
turns up a handful of events scattered across several event types and key
names, out of a million rows — see [Tracing a query end to end](#tracing-a-query-end-to-end)
for exactly how that stays fast.

## Architecture at a glance

```
web/static/*.html,css,js  --go:embed-->  web/embed.go
                                              │
cmd/server/main.go ── chi router ── humachi.New() ── internal/api  (Huma v2 handlers)
       │                                                     │
       └── internal/chstore.Open() (migrate + connect)       └── internal/chstore.Store
                                                                        │  Search / GetByID / Stats
                                                                        ▼
                                                      internal/chquery.Translate(query)
                                                          (go-lucene AST → ClickHouse SQL)
                                                                        │
                                                                        ▼
                                                                   ClickHouse

cmd/seed/main.go ── internal/seedgen (synthetic events) ── internal/flatten (JSON → Map)
                              │
                              └── chstore.Store.PrepareEventBatch (native batch insert)
```

## How this stays fast

Everything below is about one problem: the table's JSON payloads have no
fixed schema, so you can't pre-declare a column (and a normal index) for
every field a query might filter on. The schema, the indexes, and the query
translator are all answers to "how do you make an *unknown* key fast to
filter on" — from a few different angles that stack on top of each other.

### MergeTree, granules, and the primary key

Everything else here builds on one MergeTree mechanism, so it's worth being
precise about it first. A `MergeTree` table doesn't store one row at a
time or index every row individually. Rows are written in **granules** — in
this table, `index_granularity = 8192` rows per granule (ClickHouse's
default, set explicitly in `internal/chstore/schema.go` so the schema
doesn't quietly depend on a server default). For every granule, ClickHouse
records one entry in a small, in-memory **primary key index**: just the
first row's value(s) for the table's `ORDER BY` columns. That index is
*sparse* — one entry per 8192 rows, not per row — which is exactly why it
stays tiny (megabytes, not gigabytes) and fully cacheable even for a
billion-row table.

`events` is `ORDER BY (created_at, event_id)`. A query with a `created_at`
range can binary-search that small in-memory index, work out which
granules could possibly contain a matching row, and skip straight past
every other granule without reading a single byte of their column data.
Every technique below is additional machinery for the columns that
*aren't* a prefix of `ORDER BY` — `event_type`, `source`, and, critically,
anything inside the JSON body, none of which are sorted.

### The schema

```sql
CREATE TABLE events
(
    event_id    UUID DEFAULT generateUUIDv4(),
    created_at  DateTime64(3) DEFAULT now64(3),
    event_type  LowCardinality(String),
    source      LowCardinality(String),
    body        String,
    attributes  Map(LowCardinality(String), String),

    INDEX idx_event_type   event_type           TYPE set(100)              GRANULARITY 4,
    INDEX idx_source       source               TYPE set(100)              GRANULARITY 4,
    INDEX idx_body_tokens  body                 TYPE tokenbf_v1(32768, 3, 0) GRANULARITY 4,
    INDEX idx_attr_keys    mapKeys(attributes)   TYPE bloom_filter(0.01)    GRANULARITY 4,
    INDEX idx_attr_values  mapValues(attributes) TYPE bloom_filter(0.01)    GRANULARITY 4
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(created_at)
ORDER BY (created_at, event_id)
```

Two columns carry the actual JSON payload, for two different jobs:

- **`body String`** — the original payload, byte for byte. This exists so
  the UI can show you the real thing you sent in, and so free-text search
  (below) has raw text to tokenize.
- **`attributes Map(LowCardinality(String), String)`** — the payload
  *flattened*: `internal/flatten` walks the JSON recursively (nested
  objects become dot-joined keys — `{"user":{"is_active":true}}` →
  `user.is_active`; arrays are kept as their compact JSON re-encoding
  rather than exploded) and every scalar leaf becomes one map entry,
  stringified. This is what `owner_id:usr_00042` and `user.is_active:true`
  actually query against — not `body`, not a JSON-parsing function, a
  plain `Map` column.

Why `Map(LowCardinality(String), String)` specifically, not `Map(String,
String)`: `LowCardinality` dictionary-encodes a column — instead of
storing the same bytes over and over, it stores each distinct value once
in a small per-part dictionary and replaces every occurrence with a
compact integer code. Attribute *keys* are the perfect case for this:
`owner_id`, `amount`, `status`, `user.is_active` — a handful of distinct
key names repeat across every one of a million rows, so the dictionary
stays tiny while every row shrinks to an integer. Attribute *values* stay
plain `String` on purpose: `usr_00042`, order totals, tracking numbers —
genuinely high-cardinality data, where a growing dictionary would stop
paying for itself and can even work against you.

### Data skipping indexes

The primary key index only prunes granules by `ORDER BY` prefix. Everything
else on the table — `event_type`, `source`, every attribute key, every
attribute value, the raw body — needs its own, separate pruning mechanism:
a **data skipping index**, which stores small summary metadata per index
block (here, `GRANULARITY 4` → one summary per 4 granules, 32768 rows) and
lets the query engine ask "could this block possibly match?" before
reading any real data. This table uses three different kinds, because the
three problems they solve are genuinely different:

**`set(100)` on `event_type`/`source`.** For each block, store the actual
list of distinct values seen — as long as that list stays at or under 100
entries. A query for `type:purchase` checks the list; if `'purchase'`
isn't in it, the whole 32768-row block is skipped, guaranteed, no
approximation. This only works because `event_type`/`source` are
*low*-cardinality — a dozen event types, five sources — so almost every
block's list stays well under the cap. Push the same index onto a
high-cardinality column and it stops helping: past 100 distinct values in
a block, `set()` gives up on that block entirely and it must be read
unconditionally.

**`bloom_filter(0.01)` on `mapKeys(attributes)` / `mapValues(attributes)`.**
A Bloom filter is a fixed-size bit array plus a few hash functions: to
insert a value, hash it k ways and set those k bits; to test a value,
hash it the same k ways and check whether all k bits are set. If any bit
is unset, the value is **definitely absent** — skip. If all bits are set,
the value is **probably present** (the `0.01` is the target false-positive
rate) — read the block to be sure. The property that matters here: unlike
`set()`, a Bloom filter's size doesn't depend on how many distinct values
get packed in — it degrades gracefully (a slightly worse false-positive
rate) instead of failing outright. That's why attribute keys *and* values
— arbitrarily high-cardinality, exactly the case `set()` can't handle —
get this index instead. `mapKeys()`/`mapValues()` are ordinary ClickHouse
functions; indexing a function of a column, not just the column itself, is
what lets one index cover every key (and separately, every value) that
has ever been flattened into the map, without knowing any of them ahead
of time.

**`tokenbf_v1(32768, 3, 0)` on `body`.** Also a Bloom filter, but instead
of hashing one value per row, it *tokenizes* the string first — splitting
on anything that isn't `[A-Za-z0-9]`, which for JSON means every quote,
brace, colon, and comma is a delimiter — and inserts every resulting token
into the same block-level bit array. `{"owner_id":"usr_00042"}` becomes
the tokens `owner_id`, `usr`, `00042` (note: `usr_00042` splits on its own
underscore into two tokens — this comes up again below), all hashed into
one shared filter for the block. This is the index that makes the
free-text, "search any key" query actually possible: there's no way to
build a per-key index for keys you don't know in advance, but tokenizing
the raw bytes and treating *every* token as a candidate needle sidesteps
needing to know the keys at all. (`tokenbf_v1` also takes its size as an
explicit byte count and hash count rather than a target false-positive
rate like plain `bloom_filter()` — text tokenization produces a much less
predictable number of elements per block than a typical column value
count, so ClickHouse leaves that sizing decision to the schema author
instead of guessing.)

### PREWHERE: reading less of each row

A plain `WHERE` still has to decompress every column the query touches for
every granule before it can discard non-matching rows. `PREWHERE`
decompresses *only* the filter column first, evaluates it, and skips
decompressing the rest of that row's columns entirely for rows that don't
survive. On this table that specifically means: a selective filter means
the (potentially large) `body` and `attributes` columns never get read off
disk for the rows it eliminates. `internal/chstore/connect.go` sets
`optimize_move_to_prewhere` explicitly (it's ClickHouse's default) rather
than hand-writing `PREWHERE` into generated SQL — the query planner picks
the most selective condition to move automatically, using runtime
statistics the query translator doesn't have, so it consistently
outperforms a hardcoded guess.

### Counting without scanning

Two different "how many" questions, two different mechanisms, deliberately
not the same one:

- The dataset-wide total shown in the UI header (`Stats()` in
  `internal/chstore/store.go`) comes from
  `SELECT sum(rows) FROM system.parts WHERE ... active = 1`, not `SELECT
  count() FROM events`. MergeTree stores data in immutable **parts**
  (roughly: one per insert batch, until background merges combine them),
  and every part's row count is known the instant the part exists — it's
  metadata about the part's structure, not something that requires
  touching a single row of data. Summing it is a handful of in-memory
  reads regardless of whether the table holds a thousand rows or ten
  billion: zero I/O against the table itself.
- A search's per-query total (how many rows matched *this* filter) can't
  use that shortcut — it depends on the filter. `Search()` gets it from a
  `count() OVER ()` window function computed in the same query as the page
  of results, instead of a separate `COUNT(*)`. Since the entire point of
  the query language is narrowing a huge table to a small matching subset,
  ClickHouse already has to materialize that subset to satisfy the window
  function — folding the count into the same query removes a full second
  round trip for free.

### Tracing a query end to end

Take `owner_id:usr_00042 AND type:book` — cross-referencing two different
event shapes by an id that isn't a real column on either.

1. **Parse.** `go-lucene` turns the query string into an AST:
   `AND(Equals(owner_id, usr_00042), Equals(type, book))`.
2. **Translate** (`internal/chquery`). A custom `driver.Dialect`'s
   `QuoteColumn` hook resolves each field name: `type` is a known alias for
   the real `event_type` column; `owner_id` isn't recognized at all, so it
   becomes `attributes['owner_id']`. The generated SQL is
   `(attributes['owner_id'] = toString(?)) AND (event_type = ?)` with
   params `["usr_00042", "book"]` — see [The query translator](#the-query-translator)
   for the type-casting details packed into that `toString(...)`.
3. **Partition/primary-key pruning.** Neither condition touches
   `created_at`, so this step prunes nothing here — the query has to
   consider every partition and every granule from the primary key's point
   of view. (A time-scoped version of the same query, `owner_id:usr_00042
   AND created_at:[2024-06-01 TO 2024-06-02]`, would prune almost the
   entire table at this step alone.)
4. **Skip index consultation.** Before reading any column data, ClickHouse
   checks `idx_attr_values` (the Bloom filter on `mapValues(attributes)`)
   for `'usr_00042'` and `idx_event_type` (the `set()` index) for
   `'book'`, block by block. Blocks where the Bloom filter says "definitely
   not present" or the set index's list doesn't contain `'book'` are
   dropped without reading `attributes`, `body`, or anything else for
   those rows.
5. **PREWHERE.** For the surviving blocks, `event_type` (small, cheap) gets
   read and checked before `attributes` does, so blocks that pass the skip
   index but still don't actually match `type:book` skip the `attributes`
   read too.
6. **Materialize + count.** Whatever rows survive all of the above get
   assembled into full result rows, sorted per `ORDER BY created_at DESC,
   event_id`, sliced to the requested page, with `count() OVER ()`
   computing the total match count in the same pass.

The free-text case (`usr_00042`, no field) follows the same shape but
leans on `idx_body_tokens` instead of `idx_attr_values`, and only needs
`body` itself, PREWHERE-first — see the tokenizer note below for why it's
`hasToken(body,'usr') AND hasToken(body,'00042') AND position(body,
'usr_00042') > 0` rather than one `hasToken()` call.

### The query translator

`go-lucene` parses the query string into a generic AST; `internal/chquery`
supplies a ClickHouse `driver.Dialect` that renders it to SQL. The single
hook doing most of the work is `QuoteColumn`: a handful of names resolve to
real columns, everything else becomes an `attributes['name']` Map
subscript, and a bare term with no field at all becomes a sentinel value
that a custom `Equals` render function turns into `hasToken(body, ...)`
instead of `= ...`. A few correctness details worth calling out (each has
a test in `translate_test.go`):

- Attribute values are always strings (the Map's value type), but
  `go-lucene` parses a bare numeric query term as a Go `int`. Equality
  against an attribute wraps the value in `toString(...)` so
  `owner_id:12345` still compares string-to-string instead of silently
  mismatching types.
- Numeric comparisons (`amount:>100`) against an attribute instead cast the
  _column_ side — `toFloat64OrZero(attributes['amount']) > 100` — so
  ordering is numeric, not lexicographic string ordering.
- Wildcard/regex/comparison operators reject the free-text sentinel with a
  clear error ("comparison operators require an explicit field") rather
  than silently generating invalid SQL, since `hasToken()` has no
  wildcard/ordering equivalent.
- Nested JSON (`{"user": {"is_active": true}}`) needed no new code at all.
  `internal/flatten` already recurses into nested objects and joins the
  path with `.` (`user.is_active`), and go-lucene's lexer already treats
  `.` as a normal word character in a bare field name (the same rule that
  lets `12.5` lex as one token), so `user.is_active:true` arrives at
  `QuoteColumn` as the plain string `"user.is_active"` — indistinguishable
  from any other attribute key. It becomes
  `attributes['user.is_active'] = toString(?)`, same as a flat field.
- `hasToken()` rejects any needle that itself contains a separator
  character ("Needle must not contain whitespace or separator
  characters") — and the seeded ids (`usr_00042`) contain one, since
  `tokenbf_v1`/`hasToken` tokenize on underscores same as any other
  non-alphanumeric character. A bare free-text search value is tokenized
  in Go the same way (`internal/chquery/rewrite.go`), and a multi-token
  value gets rewritten from a single `hasToken()` call into
  `(hasToken(body,tok1) AND hasToken(body,tok2) ...) AND position(body,
  value) > 0` before rendering — each sub-token still gets the index
  prefilter, and the trailing `position()` guarantees an exact match
  rather than a false positive from two unrelated tokens co-occurring.

### Measured, not assumed

`EXPLAIN indexes = 1` against the live, seeded table, not a guess: on a
selective value, `hasToken()` pruned to 35/1235 granules — a measured
~6.5x wall-clock win (10ms vs 65ms). The tempting simplification of
swapping the tokenize-and-rewrite dance above for a single `body LIKE
'%value%'` was checked the same way and rejected: `LIKE` with wildcards on
both sides read 1235/1235 granules every time on this ClickHouse version —
zero pruning — even for a single clean token with no separators. The skip
index only activates through `hasToken()`/`has()`/`IN`, not substring
`LIKE`, regardless of how clean the pattern looks. Worth having actually
run the query rather than trusting the intuition that "it's basically the
same index."

### Known limitations

Reasonable trade-offs for a demo, called out rather than hidden: `IN`-lists
(`field:(a OR b OR c)`) against attributes don't get the same numeric
`toString` treatment as plain equality, so a numeric `IN` list against an
attribute may mismatch types. Range queries (`field:[a TO b]`) are solid
against `created_at`; against an attribute they compare as plain strings.
`hasToken` is case-sensitive.

## The seeder

`internal/seedgen`, `cmd/seed`. Twelve event types (`book`, `lunch`,
`login`, `purchase`, `signup`, `page_view`, `api_call`, `payment`, `error`,
`click`, `shipment`, `review`), each with a *differently named* "person"
field (`owner_id`, `eater_id`, `user_id`, `buyer_id`, ...). 70% of the time
that field is drawn from a shared pool of ~5,000 ids; 30% of the time it's
a one-off unique id — so searching one id typically turns up a handful of
events scattered across several different event types and key names, out
of a million rows, which is exactly the cross-domain haystack scenario the
free-text query and `tokenbf_v1` index above exist for. A few event types
(`login`, `purchase`, `shipment`) also nest a sub-object (`user`,
`payment_method`, `address`) so nested-field queries get exercised end to
end, not just at the unit-test level. Inserts use the native batch API
(`conn.PrepareBatch`/`Append`/`Send`) fanned out across `NumCPU` worker
goroutines, each with its own seeded generator and its own batches, for a
fast 1M-row load.

## API

- `GET /api/events?q=...&sort=created_at&order=desc&page=1&page_size=50`
- `GET /api/events/{id}`
- `GET /api/stats`
- `GET /api/health`
- `GET /docs` — interactive OpenAPI docs (generated by Huma)

## Repo layout

```
cmd/server/           API server entrypoint (chi + huma + embedded frontend)
cmd/seed/              seeder entrypoint
internal/model/        Event shape shared across layers
internal/flatten/       JSON -> Map(String,String) flattening
internal/chquery/       Lucene -> ClickHouse SQL (the go-lucene dialect)
internal/chstore/       schema migration, Search/GetByID/Stats, batch insert
internal/seedgen/       synthetic event generation
internal/api/           Huma v2 handlers/types
web/static/             frontend (embedded via web/embed.go)
docker-compose.yml, Dockerfile, Makefile
```
