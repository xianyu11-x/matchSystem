package matchsystem

import (
	"context"
	"errors"
	"testing"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
)

type descriptorTestMatchProvider struct {
	descriptor ProviderDescriptor
}

func (p descriptorTestMatchProvider) Describe() (ProviderDescriptor, error) {
	return p.descriptor.Clone(), nil
}

func (descriptorTestMatchProvider) Initialize(context.Context, InitializeInput) (Facts, error) {
	return Facts{}, nil
}

func (descriptorTestMatchProvider) OnJoin(context.Context, JoinInput) (Facts, error) {
	return Facts{}, nil
}

func descriptorRule(t *testing.T, key identity.RuleKey, facts string) []byte {
	t.Helper()
	return testRuleJSON(t, key, `{"schemaVersion":"logical-node-contract/v3","attributes":[],"facts":`+facts+`,"indexes":[]}`,
		`{"schemaVersion":"prefilter/v3","bitmap":{"resultType":"bitmap","expr":{"op":"none"}}}`,
		`{"schemaVersion":"evaluation/v3","canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}}`,
		logicalNodeConfig{})
}

func TestProviderDescriptorHandshakeUsesProviderOwnedDescribe(t *testing.T) {
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "provider-handshake", RuleID: 1}, PlacementID: "default"}
	descriptor := ProviderDescriptor{ID: "test.tick", Version: "v1", Facts: []FactSpec{{Name: "region", Type: FactTypeStrings, Scope: FactScopeTick, MaxValues: 2}}}
	provider := describedTickProvider(func(context.Context, TickFactInput) (Facts, error) { return Facts{}, nil }, descriptor)
	if _, err := NewLogicalNode(LogicalNodeSpec{Key: key, RuleJSON: descriptorRule(t, key.Rule, `[{"name":"region","type":"strings","scope":"tick","maxValues":2}]`), FactProvider: provider}); err != nil {
		t.Fatalf("NewLogicalNode: %v", err)
	}

	bad := descriptor
	bad.Facts[0].Type = FactTypeInt64
	if _, err := NewLogicalNode(LogicalNodeSpec{Key: key, RuleJSON: descriptorRule(t, key.Rule, `[{"name":"region","type":"strings","scope":"tick","maxValues":2}]`), FactProvider: describedTickProvider(func(context.Context, TickFactInput) (Facts, error) { return Facts{}, nil }, bad)}); err == nil {
		t.Fatal("NewLogicalNode accepted a Provider whose Describe type disagrees with Contract")
	} else {
		var handshakeErr *ProviderHandshakeError
		if !errors.As(err, &handshakeErr) || handshakeErr.Code != ProviderHandshakeFactTypeMismatch {
			t.Fatalf("error=%T %v, want FACT_TYPE_MISMATCH", err, err)
		}
	}
}

func TestProviderDescriptorHandshakeRejectsNonDescribingProvider(t *testing.T) {
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "provider-handshake", RuleID: 2}, PlacementID: "default"}
	_, err := NewLogicalNode(LogicalNodeSpec{
		Key: key, RuleJSON: descriptorRule(t, key.Rule, `[{"name":"value","type":"int64","scope":"tick"}]`),
		FactProvider: FactProvider(func(context.Context, TickFactInput) (Facts, error) { return Facts{}, nil }),
	})
	if err == nil {
		t.Fatal("NewLogicalNode accepted a Provider without Describe")
	}
	var handshakeErr *ProviderHandshakeError
	if !errors.As(err, &handshakeErr) || handshakeErr.Code != ProviderHandshakeDescribeFailed {
		t.Fatalf("error=%T %v, want DESCRIBE_FAILED", err, err)
	}
}

func TestProviderDescriptorHandshakeCoversObjectAndMatchScopes(t *testing.T) {
	key := identity.LogicalNodeKey{Rule: identity.RuleKey{Namespace: "provider-handshake-scopes", RuleID: 1}, PlacementID: "default"}
	ruleJSON := descriptorRule(t, key.Rule, `[
		{"name":"tick-value","type":"int64","scope":"tick"},
		{"name":"object-value","type":"strings","scope":"object","maxValues":2},
		{"name":"match-value","type":"uint64s","scope":"match","maxValues":3}
	]`)
	tick := describedTickProvider(func(context.Context, TickFactInput) (Facts, error) { return Facts{}, nil }, ProviderDescriptor{ID: "tick", Version: "v1", Facts: []FactSpec{{Name: "tick-value", Type: FactTypeInt64, Scope: FactScopeTick}}})
	object := describedObjectProvider(func(*common.Ticket, int64, Facts, ObjectFactWriter) error { return nil }, ProviderDescriptor{ID: "object", Version: "v1", Facts: []FactSpec{{Name: "object-value", Type: FactTypeStrings, Scope: FactScopeObject, MaxValues: 2}}})
	match := descriptorTestMatchProvider{descriptor: ProviderDescriptor{ID: "match", Version: "v1", Facts: []FactSpec{{Name: "match-value", Type: FactTypeUint64s, Scope: FactScopeMatch, MaxValues: 3}}}}
	if _, err := NewLogicalNode(LogicalNodeSpec{Key: key, RuleJSON: ruleJSON, FactProvider: tick, ObjectFactProvider: object, MatchFactProvider: match}); err != nil {
		t.Fatalf("NewLogicalNode with all Provider scopes: %v", err)
	}
}
