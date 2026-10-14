package protocol

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
)

type byteReader struct{ io.Reader }

func (r byteReader) Read(p []byte) (int, error) { return r.Reader.Read(p[:1]) }

func TestFraming(t *testing.T) {
	r := NewReader(byteReader{strings.NewReader("first\x11\x12second\x11\x12tail")})
	for _, want := range []string{"first", "second"} {
		p, e := r.Next()
		if e != nil || string(p) != want {
			t.Fatalf("frame %q: %v", p, e)
		}
	}
	if _, e := r.Next(); !errors.Is(e, io.ErrUnexpectedEOF) {
		t.Fatalf("partial EOF: %v", e)
	}
	for _, n := range []int{MaxFrame, MaxFrame + 1} {
		p := append(bytes.Repeat([]byte{'x'}, n), Delimiter...)
		got, e := NewReader(bytes.NewReader(p)).Next()
		if n == MaxFrame && (e != nil || len(got) != n) {
			t.Fatalf("exact boundary: %v", e)
		}
		if n > MaxFrame && e == nil {
			t.Fatal("oversize accepted")
		}
	}
}

func TestAESIndependentVectorAndModes(t *testing.T) {
	// Independently produced with Node crypto AES-128-CBC, not by this codec.
	const vector = "Emlktwjq8Sh2tS1C1hUV_A"
	p := []byte(`{"type":998}`)
	for mode := 0; mode <= 2; mode++ {
		c := Codec{Mode: mode, Key: []byte("0123456789abcdef"), IV: []byte("fedcba9876543210")}
		upload, e := c.EncodeUpload(p)
		if e != nil {
			t.Fatal(e)
		}
		got, e := c.Open(upload)
		if e != nil || !bytes.Equal(got, p) {
			t.Fatalf("mode %d: %v", mode, e)
		}
		down, e := c.Seal(p)
		if e != nil {
			t.Fatal(e)
		}
		if mode < 2 && !bytes.Equal(down, p) {
			t.Fatal("mode 0/1 downlink encrypted")
		}
		if mode > 0 && strings.TrimRight(string(upload), "=") != vector {
			t.Fatal("independent vector mismatch")
		}
		if mode == 2 {
			got, e = c.Open([]byte(vector))
			if e != nil || !bytes.Equal(got, p) {
				t.Fatal("unpadded vector rejected")
			}
		}
	}
}

func TestCipherRejectsMalformedFrames(t *testing.T) {
	c := Codec{Mode: 2, Key: []byte("0123456789abcdef"), IV: []byte("fedcba9876543210")}
	for _, v := range []string{"", "*", "YQ=="} {
		if _, e := c.Open([]byte(v)); e == nil {
			t.Fatal("malformed encrypted frame accepted")
		}
	}
	encrypted, _ := c.EncodeUpload(bytes.Repeat([]byte{'x'}, 16))
	b, _ := base64.URLEncoding.DecodeString(string(encrypted))
	b[15] ^= 16 // Last padding byte becomes zero.
	if _, e := c.Open([]byte(base64.URLEncoding.EncodeToString(b))); e == nil {
		t.Fatal("invalid PKCS7 padding accepted")
	}
}

func TestEncryptedUploadEnvelopes(t *testing.T) {
	// Independent Node AES vector, with URL and standard Base64 alphabets.
	for _, mode := range []int{1, 2} {
		c := Codec{Mode: mode, Key: []byte("0123456789abcdef"), IV: []byte("fedcba9876543210")}
		for _, payload := range []string{
			`{"p":"Emlktwjq8Sh2tS1C1hUV_A"}`,
			`{"p":"Emlktwjq8Sh2tS1C1hUV_A=="}`,
			`{"p":"Emlktwjq8Sh2tS1C1hUV/A"}`,
			`{"p":"Emlktwjq8Sh2tS1C1hUV/A=="}`,
			" \n{ \"p\" : \"Emlktwjq8Sh2tS1C1hUV_A\" }\t",
		} {
			got, err := c.Open([]byte(payload))
			if err != nil || string(got) != `{"type":998}` {
				t.Fatalf("mode %d envelope: %v", mode, err)
			}
		}
		for _, payload := range []string{
			`{}`, `{"p":null}`, `{"p":123}`, `{"p":""}`, `{"p":"*"}`,
			`{"p":"YQ=="}`, `{"type":998}`,
			`{"p":"Emlktwjq8Sh2tS1C1hUV_A","type":998}`,
			`{"p":"Emlktwjq8Sh2tS1C1hUV_A","p":"Emlktwjq8Sh2tS1C1hUV_A"}`,
			`{"p":"Emlktwjq8Sh2tS1C1hUV_A"} {}`,
			`{"p":"Emlktwjq8Sh2tS1C1hUV_A"`,
		} {
			if _, err := c.Open([]byte(payload)); err == nil {
				t.Fatalf("mode %d unsafe envelope accepted: %s", mode, payload)
			}
		}
	}
}

func TestEventValidation(t *testing.T) {
	e, err := Parse([]byte(`{"devId":"SIM-TEST","type":"501","slot":"2","phNum":"TEST-SENDER","smsTs":"1700000000","smsBd":"合成\n样例"}`))
	if err != nil || e.Slot != 2 || e.Body != "合成\n样例" {
		t.Fatalf("valid synthetic SMS: %v", err)
	}
	for _, p := range []string{`null`, `{"type":998.1}`, `{"type":501,"slot":3}`, `{"type":998,"smsGroupId":"x"}`, `{"type":501,"slot":1,"phNum":"TEST","smsTs":1700000000000}`, "{\"type\":998,\"smsBd\":\"raw\nline\"}"} {
		if _, e := Parse([]byte(p)); e == nil {
			t.Fatalf("invalid event accepted: %q", p)
		}
	}
}

func TestDeliveryReferenceBoundaries(t *testing.T) {
	for _, p := range []string{`{"type":502,"smsSendMr":[0,255]}`, `{"type":503,"slot":1,"phNum":"000","smsSendMr":0,"smsSt":0,"smsScts":"1700000000"}`} {
		if _, e := Parse([]byte(p)); e != nil {
			t.Fatal(e)
		}
	}
	for _, p := range []string{`{"type":502,"smsSendMr":[7,7]}`, `{"type":502,"smsSendMr":[256]}`, `{"type":502,"smsSendMr":null}`, `{"type":503,"slot":1,"phNum":"000","smsSendMr":[7,8],"smsSt":0}`, `{"type":503,"slot":1,"phNum":"000","smsSendMr":7,"smsSt":256}`} {
		if _, e := Parse([]byte(p)); e == nil {
			t.Fatal("unsafe receipt accepted", p)
		}
	}
}
