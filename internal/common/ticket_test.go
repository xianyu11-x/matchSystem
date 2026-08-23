package common

import "testing"

func TestCloneTicketDeepCopiesDynamicFields(t *testing.T) {
	original := Ticket{
		TicketID:    "ticket-1",
		StringLists: map[string][]string{"mode": {"ranked"}},
		Uint64Lists: map[string][]uint64{"region": {1}},
		Int64Values: map[string]int64{"score": 100},
	}
	clone := CloneTicket(original)
	clone.StringLists["mode"][0] = "casual"
	clone.Uint64Lists["region"][0] = 2
	clone.Int64Values["score"] = 200

	if original.StringLists["mode"][0] != "ranked" || original.Uint64Lists["region"][0] != 1 || original.Int64Values["score"] != 100 {
		t.Fatal("clone mutated the original ticket")
	}
}
