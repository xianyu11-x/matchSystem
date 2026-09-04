package matchsystem

import (
	"context"
	"errors"
	"testing"

	"matchSystem/internal/identity"
)

func TestProviderDescriptorHandshake(t *testing.T) {
	key := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: "provider-handshake", RuleID: 1},
		PlacementID: "default",
	}
	contract := `{
		"schemaVersion":"logical-node-contract/v3",
		"attributes":[],
		"facts":[{"name":"region","type":"strings","scope":"tick","maxValues":2}],
		"indexes":[]
	}`
	ruleJSON := testRuleJSON(t, key.Rule, contract, `{
		"schemaVersion":"prefilter/v3",
		"bitmap":{"resultType":"bitmap","expr":{"op":"none"}}
	}`, `{
		"schemaVersion":"evaluation/v3",
		"canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
		"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}
	}`, logicalNodeConfig{})

	validProvider := FactProvider(func(context.Context, TickFactInput) (Facts, error) {
		return Facts{}, nil
	})
	validDescriptor := func(specs ...FactSpec) *ProviderDescriptor {
		return &ProviderDescriptor{ID: "test.tick", Version: "v1", Facts: specs}
	}

	tests := []struct {
		name       string
		provider   FactProvider
		descriptor *ProviderDescriptor
		wantCode   string
	}{
		{
			name:       "success",
			provider:   validProvider,
			descriptor: validDescriptor(FactSpec{Name: "region", Type: FactTypeStrings, Scope: FactScopeTick, MaxValues: 2}),
		},
		{
			name:     "missing descriptor",
			provider: validProvider,
			wantCode: ProviderHandshakeMissingDescriptor,
		},
		{
			name:       "missing field",
			provider:   validProvider,
			descriptor: validDescriptor(),
			wantCode:   ProviderHandshakeMissingFact,
		},
		{
			name:     "provider may advertise extra Facts",
			provider: validProvider,
			descriptor: validDescriptor(
				FactSpec{Name: "region", Type: FactTypeStrings, Scope: FactScopeTick, MaxValues: 2},
				FactSpec{Name: "extra", Type: FactTypeInt64, Scope: FactScopeTick},
			),
		},
		{
			name:     "type mismatch",
			provider: validProvider,
			descriptor: validDescriptor(FactSpec{
				Name: "region", Type: FactTypeInt64, Scope: FactScopeTick,
			}),
			wantCode: ProviderHandshakeFactTypeMismatch,
		},
		{
			name:     "scope mismatch",
			provider: validProvider,
			descriptor: validDescriptor(FactSpec{
				Name: "region", Type: FactTypeStrings, Scope: FactScopeObject, MaxValues: 2,
			}),
			wantCode: ProviderHandshakeFactScopeMismatch,
		},
		{
			name:     "maxValues mismatch",
			provider: validProvider,
			descriptor: validDescriptor(FactSpec{
				Name: "region", Type: FactTypeStrings, Scope: FactScopeTick, MaxValues: 3,
			}),
			wantCode: ProviderHandshakeFactMaxValuesMismatch,
		},
		{
			name:       "provider nil",
			descriptor: validDescriptor(FactSpec{Name: "region", Type: FactTypeStrings, Scope: FactScopeTick, MaxValues: 2}),
			wantCode:   ProviderHandshakeMissingProvider,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewLogicalNode(LogicalNodeSpec{
				Key:                    key,
				RuleJSON:               ruleJSON,
				FactProvider:           test.provider,
				FactProviderDescriptor: test.descriptor,
			})
			if test.wantCode == "" {
				if err != nil {
					t.Fatalf("NewLogicalNode: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("NewLogicalNode accepted invalid provider handshake")
			}
			var handshakeErr *ProviderHandshakeError
			if !errors.As(err, &handshakeErr) {
				t.Fatalf("error is not ProviderHandshakeError: %T %v", err, err)
			}
			if handshakeErr.Code != test.wantCode {
				t.Fatalf("error code: got %q, want %q (%v)", handshakeErr.Code, test.wantCode, err)
			}
		})
	}
}

type descriptorTestMatchProvider struct{}

func (descriptorTestMatchProvider) Initialize(context.Context, InitializeInput) (Facts, error) {
	return Facts{}, nil
}

func (descriptorTestMatchProvider) OnJoin(context.Context, JoinInput) (Facts, error) {
	return Facts{}, nil
}

func TestProviderDescriptorHandshakeCoversObjectAndMatchScopes(t *testing.T) {
	key := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: "provider-handshake-scopes", RuleID: 1},
		PlacementID: "default",
	}
	ruleJSON := testRuleJSON(t, key.Rule, `{
		"schemaVersion":"logical-node-contract/v3",
		"attributes":[],
		"facts":[
			{"name":"tick-value","type":"int64","scope":"tick"},
			{"name":"object-value","type":"strings","scope":"object","maxValues":2},
			{"name":"match-value","type":"uint64s","scope":"match","maxValues":3}
		],
		"indexes":[]
	}`, `{
		"schemaVersion":"prefilter/v3",
		"bitmap":{"resultType":"bitmap","expr":{"op":"none"}}
	}`, `{
		"schemaVersion":"evaluation/v3",
		"canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
		"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}
	}`, logicalNodeConfig{})

	tickDescriptor := &ProviderDescriptor{
		ID:      "test.scopes.tick",
		Version: "v1",
		Facts:   []FactSpec{{Name: "tick-value", Type: FactTypeInt64, Scope: FactScopeTick}},
	}
	objectDescriptor := &ProviderDescriptor{
		ID:      "test.scopes.object",
		Version: "v1",
		Facts:   []FactSpec{{Name: "object-value", Type: FactTypeStrings, Scope: FactScopeObject, MaxValues: 2}},
	}
	matchDescriptor := &ProviderDescriptor{
		ID:      "test.scopes.match",
		Version: "v1",
		Facts:   []FactSpec{{Name: "match-value", Type: FactTypeUint64s, Scope: FactScopeMatch, MaxValues: 3}},
	}
	base := LogicalNodeSpec{
		Key:                          key,
		RuleJSON:                     ruleJSON,
		FactProvider:                 func(context.Context, TickFactInput) (Facts, error) { return Facts{}, nil },
		FactProviderDescriptor:       tickDescriptor,
		ObjectFactProvider:           func(*Ticket, int64, Facts, ObjectFactWriter) error { return nil },
		ObjectFactProviderDescriptor: objectDescriptor,
		MatchFactProvider:            descriptorTestMatchProvider{},
		MatchFactProviderDescriptor:  matchDescriptor,
	}
	if _, err := NewLogicalNode(base); err != nil {
		t.Fatalf("NewLogicalNode with all provider scopes: %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*LogicalNodeSpec)
		want   string
	}{
		{
			name: "missing object descriptor",
			mutate: func(spec *LogicalNodeSpec) {
				spec.ObjectFactProviderDescriptor = nil
			},
			want: ProviderHandshakeMissingDescriptor,
		},
		{
			name: "missing match descriptor",
			mutate: func(spec *LogicalNodeSpec) {
				spec.MatchFactProviderDescriptor = nil
			},
			want: ProviderHandshakeMissingDescriptor,
		},
		{
			name: "typed nil match provider",
			mutate: func(spec *LogicalNodeSpec) {
				var provider *descriptorTestMatchProvider
				spec.MatchFactProvider = provider
			},
			want: ProviderHandshakeMissingProvider,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := base
			test.mutate(&spec)
			_, err := NewLogicalNode(spec)
			if err == nil {
				t.Fatal("NewLogicalNode accepted a missing scope descriptor")
			}
			var handshakeErr *ProviderHandshakeError
			if !errors.As(err, &handshakeErr) || handshakeErr.Code != test.want {
				t.Fatalf("error: got %T %v, want ProviderHandshakeError code %q", err, err, test.want)
			}
		})
	}
}

func TestProviderDescriptorHandshakeRejectsEmptyIDAndVersion(t *testing.T) {
	key := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: "provider-handshake-metadata", RuleID: 1},
		PlacementID: "default",
	}
	ruleJSON := testRuleJSON(t, key.Rule, `{
		"schemaVersion":"logical-node-contract/v3",
		"attributes":[],
		"facts":[{"name":"value","type":"int64","scope":"tick"}],
		"indexes":[]
	}`, `{
		"schemaVersion":"prefilter/v3",
		"bitmap":{"resultType":"bitmap","expr":{"op":"none"}}
	}`, `{
		"schemaVersion":"evaluation/v3",
		"canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
		"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}
	}`, logicalNodeConfig{})
	provider := FactProvider(func(context.Context, TickFactInput) (Facts, error) { return Facts{}, nil })
	for _, test := range []struct {
		name string
		desc ProviderDescriptor
	}{
		{
			name: "empty ID",
			desc: ProviderDescriptor{Version: "v1", Facts: []FactSpec{{Name: "value", Type: FactTypeInt64, Scope: FactScopeTick}}},
		},
		{
			name: "empty version",
			desc: ProviderDescriptor{ID: "test.metadata", Facts: []FactSpec{{Name: "value", Type: FactTypeInt64, Scope: FactScopeTick}}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewLogicalNode(LogicalNodeSpec{
				Key:                    key,
				RuleJSON:               ruleJSON,
				FactProvider:           provider,
				FactProviderDescriptor: &test.desc,
			})
			if err == nil {
				t.Fatal("NewLogicalNode accepted an incomplete provider descriptor")
			}
			var handshakeErr *ProviderHandshakeError
			if !errors.As(err, &handshakeErr) || handshakeErr.Code != ProviderHandshakeInvalidDescriptor {
				t.Fatalf("error: got %T %v, want INVALID_DESCRIPTOR", err, err)
			}
		})
	}
}

func TestProviderDescriptorHandshakeAllowsUndeclaredScope(t *testing.T) {
	key := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: "provider-handshake-empty", RuleID: 1},
		PlacementID: "default",
	}
	ruleJSON := testRuleJSON(t, key.Rule, `{
		"schemaVersion":"logical-node-contract/v3",
		"attributes":[],"facts":[],"indexes":[]
	}`, `{
		"schemaVersion":"prefilter/v3",
		"bitmap":{"resultType":"bitmap","expr":{"op":"none"}}
	}`, `{
		"schemaVersion":"evaluation/v3",
		"canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}},
		"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}
	}`, logicalNodeConfig{})
	provider := FactProvider(func(context.Context, TickFactInput) (Facts, error) {
		return Facts{}, nil
	})
	_, err := NewLogicalNode(LogicalNodeSpec{
		Key:          key,
		RuleJSON:     ruleJSON,
		FactProvider: provider,
	})
	if err != nil {
		t.Fatalf("NewLogicalNode rejected a provider for an undeclared scope: %v", err)
	}
	if _, err := NewLogicalNode(LogicalNodeSpec{
		Key:                    key,
		RuleJSON:               ruleJSON,
		FactProvider:           provider,
		FactProviderDescriptor: &ProviderDescriptor{ID: "test.empty", Version: "v1"},
	}); err != nil {
		t.Fatalf("NewLogicalNode rejected an empty descriptor for an undeclared scope: %v", err)
	}
	_, err = NewLogicalNode(LogicalNodeSpec{
		Key:      key,
		RuleJSON: ruleJSON,
		FactProviderDescriptor: &ProviderDescriptor{
			ID:      "test.empty-extra",
			Version: "v1",
			Facts:   []FactSpec{{Name: "unexpected", Type: FactTypeInt64, Scope: FactScopeTick}},
		},
	})
	if err != nil {
		t.Fatalf("NewLogicalNode rejected provider-only Facts for an undeclared scope: %v", err)
	}

	for _, test := range []struct {
		name  string
		facts []FactSpec
		want  string
	}{
		{
			name:  "empty Fact name",
			facts: []FactSpec{{Type: FactTypeInt64, Scope: FactScopeTick}},
			want:  ProviderHandshakeInvalidDescriptor,
		},
		{
			name: "duplicate Fact name",
			facts: []FactSpec{
				{Name: "duplicate", Type: FactTypeInt64, Scope: FactScopeTick},
				{Name: "duplicate", Type: FactTypeInt64, Scope: FactScopeTick},
			},
			want: ProviderHandshakeDuplicateFact,
		},
		{
			name:  "extra Fact has wrong scope",
			facts: []FactSpec{{Name: "unexpected", Type: FactTypeInt64, Scope: FactScopeObject}},
			want:  ProviderHandshakeFactScopeMismatch,
		},
		{
			name:  "extra Fact has invalid type",
			facts: []FactSpec{{Name: "unexpected", Type: FactType(99), Scope: FactScopeTick}},
			want:  ProviderHandshakeInvalidDescriptor,
		},
		{
			name:  "extra int64 Fact has maxValues",
			facts: []FactSpec{{Name: "unexpected", Type: FactTypeInt64, Scope: FactScopeTick, MaxValues: 1}},
			want:  ProviderHandshakeInvalidDescriptor,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewLogicalNode(LogicalNodeSpec{
				Key:          key,
				RuleJSON:     ruleJSON,
				FactProvider: provider,
				FactProviderDescriptor: &ProviderDescriptor{
					ID: "test.empty-extra-invalid", Version: "v1", Facts: test.facts,
				},
			})
			if err == nil {
				t.Fatal("NewLogicalNode accepted an invalid provider-only Fact declaration")
			}
			var handshakeErr *ProviderHandshakeError
			if !errors.As(err, &handshakeErr) || handshakeErr.Code != test.want {
				t.Fatalf("error: got %T %v, want ProviderHandshakeError code %q", err, err, test.want)
			}
		})
	}
}
