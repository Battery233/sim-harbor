package server

import (
	"encoding/json"
	"errors"
	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"io"
	"net"
	"net/http"
	"strings"
)

func loopbackPeer(addr string) bool {
	h, _, err := net.SplitHostPort(addr)
	ip := net.ParseIP(h)
	return err == nil && ip != nil && ip.IsLoopback()
}
func settingsJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		reply(w, 400, map[string]string{"error": "invalid_settings"})
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		reply(w, 400, map[string]string{"error": "invalid_settings"})
		return false
	}
	return true
}
func (s *Server) settingsReady(w http.ResponseWriter) bool {
	if len(s.Config.AdminToken) < 24 {
		reply(w, 403, map[string]string{"error": "settings_access_required"})
		return false
	}
	if s.Settings == nil {
		reply(w, 503, map[string]string{"error": "settings_unavailable"})
		return false
	}
	if s.restarting.Load() {
		reply(w, 409, map[string]string{"error": "settings_restarting"})
		return false
	}
	return true
}
func settingsError(w http.ResponseWriter, err error) {
	if errors.Is(err, config.ErrConflict) {
		reply(w, 409, map[string]string{"error": "settings_conflict"})
		return
	}
	if errors.Is(err, config.ErrSettingsInvalid) {
		reply(w, 422, map[string]string{"error": "invalid_settings"})
		return
	}
	reply(w, 503, map[string]string{"error": "settings_unavailable"})
}
func (s *Server) settingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		if !s.settingsReady(w) {
			return
		}
		snap, err := s.Settings.Snapshot()
		if err != nil {
			settingsError(w, err)
			return
		}
		reply(w, 200, snap)
	})
	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		s.settingsGate.Lock()
		defer s.settingsGate.Unlock()
		if !s.settingsReady(w) {
			return
		}
		var input struct {
			Values             config.Config        `json:"values"`
			Secrets            config.SecretChanges `json:"secrets"`
			Revision           string               `json:"revision"`
			Confirmed          bool                 `json:"confirmed"`
			VoicemailConfirmed bool                 `json:"voicemail_confirmed"`
		}
		if !settingsJSON(w, r, &input) {
			return
		}
		if !input.Confirmed {
			reply(w, 400, map[string]string{"error": "settings_confirmation_required"})
			return
		}
		current, err := s.Settings.Snapshot()
		if err != nil {
			settingsError(w, err)
			return
		}
		if input.Values.VoicemailEnabled && (!current.Values.VoicemailEnabled || current.Values.VoicemailMode() != input.Values.VoicemailMode()) && !input.VoicemailConfirmed {
			reply(w, 400, map[string]string{"error": "voicemail_confirmation_required"})
			return
		}
		if input.Values.VoicemailEnabled && input.Values.VoicemailMode() == "lan" && !input.Values.AllowlistEnabled() &&
			(!current.Values.VoicemailEnabled || current.Values.VoicemailMode() != "lan") {
			reply(w, 409, map[string]string{"error": "voicemail_allowlist_required"})
			return
		}
		snap, err := s.Settings.Save(input.Values, input.Secrets, input.Revision)
		if err != nil {
			settingsError(w, err)
			return
		}
		s.Logs.Record("settings_saved", diagnostics.Fields{})
		reply(w, 200, snap)
	})
	mux.HandleFunc("POST /api/settings/restart", func(w http.ResponseWriter, r *http.Request) {
		s.settingsGate.Lock()
		defer s.settingsGate.Unlock()
		if !s.settingsReady(w) {
			return
		}
		var input struct {
			Revision  string `json:"revision"`
			Confirmed bool   `json:"confirmed"`
		}
		if !settingsJSON(w, r, &input) {
			return
		}
		if !input.Confirmed {
			reply(w, 400, map[string]string{"error": "settings_confirmation_required"})
			return
		}
		if s.Restart == nil {
			reply(w, 503, map[string]string{"error": "settings_restart_unavailable"})
			return
		}
		if err := s.Settings.CheckRevision(input.Revision); err != nil {
			settingsError(w, err)
			return
		}
		if !s.restarting.CompareAndSwap(false, true) {
			reply(w, 409, map[string]string{"error": "settings_restarting"})
			return
		}
		s.Logs.Record("settings_restart_requested", diagnostics.Fields{})
		reply(w, 200, map[string]bool{"restarting": true})
		s.Restart()
	})
}
