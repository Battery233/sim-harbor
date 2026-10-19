package server

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Battery233/sim-harbor/internal/diagnostics"
	"github.com/Battery233/sim-harbor/internal/protocol"
	"github.com/Battery233/sim-harbor/internal/store"
)

func (s *Server) recordSMSIngest(ctx context.Context, sess *session, e protocol.Event, code string, result error) {
	// A plain SMS may explicitly carry comp=true. That alone does not make
	// it multipart; keep normal single-part receipts out of these diagnostics.
	if e.Type != 501 || (e.Group == "" && e.Max <= 1 && (e.Complete == nil || *e.Complete) && len(e.Fragments) == 0) {
		return
	}
	f := diagnostics.Fields{Session: sess.diagnosticID, Type: 501, Slot: int(e.Slot),
		SMSGroup: store.SMSGroupReference(e), SMSTotal: int(e.Max), SMSComplete: e.Complete, SMSTime: int64(e.SMSTS)}
	seen := map[int]bool{}
	for _, part := range e.Fragments {
		key, _, ok := strings.Cut(part, ",")
		n, err := strconv.Atoi(key)
		if !ok || err != nil || n < 1 || n > 255 || (e.Max > 0 && n > int(e.Max)) {
			f.SMSInvalidParts++
			continue
		}
		seen[n] = true
	}
	indices := make([]int, 0, len(seen))
	for n := range seen {
		indices = append(indices, n)
	}
	sort.Ints(indices)
	parts := make([]string, 0, len(indices))
	for _, n := range indices {
		parts = append(parts, strconv.Itoa(n))
	}
	f.SMSParts = strings.Join(parts, ",")
	if result != nil {
		f.Outcome = "store_failed"
		switch {
		case errors.Is(result, store.ErrSMSTotalMismatch):
			f.Outcome = "total_mismatch"
		case errors.Is(result, store.ErrSMSFragmentInvalid):
			f.Outcome = "invalid_fragment"
		case errors.Is(result, store.ErrSMSFragmentConflict):
			f.Outcome = "conflicting_fragment"
		}
	} else if code == "sms_fragment_stored" {
		f.Outcome = "stored"
		if f.SMSGroup != "" {
			lookup, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			id, complete, err := s.Store.SMSProgress(lookup, e)
			cancel()
			if err == nil {
				f.MessageID = id
				f.SMSStoredComplete = &complete
			}
		}
	}
	s.Logs.Record(code, f)
}
