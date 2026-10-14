package protocol

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"
)

const MaxFrame = 65536

var Delimiter = []byte{0x11, 0x12}

// Reader bounds buffering and handles a delimiter split across network reads.
type Reader struct{ r *bufio.Reader }

func NewReader(r io.Reader) *Reader { return &Reader{bufio.NewReader(r)} }
func (r *Reader) Next() ([]byte, error) {
	b := make([]byte, 0, 1024)
	for {
		v, e := r.r.ReadByte()
		if e != nil {
			if len(b) > 0 && e == io.EOF {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, e
		}
		b = append(b, v)
		if len(b) >= 2 && bytes.Equal(b[len(b)-2:], Delimiter) {
			if len(b)-2 > MaxFrame {
				return nil, errors.New("frame too large")
			}
			return b[:len(b)-2], nil
		}
		if len(b) > MaxFrame+1 {
			return nil, errors.New("frame too large")
		}
	}
}

type Codec struct {
	Mode    int
	Key, IV []byte
}

func (c Codec) Seal(p []byte) ([]byte, error) {
	if c.Mode != 2 {
		return append([]byte(nil), p...), nil
	}
	return c.encrypt(p)
}
func (c Codec) EncodeUpload(p []byte) ([]byte, error) {
	if c.Mode == 0 {
		return append([]byte(nil), p...), nil
	}
	return c.encrypt(p)
}
func (c Codec) encrypt(p []byte) ([]byte, error) {
	if len(c.Key) != 16 {
		return nil, errors.New("invalid cipher configuration")
	}
	a, e := aes.NewCipher(c.Key)
	if e != nil || len(c.IV) != aes.BlockSize {
		return nil, errors.New("invalid cipher configuration")
	}
	n := aes.BlockSize - len(p)%aes.BlockSize
	b := append(append([]byte(nil), p...), bytes.Repeat([]byte{byte(n)}, n)...)
	cipher.NewCBCEncrypter(a, c.IV).CryptBlocks(b, b)
	out := make([]byte, base64.URLEncoding.EncodedLen(len(b)))
	base64.URLEncoding.Encode(out, b)
	return out, nil
}
func (c Codec) Open(p []byte) ([]byte, error) {
	if c.Mode == 0 {
		return append([]byte(nil), p...), nil
	}
	if len(c.Key) != 16 {
		return nil, errors.New("invalid cipher configuration")
	}
	// Firmware uploads wrap ciphertext in {"p":"..."}; older clients use
	// bare Base64. Unwrap only that envelope, never accept plaintext in AES mode.
	trimmed := bytes.TrimSpace(p)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var err error
		p, err = encryptedPayload(trimmed)
		if err != nil {
			return nil, err
		}
	}
	b, e := base64.URLEncoding.DecodeString(string(p))
	if e != nil {
		b, e = base64.RawURLEncoding.DecodeString(string(p))
	}
	// The older 561 reference uses standard Base64; 564 specifies Base64URL.
	if e != nil {
		b, e = base64.StdEncoding.DecodeString(string(p))
	}
	if e != nil {
		b, e = base64.RawStdEncoding.DecodeString(string(p))
	}
	if e != nil || len(b) == 0 || len(b)%aes.BlockSize != 0 {
		return nil, errors.New("invalid encrypted frame")
	}
	a, e := aes.NewCipher(c.Key)
	if e != nil || len(c.IV) != aes.BlockSize {
		return nil, errors.New("invalid cipher configuration")
	}
	cipher.NewCBCDecrypter(a, c.IV).CryptBlocks(b, b)
	n := int(b[len(b)-1])
	if n < 1 || n > aes.BlockSize || n > len(b) {
		return nil, errors.New("invalid encrypted frame")
	}
	for _, v := range b[len(b)-n:] {
		if int(v) != n {
			return nil, errors.New("invalid encrypted frame")
		}
	}
	return b[:len(b)-n], nil
}

func encryptedPayload(p []byte) ([]byte, error) {
	invalid := errors.New("invalid encrypted frame")
	d := json.NewDecoder(bytes.NewReader(p))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil, invalid
	}
	if token, err := d.Token(); err != nil || token != "p" {
		return nil, invalid
	}
	var ciphertext string
	if err := d.Decode(&ciphertext); err != nil || ciphertext == "" {
		return nil, invalid
	}
	// Requiring the closing token rejects both extra and duplicate fields.
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return nil, invalid
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, invalid
	}
	return []byte(ciphertext), nil
}

type Number int64

func (n *Number) UnmarshalJSON(b []byte) error {
	s := string(b)
	if len(b) > 0 && b[0] == '"' {
		if e := json.Unmarshal(b, &s); e != nil {
			return e
		}
	}
	v, e := strconv.ParseInt(s, 10, 64)
	*n = Number(v)
	return e
}

type Event struct {
	Results        json.RawMessage `json:"results,omitempty"`
	TelStartTS     Number          `json:"telStartTs,omitempty"`
	TelConnectedTS *Number         `json:"telConnectedTs,omitempty"`
	TelEndTS       Number          `json:"telEndTs,omitempty"`
	TelDir         *Number         `json:"telDir,omitempty"`
	SentMR         json.RawMessage `json:"smsSendMr,omitempty"`
	SMSStatus      *Number         `json:"smsSt,omitempty"`
	SCTS           Number          `json:"smsScts"`
	DeliveryTS     Number          `json:"smsDt"`
	Val            string          `json:"val"`
	DeviceID       string          `json:"devId"`
	Type           Number          `json:"type"`
	Slot           Number          `json:"slot"`
	MessageTS      Number          `json:"msgTs"`
	SMSTS          Number          `json:"smsTs"`
	Phone          string          `json:"phNum"`
	Body           string          `json:"smsBd"`
	MD5            string          `json:"smsMD5"`
	ICCID          string          `json:"iccId"`
	CardName       string          `json:"scName,omitempty"`
	Group          string          `json:"smsGroupId"`
	Max            Number          `json:"smsMaxNo"`
	Complete       *bool           `json:"comp"`
	Fragments      []string        `json:"smsBdFrag"`
	TID            string          `json:"tid"`
	Code           *Number         `json:"code"`
	SubType        Number          `json:"subType"`
	SlotInfo       map[string]any  `json:"slotInfo,omitempty"`
}

func Parse(p []byte) (Event, error) {
	var e Event
	if !utf8.Valid(p) {
		return e, errors.New("invalid UTF-8 event")
	}
	err := json.Unmarshal(p, &e)
	if err != nil {
		return e, errors.New("invalid JSON event")
	}
	if len(e.TID) > 64 {
		return e, errors.New("invalid transaction ID")
	}
	if len(e.SentMR) > 0 {
		refs, err := e.References()
		if err != nil || (e.Type == 503 && len(refs) != 1) {
			return e, errors.New("invalid delivery references")
		}
	}
	if e.Type == 503 && (e.Slot < 1 || e.Slot > 2 || e.Phone == "" || e.SMSStatus == nil || *e.SMSStatus < 0 || *e.SMSStatus > 255 || e.SCTS < 0 || e.SCTS > 9999999999 || e.DeliveryTS < 0 || e.DeliveryTS > 9999999999) {
		return e, errors.New("invalid delivery report")
	}
	if e.Type < 1 {
		return e, errors.New("event type required")
	}
	if e.Type == 501 && (e.Slot < 1 || e.Slot > 2 || e.Phone == "" || e.SMSTS <= 0 || e.SMSTS > 9999999999) {
		return e, errors.New("invalid SMS event")
	}
	if IsIncomingCall(e.Type) && (e.Slot < 1 || e.Slot > 2 || e.TelStartTS < 0 || e.TelStartTS > 9999999999 || e.TelEndTS < 0 || e.TelEndTS > 9999999999 || (e.TelConnectedTS != nil && (*e.TelConnectedTS < 0 || *e.TelConnectedTS > 9999999999)) || len(e.Phone) > 128) {
		return e, errors.New("invalid incoming call event")
	}
	if len(e.Group) > 128 || e.Max < 0 || e.Max > 255 || (e.Group != "" && e.Max < 1) {
		return e, errors.New("invalid multipart metadata")
	}
	return e, nil
}

func IsIncomingCall(kind Number) bool { return kind == 601 || kind == 602 || kind == 603 }

// JSON 502 uses an array, while 503 uses one scalar. Missing means unknown.
func (e Event) References() ([]int, error) {
	if len(e.SentMR) == 0 {
		return nil, nil
	}
	var ns []Number
	if e.SentMR[0] == '[' {
		if err := json.Unmarshal(e.SentMR, &ns); err != nil {
			return nil, err
		}
	} else {
		var n Number
		if err := json.Unmarshal(e.SentMR, &n); err != nil {
			return nil, err
		}
		ns = []Number{n}
	}
	if len(ns) < 1 || len(ns) > 255 {
		return nil, errors.New("invalid delivery references")
	}
	seen := map[int]bool{}
	out := make([]int, 0, len(ns))
	for _, n := range ns {
		if n < 0 || n > 255 || seen[int(n)] {
			return nil, errors.New("invalid delivery reference")
		}
		seen[int(n)] = true
		out = append(out, int(n))
	}
	return out, nil
}
