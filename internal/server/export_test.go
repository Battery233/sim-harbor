package server

import (
	"encoding/csv"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
)

func TestExportProtectedExactTextCSVAndNoBoardCommands(t *testing.T) {
	token := strings.Repeat("t", 24)
	s, l := fixture(t, 0, func(c *config.Config) { c.AdminToken = token })
	conn := connect(t, l)
	send(t, conn, s.codec(), map[string]any{"type": 100, "devId": "SIM-TEST"})
	reader := protocol.NewReader(conn)
	read(t, reader, s.codec())
	body := " \t=SUM(1,2)\n中文\"原文\""
	if _, e := s.Store.DB.Exec(`INSERT INTO messages(key,slot,phone,body,sms_ts,complete) VALUES('export',2,'+000000',?,1700000000,0)`, body); e != nil {
		t.Fatal(e)
	}
	for _, format := range []string{"json", "csv"} {
		path := "http://example.com/api/messages/export?slot=2&phone=%2B000000&format=" + format
		if w := request(s, "GET", path, "", ""); w.Code != 401 {
			t.Fatal(w.Code)
		}
		if w := request(s, "GET", path, "Bearer "+token, "http://other.invalid"); w.Code != 403 {
			t.Fatal(w.Code)
		}
		w := request(s, "GET", path, "Bearer "+token, "")
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") || w.Header().Get("Cache-Control") != "no-store, no-transform" {
			t.Fatal(w.Code, w.Body)
		}
		if format == "json" {
			var v struct {
				Count int
				Items []store.Message
			}
			if json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Count != 1 || v.Items[0].Body != body || v.Items[0].Read || v.Items[0].Complete {
				t.Fatal(w.Body)
			}
		} else {
			rows, e := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
			if e != nil || len(rows) != 2 || rows[1][2] != "'+000000" || rows[1][3] != "'"+body {
				t.Fatal(rows, e)
			}
		}
	}
	conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_, e := reader.Next()
	ne, ok := e.(net.Error)
	if !ok || !ne.Timeout() {
		t.Fatal("export sent command", e)
	}
	var n int
	if e = s.Store.DB.QueryRow(`SELECT (SELECT count(*) FROM outgoing)+(SELECT count(*) FROM notifications)+(SELECT count(*) FROM messages WHERE is_read!=0)`).Scan(&n); e != nil || n != 0 {
		t.Fatal("export side effect", n, e)
	}
}
func TestExportStrictFiltersAndFormulaProtection(t *testing.T) {
	s, _ := fixture(t, 0)
	for _, q := range []string{"slot=3", "slot=1&slot=2", "from=-1", "until=1&from=2", "format=txt", "before=5", "phone=%ZZ", "phone=" + strings.Repeat("x", 65)} {
		if w := request(s, "GET", "http://example.com/api/messages/export?"+q, "", ""); w.Code != 400 {
			t.Fatal(q, w.Code, w.Body)
		}
	}
	for _, v := range []string{"=X", "+X", "-X", "@X", " \t=cmd", "\r@X"} {
		if spreadsheetText(v) != "'"+v {
			t.Fatal("unsafe CSV", v)
		}
	}
	if spreadsheetText("正常短信") != "正常短信" {
		t.Fatal("ordinary text changed")
	}
	w := request(s, "GET", "http://example.com/api/messages/export", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatal(w.Body)
	}
}

func TestExportEncodingOverflowIsErrorNotPartialDownload(t *testing.T) {
	s, _ := fixture(t, 0)
	// Quoting a valid raw body can exceed the encoded CSV/JSON byte limit.
	body := strings.Repeat("\"", store.ExportBytes/2)
	if _, err := s.Store.DB.Exec(`INSERT INTO messages(key,slot,phone,body,sms_ts,complete) VALUES('encoded',1,'TEST',?,1,1)`, body); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"csv", "json"} {
		w := request(s, "GET", "http://example.com/api/messages/export?format="+format, "", "")
		if w.Code != 413 || w.Header().Get("Content-Disposition") != "" || !strings.Contains(w.Body.String(), "export_limit_exceeded") {
			t.Fatal(format, w.Code, "partial export published")
		}
	}
}
