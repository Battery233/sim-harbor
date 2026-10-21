package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
	"github.com/Battery233/sim-harbor/internal/version"
)

type session struct {
	diagnosticID    string
	conn            net.Conn
	write           sync.Mutex
	mu              sync.Mutex
	tid             string
	response        chan queryReply
	smsWatch        chan protocol.Event
	smsWatchSlot    int
	smsWatchPhone   string
	smsWatchICCID   string
	smsWatchSent    int64
	acceptSMSReport bool
	acceptTTSAck    bool
	hasStatus       bool // Protected by Server.mu; cached status must belong to this connection.
	statusAt        time.Time
	lastSeen        time.Time
	autoAttempt     time.Time
}
type queryReply struct {
	event      protocol.Event
	receivedAt time.Time
}
type Server struct {
	Logs             *diagnostics.Recorder
	Config           config.Config
	Settings         *config.Settings
	Restart          func()
	restarting       atomic.Bool
	Store            *store.Store
	mu               sync.Mutex
	active           *session
	conns            map[net.Conn]struct{}
	sendGate         sync.Mutex
	backupGate       sync.Mutex
	migrationGate    sync.Mutex
	migrationBackup  *migrationBackup
	migrationPreview *migrationPreview
	settingsGate     sync.Mutex
	notificationGate sync.Mutex
	exportGate       sync.Mutex
	query            sync.Mutex
	greetingMu       sync.Mutex
	greetingSent     map[string]time.Time
	wg               sync.WaitGroup
	client           *http.Client
}

func New(c config.Config, s *store.Store, logs ...*diagnostics.Recorder) *Server {
	recorder := diagnostics.New(c.LogLevel, c.DiagnosticsLimit, nil)
	if len(logs) > 0 && logs[0] != nil {
		recorder = logs[0]
	}
	return &Server{Logs: recorder, Config: c, Store: s, conns: map[net.Conn]struct{}{}, client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (s *Server) codec() protocol.Codec {
	return protocol.Codec{Mode: s.Config.AESMode, Key: []byte(s.Config.AESKey), IV: []byte(s.Config.AESIV)}
}
func (s *Server) ServeTCP(ctx context.Context, l net.Listener) error {
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			l.Close()
			s.mu.Lock()
			for c := range s.conns {
				c.Close()
			}
			s.mu.Unlock()
		case <-stopped:
		}
	}()
	defer func() {
		l.Close()
		s.mu.Lock()
		for c := range s.conns {
			c.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
	}()
	for {
		c, e := l.Accept()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		if !s.Config.Allows(c.RemoteAddr()) {
			c.Close()
			s.Logs.Record("tcp_source_rejected", diagnostics.Fields{})
			continue
		}
		s.mu.Lock()
		if len(s.conns) >= 8 {
			s.mu.Unlock()
			c.Close()
			s.Logs.Record("tcp_connection_limit", diagnostics.Fields{})
			continue
		}
		s.conns[c] = struct{}{}
		s.mu.Unlock()
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.handle(ctx, c) }()
	}
}
func (s *Server) handle(ctx context.Context, c net.Conn) {
	sess := &session{conn: c, diagnosticID: diagnostics.Reference(tid())}
	s.Logs.Record("tcp_connected", diagnostics.Fields{Session: sess.diagnosticID})
	closed := "tcp_peer_closed"
	bound := false
	defer func() {
		c.Close()
		s.mu.Lock()
		delete(s.conns, c)
		if s.active == sess {
			s.active = nil
		} else if bound {
			closed = "tcp_session_retired"
		}
		s.mu.Unlock()
		if ctx.Err() != nil {
			closed = "tcp_shutdown"
		}
		s.Logs.Record(closed, diagnostics.Fields{Session: sess.diagnosticID})
	}()
	reader := protocol.NewReader(c)
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		frame, err := reader.Next()
		if err != nil {
			closed = readFailure(err)
			return
		}
		plain, err := s.codec().Open(frame)
		if err != nil {
			closed = "tcp_decode_failed"
			return
		}
		e, err := protocol.Parse(plain)
		if err != nil {
			closed = "tcp_parse_failed"
			return
		}
		if !bound {
			if e.DeviceID != s.Config.DeviceID {
				closed = "tcp_identity_rejected"
				return
			}
		} else if e.DeviceID == "" {
			e.DeviceID = s.Config.DeviceID
		} else if e.DeviceID != s.Config.DeviceID {
			closed = "tcp_identity_rejected"
			return
		}
		receivedAt := time.Now()
		s.recordTimeMarker(sess, e, receivedAt)
		callReport := protocol.IsIncomingCall(e.Type)
		if callReport {
			// Serialize a contradictory call update with push selection/result.
			s.notificationGate.Lock()
		}
		// Serialize replacement with ingestion so a retired connection cannot
		// overwrite current card status or complete a pending query.
		s.mu.Lock()
		if bound && s.active != sess {
			s.mu.Unlock()
			if callReport {
				s.notificationGate.Unlock()
			}
			closed = "tcp_session_retired"
			return
		}
		if (e.Type == 501 || callReport) && strings.TrimSpace(e.CardName) == "" && sess.hasStatus {
			// Never look up a name at push/retry time: a later SIM may occupy this slot.
			name, lookupErr := s.Store.CardNameFromStatus(ctx, e.DeviceID, int(e.Slot), e.ICCID, receivedAt.Unix()-int64(s.Config.StaleSeconds), receivedAt.Unix())
			if lookupErr == nil {
				e.CardName = name
			}
		}
		s.recordSMSIngest(ctx, sess, e, "sms_fragment_received", nil)
		var voicemailSlots []int
		if s.Config.VoicemailEnabled {
			voicemailSlots = s.Config.VoicemailSlots
		}
		// Password-bearing command text never enters the inbox. A control reply is
		// skipped only when it matches a real control transaction for this slot
		// and source. Ordinary SMS, including similar wording, is stored.
		suppress, suppressErr := s.controlSMSSuppressed(ctx, e)
		if suppressErr != nil {
			s.recordSMSIngest(ctx, sess, e, "sms_fragment_rejected", suppressErr)
			s.mu.Unlock()
			if callReport {
				s.notificationGate.Unlock()
			}
			closed = "event_persistence_failed"
			return
		}
		if !suppress {
			if err = s.Store.Ingest(ctx, e, plain, s.Config.BarkKey != "", voicemailSlots...); err != nil {
				s.recordSMSIngest(ctx, sess, e, "sms_fragment_rejected", err)
				s.mu.Unlock()
				if callReport {
					s.notificationGate.Unlock()
				}
				closed = "event_persistence_failed"
				return
			}
			s.recordSMSIngest(ctx, sess, e, "sms_fragment_stored", nil)
		}
		var old *session
		becameBound := !bound
		if !bound {
			old = s.active
			s.active = sess
			bound = true
		}
		if e.Type == 402 && e.SubType == 46 && e.Code != nil && *e.Code == 0 {
			sess.hasStatus = true
			sess.statusAt = receivedAt
		} else if invalidatesStatus(e.Type) {
			sess.hasStatus = false
			sess.statusAt = time.Time{}
		}
		sess.lastSeen = receivedAt
		s.mu.Unlock()
		if callReport {
			s.notificationGate.Unlock()
		}
		if old != nil {
			old.conn.Close()
			s.Logs.Record("tcp_session_replaced", diagnostics.Fields{Session: old.diagnosticID})
		}
		if becameBound {
			s.Logs.Record("tcp_bound", diagnostics.Fields{Session: sess.diagnosticID})
		}
		s.recordProtocolEvent(ctx, sess, e, plain)
		c.SetReadDeadline(time.Now().Add(time.Duration(s.Config.StaleSeconds+15) * time.Second))
		if e.Type == 100 || e.Type == 101 || e.Type == 102 {
			if err = s.write(sess, map[string]any{"cmd": "now", "p1": time.Now().In(s.Config.Location).Format("20060102150405"), "tid": tid()}); err != nil {
				closed = "handshake_write_failed"
				return
			}
		}
		sess.mu.Lock()
		// Only a 501 that can belong to the in-flight control is offered to the waiter.
		// Source, slot, confirmable SIM and arrival after that attempt all have to match.
		if e.Type == 501 && sess.smsWatch != nil && controlSMSRelevant(e, sess.smsWatchSlot, sess.smsWatchPhone, sess.smsWatchICCID, sess.smsWatchSent) {
			select {
			case sess.smsWatch <- e:
			default:
			}
		}
		// telstarttts is accepted on 401/subType 26, including code 0. A later 402 is not required and must not occupy this waiter.
		ttsAck := sess.acceptTTSAck && e.Type == 401 && int(e.SubType) == 26 && e.Code != nil
		// Control SMS may be accepted with 401 code 0 ("sending") plus 502/503. Those must reach the waiter; 401 code 0 stays ignored for every other command.
		smsReport := sess.acceptSMSReport && (e.Type == 502 || e.Type == 503 || (e.Type == 401 && e.Code != nil))
		regularAck := !sess.acceptTTSAck && (e.Type == 402 || (e.Type == 401 && e.Code != nil && *e.Code != 0) || smsReport)
		if e.TID != "" && e.TID == sess.tid && sess.response != nil && (ttsAck || regularAck) {
			select {
			case sess.response <- queryReply{e, receivedAt}:
			default:
			}
		}
		sess.mu.Unlock()
		if job, ok := s.claimVoicemailGreeting(e); ok {
			text := s.Config.VoicemailTTS
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.playVoicemailGreeting(sess, job, text)
			}()
		}
	}
}
func tid() string {
	b := make([]byte, 12)
	if _, e := rand.Read(b); e != nil {
		panic("entropy unavailable")
	}
	return hex.EncodeToString(b)
}
func (s *Server) write(sess *session, value any) error {
	p, e := json.Marshal(value)
	if e != nil {
		return e
	}
	p, e = s.codec().Seal(p)
	if e != nil {
		return e
	}
	p = append(p, protocol.Delimiter...)
	sess.write.Lock()
	defer sess.write.Unlock()
	sess.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	for len(p) > 0 {
		n, e := sess.conn.Write(p)
		if e != nil || n <= 0 {
			sess.conn.Close()
			if e != nil {
				return e
			}
			return io.ErrNoProgress
		}
		p = p[n:]
	}
	return nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/auth", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string]string{"admin_token_header": s.Config.AdminTokenHeaderName()})
	})
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		if s.Store.DB.PingContext(r.Context()) != nil {
			reply(w, 503, map[string]string{"status": "degraded"})
			return
		}
		reply(w, 200, map[string]string{"status": "ok", "version": version.Version})
	})
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		state, e := s.Store.State(r.Context(), s.Config.DeviceID)
		if e != nil {
			s.mu.Unlock()
			reply(w, 503, map[string]string{"error": "database_unavailable"})
			return
		}
		connected := s.active != nil
		state["status_current_session"] = connected && s.active.hasStatus
		s.mu.Unlock()
		seen, _ := state["last_seen"].(int64)
		statusAt, _ := state["status_received_at"].(int64)
		current, _ := state["status_current_session"].(bool)
		reported, _ := state["reported"].(map[string]any)
		info, _ := reported["slotInfo"].(map[string]any)
		fresh := connected && current && statusAt > 0 && time.Now().Unix()-statusAt <= int64(s.Config.StaleSeconds) && time.Now().Unix() >= statusAt
		catalog, err := s.Store.Cards(r.Context(), info, fresh)
		if err != nil {
			reply(w, 503, map[string]string{"error": "database_unavailable"})
			return
		}
		stats, err := s.Store.NotificationSuccess(r.Context(), time.Now(), s.Config.Location)
		if err != nil {
			reply(w, 503, map[string]string{"error": "database_unavailable"})
			return
		}
		state["notification_success"] = stats
		state["cards"] = catalog
		state["card_locations_verified"] = fresh
		state["connected"] = connected
		state["online"] = connected && time.Now().Unix()-seen <= int64(s.Config.StaleSeconds)
		state["stale_seconds"] = s.Config.StaleSeconds
		state["status_refresh_seconds"] = int(s.statusRefreshInterval() / time.Second)
		state["environment"] = s.Config.Environment
		state["voicemail_enabled"] = s.Config.VoicemailEnabled
		slots := []int{}
		slots = append(slots, s.Config.VoicemailSlots...)
		state["voicemail_slots"] = slots
		state["version"] = version.Version
		state["bark_enabled"] = s.Config.BarkKey != ""
		alerts, err := s.Store.StatusAlertSummary(r.Context())
		if err != nil {
			reply(w, 503, map[string]string{"error": "database_unavailable"})
			return
		}
		alerts["enabled"] = s.Config.BarkKey != "" && s.Config.AlertsEnabled()
		state["status_alerts"] = alerts
		state["device_id"] = s.Config.DeviceID
		blocked, err := s.Store.RestoreBlocked(r.Context())
		if err != nil {
			reply(w, 503, map[string]string{"error": "database_unavailable"})
			return
		}
		state["outbound_enabled"] = s.Config.OutboundEnabled && !blocked
		state["outbound_restore_blocked"] = blocked
		state["send_interval_seconds"] = s.Config.SendIntervalSeconds
		state["send_daily_limit"] = s.Config.SendDailyLimit
		state["send_max_segments"] = s.Config.SendMaxSegments
		reply(w, 200, state)
	})
	mux.HandleFunc("POST /api/state/query", s.queryState)
	s.outgoingRoutes(mux)
	s.notificationRoutes(mux)
	s.cardNotificationRoutes(mux)
	s.boardSettingsRoutes(mux)
	s.boardHistoryRoutes(mux)
	s.callRoutes(mux)
	s.unlinkedVoicemailRoutes(mux)
	s.diagnosticRoutes(mux)
	s.settingsRoutes(mux)
	mux.HandleFunc("POST /api/messages/{id}/delete", s.deleteMessage)
	mux.HandleFunc("GET /api/messages/export", s.exportMessages)
	mux.HandleFunc("POST /api/backups/download", s.downloadBackup)
	s.migrationRoutes(mux)
	mux.HandleFunc("GET /api/messages", func(w http.ResponseWriter, r *http.Request) {
		slot, e := strconv.Atoi(r.URL.Query().Get("slot"))
		if r.URL.Query().Get("slot") == "" {
			slot = 0
			e = nil
		}
		q := r.URL.Query().Get("q")
		before, be := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		if r.URL.Query().Get("before") == "" {
			before = 0
			be = nil
		}
		card := r.URL.Query().Get("card")
		if !store.ValidCardFilter(card) || (card != "" && slot != 0) || e != nil || slot < 0 || slot > 2 || len(q) > 256 || be != nil || before < 0 {
			reply(w, 400, map[string]string{"error": "invalid_filter"})
			return
		}
		ms, e := s.Store.CardMessagePage(r.Context(), card, slot, q, before, 51)
		if e != nil {
			reply(w, 503, map[string]string{"error": "database_unavailable"})
			return
		}
		var next int64
		if len(ms) > 50 {
			ms = ms[:50]
			next = ms[49].ID
		}
		views := make([]messageView, 0, len(ms))
		for _, m := range ms {
			views = append(views, s.messageView(m))
		}
		reply(w, 200, map[string]any{"items": views, "limit": 50, "next_before": next})
	})
	mux.HandleFunc("POST /api/messages/{id}/read", func(w http.ResponseWriter, r *http.Request) {
		id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if e != nil || id < 1 {
			reply(w, 400, map[string]string{"error": "invalid_id"})
			return
		}
		e = s.Store.Read(r.Context(), id)
		if errors.Is(e, sql.ErrNoRows) {
			reply(w, 404, map[string]string{"error": "not_found"})
			return
		}
		if e != nil {
			reply(w, 503, map[string]string{"error": "database_unavailable"})
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if e != nil || id < 1 {
			reply(w, 400, map[string]string{"error": "invalid_id"})
			return
		}
		m, e := s.Store.Message(r.Context(), id)
		if errors.Is(e, sql.ErrNoRows) {
			reply(w, 404, map[string]string{"error": "not_found"})
			return
		}
		if e != nil {
			reply(w, 503, map[string]string{"error": "database_unavailable"})
			return
		}
		reply(w, 200, s.messageView(m))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(405)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.WriteHeader(404)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			http.FileServer(http.Dir(s.Config.StaticDir)).ServeHTTP(w, r)
			return
		}
		p := filepath.Join(s.Config.StaticDir, "index.html")
		if _, e := os.Stat(p); e != nil {
			reply(w, 503, map[string]string{"error": "frontend_not_built"})
			return
		}
		http.ServeFile(w, r, p)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// wasm-unsafe-eval compiles the in-page AMR decoder. It does not allow eval.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if s.Config.AdminToken == "" && !localHost(r.Host) {
			reply(w, 403, map[string]string{"error": "host_rejected"})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Method == http.MethodPost && voicemailUploadPath(r.URL.Path) {
				if origin := r.Header.Get("Origin"); origin != "" {
					u, e := url.Parse(origin)
					if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host != r.Host || u.User != nil {
						reply(w, 403, map[string]string{"error": "origin_rejected"})
						return
					}
				}
				s.receiveVoicemail(w, r)
				return
			}
			localHealth := r.Method == "GET" && r.URL.Path == "/api/health" && loopbackPeer(r.RemoteAddr)
			publicAuth := r.Method == http.MethodGet && r.URL.Path == "/api/auth"
			auth := r.Header.Values(s.Config.AdminTokenHeaderName())
			if !localHealth && !publicAuth && s.Config.AdminToken != "" && (len(auth) != 1 || !strings.HasPrefix(auth[0], "Bearer ") || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth[0], "Bearer ")), []byte(s.Config.AdminToken)) != 1) {
				reply(w, 401, map[string]string{"error": "access_required"})
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, e := url.Parse(origin)
				if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host != r.Host || u.User != nil {
					reply(w, 403, map[string]string{"error": "origin_rejected"})
					return
				}
			}
		}
		mux.ServeHTTP(w, r)
	})
}

// Credential-free development is only accessible through loopback host names.
// A domain resolving to 127.0.0.1 must not gain access through DNS rebinding.
func localHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if strings.EqualFold(host, "localhost") || strings.EqualFold(host, "localhost.") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func (s *Server) queryState(w http.ResponseWriter, r *http.Request) {
	_, _, code, err := s.queryDevice(r.Context())
	if err != "" {
		reply(w, code, map[string]string{"error": err})
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
func (s *Server) queryDevice(ctx context.Context) (connection *session, response queryReply, status int, problem string) {
	started := time.Now()
	fields := diagnostics.Fields{}
	defer func() {
		fields.DurationMS, fields.Outcome = time.Since(started).Milliseconds(), problem
		code := "query_completed"
		if problem != "" {
			code = "query_failed"
		}
		s.Logs.Record(code, fields)
	}()
	if !s.query.TryLock() {
		return nil, queryReply{}, 409, "query_in_progress"
	}
	defer s.query.Unlock()
	s.mu.Lock()
	sess := s.active
	s.mu.Unlock()
	if sess == nil {
		return nil, queryReply{}, 409, "device_offline"
	}
	id := tid()
	fields.Session, fields.Transaction = sess.diagnosticID, diagnostics.Reference(id)
	s.Logs.Record("query_started", fields)
	ch := make(chan queryReply, 1)
	sess.mu.Lock()
	sess.tid = id
	sess.response = ch
	sess.mu.Unlock()
	defer func() { sess.mu.Lock(); sess.tid = ""; sess.response = nil; sess.mu.Unlock() }()
	if s.write(sess, map[string]string{"cmd": "stat", "tid": id}) != nil {
		return nil, queryReply{}, 502, "device_write_failed"
	}
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	select {
	case result := <-ch:
		e := result.event
		if e.Code == nil || *e.Code != 0 || e.Type != 402 || e.SubType != 46 {
			return nil, queryReply{}, 502, "device_query_failed"
		}
		return sess, result, 200, ""
	case <-timer.C:
		return nil, queryReply{}, 504, "device_result_unknown"
	case <-ctx.Done():
		return nil, queryReply{}, 408, "request_canceled"
	}
}

func (s *Server) RunNotifications(ctx context.Context) {
	if s.Config.BarkKey == "" {
		return
	}
	if err := s.Store.ResetAlertObservation(ctx); err != nil {
		s.Logs.Record("status_alert_observation_failed", diagnostics.Fields{})
		return
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	lastObserve := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if s.Config.AlertsEnabled() && time.Since(lastObserve) >= 15*time.Second {
				s.notificationGate.Lock()
				err := s.observeStatusAlerts(ctx, time.Now())
				s.notificationGate.Unlock()
				lastObserve = time.Now()
				if err != nil {
					s.Logs.Record("status_alert_observation_failed", diagnostics.Fields{})
				}
			}
			for i := 0; i < 10; i++ {
				s.notificationGate.Lock()
				if e := s.Store.QueueCallTimeouts(ctx, time.Now().Unix()); e != nil {
					s.notificationGate.Unlock()
					s.Logs.Record("notification_queue_failed", diagnostics.Fields{})
					break
				}
				n, e := s.Store.NextNotification(ctx)
				if errors.Is(e, sql.ErrNoRows) {
					s.notificationGate.Unlock()
					break
				}
				if e != nil {
					s.notificationGate.Unlock()
					s.Logs.Record("notification_queue_failed", diagnostics.Fields{})
					break
				}
				if n.CallID > 0 {
					reserved, err := s.Store.ReserveCallNotification(ctx, n, time.Now().Unix())
					if err != nil || !reserved {
						s.notificationGate.Unlock()
						s.Logs.Record("notification_persistence_failed", diagnostics.Fields{})
						break
					}
				}
				ok, code := s.pushNotification(ctx, n)
				persistErr := s.Store.NotificationResultCode(ctx, n, ok, code)
				s.notificationGate.Unlock()
				if persistErr != nil {
					s.Logs.Record("notification_persistence_failed", diagnostics.Fields{Slot: n.Slot})
					break
				}
			}
		}
	}
}
func (s *Server) push(ctx context.Context, n store.Notification) bool {
	ok, _ := s.pushNotification(ctx, n)
	return ok
}
func fmtSlot(slot int) string {
	if slot == 1 {
		return "卡 1"
	}
	return "卡 2"
}
