package fact

import (
	"testing"

	"matchSystem/internal/common"
)

func BenchmarkObjectSlotColdRefresh(b *testing.B) {
	layout, err := NewObjectLayout([]Spec{{Name: "label", Type: TypeStrings, MaxValues: 1, Scope: ScopeObject}})
	if err != nil {
		b.Fatal(err)
	}
	ticket := &common.Ticket{TicketID: 1}
	provider := ObjectProvider(func(_ *common.Ticket, _ int64, _ Values, out Writer) error {
		return out.SetStrings("label", []string{"value"})
	})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var slot ObjectSlot
		slot.Init(layout)
		if _, _, err := slot.ensure(1, ticket, 0, Values{}, provider, false); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkObjectSlotSteadyCacheHit(b *testing.B) {
	layout, err := NewObjectLayout([]Spec{{Name: "label", Type: TypeStrings, MaxValues: 1, Scope: ScopeObject}})
	if err != nil {
		b.Fatal(err)
	}
	var slot ObjectSlot
	slot.Init(layout)
	ticket := &common.Ticket{TicketID: 1}
	provider := ObjectProvider(func(_ *common.Ticket, _ int64, _ Values, out Writer) error {
		return out.SetStrings("label", []string{"value"})
	})
	if _, _, err := slot.ensure(1, ticket, 0, Values{}, provider, false); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, _, err := slot.ensure(1, ticket, 0, Values{}, provider, false); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkObjectSlotSteadyRefreshReuse(b *testing.B) {
	layout, err := NewObjectLayout([]Spec{{Name: "label", Type: TypeStrings, MaxValues: 1, Scope: ScopeObject}})
	if err != nil {
		b.Fatal(err)
	}
	var slot ObjectSlot
	slot.Init(layout)
	ticket := &common.Ticket{TicketID: 1}
	provider := ObjectProvider(func(_ *common.Ticket, _ int64, _ Values, out Writer) error {
		return out.SetStrings("label", []string{"value"})
	})
	if _, _, err := slot.ensure(1, ticket, 0, Values{}, provider, false); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, _, err := slot.ensure(uint64(i+2), ticket, 0, Values{}, provider, false); err != nil {
			b.Fatal(err)
		}
	}
}
