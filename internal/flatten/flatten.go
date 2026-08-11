// Package flatten turns an arbitrary JSON document into a flat
// map[string]string of dot-notation-key -> stringified value.
//
// This is a key-value flattening pattern: rather than trying to index every
// possible JSON shape a domain might send, we flatten each event's body once
// at write time into a ClickHouse Map(LowCardinality(String), String)
// column. That lets `owner_id:usr_42` and `eater_id:usr_42` both resolve to
// a plain `attributes['owner_id'] = 'usr_42'` lookup, accelerated by a
// bloom_filter index on mapKeys()/mapValues(), with zero knowledge of the
// payload shape ahead of time. See the README ("How this stays fast") for
// the full mechanism.
package flatten

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
)

// maxDepth bounds how far into nested objects we recurse before giving up
// and storing the remainder as a single JSON-encoded string value. This
// keeps pathological payloads from exploding into thousands of map entries.
const maxDepth = 4

// Flatten decodes raw JSON and returns its dot-notation flattening.
// Arrays are stored as their compact JSON re-encoding rather than exploded
// into indexed keys (array-of-scalars vs array-of-objects would otherwise
// need different handling, and observability payloads rarely filter on
// array position).
func Flatten(raw []byte) (map[string]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}

	out := map[string]string{}
	walk("", v, out, 0)
	return out, nil
}

func walk(prefix string, v any, out map[string]string, depth int) {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 {
			if prefix != "" {
				out[prefix] = "{}"
			}
			return
		}
		if depth >= maxDepth {
			out[prefix] = encode(t)
			return
		}
		// Sort keys for deterministic output (map iteration order isn't
		// stable, and stable insert order makes the generated SQL/log output
		// easier to read while testing).
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			walk(key, t[k], out, depth+1)
		}
	case []any:
		out[prefix] = encode(t)
	case string:
		out[prefix] = t
	case json.Number:
		out[prefix] = t.String()
	case bool:
		out[prefix] = strconv.FormatBool(t)
	case nil:
		out[prefix] = ""
	}
}

func encode(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
