// Package chquery translates Lucene-style search queries (`type:purchase AND
// owner_id:usr_42`, bare `usr_42`, `amount:>100`, `created_at:[a TO b]`, ...)
// into ClickHouse SQL, using github.com/grindlemire/go-lucene for parsing and
// a custom driver.Dialect for the ClickHouse-specific rendering.
//
// The interesting design decision lives in how a bare field name gets
// resolved (see chDialect.QuoteColumn):
//
//   - A handful of well-known names (event_id, created_at, event_type,
//     source, and some aliases) map to real typed columns.
//   - Anything else is assumed to be a JSON attribute and becomes a
//     `attributes['name']` Map subscript — the flattened-body column
//     described in internal/flatten. This is what lets `owner_id:usr_42` and
//     `eater_id:usr_42` both resolve to a fast, bloom-filter-accelerated
//     lookup without either key being declared ahead of time.
//   - A bare term with no field at all (`usr_42` on its own) is rendered as
//     `hasToken(body, 'usr_42')`, a tokenized haystack search across the raw
//     JSON payload backed by a tokenbf_v1 index — the "find this value
//     no matter which key it's under" case from the design notes.
package chquery

import (
	"fmt"
	"maps"
	"strings"

	"github.com/grindlemire/go-lucene/pkg/driver"
	"github.com/grindlemire/go-lucene/pkg/lucene/expr"
)

// defaultFieldMarker is handed to lucene.WithDefaultField so that every bare
// (field-less) term in the query gets wrapped as an Equals expression
// against this pseudo-field. chDialect.QuoteColumn recognizes it and swaps
// in freeTextSentinel instead of treating it as a real column/attribute, and
// the custom Equals render function turns that sentinel into a hasToken()
// search over the raw body.
const defaultFieldMarker = "_text"

// freeTextSentinel is a value QuoteColumn can hand back that can never
// collide with real rendered SQL: every other QuoteColumn result is wrapped
// in backticks or an attributes['...'] subscript, so a bare, unwrapped
// marker string can never equal one by accident. It deliberately avoids
// control characters (particularly NUL) because a `field:value` on the
// default field round-trips through go-lucene's generic Literal renderer,
// which runs the same validation it uses for real user-supplied literal
// values against whatever QuoteColumn returns.
const freeTextSentinel = "__lucene_free_text_sentinel__"

// exactFieldMarker/freeTextExactSentinel are the same trick as
// defaultFieldMarker/freeTextSentinel, for the exact-substring half of a
// multi-token free-text search. See rewriteFreeText in rewrite.go for why
// this exists: ClickHouse's hasToken() rejects any needle that itself
// contains a separator character, so a bare search for e.g. "usr_00042"
// (which contains '_') can't be a single hasToken() call.
const exactFieldMarker = "_text_exact"
const freeTextExactSentinel = "__lucene_free_text_exact_sentinel__"

// columnAliases maps recognized query field names (case-insensitive) to the
// real column they address. Everything else falls through to the
// attributes map.
var columnAliases = map[string]string{
	"event_id":   "event_id",
	"id":         "event_id",
	"created_at": "created_at",
	"time":       "created_at",
	"timestamp":  "created_at",
	"ts":         "created_at",
	"event_type": "event_type",
	"type":       "event_type",
	"source":     "source",
	"src":        "source",
}

// Driver renders a parsed Lucene expression to ClickHouse SQL.
type Driver struct {
	driver.Base
}

// NewDriver builds the ClickHouse driver: the operator-independent render
// functions come from driver.Shared, with Equals/Greater/Less/GreaterEq/
// LessEq overridden so they can special-case free-text search and numeric
// comparisons against the (always-String) attributes map.
func NewDriver() Driver {
	fns := make(map[expr.Operator]driver.RenderFN, len(driver.Shared))
	maps.Copy(fns, driver.Shared)
	fns[expr.Equals] = chEquals
	fns[expr.Greater] = chCompare(">")
	fns[expr.GreaterEq] = chCompare(">=")
	fns[expr.Less] = chCompare("<")
	fns[expr.LessEq] = chCompare("<=")

	return Driver{driver.Base{RenderFNs: fns, Dialect: chDialect{}}}
}

func isAttributeLookup(left string) bool {
	return strings.HasPrefix(left, "attributes[")
}

// chEquals implements field:value. Free-text bare terms become a haystack
// search over the raw JSON body (hasToken() for a single clean token, or
// position()>0 for the exact-match half rewriteFreeText adds when the
// original term contained separator characters); everything else is a
// plain equality, with map lookups cast through toString() so a bare
// numeric query term (parsed as a Go int by go-lucene) still compares
// cleanly against the String-typed attribute value instead of tripping a
// type mismatch.
func chEquals(left, right string) (string, error) {
	if left == freeTextSentinel {
		return fmt.Sprintf("hasToken(body, %s)", right), nil
	}
	if left == freeTextExactSentinel {
		return fmt.Sprintf("position(body, %s) > 0", right), nil
	}
	if isAttributeLookup(left) {
		return fmt.Sprintf("%s = toString(%s)", left, right), nil
	}
	return fmt.Sprintf("%s = %s", left, right), nil
}

// chCompare implements field:>N, field:>=N, field:<N, field:<=N. Attribute
// values are stored as strings, so ordering comparisons only make sense
// numerically if we cast the map value back to a number first; real typed
// columns compare directly.
func chCompare(op string) driver.RenderFN {
	return func(left, right string) (string, error) {
		if left == freeTextSentinel {
			return "", fmt.Errorf("comparison operators require an explicit field, e.g. amount%svalue", op)
		}
		if isAttributeLookup(left) {
			return fmt.Sprintf("toFloat64OrZero(%s) %s %s", left, op, right), nil
		}
		return fmt.Sprintf("%s %s %s", left, op, right), nil
	}
}

// chDialect implements driver.Dialect for ClickHouse.
type chDialect struct{}

func (chDialect) RenderLike(left, right string, isRegex bool) (string, error) {
	if left == freeTextSentinel {
		return "", fmt.Errorf("wildcard/regex searches require an explicit field, e.g. type:err*")
	}
	if isRegex {
		return fmt.Sprintf("match(%s, %s)", left, right), nil
	}
	return fmt.Sprintf("%s LIKE %s", left, right), nil
}

func (chDialect) RenderStandaloneWild(left string) (string, error) {
	if left == freeTextSentinel {
		// bare `*` (match everything)
		return "1=1", nil
	}
	// Map subscript access on a missing key and an empty real column both
	// read back as '', so "has any value" is "is not the empty string".
	return fmt.Sprintf("%s != ''", left), nil
}

// PrepareLikePattern converts Lucene wildcard syntax (* and ?) into
// ClickHouse's LIKE syntax (% and _), escaping any literal % or _ already
// present in the pattern first. ClickHouse's LIKE treats a backslash before
// % or _ as a literal match, no ESCAPE clause required.
func (chDialect) PrepareLikePattern(pattern string) (string, bool) {
	var b strings.Builder
	for _, r := range pattern {
		switch r {
		case '%':
			b.WriteString(`\%`)
		case '_':
			b.WriteString(`\_`)
		case '*':
			b.WriteByte('%')
		case '?':
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String(), false
}

// EscapeStringLiteral quotes and escapes a string for inline embedding.
// ClickHouse string literals use backslash escaping (like MySQL), not
// doubled quotes.
func (chDialect) EscapeStringLiteral(s string) string {
	return "'" + escapeCH(s) + "'"
}

func (chDialect) SerializeBool(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func (chDialect) BoolParam(b bool) any {
	if b {
		return 1
	}
	return 0
}

// QuoteColumn resolves a query field name to the SQL fragment that should
// stand in for it: the free-text sentinel, a real backtick-quoted column, or
// a Map subscript into the flattened JSON attributes.
func (chDialect) QuoteColumn(name string) (string, error) {
	if name == defaultFieldMarker {
		return freeTextSentinel, nil
	}
	if name == exactFieldMarker {
		return freeTextExactSentinel, nil
	}
	if col, ok := columnAliases[strings.ToLower(name)]; ok {
		return "`" + col + "`", nil
	}
	return "attributes['" + escapeCH(name) + "']", nil
}

// escapeCH escapes backslashes and single quotes for safe embedding inside a
// ClickHouse single-quoted string literal (used both for plain string
// literals and for the key inside an attributes['key'] subscript).
func escapeCH(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\'':
			b.WriteString(`\'`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
