package simulatorapi

import (
	"encoding/json"
	"strconv"
)

// MarshalJSON preserves large observed attributes for JavaScript clients.
// Input decoding retains the existing numeric Ticket contract.
func (t Ticket) MarshalJSON() ([]byte, error) {
	u := map[string][]any{}
	for name, values := range t.Uint64Lists {
		out := make([]any, len(values))
		for i, v := range values {
			if v > 9007199254740991 {
				out[i] = strconv.FormatUint(v, 10)
			} else {
				out[i] = v
			}
		}
		u[name] = out
	}
	integers := map[string]any{}
	for name, v := range t.Int64Values {
		if v > 9007199254740991 || v < -9007199254740991 {
			integers[name] = strconv.FormatInt(v, 10)
		} else {
			integers[name] = v
		}
	}
	return json.Marshal(struct {
		TicketID  uint64              `json:"ticketId"`
		CreatedAt int64               `json:"createdAt"`
		Strings   map[string][]string `json:"stringLists,omitempty"`
		Uint64s   map[string][]any    `json:"uint64Lists,omitempty"`
		Int64s    map[string]any      `json:"int64Values,omitempty"`
		Omitted   int                 `json:"omittedNumericSamples,omitempty"`
	}{t.TicketID, t.CreatedAt, t.StringLists, u, integers, t.OmittedNumericSamples})
}
