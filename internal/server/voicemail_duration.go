package server

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/Battery233/sim-harbor/internal/recording"
)

// Duration is requested only for displayed recordings, independently of list
// polling. It neither downloads audio to the browser nor marks it as played.
func (s *Server) voicemailDuration(w http.ResponseWriter, r *http.Request) {
	media := r.PathValue("media")
	if !recording.ValidMediaID(media) {
		reply(w, 400, map[string]string{"error": "invalid_id"})
		return
	}
	file, err := s.Store.VoicemailMediaFile(r.Context(), media)
	if errors.Is(err, sql.ErrNoRows) {
		reply(w, 404, map[string]string{"error": "voicemail_unavailable"})
		return
	}
	if err != nil {
		outgoingError(w, err)
		return
	}
	f, err := s.openVoicemailFile(file)
	if err != nil {
		reply(w, 404, map[string]string{"error": "voicemail_audio_missing"})
		return
	}
	defer f.Close()
	var duration *int64
	if ms, err := recording.DurationMS(f); err == nil {
		duration = &ms
	}
	reply(w, 200, map[string]*int64{"duration_ms": duration})
}
