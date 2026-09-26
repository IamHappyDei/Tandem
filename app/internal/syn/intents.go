package syn

import (
	"regexp"
	"sort"

	"strconv"
	"strings"
	"sync"
	"tandem/internal/wire"
)

type Vocab struct {
	Name      string   `json:"name"`
	Tokens    []string `json:"tokens"`
	KnownVerb *bool    `json:"knownVerb,omitempty"`
}

var yes = true
var no = false

func ServiceVocab() []Vocab {
	return []Vocab{
		{Name: "Deboarding", Tokens: []string{"deboard", "offload", "deplane"}},
		{Name: "Boarding", Tokens: []string{"boarding", "board", "pax board"}},
		{Name: "Refueling", Tokens: []string{"refuel", "refueling", "fuelling", "fueling", "fuel", "hydrant"}},
		{Name: "Catering", Tokens: []string{"catering", "truck cater"}},
		{Name: "Departure", Tokens: []string{"departure", "all complete", "complete service"}},
		{Name: "OperateJetways", Tokens: []string{"jetway", "jet bridge", "jetbridge", "bridge", "paxbridge"}},
		{Name: "OperateStairs", Tokens: []string{"stair", "ramp stairs", "stairs"}},
		{Name: "GPU", Tokens: []string{"gpu", "ground power", "ground_power", "pdu", "pre-condition", "air con", "pac "}},
		{Name: "DeIce", Tokens: []string{"deice", "de-ice", "anti icing", "antiicing", "ice protection"}},
		{Name: "Water", Tokens: []string{"potable", "water service", "water"}},
		{Name: "Lavatory", Tokens: []string{"lavatory", "lav service", "toilet", "dmin"}},
		{Name: "Cleaning", Tokens: []string{"cleaning", "clean cab"}},

		{Name: "Chocks", Tokens: []string{"chock"}, KnownVerb: &no},
		{Name: "Pushback", Tokens: []string{"pushback", "push back", "push-out", "tow", "tractor"}, KnownVerb: &no},
		{Name: "Baggage", Tokens: []string{"baggage", "cargo", "container", "loader", "belt"}, KnownVerb: &no},
		{Name: "Crew", Tokens: []string{"crew transport", "crew bus", "crew"}, KnownVerb: &no},
		{Name: "FuelTruck", Tokens: []string{"fuel truck", "bowser", "fueler"}, KnownVerb: &no},
	}
}

var vocab = ServiceVocab()

func verbKnown(name string) bool {
	for _, v := range vocab {
		if v.Name == name {
			return v.KnownVerb == nil || *v.KnownVerb
		}
	}
	return false
}

func Canonicalize(raw any) string {
	if raw == nil {
		return ""
	}
	s := " " + strings.ToLower(str(raw))
	s = strings.Join(strings.Fields(regexp.MustCompile(`[_\-.]+`).ReplaceAllString(s, " ")), " ")
	best := ""
	bestAt := 1 << 30
	bestLen := 0
	for _, v := range vocab {
		for _, t := range v.Tokens {
			at := strings.Index(s, t)
			if at < 0 {
				continue
			}

			if at < bestAt || (at == bestAt && len(t) > bestLen) {
				best, bestAt, bestLen = v.Name, at, len(t)
			}
		}
	}
	return best
}

type Phase = wire.Phase

var (
	reStateEnum  = regexp.MustCompile(`(?i)^(state|status|phase|stateclass|statecode)$`)
	reRawState   = regexp.MustCompile(`(?i)^(stateraw|statecode|statuscode|stateid)$`)
	reProse      = regexp.MustCompile(`(?i)^(statetext|caption|text|description|desc|label|displayname)$`)
	reProgressNo = regexp.MustCompile(`(?i)^(progress|percent|pct|completion)$`)
	reProgressTx = regexp.MustCompile(`(?i)^(progresstext|progress|percenttext)$`)
	pctRe        = regexp.MustCompile(`(\d{1,3})\s*%`)
)

var phaseTokens = []struct {
	state  string
	tokens []string
}{
	{"idle", []string{"notrequested", "available", "unavailable", "idle", "none", "off", "disabled", "standby", "inactive", "notstarted", "cancelled", "canceled", "stopped", "unset", "ready"}},
	{"done", []string{"completed", "complete", "done", "finished", "invoiced", "closed", "deplaned"}},
	{"active", []string{"inprogress", "inuse", "performing", "requested", "running", "active", "engaged", "operating", "busy", "waiting", "queued", "underway", "started", "loading", "unloading", "on", "true", "yes", "1"}},
}

var (
	proseIdle   = regexp.MustCompile(`(?i)(can be (requested|started|called|toggled)|is available|not requested|no service|not set|not started|awaiting|\bidle\b|\bnone\b|unchecked|disabled|\boff\b|\bfalse\b)`)
	proseDone   = regexp.MustCompile(`(?i)(complet|finished|invoiced|\bdone\b)`)
	proseAct    = regexp.MustCompile(`(?i)(in progress|underway|running|operating|engaged|in use|requested|waiting|queued|loading|unloading|departing|being performed|performing|\bon\b)`)
	identityKey = regexp.MustCompile(`(?i)^(id|icon|displayname|name)$`)
)

func normTok(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func classifyToken(tok string) string {
	t := normTok(tok)
	if t == "" {
		return ""
	}
	for _, b := range phaseTokens {
		for _, x := range b.tokens {
			if x == t {
				return b.state
			}
		}
	}
	return ""
}

func classifyProse(s string) string {
	s = strings.ToLower(s)
	if s == "" {
		return ""
	}
	if proseIdle.MatchString(s) {
		return "idle"
	}
	if proseDone.MatchString(s) {
		return "done"
	}
	if proseAct.MatchString(s) {
		return "active"
	}
	return classifyToken(s)
}

func PhaseOf(svc any) Phase {
	if svc == nil {
		return Phase{State: "absent", PhaseHash: "0"}
	}
	enumTok := firstString(svc, reStateEnum)
	raw, hasRaw := firstNumber(svc, reRawState)
	prose := firstString(svc, reProse)

	state := classifyToken(enumTok)
	if state == "" {
		state = classifyProse(prose)
	}
	known := state != ""
	if !known {

		p := Project(svc, "", true)
		switch t := p.(type) {
		case nil:
			state = "idle"
		case bool:
			if t {
				state = "active"
			} else {
				state = "idle"
			}
		case float64:
			if t == 0 {
				state = "idle"
			} else {
				state = "active"
			}
		default:
			state = "unknown"
			noteUnknown(orEmpty(enumTok, itoaF(raw, hasRaw)))
		}
	}
	drop := state != "unknown"
	detail := Project(svc, "", drop)
	if dm, ok := detail.(map[string]any); ok && !hasSignal(dm) {
		detail = Project(svc, "", false)
	}
	progress, _ := readProgress(svc)
	return Phase{
		State:     state,
		PhaseHash: Hash(map[string]any{"state": state, "detail": detail}),
		Progress:  progress,
		Label:     orEmpty(prose, enumTok),
		Token:     orEmpty(enumTok, itoaF(raw, hasRaw)),
	}
}

func hasSignal(m map[string]any) bool {
	for k := range m {
		if !identityKey.MatchString(k) {
			return true
		}
	}
	return false
}

func readProgress(svc any) (float64, bool) {
	if n, ok := firstNumber(svc, reProgressNo); ok {
		return n, true
	}
	if t := firstString(svc, reProgressTx); t != "" {
		if m := pctRe.FindStringSubmatch(t); m != nil {
			f, _ := strconv.ParseFloat(m[1], 64)
			return f, true
		}
	}
	return 0, false
}

func orEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func itoaF(f float64, ok bool) string {
	if !ok {
		return ""
	}
	return "#" + strconv.FormatFloat(f, 'f', -1, 64)
}

func ServicePhaseMap(services any) map[string]Phase {
	out := map[string]Phase{}
	switch s := services.(type) {
	case nil:
		return out
	case []any:
		for _, item := range s {
			m, ok := item.(map[string]any)
			if !ok {
				out[str(item)] = PhaseOf(item)
				continue
			}
			key := firstNonEmpty(m["name"], m["label"], m["id"], m["service"], m["title"])
			if key == "" {
				key = str(firstAny(m))
			}
			out[key] = phaseOfRow(key, item)
		}
	case map[string]any:
		for k, v := range s {
			out[k] = phaseOfRow(k, v)
		}
	}
	return out
}

func phaseOfRow(key string, value any) Phase {
	p := PhaseOf(value)
	p.Key = key
	can := Canonicalize(key)
	if can == "" && isObj(value) {
		can = Canonicalize(firstNonEmpty(value.(map[string]any)["name"], value.(map[string]any)["label"]))
	}
	p.Canonical = can
	p.KnownVerb = can != "" && verbKnown(can)
	if p.Label == "" {
		p.Label = key
	}
	if can != "" {
		p.Label = can
	}
	return p
}

func firstAny(m map[string]any) any {
	for _, v := range m {
		return v
	}
	return nil
}

func firstNonEmpty(vals ...any) string {
	for _, v := range vals {
		if s := str(v); s != "" {
			return s
		}
	}
	return ""
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case nil:
		return ""
	}
	b := canonicalJSON(v)
	return strings.TrimSpace(string(b))
}

type MenuInfo = wire.MenuInfo

func CheapMenu(m any) *MenuInfo {
	mm, ok := m.(map[string]any)
	if !ok {
		return nil
	}
	info := &MenuInfo{Title: str(mm["title"])}
	if arr, ok := mm["entries"].([]any); ok {
		for _, e := range arr {
			info.Entries = append(info.Entries, strings.TrimSpace(str(e)))
		}
	}
	if arr, ok := mm["disabled"].([]any); ok {
		for _, e := range arr {
			b, _ := e.(bool)
			info.Disabled = append(info.Disabled, b)
		}
	}
	if arr, ok := mm["stateClass"].([]any); ok {
		for _, e := range arr {
			info.StateClass = append(info.StateClass, str(e))
		}
	}
	return info
}

type Intent = wire.Intent

type Change = wire.Change

func Classify(c Change) []*Intent {
	out := []*Intent{}
	b := ServicePhaseMap(c.Before["services"])
	a := ServicePhaseMap(c.After["services"])
	if len(b) == 0 && len(a) > 0 {
		out = append(out, &Intent{Kind: "bootstrap", Replay: "none", Why: "services map first seen (" + itoa(len(a)) + " rows)"})
	} else {
		for _, key := range unionKeys(a, b) {
			aa, hasA := a[key]
			bb, hasB := b[key]
			if hasA && hasB && aa.PhaseHash == bb.PhaseHash && aa.State == bb.State {
				continue
			}
			from := "absent"
			to := "absent"
			if hasB {
				from = bb.State
			}
			if hasA {
				to = aa.State
			}
			name := ""
			label := ""
			known := false
			if hasA {
				name, label, known = aa.Canonical, aa.Label, aa.KnownVerb
			} else if hasB {
				name, label, known = bb.Canonical, bb.Label, bb.KnownVerb
			}
			replay := "menu"
			if name != "" && known {
				replay = "service"
			}
			out = append(out, &Intent{
				Kind: "service", Replay: replay, Name: name, Key: key, From: from, To: to, Label: label,
				Why: orEmpty(name, key) + ": " + from + " -> " + to,
			})
		}
	}

	bm := menuInfo(c.Before["menu"], c.Before["menuShown"])
	am := menuInfo(c.After["menu"], c.After["menuShown"])
	if bm != nil && am != nil && shown(c.After["menuShown"]) {
		picked := guessPickedLabel(bm, am)
		movedPage := bm.Title != am.Title || strings.Join(bm.Entries, "|") != strings.Join(am.Entries, "|")
		switch {
		case picked != "":

			out = append(out, &Intent{
				Kind: "menu", Replay: "menu", From: bm.Title, To: am.Title,
				Picked: picked, Entries: am.Entries,
				Why: "menu pick \"" + picked + "\" (page \"" + orEmpty(am.Title, "?") + "\")",
			})
		case movedPage:

			out = append(out, &Intent{
				Kind: "menu", Replay: "none", From: bm.Title, To: am.Title, Entries: am.Entries,
				Why: "menu \"" + orEmpty(bm.Title, "?") + "\" -> \"" + orEmpty(am.Title, "?") + "\"",
			})
		}
	}

	bk := map[string]bool{}
	for _, k := range splitKeys(c.Before["top"]) {
		bk[k] = true
	}
	for _, k := range splitKeys(c.After["top"]) {
		if !bk[k] && k != "services" && k != "menu" {
			out = append(out, &Intent{Kind: "unknown", Key: k, Replay: "none", Why: "new state key \"" + k + "\""})
		}
	}
	return out
}

func menuInfo(v any, shown any) *MenuInfo {
	m := CheapMenu(v)
	if m == nil {
		return nil
	}
	m.Shown = shown != nil && shown != false
	return m
}

func shown(v any) bool {
	b, _ := v.(bool)
	return b
}

func splitKeys(v any) []string {
	s, _ := v.(string)
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func unionKeys(a, b map[string]Phase) []string {
	set := map[string]bool{}
	for k := range a {
		set[k] = true
	}
	for k := range b {
		set[k] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func guessPickedLabel(before, after *MenuInfo) string {
	if before == nil || after == nil || len(after.Entries) != len(before.Entries) {
		return ""
	}
	moved, label := 0, ""
	for i := range after.Entries {
		if after.Entries[i] != before.Entries[i] {
			return ""
		}
		a, b := lit(at(after.StateClass, i)), lit(at(before.StateClass, i))
		if a != b {
			moved++
			label = after.Entries[i]
		}
	}

	if moved == 1 {
		return label
	}
	return ""
}

func at(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return ""
}

func lit(s string) string {
	s = strings.ToLower(s)
	for _, k := range []string{"pick", "select", "check", "active", "on", "high", "current", "done", "complete"} {
		if strings.Contains(s, k) {
			return k
		}
	}
	return ""
}

var (
	unknownMu sync.Mutex
	unknown   = map[string]int{}
)

func noteUnknown(tok string) {
	if tok == "" {
		return
	}
	if len(tok) > 60 {
		tok = tok[:60]
	}
	unknownMu.Lock()
	unknown[tok]++
	unknownMu.Unlock()
}

type UnknownState struct {
	Token string `json:"token"`
	Seen  int    `json:"seen"`
}

func UnknownStates() []UnknownState {
	unknownMu.Lock()
	defer unknownMu.Unlock()
	out := make([]UnknownState, 0, len(unknown))
	for t, n := range unknown {
		out = append(out, UnknownState{Token: t, Seen: n})
	}
	sortStrings2(out)
	return out
}

func sortStrings2(v []UnknownState) {
	sort.Slice(v, func(i, j int) bool { return v[i].Token < v[j].Token })
}
