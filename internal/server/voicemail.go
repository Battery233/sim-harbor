package server

import (
	"bytes"
	"crypto/subtle"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Battery233/sim-harbor/internal/config"
	"github.com/Battery233/sim-harbor/internal/diagnostics"
)

const maxVoicemailBytes = 2 << 20

var voicemailTokenPattern = regexp.MustCompile(`^[0-9a-f]{32,64}$`)

func voicemailUploadPath(path string) bool {
	const prefix = "/api/voicemail/amr/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	token := strings.TrimPrefix(path, prefix)
	return !strings.Contains(token, "/") && voicemailTokenPattern.MatchString(token)
}

func (s *Server) receiveVoicemail(w http.ResponseWriter, r *http.Request) {
	if !s.Config.VoicemailEnabled {
		reply(w, 404, map[string]string{"error": "not_found"})
		return
	}
	if !s.voicemailSourceOK(r) {
		s.Logs.Record("voicemail_rejected", diagnostics.Fields{})
		reply(w, 403, map[string]string{"error": "voicemail_source_rejected"})
		return
	}
	expected, ok := config.VoicemailUploadToken(s.Config.VoicemailAMRURL)
	got := strings.TrimPrefix(r.URL.Path, "/api/voicemail/amr/")
	if !ok || len(got) != len(expected) || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
		s.Logs.Record("voicemail_rejected", diagnostics.Fields{})
		reply(w, 401, map[string]string{"error": "voicemail_upload_rejected"})
		return
	}
	audio, slot, phone, start, problem := readVoicemailUpload(r)
	if problem != "" {
		s.Logs.Record("voicemail_rejected", diagnostics.Fields{Slot: slot})
		reply(w, 400, map[string]string{"error": problem})
		return
	}
	s.notificationGate.Lock()
	defer s.notificationGate.Unlock()
	mediaID, createdAt, _, err := s.Store.SaveVoicemail(r.Context(), s.Config.DataDir, audio, slot, phone, start, time.Now().Unix())
	if err != nil || mediaID == "" {
		s.Logs.Record("voicemail_rejected", diagnostics.Fields{Slot: slot})
		reply(w, 503, map[string]string{"error": "database_unavailable"})
		return
	}
	s.Logs.Record("voicemail_stored", diagnostics.Fields{Slot: slot})
	reply(w, 200, map[string]any{"errcode": 0, "errmsg": "success", "type": "amr", "media_id": mediaID, "created_at": createdAt})
}

func (s *Server) voicemailSourceOK(r *http.Request) bool {
	if !s.Config.AllowlistEnabled() {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return s.Config.Allows(&net.TCPAddr{IP: ip, Port: 1})
}

func readVoicemailUpload(r *http.Request) ([]byte, int, string, int64, string) {
	slot, start, phone, problem := voicemailQuery(r)
	if problem != "" {
		return nil, slot, "", start, problem
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxVoicemailBytes+64*1024)
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	var audio []byte
	if err == nil && strings.HasPrefix(media, "multipart/") {
		reader, readErr := r.MultipartReader()
		if readErr != nil {
			return nil, slot, "", start, "voicemail_media_rejected"
		}
		audio, problem = firstAMRPart(reader)
		if problem != "" {
			return nil, slot, "", start, problem
		}
	} else {
		audio, err = io.ReadAll(io.LimitReader(r.Body, maxVoicemailBytes+1))
		if err != nil || len(audio) == 0 || len(audio) > maxVoicemailBytes || !bytes.HasPrefix(audio, []byte("#!AMR")) {
			return nil, slot, "", start, "voicemail_media_rejected"
		}
	}
	return audio, slot, phone, start, ""
}

func voicemailQuery(r *http.Request) (int, int64, string, string) {
	slotText := r.URL.Query().Get("slot")
	slot := 0
	if slotText != "" {
		n, err := strconv.Atoi(slotText)
		if err != nil || (n != 1 && n != 2) {
			return 0, 0, "", "voicemail_media_rejected"
		}
		slot = n
	}
	startText := r.URL.Query().Get("start")
	var start int64
	if startText != "" {
		n, err := strconv.ParseInt(startText, 10, 64)
		if err != nil || n < 0 || n > 9999999999 {
			return slot, 0, "", "voicemail_media_rejected"
		}
		start = n
	}
	phone := strings.TrimSpace(r.URL.Query().Get("phone"))
	if len(phone) > 128 || strings.ContainsAny(phone, "\x00\r\n") {
		return slot, start, "", "voicemail_media_rejected"
	}
	return slot, start, phone, ""
}

func firstAMRPart(reader *multipart.Reader) ([]byte, string) {
	var fallback []byte
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "voicemail_media_rejected"
		}
		data, err := io.ReadAll(io.LimitReader(part, maxVoicemailBytes+1))
		part.Close()
		if err != nil || len(data) > maxVoicemailBytes {
			return nil, "voicemail_media_rejected"
		}
		if !bytes.HasPrefix(data, []byte("#!AMR")) {
			continue
		}
		if part.FormName() == "media" {
			return data, ""
		}
		if fallback == nil {
			fallback = data
		}
	}
	if fallback == nil {
		return nil, "voicemail_media_rejected"
	}
	return fallback, ""
}
