package matchsystem

import (
	"context"
	"errors"
	"strings"
	"testing"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
)

func providerHandshakeRegressionKey(namespace string) identity.LogicalNodeKey {
	return identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: namespace, RuleID: 1},
		PlacementID: "default",
	}
}

func providerHandshakeRegressionTickProvider(descriptor ProviderDescriptor) DescribedFactProvider {
	return describedTickProvider(
		func(context.Context, TickFactInput) (Facts, error) { return Facts{}, nil },
		descriptor,
	)
}

func TestProviderHandshakeRegressionMatchesContractMetadata(t *testing.T) {
	key := providerHandshakeRegressionKey("provider-handshake-regression-metadata")
	const contractFacts = `[{"name":"region","type":"strings","scope":"tick","maxValues":2}]`
	tests := []struct {
		name       string
		descriptor ProviderDescriptor
		provider   TickProvider
		wantCode   string
	}{
		{
			name: "missing Contract Fact",
			descriptor: ProviderDescriptor{
				ID: "test.tick", Version: "v1",
			},
			wantCode: ProviderHandshakeMissingFact,
		},
		{
			name: "Fact name mismatch",
			descriptor: ProviderDescriptor{
				ID: "test.tick", Version: "v1",
				Facts: []FactSpec{{Name: "other", Type: FactTypeStrings, Scope: FactScopeTick, MaxValues: 2}},
			},
			wantCode: ProviderHandshakeMissingFact,
		},
		{
			name: "Fact type mismatch",
			descriptor: ProviderDescriptor{
				ID: "test.tick", Version: "v1",
				Facts: []FactSpec{{Name: "region", Type: FactTypeInt64, Scope: FactScopeTick}},
			},
			wantCode: ProviderHandshakeFactTypeMismatch,
		},
		{
			name: "Fact scope mismatch",
			descriptor: ProviderDescriptor{
				ID: "test.tick", Version: "v1",
				Facts: []FactSpec{{Name: "region", Type: FactTypeStrings, Scope: FactScopeObject, MaxValues: 2}},
			},
			wantCode: ProviderHandshakeFactScopeMismatch,
		},
		{
			name: "Fact maxValues mismatch",
			descriptor: ProviderDescriptor{
				ID: "test.tick", Version: "v1",
				Facts: []FactSpec{{Name: "region", Type: FactTypeStrings, Scope: FactScopeTick, MaxValues: 3}},
			},
			wantCode: ProviderHandshakeFactMaxValuesMismatch,
		},
		{
			name:     "missing provider",
			provider: nil,
			wantCode: ProviderHandshakeMissingProvider,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := test.provider
			if provider == nil && test.descriptor.ID != "" {
				provider = providerHandshakeRegressionTickProvider(test.descriptor)
			}
			_, err := NewLogicalNode(LogicalNodeSpec{
				Key:          key,
				RuleJSON:     descriptorRule(t, key.Rule, contractFacts),
				FactProvider: provider,
			})
			if err == nil {
				t.Fatalf("NewLogicalNode accepted invalid %s handshake", test.name)
			}
			var handshakeErr *ProviderHandshakeError
			if !errors.As(err, &handshakeErr) {
				t.Fatalf("error=%T %v, want ProviderHandshakeError", err, err)
			}
			if handshakeErr.Code != test.wantCode {
				t.Fatalf("error code=%q, want %q (%v)", handshakeErr.Code, test.wantCode, err)
			}
		})
	}
}

func TestProviderHandshakeRegressionAcceptsProviderSuperset(t *testing.T) {
	key := providerHandshakeRegressionKey("provider-handshake-regression-superset")
	descriptor := ProviderDescriptor{
		ID:      "test.superset",
		Version: "v1",
		Facts: []FactSpec{
			{Name: "region", Type: FactTypeStrings, Scope: FactScopeTick, MaxValues: 2},
			{Name: "diagnostic", Type: FactTypeInt64, Scope: FactScopeTick},
		},
	}
	if _, err := NewLogicalNode(LogicalNodeSpec{
		Key:          key,
		RuleJSON:     descriptorRule(t, key.Rule, `[{"name":"region","type":"strings","scope":"tick","maxValues":2}]`),
		FactProvider: providerHandshakeRegressionTickProvider(descriptor),
	}); err != nil {
		t.Fatalf("NewLogicalNode rejected a valid Provider superset: %v", err)
	}
}

func TestProviderHandshakeRegressionRejectsInvalidExtraFacts(t *testing.T) {
	key := providerHandshakeRegressionKey("provider-handshake-regression-extra")
	tests := []struct {
		name     string
		facts    []FactSpec
		wantCode string
	}{
		{
			name:     "empty Fact name",
			facts:    []FactSpec{{Type: FactTypeInt64, Scope: FactScopeTick}},
			wantCode: ProviderHandshakeInvalidDescriptor,
		},
		{
			name: "duplicate Fact name",
			facts: []FactSpec{
				{Name: "duplicate", Type: FactTypeInt64, Scope: FactScopeTick},
				{Name: "duplicate", Type: FactTypeInt64, Scope: FactScopeTick},
			},
			wantCode: ProviderHandshakeDuplicateFact,
		},
		{
			name:     "extra Fact has wrong scope",
			facts:    []FactSpec{{Name: "unexpected", Type: FactTypeInt64, Scope: FactScopeObject}},
			wantCode: ProviderHandshakeFactScopeMismatch,
		},
		{
			name:     "extra Fact has invalid type",
			facts:    []FactSpec{{Name: "unexpected", Type: FactType(99), Scope: FactScopeTick}},
			wantCode: ProviderHandshakeInvalidDescriptor,
		},
		{
			name:     "extra int64 Fact has maxValues",
			facts:    []FactSpec{{Name: "unexpected", Type: FactTypeInt64, Scope: FactScopeTick, MaxValues: 1}},
			wantCode: ProviderHandshakeInvalidDescriptor,
		},
		{
			name:     "extra strings Fact has no maxValues",
			facts:    []FactSpec{{Name: "unexpected", Type: FactTypeStrings, Scope: FactScopeTick}},
			wantCode: ProviderHandshakeInvalidDescriptor,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			descriptor := ProviderDescriptor{ID: "test.extra", Version: "v1", Facts: test.facts}
			_, err := NewLogicalNode(LogicalNodeSpec{
				Key:          key,
				RuleJSON:     descriptorRule(t, key.Rule, `[]`),
				FactProvider: providerHandshakeRegressionTickProvider(descriptor),
			})
			if err == nil {
				t.Fatal("NewLogicalNode accepted invalid provider-only Fact metadata")
			}
			var handshakeErr *ProviderHandshakeError
			if !errors.As(err, &handshakeErr) || handshakeErr.Code != test.wantCode {
				t.Fatalf("error=%T %v, want ProviderHandshakeError code %q", err, err, test.wantCode)
			}
		})
	}
}

func TestProviderHandshakeRegressionRejectsEmptyIDAndVersion(t *testing.T) {
	key := providerHandshakeRegressionKey("provider-handshake-regression-identity")
	tests := []struct {
		name       string
		descriptor ProviderDescriptor
	}{
		{
			name: "empty ID",
			descriptor: ProviderDescriptor{
				Version: "v1",
				Facts:   []FactSpec{{Name: "value", Type: FactTypeInt64, Scope: FactScopeTick}},
			},
		},
		{
			name: "empty version",
			descriptor: ProviderDescriptor{
				ID:    "test.identity",
				Facts: []FactSpec{{Name: "value", Type: FactTypeInt64, Scope: FactScopeTick}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewLogicalNode(LogicalNodeSpec{
				Key:          key,
				RuleJSON:     descriptorRule(t, key.Rule, `[{"name":"value","type":"int64","scope":"tick"}]`),
				FactProvider: providerHandshakeRegressionTickProvider(test.descriptor),
			})
			if err == nil {
				t.Fatal("NewLogicalNode accepted incomplete Provider identity")
			}
			var handshakeErr *ProviderHandshakeError
			if !errors.As(err, &handshakeErr) || handshakeErr.Code != ProviderHandshakeInvalidDescriptor {
				t.Fatalf("error=%T %v, want INVALID_DESCRIPTOR", err, err)
			}
		})
	}
}

type providerHandshakeRegressionDescribeError struct{}

func (providerHandshakeRegressionDescribeError) Provide(context.Context, TickFactInput) (Facts, error) {
	return Facts{}, nil
}

func (providerHandshakeRegressionDescribeError) Describe() (ProviderDescriptor, error) {
	return ProviderDescriptor{}, errors.New("describe regression failure")
}

func TestProviderHandshakeRegressionPropagatesDescribeError(t *testing.T) {
	key := providerHandshakeRegressionKey("provider-handshake-regression-describe-error")
	_, err := NewLogicalNode(LogicalNodeSpec{
		Key:          key,
		RuleJSON:     descriptorRule(t, key.Rule, `[{"name":"value","type":"int64","scope":"tick"}]`),
		FactProvider: providerHandshakeRegressionDescribeError{},
	})
	if err == nil {
		t.Fatal("NewLogicalNode accepted a Provider whose Describe failed")
	}
	var handshakeErr *ProviderHandshakeError
	if !errors.As(err, &handshakeErr) || handshakeErr.Code != ProviderHandshakeDescribeFailed {
		t.Fatalf("error=%T %v, want DESCRIBE_FAILED", err, err)
	}
	if handshakeErr.Err == nil || !strings.Contains(handshakeErr.Err.Error(), "describe regression failure") {
		t.Fatalf("structured Describe error=%#v", handshakeErr)
	}
}

type providerHandshakeRegressionTypedNilTick struct{}

func (*providerHandshakeRegressionTypedNilTick) Provide(context.Context, TickFactInput) (Facts, error) {
	return Facts{}, nil
}

func (*providerHandshakeRegressionTypedNilTick) Describe() (ProviderDescriptor, error) {
	return ProviderDescriptor{ID: "typed-nil", Version: "v1"}, nil
}

func TestProviderHandshakeRegressionRejectsTypedNilProvider(t *testing.T) {
	key := providerHandshakeRegressionKey("provider-handshake-regression-typed-nil")
	var provider *providerHandshakeRegressionTypedNilTick
	_, err := NewLogicalNode(LogicalNodeSpec{
		Key:          key,
		RuleJSON:     descriptorRule(t, key.Rule, `[{"name":"value","type":"int64","scope":"tick"}]`),
		FactProvider: provider,
	})
	if err == nil {
		t.Fatal("NewLogicalNode accepted a typed nil Provider")
	}
	var handshakeErr *ProviderHandshakeError
	if !errors.As(err, &handshakeErr) || handshakeErr.Code != ProviderHandshakeMissingProvider {
		t.Fatalf("error=%T %v, want MISSING_PROVIDER", err, err)
	}
}

func TestProviderHandshakeRegressionStructuredMismatchError(t *testing.T) {
	key := providerHandshakeRegressionKey("provider-handshake-regression-error-details")
	provider := providerHandshakeRegressionTickProvider(ProviderDescriptor{
		ID:      "test.structured",
		Version: "v9",
		Facts:   []FactSpec{{Name: "region", Type: FactTypeInt64, Scope: FactScopeTick}},
	})
	_, err := NewLogicalNode(LogicalNodeSpec{
		Key:          key,
		RuleJSON:     descriptorRule(t, key.Rule, `[{"name":"region","type":"strings","scope":"tick","maxValues":2}]`),
		FactProvider: provider,
	})
	if err == nil {
		t.Fatal("NewLogicalNode accepted a mismatched Provider descriptor")
	}
	var handshakeErr *ProviderHandshakeError
	if !errors.As(err, &handshakeErr) {
		t.Fatalf("error=%T %v, want ProviderHandshakeError", err, err)
	}
	if handshakeErr.Provider != "tick" || handshakeErr.Scope != FactScopeTick ||
		handshakeErr.Code != ProviderHandshakeFactTypeMismatch || handshakeErr.Name != "region" {
		t.Fatalf("unexpected handshake error metadata: %#v", handshakeErr)
	}
	if handshakeErr.ProviderID != "test.structured" || handshakeErr.ProviderVersion != "v9" {
		t.Fatalf("unexpected Provider identity: %#v", handshakeErr)
	}
	if handshakeErr.Expected == nil || handshakeErr.Expected.Type != FactTypeStrings || handshakeErr.Expected.MaxValues != 2 {
		t.Fatalf("unexpected expected Fact: %#v", handshakeErr.Expected)
	}
	if handshakeErr.Actual == nil || handshakeErr.Actual.Type != FactTypeInt64 || handshakeErr.Actual.Scope != FactScopeTick {
		t.Fatalf("unexpected actual Fact: %#v", handshakeErr.Actual)
	}
	message := handshakeErr.Error()
	for _, fragment := range []string{"tick provider", "test.structured", "v9", ProviderHandshakeFactTypeMismatch, `Fact "region"`} {
		if !strings.Contains(message, fragment) {
			t.Fatalf("error message %q lacks %q", message, fragment)
		}
	}
}

func TestProviderHandshakeRegressionClonesCapturedDescriptors(t *testing.T) {
	key := providerHandshakeRegressionKey("provider-handshake-regression-clone")
	tickDescriptor := ProviderDescriptor{
		ID: "test.tick.clone", Version: "v1",
		Facts: []FactSpec{{Name: "tick-value", Type: FactTypeInt64, Scope: FactScopeTick}},
	}
	objectDescriptor := ProviderDescriptor{
		ID: "test.object.clone", Version: "v1",
		Facts: []FactSpec{{Name: "object-value", Type: FactTypeStrings, Scope: FactScopeObject, MaxValues: 2}},
	}
	matchDescriptor := ProviderDescriptor{
		ID: "test.match.clone", Version: "v1",
		Facts: []FactSpec{{Name: "match-value", Type: FactTypeUint64s, Scope: FactScopeMatch, MaxValues: 3}},
	}
	node, err := NewLogicalNode(LogicalNodeSpec{
		Key: key,
		RuleJSON: descriptorRule(t, key.Rule, `[
			{"name":"tick-value","type":"int64","scope":"tick"},
			{"name":"object-value","type":"strings","scope":"object","maxValues":2},
			{"name":"match-value","type":"uint64s","scope":"match","maxValues":3}
		]`),
		FactProvider:       providerHandshakeRegressionTickProvider(tickDescriptor),
		ObjectFactProvider: describedObjectProvider(func(*common.Ticket, int64, Facts, ObjectFactWriter) error { return nil }, objectDescriptor),
		MatchFactProvider:  descriptorTestMatchProvider{descriptor: matchDescriptor},
	})
	if err != nil {
		t.Fatalf("NewLogicalNode: %v", err)
	}

	first := node.ProviderDescriptors()
	if first.Tick == nil || first.Object == nil || first.Match == nil {
		t.Fatalf("captured Provider descriptors=%#v", first)
	}
	first.Tick.ID = "mutated.tick"
	first.Tick.Facts[0].Name = "mutated-tick"
	first.Object.Version = "mutated-object"
	first.Object.Facts[0].MaxValues = 99
	first.Match.Facts[0].Name = "mutated-match"

	second := node.ProviderDescriptors()
	if second.Tick.ID != tickDescriptor.ID || second.Tick.Facts[0].Name != "tick-value" {
		t.Fatalf("Tick descriptor leaked through ProviderDescriptors clone: %#v", second.Tick)
	}
	if second.Object.Version != objectDescriptor.Version || second.Object.Facts[0].MaxValues != 2 {
		t.Fatalf("Object descriptor leaked through ProviderDescriptors clone: %#v", second.Object)
	}
	if second.Match.Facts[0].Name != "match-value" {
		t.Fatalf("Match descriptor leaked through ProviderDescriptors clone: %#v", second.Match)
	}

	tickDescriptor.ID = "mutated-source"
	tickDescriptor.Facts[0].Name = "mutated-source-fact"
	if captured := node.ProviderDescriptors(); captured.Tick.ID != "test.tick.clone" || captured.Tick.Facts[0].Name != "tick-value" {
		t.Fatalf("Provider descriptor source was retained by the node: %#v", captured.Tick)
	}
}
