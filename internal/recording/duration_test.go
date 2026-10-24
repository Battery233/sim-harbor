package recording

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestDurationCountsSpeechSilenceAndChangingModes(t *testing.T) {
	// Independent lengths from RFC 4867's bit counts, including the frame header.
	lengths := []int{13, 14, 16, 18, 20, 21, 27, 32, 6}
	audio := []byte(Header)
	for mode, length := range lengths {
		frame := make([]byte, length)
		frame[0] = byte(mode<<3) | 4
		audio = append(audio, frame...)
		ms, err := DurationMS(bytes.NewReader(audio))
		if err != nil || ms != int64(mode+1)*20 {
			t.Fatalf("mode %d: duration %d, error %v", mode, ms, err)
		}
	}
	// Quality=0 and nonzero padding are still valid for reading duration.
	audio = append(audio, 0xfb)
	if ms, err := DurationMS(bytes.NewReader(audio)); err != nil || ms != 200 {
		t.Fatal("NO_DATA or padding changed duration", ms, err)
	}
	audio = append([]byte(Header), bytes.Repeat([]byte{0x7c}, 500)...)
	if ms, err := DurationMS(bytes.NewReader(audio)); err != nil || ms != 10000 {
		t.Fatal("10-second silence", ms, err)
	}
}

func TestDurationRejectsIncompleteAndUnsupportedFrames(t *testing.T) {
	for _, audio := range [][]byte{
		nil, []byte("#!AMR"), []byte("#!AMR-WB\n\x7c"), []byte(Header),
		[]byte(Header + "\x04"), []byte(Header + "\x7c\x3c\x00"),
	} {
		if ms, err := DurationMS(bytes.NewReader(audio)); err == nil || ms != 0 {
			t.Fatal("incomplete audio produced duration", ms, err)
		}
	}
	for mode := 9; mode <= 14; mode++ {
		audio := append([]byte(Header), byte(mode<<3))
		if ms, err := DurationMS(bytes.NewReader(audio)); err == nil || ms != 0 {
			t.Fatal("unsupported frame produced duration", mode, ms, err)
		}
	}
}

func TestDurationBoundsInputAndRejectsReadFailure(t *testing.T) {
	audio := append([]byte(Header), bytes.Repeat([]byte{0x7c}, MaxBytes-len(Header))...)
	if ms, err := DurationMS(bytes.NewReader(audio)); err != nil || ms != int64(MaxBytes-len(Header))*20 {
		t.Fatal("maximum input rejected", ms, err)
	}
	if ms, err := DurationMS(bytes.NewReader(append(audio, 0x7c))); err == nil || ms != 0 {
		t.Fatal("oversize input accepted", ms, err)
	}
	reader := io.MultiReader(bytes.NewReader([]byte(Header+"\x7c")), brokenAudioReader{})
	if ms, err := DurationMS(reader); err == nil || ms != 0 {
		t.Fatal("read failure reported a partial duration", ms, err)
	}
}

type brokenAudioReader struct{}

func (brokenAudioReader) Read([]byte) (int, error) { return 0, errors.New("synthetic read failure") }
