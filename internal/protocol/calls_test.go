package protocol

import "testing"

func TestIncomingCallPresenceAndNumericStrings(t *testing.T) {
	e, err := Parse([]byte(`{"type":603,"slot":"2","phNum":"anonymous","telStartTs":"1700000000","telConnectedTs":"0","telEndTs":"1700000023","telDir":1}`))
	if err != nil || e.TelConnectedTS == nil || *e.TelConnectedTS != 0 || e.TelStartTS != 1700000000 || e.TelEndTS != 1700000023 {
		t.Fatal("missed report not parsed", err)
	}
	missing, err := Parse([]byte(`{"type":603,"slot":1,"telStartTs":1700000000,"telEndTs":1700000010}`))
	if err != nil || missing.TelConnectedTS != nil {
		t.Fatal("missing connected field guessed as missed", err)
	}
	if _, err = Parse([]byte(`{"type":603,"slot":1}`)); err != nil {
		t.Fatal("incomplete call must be recorded as unknown rather than dropping TCP", err)
	}
	for _, raw := range []string{`{"type":601,"slot":0}`, `{"type":603,"slot":1,"telEndTs":-1}`, `{"type":602,"slot":1,"telConnectedTs":"garbage"}`} {
		if _, err = Parse([]byte(raw)); err == nil {
			t.Fatal("malformed call accepted")
		}
	}
}
