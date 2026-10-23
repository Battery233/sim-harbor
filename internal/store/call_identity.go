package store

import (
	"encoding/json"
	"strings"

	"github.com/Battery233/sim-harbor/internal/protocol"
)

// The same persisted identity is used for ingestion and diagnostic correlation.
// A missing start is isolated by the exact report, never by the nearest call.
func CallEventKey(e protocol.Event, raw []byte) string {
	identity := any(cardIdentity(e.ICCID))
	if identity == "" {
		identity = int(e.Slot)
	}
	data, _ := json.Marshal([]any{e.DeviceID, identity, int64(e.TelStartTS), strings.TrimSpace(e.Phone)})
	if e.TelStartTS == 0 {
		data, _ = json.Marshal([]any{e.DeviceID, "unknown-start", hash(raw)})
	}
	return hash(data)
}
