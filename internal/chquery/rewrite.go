package chquery

import (
	"fmt"

	"github.com/grindlemire/go-lucene/pkg/lucene/expr"
)

// rewriteFreeText walks a parsed AST and expands bare free-text terms
// (`usr_00042`, with no `field:` prefix) whose value contains separator
// characters into an index-friendly, ClickHouse-safe form.
//
// Why this is needed: ClickHouse's hasToken() throws
// "Needle must not contain whitespace or separator characters" if the
// needle itself isn't a single clean token — and tokenbf_v1's tokenizer
// (and hasToken's) splits on anything that isn't [A-Za-z0-9], so an id like
// "usr_00042" is two tokens, not one. A bare hasToken(body, 'usr_00042')
// fails outright rather than just failing to match.
//
// The fix is a fast-prefilter-then-exact-check pattern: split the term into
// its sub-tokens, AND together a hasToken() check per sub-token (each one is
// index-accelerated by tokenbf_v1) to prune down to a small set of candidate
// granules, then AND a plain position(body, term) > 0 for an exact match so
// "usr" and "00042" appearing in unrelated fields of the same row can't
// produce a false positive. See the README ("How this stays fast") for the
// index mechanism this leans on.
//
// Single-token terms (no separators) are left untouched — plain
// hasToken(body, term) already works and is already exact.
func rewriteFreeText(e *expr.Expression) *expr.Expression {
	if e == nil {
		return nil
	}
	if isFreeTextEquals(e) {
		if rewritten, ok := expandFreeText(e); ok {
			return rewritten
		}
		return e
	}
	e.Left = rewriteAny(e.Left)
	e.Right = rewriteAny(e.Right)
	return e
}

func rewriteAny(v any) any {
	switch t := v.(type) {
	case *expr.Expression:
		return rewriteFreeText(t)
	case []*expr.Expression:
		for i, sub := range t {
			t[i] = rewriteFreeText(sub)
		}
		return t
	default:
		return v
	}
}

// isFreeTextEquals reports whether e is `Equals(Column(defaultFieldMarker), <literal>)`.
// go-lucene produces the Column side in one of two equivalent shapes
// depending on which parser path built it (a bare expr.Column, or a
// Literal expression wrapping one) — columnName handles both.
func isFreeTextEquals(e *expr.Expression) bool {
	if e.Op != expr.Equals {
		return false
	}
	name, ok := columnName(e.Left)
	return ok && name == defaultFieldMarker
}

func columnName(v any) (string, bool) {
	switch t := v.(type) {
	case expr.Column:
		return string(t), true
	case *expr.Expression:
		if t.Op == expr.Literal {
			if col, ok := t.Left.(expr.Column); ok {
				return string(col), true
			}
		}
	}
	return "", false
}

func literalValue(v any) (any, bool) {
	e, ok := v.(*expr.Expression)
	if !ok || e.Op != expr.Literal {
		return nil, false
	}
	return e.Left, true
}

// expandFreeText tokenizes the free-text term's value and, if it's more
// than one token, rebuilds the node as described above. ok is false when no
// rewrite is needed (single token, or a value shape it doesn't recognize),
// in which case the caller keeps the original node.
func expandFreeText(e *expr.Expression) (*expr.Expression, bool) {
	val, ok := literalValue(e.Right)
	if !ok {
		return nil, false
	}
	s := fmt.Sprintf("%v", val)

	tokens := tokenize(s)
	if len(tokens) <= 1 {
		return nil, false
	}

	var combined *expr.Expression
	for _, tok := range tokens {
		cond := expr.Eq(expr.Column(defaultFieldMarker), expr.Lit(tok))
		if combined == nil {
			combined = cond
		} else {
			combined = expr.AND(combined, cond)
		}
	}

	exact := expr.Eq(expr.Column(exactFieldMarker), expr.Lit(s))
	return expr.AND(combined, exact), true
}

// tokenize splits s the same way ClickHouse's default tokenizer does for
// tokenbf_v1/hasToken: any run of characters outside [A-Za-z0-9] is a
// delimiter.
func tokenize(s string) []string {
	var tokens []string
	start := -1
	for i, r := range s {
		isWord := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if isWord {
			if start == -1 {
				start = i
			}
			continue
		}
		if start != -1 {
			tokens = append(tokens, s[start:i])
			start = -1
		}
	}
	if start != -1 {
		tokens = append(tokens, s[start:])
	}
	return tokens
}
