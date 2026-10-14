package server

import (
	"strings"

	"github.com/Battery233/sim/internal/store"
)

// formatIncomingText is an explicit firmware compatibility policy, not JSON
// unescaping. Preserve raw storage and protect pairs of literal backslashes.
func formatIncomingText(body string, enabled bool) string {
	if !enabled {
		return body
	}
	var out strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' || i+1 == len(body) {
			out.WriteByte(body[i])
			continue
		}
		switch body[i+1] {
		case '\\':
			out.WriteString(body[i : i+2])
			i++
		case 'n':
			out.WriteByte('\n')
			i++
		case 'r':
			out.WriteByte('\r')
			i++
		case 't':
			out.WriteByte('\t')
			i++
		default:
			out.WriteByte(body[i])
		}
	}
	return out.String()
}

type messageView struct {
	store.Message
	DisplayBody *string `json:"display_body,omitempty"`
}

func (s *Server) messageView(m store.Message) messageView {
	v := messageView{Message: m}
	if s.Config.IncomingTextEscapes {
		body := formatIncomingText(m.Body, true)
		v.DisplayBody = &body
	}
	return v
}
