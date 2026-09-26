package syn

import (
	"regexp"
	"sort"
	"strconv"
)

func FindKey(node any, re *regexp.Regexp, depth int) any {
	if depth > 6 {
		return nil
	}
	m, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	keys := SortedKeys(m)
	for _, k := range keys {
		if re.MatchString(k) {
			return m[k]
		}
	}
	for _, k := range keys {
		if isObj(m[k]) {
			if hit := FindKey(m[k], re, depth+1); hit != nil {
				return hit
			}
		}
	}
	return nil
}

func firstString(node any, re *regexp.Regexp) string {
	switch v := FindKey(node, re, 0).(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func firstNumber(node any, re *regexp.Regexp) (float64, bool) {
	switch v := FindKey(node, re, 0).(type) {
	case float64:
		return v, true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err == nil {
			return f, true
		}
	}
	return 0, false
}

func num(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

func SortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
