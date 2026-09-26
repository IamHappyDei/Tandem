package syn

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

var (
	volatileKey = regexp.MustCompile(`(?i)^(progress|percent|pct|completion|elapsed|remaining|time|timer|secs?|seconds|minutes|count|qty|quantity|amount|liters?|litres?|gallons?|kg|lb|weight|fuel|pax|passengers|bags?|suitcases|lat|lon|alt|agl|speed|cur|x|y|z|pos|position|heading|rpm|volts?|hz)$`)

	stableKey = regexp.MustCompile(`(?i)^(state|status|phase|stage|step|index|gen|id|mode|type|door|doors|armed|enabled|active|connected|on|off|ready|done|finished|available|busy|stateraw|statecode|statuscode|stateid)$`)

	noiseKey = regexp.MustCompile(`(?i)^(statetext|progresstext|statustext|text|caption|description|desc|label|displayname|title|message|html|statushtml|tooltip|hint|timestamp|time|datetime|duration|eta|icon|iconsvg|svg|url|path|note|detail|substate)$`)
)

func Project(value any, keyHint string, dropNoise bool) any {
	switch v := value.(type) {
	case nil:
		return nil
	case bool:
		return v
	case float64:
		if volatileKey.MatchString(keyHint) && !stableKey.MatchString(keyHint) {
			return nil
		}
		if v != v || v > 1e308 || v < -1e308 {
			return nil
		}
		if stableKey.MatchString(keyHint) {
			return float64(int64(round(v)))
		}
		if v == 0 {
			return float64(0)
		}
		return float64(1)
	case json.Number:
		f, _ := v.Float64()
		return Project(f, keyHint, dropNoise)
	case string:
		if dropNoise && noiseKey.MatchString(keyHint) {
			return nil
		}
		s := strings.TrimSpace(v)
		if s == "" {
			return nil
		}
		if strings.HasPrefix(s, "data:") {
			return "data"
		}
		if len(s) > 400 {
			return nil
		}
		if isoTS.MatchString(s) {
			return nil
		}
		if len(s) > 160 {
			return s[:160]
		}
		return s
	case []any:
		nums := true
		for _, e := range v {
			if _, ok := e.(float64); !ok {
				nums = false
				break
			}
		}
		if nums && len(v) > 0 {
			return float64(len(v))
		}
		out := make([]any, 0, len(v))
		for i, e := range v {
			out = append(out, Project(e, itoa(i), dropNoise))
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, val := range v {
			p := Project(val, k, dropNoise)
			if p != nil {
				out[k] = p
			}
		}
		return out
	}
	return nil
}

var isoTS = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T`)

func Hash(value any) string {
	sum := sha256.Sum256(canonicalJSON(value))
	return hex.EncodeToString(sum[:])[:16]
}

func canonicalJSON(value any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return []byte("null")
	}
	return buf.Bytes()
}

func Summarize(obj any, max int) string {
	m, ok := obj.(map[string]any)
	if !ok {
		return "not an object"
	}
	keys := SortedKeys(m)
	if len(keys) > max {
		keys = keys[:max]
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+":"+typeName(m[k]))
	}
	return strings.Join(parts, " ")
}

func typeName(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case []any:
		return "array(" + itoa(len(t)) + ")"
	case map[string]any:
		return "object"
	case string:
		return "string"
	case bool:
		return "bool"
	case float64:
		return "number"
	}
	return "unknown"
}

func isObj(v any) bool {
	_, ok := v.(map[string]any)
	return ok
}

func round(f float64) float64 {
	if f >= 0 {
		return float64(int64(f + 0.5))
	}
	return float64(int64(f - 0.5))
}

func itoa(i int) string { return strconv.Itoa(i) }
