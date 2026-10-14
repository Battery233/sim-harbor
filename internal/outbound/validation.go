// Package outbound holds the shared, conservative validation for chargeable SMS.
package outbound

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var operationID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var phoneNumber = regexp.MustCompile(`^\+?[0-9]{3,20}$`)

type Input struct {
	ID    string `json:"id"`
	Slot  int    `json:"slot"`
	Phone string `json:"phone"`
	Body  string `json:"body"`
}

func ValidID(id string) bool { return operationID.MatchString(id) }

func Normalize(in Input, maxSegments int) (Input, int, error) {
	in.Phone = strings.TrimSpace(in.Phone)
	in.Body = strings.TrimSpace(in.Body)
	if !ValidID(in.ID) || in.Slot < 1 || in.Slot > 2 || !phoneNumber.MatchString(in.Phone) || in.Body == "" || !utf8.ValidString(in.Body) || len(in.Body) > 4000 {
		return in, 0, errors.New("invalid_sms")
	}
	for _, r := range in.Body {
		if r < 32 && r != '\n' && r != '\r' && r != '\t' || r == 127 {
			return in, 0, errors.New("invalid_sms")
		}
	}
	// Count UTF-16 units for every message, including plain English. This is
	// deliberately more conservative than assuming the modem uses GSM-7.
	units := len(utf16.Encode([]rune(in.Body)))
	segments := 1
	if units > 70 {
		segments = (units + 66) / 67
	}
	if maxSegments < 1 || segments > maxSegments {
		return in, segments, errors.New("segment_limit")
	}
	return in, segments, nil
}
