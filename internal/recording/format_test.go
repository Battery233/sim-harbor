package recording

import (
	"bytes"
	"strings"
	"testing"
)

func TestAcceptedRecordingFormat(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want bool
	}{
		{"nb", []byte(Header + "SYNTHETIC"), true},
		{"empty", nil, false},
		{"truncated", []byte("#!AMR"), false},
		{"wideband", []byte("#!AMR-WB\nSYNTHETIC"), false},
		{"fake-prefix", []byte("#!AMRX\nSYNTHETIC"), false},
		{"maximum", append([]byte(Header), bytes.Repeat([]byte{0}, MaxBytes-len(Header))...), true},
		{"oversize", append([]byte(Header), bytes.Repeat([]byte{0}, MaxBytes)...), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ValidAudio(tc.data) != tc.want {
				t.Fatal("unexpected audio format decision")
			}
		})
	}
}

func TestRecordingNamesStayInsidePrivateDirectory(t *testing.T) {
	id := strings.Repeat("ab", 16)
	if !ValidMediaID(id) || !ValidFileName(id+".amr") {
		t.Fatal("valid recording identity rejected")
	}
	for _, name := range []string{"", id[:31], id + "0", strings.ToUpper(id), "../" + id, id + ".amr.partial", id + ".AMR", id + ".amr/extra"} {
		if ValidMediaID(name) || ValidFileName(name) {
			t.Fatal("invalid recording identity accepted")
		}
	}
}
