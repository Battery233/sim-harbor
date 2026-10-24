package recording

import (
	"bufio"
	"io"
)

// DurationMS counts complete AMR-NB storage frames without decoding audio.
// RFC 4867 sections 3.6 and 5.3: each frame, including SID and NO_DATA,
// represents 20 ms. Padding and the quality bit do not affect its duration.
func DurationMS(r io.Reader) (int64, error) {
	r = io.LimitReader(r, MaxBytes+1)
	var header [len(Header)]byte
	if _, err := io.ReadFull(r, header[:]); err != nil || string(header[:]) != Header {
		return 0, ErrInvalid
	}
	reader := bufio.NewReader(r)
	// Payload bytes, excluding the one-byte frame header; -1 is unsupported.
	sizes := [...]int{12, 13, 15, 17, 19, 20, 26, 31, 5, -1, -1, -1, -1, -1, -1, 0}
	var payload [31]byte
	var frames int64
	used := len(Header)
	for {
		header, err := reader.ReadByte()
		if err == io.EOF && frames > 0 {
			return frames * 20, nil
		}
		if err != nil {
			return 0, ErrInvalid
		}
		size := sizes[(header>>3)&15]
		used += 1 + size
		if size < 0 || used > MaxBytes {
			return 0, ErrInvalid
		}
		if _, err := io.ReadFull(reader, payload[:size]); err != nil {
			return 0, ErrInvalid
		}
		frames++
	}
}
