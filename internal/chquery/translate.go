package chquery

import (
	"fmt"
	"strings"

	lucene "github.com/grindlemire/go-lucene"
)

var chDriver = NewDriver()

// Translate parses a Lucene-style query string and renders it to a
// ClickHouse WHERE-clause fragment plus its positional `?` parameters.
// An empty/whitespace-only query matches everything.
//
// Examples this accepts:
//
//	usr_42                                  -> hasToken(body, ?) over the raw payload
//	type:purchase                           -> event_type = ?
//	owner_id:usr_42 AND type:book           -> attributes['owner_id'] = toString(?) AND event_type = ?
//	type:err* OR type:fail*                 -> event_type LIKE ? OR event_type LIKE ?
//	amount:>100                             -> toFloat64OrZero(attributes['amount']) > ?
//	created_at:[2024-01-01 TO 2024-01-02]   -> `created_at` BETWEEN ? AND ?
//	NOT source:web                          -> NOT (`source` = ?)
func Translate(query string) (whereSQL string, args []any, err error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "1=1", nil, nil
	}

	ast, err := lucene.Parse(query, lucene.WithDefaultField(defaultFieldMarker))
	if err != nil {
		return "", nil, fmt.Errorf("invalid query: %w", err)
	}
	ast = rewriteFreeText(ast)

	sql, params, err := chDriver.RenderParam(ast)
	if err != nil {
		return "", nil, fmt.Errorf("invalid query: %w", err)
	}
	if params == nil {
		params = []any{}
	}
	return sql, params, nil
}
