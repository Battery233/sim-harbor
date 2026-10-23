package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Battery233/sim-harbor/internal/diagnostics"
)

// The existing private checkpoint remains the delivery deduplication authority.
// The database now additionally records queryable capacity task history.
type voicemailCapacityCheckpoint struct {
	JobID    int64 `json:"job_id,omitempty"`
	Limit    int64 `json:"limit"`
	Notified int64 `json:"notified"`
	Target   int64 `json:"target"`
	Attempts int   `json:"attempts"`
	NextAt   int64 `json:"next_at"`
}

func voicemailCapacityLevel(used, limit int64) int64 {
	half := limit / 2
	if used < half {
		return 0
	}
	return min(limit, half+(used-half)/(32<<20)*(32<<20))
}

// Owned by the notification worker. Network I/O never holds notificationGate.
// Never run network requests on the recording-upload request path.
func (s *Server) observeVoicemailCapacity(ctx context.Context, now time.Time) error {
	if s.Config.VoicemailMode() != "public" || !s.Config.VoicemailEnabled {
		return s.Store.CancelCapacityNotifications(ctx, "capacity_disabled")
	}
	if s.Config.BarkKey == "" {
		return nil
	}
	blocked, err := s.Store.RestoreBlocked(ctx)
	if err != nil || blocked {
		return err
	}
	used, err := s.Store.VoicemailUsage(ctx, s.Config.DataDir)
	if err != nil {
		return err
	}
	limit := s.Config.VoicemailStorageLimit()
	level := voicemailCapacityLevel(used, limit)
	path := filepath.Join(s.Config.DataDir, "voicemail-capacity.private.json")
	state, exists, err := readVoicemailCapacity(path)
	if err != nil {
		return err
	}
	changedLimit := state.Limit != limit
	if changedLimit {
		if err = s.Store.CancelCapacityNotifications(ctx, "capacity_limit_changed"); err != nil {
			return err
		}
		state = voicemailCapacityCheckpoint{Limit: limit}
	}
	if level == 0 {
		if err = s.Store.CancelCapacityNotifications(ctx, "capacity_recovered"); err != nil {
			return err
		}
		if exists && (changedLimit || state.Notified != 0 || state.Target != 0 || state.Attempts != 0 || state.NextAt != 0) {
			return writeVoicemailCapacity(path, voicemailCapacityCheckpoint{Limit: limit})
		}
		return nil
	}
	if level <= state.Notified {
		return nil
	}
	// Coalesce missed bands and pending retries into one current-state reminder.
	previousJob, previousTarget := state.JobID, state.Target
	state.Target = level
	jobID, accepted, err := s.Store.PrepareCapacityNotification(ctx, state.JobID, level, limit, used, state.NextAt, now.Unix())
	if err != nil {
		return err
	}
	state.JobID = jobID
	if accepted {
		state.Notified = level
		state.Target, state.Attempts, state.NextAt = 0, 0, 0
		return writeVoicemailCapacity(path, state)
	}
	if now.Unix() < state.NextAt {
		if previousJob != state.JobID || previousTarget != state.Target {
			return writeVoicemailCapacity(path, state)
		}
		return nil
	}
	state.Attempts = min(state.Attempts+1, 16)
	delay := min(3600, 30*(1<<min(state.Attempts-1, 7)))
	state.NextAt = now.Unix() + int64(delay)
	// Reserve the retry time before contacting Bark. A lost response or process
	// crash may lead to a retry, but cannot create a rapid notification loop.
	if err = writeVoicemailCapacity(path, state); err != nil {
		return err
	}
	if err = s.Store.ReserveCapacityNotification(ctx, state.JobID, state.NextAt); err != nil {
		return err
	}
	ok, outcome := s.sendPush(ctx, map[string]any{
		"device_key": s.Config.BarkKey,
		"title":      "录音存储空间提醒",
		"body":       fmt.Sprintf("录音目录已用 %.1f / %.0f MiB，剩余 %.1f MiB。\n容量达到一半后，每增加 32 MiB 提醒一次。达到上限后拒收新录音，已有文件保留。", float64(used)/(1<<20), float64(limit)/(1<<20), float64(max(0, limit-used))/(1<<20)),
		"group":      "SIM 运行提醒",
		"url":        s.Config.PublicURL,
	})
	code := "voicemail_capacity_notify_failed"
	if ok {
		state.Notified = level
		state.Target, state.Attempts, state.NextAt = 0, 0, 0
		code = "voicemail_capacity_notified"
	}
	s.Logs.Record(code, diagnostics.Fields{Outcome: outcome})
	resultCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err = s.Store.CapacityNotificationResult(resultCtx, state.JobID, ok, outcome, now.Unix()); err != nil {
		return err
	}
	// Persist even if the worker context was canceled while Bark responded.
	return writeVoicemailCapacity(path, state)
}

func readVoicemailCapacity(path string) (voicemailCapacityCheckpoint, bool, error) {
	var state voicemailCapacityCheckpoint
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return state, true, errors.New("invalid capacity checkpoint")
	}
	p, err := os.ReadFile(path)
	if err != nil {
		return state, true, err
	}
	d := json.NewDecoder(bytes.NewReader(p))
	d.DisallowUnknownFields()
	if d.Decode(&state) != nil || d.Decode(new(any)) != io.EOF || state.JobID < 0 || state.Limit < 16<<20 || state.Limit > 65536<<20 || state.Notified < 0 || state.Notified > state.Limit || state.Target < 0 || state.Target > state.Limit || state.Attempts < 0 || state.Attempts > 16 || state.NextAt < 0 {
		return state, true, errors.New("invalid capacity checkpoint")
	}
	return state, true, nil
}

func writeVoicemailCapacity(path string, state voicemailCapacityCheckpoint) error {
	p, err := json.Marshal(state)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".voicemail-capacity-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(append(p, '\n')); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	if dir, e := os.Open(filepath.Dir(path)); e == nil {
		defer dir.Close()
		return dir.Sync()
	}
	return nil
}
