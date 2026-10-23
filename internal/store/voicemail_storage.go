package store

import (
	"context"
	"errors"
)

const VoicemailIndexLimit = 10000
const VoicemailDirectoryLimit = 10000

type VoicemailCapacityError struct{ Reason string }

func (e *VoicemailCapacityError) Error() string { return ErrVoicemailCapacity.Error() }
func (e *VoicemailCapacityError) Unwrap() error { return ErrVoicemailCapacity }

type VoicemailStorage struct {
	Files        int   `json:"files"`
	Bytes        int64 `json:"bytes"`
	Complete     bool  `json:"complete"`
	IndexCount   int   `json:"index_count"`
	DeletedCount int   `json:"deleted_count"`
	IndexLimit   int   `json:"index_limit"`
	FileLimit    int   `json:"file_limit"`
}

// Extend the existing directory statistics with a cheap index count. Read file
// metadata only, under the same coordination as uploads/deletions. At the scan
// boundary report partial observations instead of pretending the usage is zero.
func (s *Store) VoicemailStorage(ctx context.Context, dataDir string) (VoicemailStorage, error) {
	s.voicemailMu.Lock()
	defer s.voicemailMu.Unlock()
	stats := VoicemailStorage{IndexLimit: VoicemailIndexLimit, FileLimit: VoicemailDirectoryLimit}
	var err error
	stats.Bytes, stats.Files, err = scanVoicemailUsage(ctx, dataDir)
	stats.Complete = err == nil
	if err != nil && !errors.Is(err, ErrVoicemailCapacity) {
		return stats, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(stored_name=''),0) FROM voicemails`).Scan(&stats.IndexCount, &stats.DeletedCount)
	return stats, err
}
