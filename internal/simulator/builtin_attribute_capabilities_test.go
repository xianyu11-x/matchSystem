package simulator

import (
	"context"
	"reflect"
	"testing"
)

func TestBuiltinAttributeCapabilitiesAreDetached(t *testing.T) {
	sim, err := NewSimulator(Scenario{})
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	first, err := sim.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := BuiltinAttributeCapability{Name: "createTime", Type: "int64", ReadOnly: true, Source: "ticket.createdAt", IndexTypes: []string{"int64_range"}}
	if len(first.BuiltinAttributes) != 1 || !reflect.DeepEqual(first.BuiltinAttributes[0], want) {
		t.Fatalf("builtin attribute capabilities = %#v", first.BuiltinAttributes)
	}
	first.BuiltinAttributes[0].Name = "changed"
	first.BuiltinAttributes[0].IndexTypes[0] = "changed"
	second, err := sim.Capabilities(context.Background())
	if err != nil || len(second.BuiltinAttributes) != 1 || !reflect.DeepEqual(second.BuiltinAttributes[0], want) {
		t.Fatalf("capabilities leaked mutable state: %#v, err=%v", second.BuiltinAttributes, err)
	}
}
