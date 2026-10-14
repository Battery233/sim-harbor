package outbound

import (
	"strings"
	"testing"
)

func TestConservativeSegmentsAndValidation(t *testing.T) {
	base := Input{ID: strings.Repeat("a", 32), Slot: 1, Phone: "000", Body: "合成"}
	for _, tc := range []struct {
		body      string
		max, want int
		ok        bool
	}{{"合成", 1, 1, true}, {strings.Repeat("a", 70), 1, 1, true}, {strings.Repeat("a", 71), 1, 2, false}, {strings.Repeat("a", 71), 2, 2, true}, {strings.Repeat("😀", 35), 1, 1, true}, {strings.Repeat("😀", 36), 1, 2, false}, {"\x11", 1, 0, false}, {"", 1, 0, false}, {" 合成\n正文 ", 1, 1, true}} {
		in := base
		in.Body = tc.body
		_, n, e := Normalize(in, tc.max)
		if (e == nil) != tc.ok || (tc.ok && n != tc.want) {
			t.Fatalf("units/validation: %+v %d %v", tc, n, e)
		}
	}
	for _, field := range []string{"id", "slot", "phone"} {
		in := base
		switch field {
		case "id":
			in.ID = "unsafe"
		case "slot":
			in.Slot = 3
		case "phone":
			in.Phone = "000;cmd"
		}
		if _, _, e := Normalize(in, 1); e == nil {
			t.Fatal(field)
		}
	}
}
