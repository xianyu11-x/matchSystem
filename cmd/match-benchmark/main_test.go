package main

import (
	"strings"
	"testing"

	"matchSystem/internal/identity"
)

func TestBuildTicketsUsesTicketIDAttributesAndLists(t *testing.T) {
	tickets := buildTickets(whiteListSize + blackListSize)
	if len(tickets) != whiteListSize+blackListSize {
		t.Fatalf("ticket count=%d, want %d", len(tickets), whiteListSize+blackListSize)
	}

	seed := tickets[0]
	if got := seed.Uint64Lists["whitelist"]; len(got) != whiteListSize || got[0] != 1 || got[len(got)-1] != whiteListSize {
		t.Fatalf("seed whitelist=%v", got)
	}
	if got := seed.Uint64Lists["blacklist"]; len(got) != blackListSize || got[0] != whiteListSize+1 || got[len(got)-1] != whiteListSize+blackListSize {
		t.Fatalf("seed blacklist=%v", got)
	}
	for index, ticket := range tickets {
		want := uint64(index + 1)
		if got := ticket.Uint64Lists["ticketId"]; len(got) != 1 || got[0] != want {
			t.Fatalf("ticket %d ticketId attribute=%v, want [%d]", ticket.TicketID, got, want)
		}
	}
}

func TestBenchmarkRuleUsesTicketIDIndexForLists(t *testing.T) {
	rule := string(benchmarkRuleJSON(identity.RuleKey{Namespace: benchmarkRuleNamespace, RuleID: 1}, 1000))
	for _, fragment := range []string{
		`"name":"ticketId","type":"uint64s"`,
		`"name":"ticketId","keyType":"uint64"`,
		`"op":"lookup_uint64","index":"ticketId"`,
		`"op":"uint64s_ref","source":"seed_attributes","name":"whitelist"`,
		`"op":"uint64s_ref","source":"seed_attributes","name":"blacklist"`,
	} {
		if !strings.Contains(rule, fragment) {
			t.Fatalf("rule is missing %q", fragment)
		}
	}
	if strings.Contains(rule, `"yes"`) {
		t.Fatal("rule still contains shared yes/no list marker")
	}
}
