package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/store"
)

func (s *Server) pushNotification(ctx context.Context, n store.Notification) (bool, string) {
	fields := diagnostics.Fields{Slot: n.Slot, Operation: diagnostics.Reference("notification:" + strconv.FormatInt(n.ID, 10))}
	if n.AlertID > 0 {
		fields.Operation = diagnostics.Reference("status-alert:" + strconv.FormatInt(n.AlertID, 10))
	}
	if n.CallID > 0 {
		fields.Operation = diagnostics.Reference("call-notification:" + strconv.FormatInt(n.ID, 10))
		lookup, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		fields.CallReference = callReference(s.Store.DiagnosticCallKey(lookup, s.Config.DeviceID, n.CallID))
		cancel()
	}
	s.Logs.Record("notification_attempt", fields)
	started := time.Now()
	name := n.CardName
	if name == "" {
		name = fmtSlot(n.Slot)
	}
	payload := map[string]any{"device_key": s.Config.BarkKey, "title": name + " 收到短信", "body": formatIncomingText(n.Body, s.Config.IncomingTextEscapes), "group": n.CardGroup, "url": strings.TrimRight(s.Config.PublicURL, "/") + "/?message=" + strconv.FormatInt(n.MessageID, 10)}
	if n.CallID > 0 {
		loc := s.Config.Location
		if loc == nil {
			loc = time.UTC
		}
		caller := n.Phone
		if strings.EqualFold(caller, "anonymous") {
			caller = "匿名来电"
		} else if caller == "" {
			caller = "号码未提供"
		}
		when := time.Unix(n.CallStartTS, 0).In(loc).Format("2006-01-02 15:04:05")
		switch n.CallKind {
		case "voicemail":
			payload["title"] = name + " 有留言"
			payload["body"] = caller + " 有留言\n" + when
		case "voicemail_missing":
			payload["title"] = name + " 来电已接通，录音未收到"
			payload["body"] = "来电：" + caller + "\n来电时间：" + when + "\n通话已接通并结束，等待 3 分钟仍未收到关联录音。"
		case "voicemail_late":
			payload["title"] = name + " 迟到的录音已收到"
			payload["body"] = "来电：" + caller + "\n来电时间：" + when + "\n此前未收到的录音现已保存，可打开来电记录播放。"
		case "timeout":
			payload["title"] = name + " 来电提示"
			payload["body"] = caller + " 曾来电，结束时间未确认\n" + when
		default:
			payload["title"] = name + " 未接来电"
			payload["body"] = caller + " 响铃 " + strconv.FormatInt(n.RingSeconds, 10) + " 秒\n" + when
		}
		payload["url"] = strings.TrimRight(s.Config.PublicURL, "/") + "/?call=" + strconv.FormatInt(n.CallID, 10)
	}
	if n.AlertID > 0 {
		title, body := "板子离线", "连续15分钟未确认连接，请检查家庭WiFi和电源。"
		if n.AlertKey != "board" {
			title = n.AlertLabel + " SIM注册异常"
			body = "连续30分钟观察到SIM未就绪，请查看卡状态。"
		}
		if n.AlertKind == "recovery" {
			title = n.AlertLabel + " 已恢复"
			body = "已连续5分钟确认恢复正常。"
		}
		payload["title"] = title
		payload["body"] = body
		payload["group"] = "SIM 运行提醒"
		payload["url"] = s.Config.PublicURL
	}
	ok, outcome := s.sendPush(ctx, payload)
	fields.DurationMS, fields.Outcome = time.Since(started).Milliseconds(), outcome
	code := "notification_failed"
	if ok {
		code = "notification_accepted"
	}
	s.Logs.Record(code, fields)
	return ok, outcome
}
func (s *Server) sendPush(ctx context.Context, payload map[string]any) (bool, string) {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(s.Config.BarkBaseURL, "/")+"/push", strings.NewReader(string(b)))
	if err != nil {
		return false, "transport_failed"
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return false, "transport_failed"
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, "http_rejected"
	}
	p, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	var result struct {
		Code int `json:"code"`
	}
	if err != nil || len(p) > 65536 || json.Unmarshal(p, &result) != nil {
		return false, "invalid_response"
	}
	if result.Code != 200 {
		return false, "bark_rejected"
	}
	return true, "accepted"
}
func (s *Server) notificationRoutes(mux *http.ServeMux) {
	s.notificationRecordRoutes(mux)
	mux.HandleFunc("GET /api/notifications", func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		if state == "" {
			state = "all"
		}
		before := int64(0)
		var err error
		if value := r.URL.Query().Get("before"); value != "" {
			before, err = strconv.ParseInt(value, 10, 64)
		}
		if err != nil || before < 0 || (state != "all" && state != "pending" && state != "sent" && state != "restore_held" && state != "canceled") {
			reply(w, 400, map[string]string{"error": "invalid_filter"})
			return
		}
		rows, summary, err := s.Store.NotificationPage(r.Context(), state, before)
		if err != nil {
			outgoingError(w, err)
			return
		}
		next := int64(0)
		if len(rows) > 50 {
			rows = rows[:50]
			next = rows[49].ID
		}
		u, _ := url.Parse(s.Config.BarkBaseURL)
		reply(w, 200, map[string]any{"items": rows, "next_before": next, "summary": summary, "enabled": s.Config.BarkKey != "", "bark_host": u.Hostname(), "public_url": s.Config.PublicURL, "test_interval_seconds": 60})
	})
	mux.HandleFunc("POST /api/notifications/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Confirmed bool `json:"confirmed"`
		}
		if !decodeAction(w, r, &in) {
			return
		}
		if !in.Confirmed {
			reply(w, 400, map[string]string{"error": "notification_confirmation_required"})
			return
		}
		if s.Config.BarkKey == "" {
			reply(w, 409, map[string]string{"error": "notifications_disabled"})
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			reply(w, 400, map[string]string{"error": "invalid_filter"})
			return
		}
		if err = s.Store.RetryNotification(r.Context(), id, time.Now().Unix()); err != nil {
			outgoingError(w, err)
			return
		}
		reply(w, 200, map[string]bool{"queued": true})
	})
	mux.HandleFunc("POST /api/notifications/test", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Confirmed bool `json:"confirmed"`
		}
		if !decodeAction(w, r, &in) {
			return
		}
		if !in.Confirmed {
			reply(w, 400, map[string]string{"error": "notification_confirmation_required"})
			return
		}
		if s.Config.BarkKey == "" {
			reply(w, 409, map[string]string{"error": "notifications_disabled"})
			return
		}
		at := time.Now().Unix()
		if err := s.Store.ReserveNotificationTest(r.Context(), at); err != nil {
			outgoingError(w, err)
			return
		}
		ok, code := s.sendPush(r.Context(), map[string]any{"device_key": s.Config.BarkKey, "title": "SIM 通知连接测试", "body": "这是一条手动连接测试，不是收到或发出的短信。请在 iPhone 上核对通知。", "group": "SIM 连接测试", "url": s.Config.PublicURL})
		// Preserve uncertainty if persistence fails or a response is lost.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
		defer cancel()
		if err := s.Store.CompleteNotificationTest(ctx, at, code); err != nil {
			outgoingError(w, err)
			return
		}
		if !ok {
			reply(w, 502, map[string]string{"error": "push_request_failed", "outcome": code})
			return
		}
		reply(w, 200, map[string]any{"accepted": true, "outcome": code, "requested_at": at})
	})
}
