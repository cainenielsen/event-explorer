package flatten

import "testing"

func TestFlatten(t *testing.T) {
	raw := []byte(`{
		"owner_id": "usr_42",
		"price": 12.5,
		"pages": 240,
		"in_stock": true,
		"tags": ["fiction", "sale"],
		"address": {"city": "Seattle", "zip": "98101"},
		"note": null
	}`)

	got, err := Flatten(raw)
	if err != nil {
		t.Fatalf("Flatten error: %v", err)
	}

	want := map[string]string{
		"owner_id":     "usr_42",
		"price":        "12.5",
		"pages":        "240",
		"in_stock":     "true",
		"tags":         `["fiction","sale"]`,
		"address.city": "Seattle",
		"address.zip":  "98101",
		"note":         "",
	}

	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("Flatten produced %d keys, want %d: %v", len(got), len(want), got)
	}
}

func TestFlattenDepthLimit(t *testing.T) {
	raw := []byte(`{"a":{"b":{"c":{"d":{"e":"too deep"}}}}}`)
	got, err := Flatten(raw)
	if err != nil {
		t.Fatalf("Flatten error: %v", err)
	}
	// depth 0=a, 1=b, 2=c, 3=d -> at depth>=maxDepth(4) the remainder under
	// "a.b.c.d" is stored as a single JSON-encoded value instead of
	// recursing further.
	if _, ok := got["a.b.c.d"]; !ok {
		t.Errorf("expected a.b.c.d to be collapsed, got keys: %v", got)
	}
}
