package chquery

import "testing"

func TestTranslate(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
		args  int
	}{
		{"empty", "", "1=1", 0},
		{"bare free text, single token", "usr42", "hasToken(body, ?)", 1},
		{"top level field", "type:purchase", "`event_type` = ?", 1},
		{"attribute field", "owner_id:usr42", "attributes['owner_id'] = toString(?)", 1},
		{"alias field", "id:abc123", "`event_id` = ?", 1},
		{"and", "type:book AND owner_id:usr42", "(`event_type` = ?) AND (attributes['owner_id'] = toString(?))", 2},
		{"or bare terms, single token", "abc OR xyz", "(hasToken(body, ?)) OR (hasToken(body, ?))", 2},
		{"not", "NOT source:web", "NOT(`source` = ?)", 1},
		{"wildcard", "type:err*", "`event_type` LIKE ?", 1},
		{"numeric compare", "amount:>100", "toFloat64OrZero(attributes['amount']) > ?", 1},
		{"range", "created_at:[2024-01-01 TO 2024-01-02]", "`created_at` BETWEEN ? AND ?", 2},
		{"exists", "owner_id:*", "attributes['owner_id'] != ''", 0},
		{"quoted phrase", `body:"hello world"`, "attributes['body'] = toString(?)", 1},
		{"implicit and", "type:book owner_id:usr42", "(`event_type` = ?) AND (attributes['owner_id'] = toString(?))", 2},
		// ClickHouse's hasToken() rejects a needle that itself contains
		// separator characters ("Needle must not contain whitespace or
		// separator characters"), and tokenbf_v1/hasToken split on anything
		// outside [A-Za-z0-9] — so "usr_00042" is two tokens, not one. This
		// is exactly the shape the seeder generates (see internal/seedgen),
		// so it's the realistic case, not an edge case.
		{
			"bare free text, multi-token id",
			"usr_00042",
			"((hasToken(body, ?)) AND (hasToken(body, ?))) AND (position(body, ?) > 0)",
			3,
		},
		{
			"or bare terms, multi-token ids",
			"usr_00042 OR usr_00043",
			"(((hasToken(body, ?)) AND (hasToken(body, ?))) AND (position(body, ?) > 0)) OR " +
				"(((hasToken(body, ?)) AND (hasToken(body, ?))) AND (position(body, ?) > 0))",
			6,
		},
		{
			"field-scoped multi-token value is untouched",
			// only bare (field-less) terms get the tokenize-and-rewrite
			// treatment; field:value always renders as a single toString()
			// equality regardless of what's in the value.
			"owner_id:usr_00042",
			"attributes['owner_id'] = toString(?)",
			1,
		},
		// Nested JSON (see internal/flatten and internal/seedgen, e.g. the
		// "login" event's user.is_active) flattens to dot-notation attribute
		// keys, and the field name lexer treats '.' as part of a bare word
		// (see go-lucene's lexWord), so a dotted field name is just another
		// attribute key — no special-casing needed anywhere in this package.
		{"nested field", "user.is_active:true", "attributes['user.is_active'] = toString(?)", 1},
		{"nested field, deeper path", "address.city:Seattle", "attributes['address.city'] = toString(?)", 1},
		{"nested field in AND", "user.is_active:true AND type:login", "(attributes['user.is_active'] = toString(?)) AND (`event_type` = ?)", 2},
		{"nested field wildcard", "payment_method.type:pay*", "attributes['payment_method.type'] LIKE ?", 1},
		{"nested field exists", "user.is_active:*", "attributes['user.is_active'] != ''", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := Translate(tc.query)
			if err != nil {
				t.Fatalf("Translate(%q) error: %v", tc.query, err)
			}
			if sql != tc.want {
				t.Errorf("Translate(%q) sql =\n  %s\nwant\n  %s", tc.query, sql, tc.want)
			}
			if len(args) != tc.args {
				t.Errorf("Translate(%q) args = %v (len %d), want len %d", tc.query, args, len(args), tc.args)
			}
		})
	}
}

func TestTranslateErrors(t *testing.T) {
	cases := []string{
		"amount:>100 AND ",
		"AND AND",
	}
	for _, q := range cases {
		if _, _, err := Translate(q); err == nil {
			t.Errorf("Translate(%q) expected error, got none", q)
		}
	}
}

func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"usr_00042", []string{"usr", "00042"}},
		{"usr42", []string{"usr42"}},
		{"12.5", []string{"12", "5"}},
		{"a-b_c.d", []string{"a", "b", "c", "d"}},
		{"", nil},
		{"___", nil},
	}
	for _, tc := range cases {
		got := tokenize(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("tokenize(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("tokenize(%q) = %v, want %v", tc.in, got, tc.want)
				break
			}
		}
	}
}
