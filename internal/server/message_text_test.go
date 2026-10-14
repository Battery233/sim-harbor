package server

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Battery233/sim/internal/protocol"
	"github.com/Battery233/sim/internal/store"
)

func TestIncomingTextExplicitFormatting(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"controls", `first\n\nsecond\r\nthird\tend\r`, "first\n\nsecond\r\nthird\tend\r"},
		{"actual controls unchanged", "first\nsecond\tend", "first\nsecond\tend"},
		{"double backslash protected", `literal \\n \\t \\r`, `literal \\n \\t \\r`},
		{"odd backslash run once", `\\\n`, "\\\\\n"},
		{"unknown escapes and trailing slash", `\u0041 \" \/ \q tail\`, `\u0041 \" \/ \q tail\`},
		{"Unicode and HTML are plain text", `测试🙂\n<script>alert(1)</script>`, "测试🙂\n<script>alert(1)</script>"},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatIncomingText(tc.raw, false); got != tc.raw {
				t.Fatal("default policy changed raw text")
			}
			if got := formatIncomingText(tc.raw, true); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestMessageFormattingKeepsRawStorageAndAPI(t *testing.T) {
	s, _ := fixture(t, 0)
	e := protocol.Event{DeviceID: "SIM-TEST", Type: 501, Slot: 1, Phone: "TEST-SENDER", Body: `synthetic\n\nmessage`, SMSTS: 1700000000}
	p, _ := json.Marshal(e)
	if err := s.Store.Ingest(context.Background(), e, p, false); err != nil {
		t.Fatal(err)
	}
	ms, err := s.Store.MessagePage(context.Background(), 0, "", 0, 50)
	if err != nil || len(ms) != 1 {
		t.Fatal("synthetic message missing", err)
	}
	for _, enabled := range []bool{false, true} {
		s.Config.IncomingTextEscapes = enabled
		for _, path := range []string{"/api/messages", "/api/messages/1"} {
			w := request(s, "GET", "http://localhost"+path, "", "")
			if w.Code != 200 {
				t.Fatal("message API failed", w.Code)
			}
			var v map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
				t.Fatal(err)
			}
			if path == "/api/messages" {
				v = v["items"].([]any)[0].(map[string]any)
			}
			if v["body"] != e.Body {
				t.Fatal("raw API body changed")
			}
			if enabled {
				if v["display_body"] != "synthetic\n\nmessage" {
					t.Fatal("formatted API body missing")
				}
			} else if _, ok := v["display_body"]; ok {
				t.Fatal("default API applied formatting")
			}
		}
	}
	m, err := s.Store.Message(context.Background(), ms[0].ID)
	if err != nil || m.Body != e.Body {
		t.Fatal("formatting changed persisted raw text", err)
	}
	for _, format := range []string{"json", "csv"} {
		w := request(s, "GET", "http://localhost/api/messages/export?format="+format, "", "")
		if w.Code != 200 {
			t.Fatal("export failed", w.Code)
		}
		if format == "json" {
			var exported struct{ Items []store.Message }
			if json.Unmarshal(w.Body.Bytes(), &exported) != nil || len(exported.Items) != 1 || exported.Items[0].Body != e.Body {
				t.Fatal("JSON export changed raw text")
			}
		} else {
			rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
			if err != nil || len(rows) != 2 || rows[1][3] != e.Body {
				t.Fatal("CSV export changed raw text", err)
			}
		}
	}
}

func TestBarkUsesSameIncomingTextPolicy(t *testing.T) {
	s, _ := fixture(t, 0)
	s.Config.BarkKey = "SYNTHETIC-KEY"
	s.Config.PublicURL = "https://example.invalid"
	received := make(chan string, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		received <- p.Body
		w.Write([]byte(`{"code":200}`))
	}))
	defer receiver.Close()
	s.Config.BarkBaseURL = receiver.URL
	n := store.Notification{MessageID: 1, Slot: 1, Body: `synthetic\nmessage`}
	for _, enabled := range []bool{false, true} {
		s.Config.IncomingTextEscapes = enabled
		if !s.push(context.Background(), n) {
			t.Fatal("synthetic push failed")
		}
		if <-received != formatIncomingText(n.Body, enabled) || n.Body != `synthetic\nmessage` {
			t.Fatal("push formatting differs or mutated task")
		}
	}
}
