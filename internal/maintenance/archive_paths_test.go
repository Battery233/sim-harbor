package maintenance

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnsafeArchiveNamesRejectBeforeRestoreAndCleanWorkspace(t *testing.T) {
	work := t.TempDir()
	t.Setenv("TMPDIR", work)
	s, dir, _, _, _ := audioFixture(t)
	archive, err := Create(t.Context(), s, t.TempDir(), BackupOptions{IncludeRecordings: true, DataDirectory: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	name := strings.Repeat("a", 32) + ".amr"
	escaped := filepath.Join(work, "escaped.amr")
	for _, entry := range []struct{ label, name string }{
		{"parent", "../escaped.amr"},
		{"nested-parent", "voicemail/../../escaped.amr"},
		{"absolute", escaped},
		{"parent-to-valid-directory", "voicemail/../voicemail/" + name},
		{"dot", "voicemail/./" + name},
		{"duplicate-separator", "voicemail//" + name},
		{"backslash-parent", `voicemail\..\escaped.amr`},
		{"mixed-separator-parent", `voicemail/..\escaped.amr`},
		{"windows-drive", "C:/escaped.amr"},
		{"windows-network", `\\server\share\escaped.amr`},
		{"encoded-parent", "voicemail/%2e%2e/" + name},
		{"alternate-stream", "voicemail/" + name + ":stream"},
		{"upper-case-id", "voicemail/" + strings.Repeat("A", 32) + ".amr"},
		{"nul", "voicemail/" + name + "\x00"},
	} {
		t.Run(entry.label, func(t *testing.T) {
			changed := false
			bad := rewrite(t, archive.Path, func(original string, data []byte) (string, []byte) {
				if !changed && recordingArchiveName(original) {
					changed = true
					return entry.name, data
				}
				return original, data
			}, false)
			if !changed {
				t.Fatal("recording fixture missing")
			}
			if _, err := Inspect(t.Context(), bad); !errors.Is(err, ErrInvalidBackup) {
				t.Fatal("unsafe inspection", err)
			}
			_, digest, err := fileDigest(bad)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "restored")
			if _, err := Restore(t.Context(), bad, target, digest); !errors.Is(err, ErrInvalidBackup) {
				t.Fatal("unsafe restore", err)
			}
			for _, path := range []string{target, escaped} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("rejected archive modified the filesystem", err)
				}
			}
			staging, err := filepath.Glob(filepath.Join(work, "sim-inspect-*"))
			if err != nil || len(staging) != 0 {
				t.Fatal("rejected archive retained extraction work", len(staging), err)
			}
		})
	}
}
