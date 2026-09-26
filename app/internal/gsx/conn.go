package gsx

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"tandem/internal/logx"
)

type wsConn struct {
	ws      *websocket.Conn
	log     *logx.Log
	writeMu sync.Mutex
	closed  bool
}

func newWSConn(ws *websocket.Conn, log *logx.Log) *wsConn { return &wsConn{ws: ws, log: log} }

func (w *wsConn) Send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	if w.closed {
		return errClosed
	}
	w.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return w.ws.WriteMessage(websocket.TextMessage, b)
}

func (w *wsConn) Run(onFrame func(*Frame), onClosed func(error)) {
	w.ws.SetPongHandler(func(string) error { return nil })
	w.ws.SetPingHandler(func(data string) error {
		w.writeMu.Lock()
		defer w.writeMu.Unlock()
		if w.closed {
			return nil
		}
		return w.ws.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(5*time.Second))
	})
	for {
		_, msg, err := w.ws.ReadMessage()
		if err != nil {
			w.closed = true
			if onClosed != nil {
				onClosed(err)
			}
			return
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(msg, &raw); err != nil {
			continue
		}
		f := &Frame{Rest: map[string]any{}}
		if len(raw["type"]) > 0 {
			_ = json.Unmarshal(raw["type"], &f.Type)
		}
		for k, v := range raw {
			switch k {
			case "type":
			case "path":
				_ = json.Unmarshal(v, &f.Path)
			case "value":
				f.Value = v
			case "ok":
				var b bool
				if json.Unmarshal(v, &b) == nil {
					f.Ok = &b
				}
			case "gsxRunning":
				var b bool
				if json.Unmarshal(v, &b) == nil {
					f.GsxRun = &b
				}
			case "authRequired":
				var b bool
				if json.Unmarshal(v, &b) == nil {
					f.AuthReq = &b
				}
			case "topic":
				_ = json.Unmarshal(v, &f.Topic)
			case "error":
				var s string
				if json.Unmarshal(v, &s) == nil {
					f.Error = s
				} else {
					var e map[string]any
					if json.Unmarshal(v, &e) == nil {
						f.Error = e
					}
				}
			default:
				var a any
				if json.Unmarshal(v, &a) == nil {
					f.Rest[k] = a
				}
			}
		}
		if f.Type == "" {
			continue
		}
		onFrame(f)
	}
}

func (w *wsConn) Close() {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	w.closed = true
	_ = w.ws.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(2*time.Second))
	_ = w.ws.Close()
}

func must(r json.RawMessage) []byte {
	if len(r) == 0 {
		return []byte("{}")
	}
	return r
}

func errorText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		if m, ok := t["message"].(string); ok {
			return m + " (" + strings.TrimSpace(asString(t["code"])) + ")"
		}
	}
	return "rejected"
}
