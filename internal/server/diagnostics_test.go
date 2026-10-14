package server

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/protocol"
)

func TestDiagnosticAccessAndRepeatedReadsNeverWriteBoard(t *testing.T) {
	token := strings.Repeat("t", 24)
	s, listener := fixture(t, 0, func(c *config.Config) { c.AdminToken = token })
	conn := connect(t, listener)
	send(t, conn, s.codec(), map[string]any{"type": 100, "devId": "SIM-TEST"})
	reader := protocol.NewReader(conn)
	read(t, reader, s.codec())
	for _, path := range []string{"/api/diagnostics", "/api/diagnostics/export"} {
		if w := request(s, "GET", "http://example.com"+path, "", ""); w.Code != 401 {
			t.Fatal("unauthorised diagnostics", w.Code)
		}
		if w := request(s, "GET", "http://example.com"+path, "Bearer "+token, "http://other.invalid"); w.Code != 403 {
			t.Fatal("origin bypass", w.Code)
		}
		for i := 0; i < 3; i++ {
			if w := request(s, "GET", "http://example.com"+path, "Bearer "+token, ""); w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
		}
		if w := request(s, "POST", "http://example.com"+path, "Bearer "+token, ""); w.Code < 400 {
			t.Fatal("diagnostic write endpoint allowed")
		}
	}
	conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_, err := reader.Next()
	failure, ok := err.(net.Error)
	if !ok || !failure.Timeout() {
		t.Fatal("diagnostic GET sent a board command", err)
	}
	var outgoing int
	if err = s.Store.DB.QueryRow(`SELECT count(*) FROM outgoing`).Scan(&outgoing); err != nil || outgoing != 0 {
		t.Fatal("diagnostic GET created outgoing", outgoing, err)
	}
}

func TestDiagnosticCursorFiltersExportAndPrivateData(t *testing.T) {
	s, listener := fixture(t, 0, func(c *config.Config) { c.LogLevel = "debug" })
	id := strings.Repeat("a", 32)
	for i := 0; i < 80; i++ {
		s.Logs.Record("sms_write_unknown", diagnostics.Fields{Operation: diagnostics.Reference(id), Slot: 1})
	}
	w := request(s, "GET", "http://example.com/api/diagnostics?category=sms&level=warn", "", "")
	var page struct {
		Items []diagnostics.Event `json:"items"`
		Next  int64               `json:"next_before"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 50 || page.Next == 0 {
		t.Fatal(w.Code, w.Body)
	}
	older := request(s, "GET", "http://example.com/api/diagnostics?category=sms&before="+jsonNumber(page.Next), "", "")
	var second struct {
		Items []diagnostics.Event `json:"items"`
	}
	if older.Code != 200 || json.Unmarshal(older.Body.Bytes(), &second) != nil || len(second.Items) != 30 || second.Items[0].ID >= page.Next {
		t.Fatal("pagination overlap", older.Body)
	}
	secret := "synthetic-private-body-ICCID-note"
	conn := connect(t, listener)
	send(t, conn, s.codec(), map[string]any{"type": 100, "devId": "SIM-TEST"})
	read(t, protocol.NewReader(conn), s.codec())
	send(t, conn, s.codec(), map[string]any{"type": 501, "slot": 1, "phNum": "000123456789", "smsBd": secret, "smsTs": 1700000000, "tid": secret})
	until := time.Now().Add(2 * time.Second)
	smsLogged := false
	for time.Now().Before(until) {
		rows, _ := s.Logs.Snapshot(diagnostics.Filter{}, 1000)
		for _, row := range rows {
			if row.Code == "event_received" && row.Type == 501 {
				smsLogged = true
			}
		}
		if smsLogged {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !smsLogged {
		t.Fatal("SMS event was not observed before privacy check")
	}
	exported := request(s, "GET", "http://example.com/api/diagnostics/export?operation_id="+id, "", "")
	var bundle struct {
		Format int                 `json:"format_version"`
		Scope  string              `json:"scope"`
		Items  []diagnostics.Event `json:"items"`
	}
	if exported.Code != 200 || json.Unmarshal(exported.Body.Bytes(), &bundle) != nil || bundle.Format != 1 || bundle.Scope != "recent_diagnostic_events" || len(bundle.Items) != 80 {
		t.Fatal(exported.Code, exported.Body)
	}
	all := request(s, "GET", "http://example.com/api/diagnostics/export", "", "")
	for _, private := range []string{secret, "000123456789", s.Config.DeviceID, s.Config.AESKey, id} {
		if strings.Contains(all.Body.String(), private) {
			t.Fatal("private data in exported diagnostics", private)
		}
	}
}
func jsonNumber(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestDiagnosticInvalidFiltersAndExportBound(t *testing.T) {
	s, _ := fixture(t, 0, func(c *config.Config) { c.DiagnosticsLimit = 100 })
	for i := 0; i < 180; i++ {
		s.Logs.Record("service_started", diagnostics.Fields{})
	}
	for _, query := range []string{"?level=%ZZ", "?category=sms;level=warn", "?before=-1", "?level=trace", "?category=private", "?ref=private", "?operation_id=000", "?level=info&level=warn", "?unknown=1", "?ref=" + strings.Repeat("a", 24) + "&operation_id=" + strings.Repeat("b", 32)} {
		w := request(s, "GET", "http://example.com/api/diagnostics"+query, "", "")
		if w.Code != 400 {
			t.Fatal("bad filter accepted", query, w.Code)
		}
	}
	if w := request(s, "GET", "http://example.com/api/diagnostics/export?before=1", "", ""); w.Code != 400 {
		t.Fatal("partial cursor export accepted")
	}
	w := request(s, "GET", "http://example.com/api/diagnostics/export", "", "")
	var v struct {
		Items  []diagnostics.Event `json:"items"`
		Status diagnostics.Status  `json:"status"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || len(v.Items) != 100 || v.Status.Limit != 100 {
		t.Fatal("unbounded export", w.Body)
	}
}
