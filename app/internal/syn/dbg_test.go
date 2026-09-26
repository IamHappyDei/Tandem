package syn_test

import (
	"fmt"
	"testing"
	"time"

	"tandem/internal/fake"
	"tandem/internal/gsx"
	"tandem/internal/logx"
	"tandem/internal/syn"
)

func TestDBClassify(t *testing.T) {
	before := map[string]any{"services": []any{
		map[string]any{"id": "Boarding", "displayName": "Boarding", "state": "available", "stateRaw": 1, "stateText": "Boarding service can be requested", "canTrigger": true},
	}}
	after := map[string]any{"services": []any{
		map[string]any{"id": "Boarding", "displayName": "Boarding", "state": "performing", "stateRaw": 5, "stateText": "Boarding service is being performed", "canTrigger": false},
	}}
	in := syn.Classify(syn.Change{Before: before, After: after})
	if len(in) == 0 {
		t.Fatal("Classify produced nothing for a real state change")
	}
	for _, i := range in {
		t.Logf("intent kind=%s replay=%s name=%s from=%s to=%s why=%s", i.Kind, i.Replay, i.Name, i.From, i.To, i.Why)
	}
}

func TestDBGsxSeesPatch(t *testing.T) {
	f := fake.New(19999, false)
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	defer f.Stop()
	log := logx.New("debug")
	g := gsx.New("ws://127.0.0.1:19999", nil, log.Child("[gsx]"), 20)
	hits := make(chan string, 8)
	g.OnChange(func(c syn.Change) {
		hits <- fmt.Sprintf("%d intents", len(syn.Classify(c)))
	})
	g.Start()
	defer g.Stop()
	time.Sleep(900 * time.Millisecond)
	t.Logf("connected=%v stats=%+v topKeys=%v", g.Connected(), g.Stats(), g.TopKeys())
	f.Toggle("Boarding")
	time.Sleep(400 * time.Millisecond)
	t.Logf("mid stats=%+v", g.Stats())
	time.Sleep(900 * time.Millisecond)
	t.Logf("after toggle: patches=%v changes=%v phases=%v", g.Stats().Patches, len(hits), g.ServicesRaw() != nil)
	select {
	case h := <-hits:
		t.Logf("change delivered: %s", h)
	default:
		t.Error("no change reached the engine")
	}
}
