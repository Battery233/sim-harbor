// Package recording defines the AMR-NB format accepted across upload and backup.
package recording

import (
	"bytes"
	"errors"
	"strings"
)

const Header = "#!AMR\n"
const MaxBytes = 2 << 20

var ErrInvalid = errors.New("invalid AMR-NB recording")

func ValidAudio(audio []byte) bool {
	return len(audio) <= MaxBytes && bytes.HasPrefix(audio, []byte(Header))
}

func ValidMediaID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func ValidFileName(name string) bool {
	id, ok := strings.CutSuffix(name, ".amr")
	return ok && ValidMediaID(id)
}
