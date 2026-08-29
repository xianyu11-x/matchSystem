// Package simulator hosts the in-process simulator application around the
// matchsystem core. It owns scenario configuration, node orchestration,
// observations, deterministic data generation, and query-friendly snapshots.
package simulator

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"matchSystem/internal/common"
	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem"
)

const (
	ScenarioSchemaVersion   = "simulator-scenario/v1"
	ContractSchemaVersion   = "logical-node-contract/v3"
	ExpressionSchemaVersion = "expression-scalar/v3"
	PrefilterSchemaVersion  = "prefilter/v3"
	EvaluationSchemaVersion = "evaluation/v3"
)

// SelectorKind identifies a built-in PhysicalNode logical-node selector.
type SelectorKind string

const (
	SelectorRoundRobin    SelectorKind = "round_robin"
	SelectorLargestQueue  SelectorKind = "largest_queue"
	SelectorOldestWaiting SelectorKind = "oldest_waiting"
	SelectorWeighted      SelectorKind = "smooth_weighted_round_robin"
)

// PhysicalNodeSpec is the JSON-friendly topology declaration for one
// simulator PhysicalNode. Selector is optional and defaults to round_robin.
type PhysicalNodeSpec struct {
	ID       identity.PhysicalNodeID `json:"id"`
	Endpoint common.Endpoint         `json:"endpoint"`
	Enabled  bool                    `json:"enabled"`
	Selector SelectorKind            `json:"selector,omitempty"`
}

// NewPhysicalNodeSpec creates an enabled in-process node with the default
// round-robin selector.
func NewPhysicalNodeSpec(id identity.PhysicalNodeID, endpoint common.Endpoint) PhysicalNodeSpec {
	return PhysicalNodeSpec{ID: id, Endpoint: endpoint, Enabled: true, Selector: SelectorRoundRobin}
}

// RuleSpec joins a LogicalNode definition to one PhysicalNode and its route.
// Runtime callbacks are deliberately excluded from JSON; an HTTP host can
// register them in Go while the four JSON documents remain wire data.
type RuleSpec struct {
	LogicalNode    identity.LogicalNodeKey       `json:"logicalNode"`
	PhysicalNodeID identity.PhysicalNodeID       `json:"physicalNodeId"`
	Weight         uint32                        `json:"weight"`
	Enabled        bool                          `json:"enabled"`
	ContractJSON   json.RawMessage               `json:"contract"`
	PrefilterJSON  json.RawMessage               `json:"prefilter"`
	EvaluationJSON json.RawMessage               `json:"evaluation"`
	Config         matchsystem.LogicalNodeConfig `json:"config"`
	// TickFacts is the static Tick-scoped layer used when FactProvider is not
	// supplied by the host. It is copied into every matching attempt.
	TickFacts FactSnapshot `json:"tickFacts,omitempty"`

	CandidateScorer    matchsystem.CandidateScorer    `json:"-"`
	FactProvider       matchsystem.FactProvider       `json:"-"`
	ObjectFactProvider matchsystem.ObjectFactProvider `json:"-"`
	MatchFactProvider  matchsystem.MatchFactProvider  `json:"-"`
	SeedOrderPolicy    matchsystem.SeedOrderPolicy    `json:"-"`
}

// NewRuleSpec creates a JSON-backed rule definition with the normal route
// defaults. Runtime callbacks may be assigned after construction.
func NewRuleSpec(key identity.LogicalNodeKey, physical identity.PhysicalNodeID, contractJSON, prefilterJSON, evaluationJSON []byte) RuleSpec {
	return RuleSpec{
		LogicalNode:    key,
		PhysicalNodeID: physical,
		Weight:         1,
		Enabled:        true,
		ContractJSON:   append(json.RawMessage(nil), contractJSON...),
		PrefilterJSON:  append(json.RawMessage(nil), prefilterJSON...),
		EvaluationJSON: append(json.RawMessage(nil), evaluationJSON...),
	}
}

// Scenario is the complete simulator topology and logical-node definition.
// A successful replacement builds a complete new runtime before swapping it
// into service.
type Scenario struct {
	SchemaVersion string             `json:"schemaVersion"`
	PhysicalNodes []PhysicalNodeSpec `json:"physicalNodes"`
	Rules         []RuleSpec         `json:"rules"`
}

// Clone returns an independent copy of scenario JSON and slices. Callback
// values are function/interface references and intentionally remain bound to
// their caller-owned implementations.
func (s Scenario) Clone() Scenario {
	out := Scenario{
		SchemaVersion: s.SchemaVersion,
		PhysicalNodes: append([]PhysicalNodeSpec(nil), s.PhysicalNodes...),
		Rules:         make([]RuleSpec, len(s.Rules)),
	}
	for i, rule := range s.Rules {
		out.Rules[i] = rule
		out.Rules[i].ContractJSON = append(json.RawMessage(nil), rule.ContractJSON...)
		out.Rules[i].PrefilterJSON = append(json.RawMessage(nil), rule.PrefilterJSON...)
		out.Rules[i].EvaluationJSON = append(json.RawMessage(nil), rule.EvaluationJSON...)
		out.Rules[i].TickFacts = rule.TickFacts.clone()
	}
	return out
}

// FactSnapshot is the JSON-friendly representation of one typed Fact layer.
type FactSnapshot struct {
	StringLists map[string][]string `json:"strings,omitempty"`
	Uint64Lists map[string][]uint64 `json:"uint64s,omitempty"`
	Int64Values map[string]int64    `json:"int64s,omitempty"`
}

func (f FactSnapshot) clone() FactSnapshot {
	out := FactSnapshot{
		StringLists: make(map[string][]string, len(f.StringLists)),
		Uint64Lists: make(map[string][]uint64, len(f.Uint64Lists)),
		Int64Values: make(map[string]int64, len(f.Int64Values)),
	}
	for name, values := range f.StringLists {
		out.StringLists[name] = append([]string(nil), values...)
	}
	for name, values := range f.Uint64Lists {
		out.Uint64Lists[name] = append([]uint64(nil), values...)
	}
	for name, value := range f.Int64Values {
		out.Int64Values[name] = value
	}
	return out
}

func (f FactSnapshot) values() matchsystem.Facts {
	return matchsystem.Facts{
		StringLists: cloneStringLists(f.StringLists),
		Uint64Lists: cloneUint64Lists(f.Uint64Lists),
		Int64Values: cloneInt64Values(f.Int64Values),
	}
}

func factSnapshot(values matchsystem.Facts) FactSnapshot {
	return FactSnapshot{
		StringLists: cloneStringLists(values.StringLists),
		Uint64Lists: cloneUint64Lists(values.Uint64Lists),
		Int64Values: cloneInt64Values(values.Int64Values),
	}
}

func cloneStringLists(values map[string][]string) map[string][]string {
	out := make(map[string][]string, len(values))
	for name, list := range values {
		out[name] = append([]string(nil), list...)
	}
	return out
}

func cloneUint64Lists(values map[string][]uint64) map[string][]uint64 {
	out := make(map[string][]uint64, len(values))
	for name, list := range values {
		out[name] = append([]uint64(nil), list...)
	}
	return out
}

func cloneInt64Values(values map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(values))
	for name, value := range values {
		out[name] = value
	}
	return out
}

// TicketInput is the application-level Add Ticket request. ObjectFacts are
// kept outside common.Ticket because they are observations, not indexed
// attributes.
type TicketInput struct {
	Rule        identity.RuleKey    `json:"rule"`
	TicketID    common.TicketID     `json:"ticketId"`
	AffinityKey string              `json:"affinityKey,omitempty"`
	RequestID   string              `json:"requestId,omitempty"`
	CreatedAt   int64               `json:"createdAt"`
	StringLists map[string][]string `json:"strings,omitempty"`
	Uint64Lists map[string][]uint64 `json:"uint64s,omitempty"`
	Int64Values map[string]int64    `json:"int64s,omitempty"`
	ObjectFacts FactSnapshot        `json:"objectFacts,omitempty"`
}

// AddTicketRequest is a descriptive alias for HTTP/application callers.
type AddTicketRequest = TicketInput

func (in TicketInput) ticket() *common.Ticket {
	return &common.Ticket{
		TicketID:    in.TicketID,
		CreatedAt:   in.CreatedAt,
		StringLists: cloneStringLists(in.StringLists),
		Uint64Lists: cloneUint64Lists(in.Uint64Lists),
		Int64Values: cloneInt64Values(in.Int64Values),
	}
}

func (in TicketInput) routeRequest() common.RouteRequest {
	return common.RouteRequest{
		Rule:        in.Rule,
		TicketID:    in.TicketID,
		AffinityKey: in.AffinityKey,
		RequestID:   in.RequestID,
	}
}

// TicketStatus is the simulator observation state of one Ticket.
type TicketStatus string

const (
	TicketWaiting TicketStatus = "waiting"
	TicketMatched TicketStatus = "matched"
	TicketRemoved TicketStatus = "removed"
)

// TicketView is a detached, query-safe Ticket observation. It never exposes
// the core's private DocID or borrowed pointers.
type TicketView struct {
	Rule        identity.RuleKey     `json:"rule"`
	TicketID    common.TicketID      `json:"ticketId"`
	CreatedAt   int64                `json:"createdAt"`
	StringLists map[string][]string  `json:"strings,omitempty"`
	Uint64Lists map[string][]uint64  `json:"uint64s,omitempty"`
	Int64Values map[string]int64     `json:"int64s,omitempty"`
	ObjectFacts FactSnapshot         `json:"objectFacts,omitempty"`
	Owner       identity.OwnerRef    `json:"owner"`
	Decision    common.RouteDecision `json:"decision"`
	Status      TicketStatus         `json:"status"`
}

// MatchRecord is an immutable simulator match event and query result.
type MatchRecord struct {
	ID             string                  `json:"id"`
	Round          uint64                  `json:"round"`
	Now            int64                   `json:"now"`
	PhysicalNodeID identity.PhysicalNodeID `json:"physicalNodeId"`
	LogicalNode    identity.LogicalNodeKey `json:"logicalNode"`
	Tickets        []TicketView            `json:"tickets"`
	Facts          FactSnapshot            `json:"facts"`
}

// NodeDescriptor is a query-safe PhysicalNode LogicalNode snapshot.
type NodeDescriptor struct {
	PhysicalNodeID identity.PhysicalNodeID `json:"physicalNodeId"`
	Key            identity.LogicalNodeKey `json:"key"`
	State          string                  `json:"state"`
	TicketCount    int                     `json:"ticketCount"`
}

// PhysicalNodeView is the topology representation returned to an HTTP layer.
type PhysicalNodeView struct {
	ID           identity.PhysicalNodeID `json:"id"`
	Endpoint     common.Endpoint         `json:"endpoint"`
	Enabled      bool                    `json:"enabled"`
	LogicalNodes []NodeDescriptor        `json:"logicalNodes"`
}

type TopologySnapshot struct {
	PhysicalNodes []PhysicalNodeView `json:"physicalNodes"`
}

// ProduceResult is one core ProduceMatch attempt. Match is nil when the seed
// was consumed but no complete group was formed.
type ProduceResult struct {
	PhysicalNodeID identity.PhysicalNodeID `json:"physicalNodeId"`
	LogicalNode    identity.LogicalNodeKey `json:"logicalNode"`
	Match          *MatchRecord            `json:"match,omitempty"`
}

type RoundResult struct {
	Round    uint64        `json:"round"`
	Now      int64         `json:"now"`
	Attempts int           `json:"attempts"`
	Matches  []MatchRecord `json:"matches"`
}

// AddTicketResult is the explicit form of AddTicket's detached observation.
// TicketView already contains the same RouteDecision; this wrapper is useful
// to HTTP callers that want the decision as a top-level response field.
type AddTicketResult struct {
	Ticket   TicketView           `json:"ticket"`
	Decision common.RouteDecision `json:"decision"`
}

// TicketQuery supports deterministic cursor pagination over observations.
type TicketQuery struct {
	Cursor         string                  `json:"cursor,omitempty"`
	Limit          int                     `json:"limit,omitempty"`
	Search         string                  `json:"search,omitempty"`
	PhysicalNodeID identity.PhysicalNodeID `json:"physicalNodeId,omitempty"`
	Rule           *identity.RuleKey       `json:"rule,omitempty"`
	Status         TicketStatus            `json:"status,omitempty"`
}

type TicketPage struct {
	Items      []TicketView `json:"items"`
	NextCursor string       `json:"nextCursor,omitempty"`
	Total      int          `json:"total"`
}

type MatchQuery struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type MatchPage struct {
	Items      []MatchRecord `json:"items"`
	NextCursor string        `json:"nextCursor,omitempty"`
	Total      int           `json:"total"`
}

type Event struct {
	Sequence uint64         `json:"sequence"`
	Type     string         `json:"type"`
	Data     map[string]any `json:"data,omitempty"`
}

type EventQuery struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type EventPage struct {
	Items      []Event `json:"items"`
	NextCursor string  `json:"nextCursor,omitempty"`
	Total      int     `json:"total"`
}

// Capabilities describes the closed production vocabulary exposed to a rule
// editor. It intentionally contains no internal expression nodes or handles.
type Capabilities struct {
	SchemaVersions          []string `json:"schemaVersions"`
	ScenarioSchemaVersion   string   `json:"scenarioSchemaVersion"`
	ContractSchemaVersion   string   `json:"contractSchemaVersion"`
	ExpressionSchemaVersion string   `json:"expressionSchemaVersion"`
	PrefilterSchemaVersion  string   `json:"prefilterSchemaVersion"`
	EvaluationSchemaVersion string   `json:"evaluationSchemaVersion"`
	Selectors               []string `json:"selectors"`
	SeedOrders              []string `json:"seedOrders"`
	FactTypes               []string `json:"factTypes"`
	FactScopes              []string `json:"factScopes"`
	IndexTypes              []string `json:"indexTypes"`
	// ExpressionOps and BitmapOps retain the compact names used by older HTTP
	// adapters. The detailed catalogs below are the authoritative closed
	// vocabulary for a rule editor.
	ExpressionOps   []string             `json:"expressionOps"`
	BitmapOps       []string             `json:"bitmapOps"`
	ScalarOperators []OperatorCapability `json:"scalarOperators"`
	BitmapOperators []OperatorCapability `json:"bitmapOperators"`
}

// OperatorCapability describes one legal JSON operator. Inputs are result
// types (with [] denoting a variadic list) and Fields are the exact JSON keys
// accepted by the operator. The simulator exposes this rather than leaking
// expression compiler internals to a frontend palette.
type OperatorCapability struct {
	Name       string   `json:"name"`
	ResultType string   `json:"resultType"`
	Inputs     []string `json:"inputs,omitempty"`
	Fields     []string `json:"fields"`
}

func defaultCapabilities() Capabilities {
	scalar := scalarOperatorCapabilities()
	bitmap := bitmapOperatorCapabilities()
	expressionOps := make([]string, 0, len(scalar))
	for _, operator := range scalar {
		expressionOps = append(expressionOps, operator.Name)
	}
	bitmapOps := make([]string, 0, len(bitmap))
	for _, operator := range bitmap {
		bitmapOps = append(bitmapOps, operator.Name)
	}
	return Capabilities{
		ScenarioSchemaVersion:   ScenarioSchemaVersion,
		SchemaVersions:          []string{ScenarioSchemaVersion, ContractSchemaVersion, ExpressionSchemaVersion, PrefilterSchemaVersion, EvaluationSchemaVersion},
		ContractSchemaVersion:   ContractSchemaVersion,
		ExpressionSchemaVersion: ExpressionSchemaVersion,
		PrefilterSchemaVersion:  PrefilterSchemaVersion,
		EvaluationSchemaVersion: EvaluationSchemaVersion,
		Selectors:               []string{string(SelectorRoundRobin), string(SelectorLargestQueue), string(SelectorOldestWaiting), string(SelectorWeighted)},
		SeedOrders:              []string{string(matchsystem.SeedOrderArrival), string(matchsystem.SeedOrderOldest), string(matchsystem.SeedOrderInt64Priority), string(matchsystem.SeedOrderRandom)},
		FactTypes:               []string{"strings", "int64", "uint64s"},
		FactScopes:              []string{"tick", "object", "match"},
		IndexTypes:              []string{"multi_value", "int64_range"},
		ExpressionOps:           expressionOps,
		BitmapOps:               bitmapOps,
		ScalarOperators:         scalar,
		BitmapOperators:         bitmap,
	}
}

func scalarOperatorCapabilities() []OperatorCapability {
	const (
		boolType    = "bool"
		int64Type   = "int64"
		stringsType = "strings"
		uint64sType = "uint64s"
	)
	return []OperatorCapability{
		{Name: "bool_literal", ResultType: boolType, Fields: []string{"op", "value"}},
		{Name: "bool_and", ResultType: boolType, Inputs: []string{"bool[]"}, Fields: []string{"op", "children"}},
		{Name: "bool_or", ResultType: boolType, Inputs: []string{"bool[]"}, Fields: []string{"op", "children"}},
		{Name: "bool_not", ResultType: boolType, Inputs: []string{boolType}, Fields: []string{"op", "value"}},
		{Name: "int64_literal", ResultType: int64Type, Fields: []string{"op", "value"}},
		{Name: "int64_ref", ResultType: int64Type, Fields: []string{"op", "source", "name"}},
		{Name: "int64_step", ResultType: int64Type, Inputs: []string{int64Type}, Fields: []string{"op", "input", "steps"}},
		{Name: "int64_clamp", ResultType: int64Type, Inputs: []string{int64Type, int64Type, int64Type}, Fields: []string{"op", "value", "min", "max"}},
		{Name: "int64_add", ResultType: int64Type, Inputs: []string{int64Type, int64Type}, Fields: []string{"op", "left", "right"}},
		{Name: "int64_sub", ResultType: int64Type, Inputs: []string{int64Type, int64Type}, Fields: []string{"op", "left", "right"}},
		{Name: "int64_min", ResultType: int64Type, Inputs: []string{int64Type, int64Type}, Fields: []string{"op", "left", "right"}},
		{Name: "int64_max", ResultType: int64Type, Inputs: []string{int64Type, int64Type}, Fields: []string{"op", "left", "right"}},
		{Name: "strings_literal", ResultType: stringsType, Fields: []string{"op", "values"}},
		{Name: "strings_ref", ResultType: stringsType, Fields: []string{"op", "source", "name"}},
		{Name: "strings_union", ResultType: stringsType, Inputs: []string{"strings[]"}, Fields: []string{"op", "items"}},
		{Name: "uint64s_literal", ResultType: uint64sType, Fields: []string{"op", "values"}},
		{Name: "uint64s_ref", ResultType: uint64sType, Fields: []string{"op", "source", "name"}},
		{Name: "uint64s_union", ResultType: uint64sType, Inputs: []string{"uint64s[]"}, Fields: []string{"op", "items"}},
		{Name: "int64_eq", ResultType: boolType, Inputs: []string{int64Type, int64Type}, Fields: []string{"op", "left", "right"}},
		{Name: "int64_neq", ResultType: boolType, Inputs: []string{int64Type, int64Type}, Fields: []string{"op", "left", "right"}},
		{Name: "int64_lt", ResultType: boolType, Inputs: []string{int64Type, int64Type}, Fields: []string{"op", "left", "right"}},
		{Name: "int64_lte", ResultType: boolType, Inputs: []string{int64Type, int64Type}, Fields: []string{"op", "left", "right"}},
		{Name: "int64_gt", ResultType: boolType, Inputs: []string{int64Type, int64Type}, Fields: []string{"op", "left", "right"}},
		{Name: "int64_gte", ResultType: boolType, Inputs: []string{int64Type, int64Type}, Fields: []string{"op", "left", "right"}},
		{Name: "strings_eq", ResultType: boolType, Inputs: []string{stringsType, stringsType}, Fields: []string{"op", "values", "other"}},
		{Name: "strings_neq", ResultType: boolType, Inputs: []string{stringsType, stringsType}, Fields: []string{"op", "values", "other"}},
		{Name: "strings_is_empty", ResultType: boolType, Inputs: []string{stringsType}, Fields: []string{"op", "values"}},
		{Name: "strings_contains", ResultType: boolType, Inputs: []string{stringsType}, Fields: []string{"op", "values", "needle"}},
		{Name: "strings_contains_any", ResultType: boolType, Inputs: []string{stringsType, stringsType}, Fields: []string{"op", "values", "other"}},
		{Name: "strings_contains_all", ResultType: boolType, Inputs: []string{stringsType, stringsType}, Fields: []string{"op", "values", "other"}},
		{Name: "strings_intersects", ResultType: boolType, Inputs: []string{stringsType, stringsType}, Fields: []string{"op", "values", "other"}},
		{Name: "uint64s_eq", ResultType: boolType, Inputs: []string{uint64sType, uint64sType}, Fields: []string{"op", "values", "other"}},
		{Name: "uint64s_neq", ResultType: boolType, Inputs: []string{uint64sType, uint64sType}, Fields: []string{"op", "values", "other"}},
		{Name: "uint64s_is_empty", ResultType: boolType, Inputs: []string{uint64sType}, Fields: []string{"op", "values"}},
		{Name: "uint64s_contains", ResultType: boolType, Inputs: []string{uint64sType}, Fields: []string{"op", "values", "needle"}},
		{Name: "uint64s_contains_any", ResultType: boolType, Inputs: []string{uint64sType, uint64sType}, Fields: []string{"op", "values", "other"}},
		{Name: "uint64s_contains_all", ResultType: boolType, Inputs: []string{uint64sType, uint64sType}, Fields: []string{"op", "values", "other"}},
		{Name: "uint64s_intersects", ResultType: boolType, Inputs: []string{uint64sType, uint64sType}, Fields: []string{"op", "values", "other"}},
	}
}

func bitmapOperatorCapabilities() []OperatorCapability {
	return []OperatorCapability{
		{Name: "none", ResultType: "bitmap", Fields: []string{"op"}},
		{Name: "and", ResultType: "bitmap", Inputs: []string{"bitmap[]"}, Fields: []string{"op", "children"}},
		{Name: "or", ResultType: "bitmap", Inputs: []string{"bitmap[]"}, Fields: []string{"op", "children"}},
		{Name: "exclude", ResultType: "bitmap", Inputs: []string{"bitmap"}, Fields: []string{"op", "value"}},
		{Name: "if", ResultType: "bitmap", Inputs: []string{"bool", "bitmap", "bitmap"}, Fields: []string{"op", "when", "then", "else"}},
		{Name: "lookup_string", ResultType: "bitmap", Inputs: []string{"strings"}, Fields: []string{"op", "index", "values"}},
		{Name: "lookup_uint64", ResultType: "bitmap", Inputs: []string{"uint64s"}, Fields: []string{"op", "index", "values"}},
		{Name: "lookup_range", ResultType: "bitmap", Inputs: []string{"int64", "int64"}, Fields: []string{"op", "index", "min", "max"}},
	}
}

// ValidationIssue is one structured scenario/rule validation failure.
type ValidationIssue struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ValidationError struct {
	Issues []ValidationIssue `json:"issues"`
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return "simulator validation failed"
	}
	first := e.Issues[0]
	return fmt.Sprintf("simulator validation failed at %s [%s]: %s", first.Path, first.Code, first.Message)
}

type ValidationReport struct {
	Valid  bool              `json:"valid"`
	Issues []ValidationIssue `json:"issues,omitempty"`
}

func (r ValidationReport) Err() error {
	if r.Valid {
		return nil
	}
	return &ValidationError{Issues: append([]ValidationIssue(nil), r.Issues...)}
}

// Int64Range controls one deterministic integer field in a batch generator.
type Int64Range struct {
	Min int64 `json:"min"`
	Max int64 `json:"max"`
}

// BatchGeneratorSpec is evaluated server-side. Choices and ranges are sampled
// using Seed, so the same scenario and request can be replayed exactly.
type BatchGeneratorSpec struct {
	Rule            identity.RuleKey      `json:"rule"`
	Count           int                   `json:"count"`
	Seed            int64                 `json:"seed"`
	FirstTicketID   common.TicketID       `json:"firstTicketId"`
	CreatedAtStart  int64                 `json:"createdAtStart"`
	CreatedAtStep   int64                 `json:"createdAtStep"`
	AffinityPrefix  string                `json:"affinityPrefix,omitempty"`
	RequestIDPrefix string                `json:"requestIdPrefix,omitempty"`
	StringChoices   map[string][]string   `json:"stringChoices,omitempty"`
	Uint64Choices   map[string][]uint64   `json:"uint64Choices,omitempty"`
	Int64Ranges     map[string]Int64Range `json:"int64Ranges,omitempty"`
	StringLists     map[string][]string   `json:"strings,omitempty"`
	Uint64Lists     map[string][]uint64   `json:"uint64s,omitempty"`
	Int64Values     map[string]int64      `json:"int64s,omitempty"`
	ObjectFacts     FactSnapshot          `json:"objectFacts,omitempty"`
}

type GeneratorSpec = BatchGeneratorSpec

type BatchResult struct {
	Seed      int64                  `json:"seed"`
	Requested int                    `json:"requested"`
	Added     int                    `json:"added"`
	TicketIDs []common.TicketID      `json:"ticketIds"`
	Decisions []common.RouteDecision `json:"decisions"`
}

// NodePort is the narrow simulator-facing PhysicalNode port. The core adapter
// is the only place that knows about matchsystem.PhysicalNode concrete types.
type NodePort interface {
	ID() identity.PhysicalNodeID
	Load(context.Context, matchsystem.LogicalNodeSpec) error
	Add(context.Context, identity.OwnerRef, *common.Ticket) (uint32, error)
	Remove(context.Context, identity.OwnerRef, common.TicketID) (bool, error)
	Get(context.Context, identity.OwnerRef, common.TicketID) (*common.Ticket, bool, error)
	BeginMatchRound(context.Context, int64) error
	ProduceMatch(context.Context) (NodeProduceResult, error)
	BeginDrain(context.Context, identity.LogicalNodeKey) error
	Stop(context.Context, identity.LogicalNodeKey) error
	Describe(context.Context) ([]NodeDescriptor, error)
}

type RoutePort interface {
	RouteNew(context.Context, common.RouteRequest) (common.RouteDecision, error)
	ResolveOwner(identity.OwnerRef) (common.ResolvedOwner, error)
}

type NodeProduceResult struct {
	PhysicalNodeID identity.PhysicalNodeID
	LogicalNode    identity.LogicalNodeKey
	Match          *common.Match
}

func sortPhysicalIDs(nodes map[identity.PhysicalNodeID]*physicalNodeAdapter) []identity.PhysicalNodeID {
	ids := make([]identity.PhysicalNodeID, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func normalizeSelector(value SelectorKind) SelectorKind {
	value = SelectorKind(strings.ToLower(strings.TrimSpace(string(value))))
	if value == "" {
		return SelectorRoundRobin
	}
	return value
}
