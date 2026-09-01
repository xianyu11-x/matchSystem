// Package simulatorapi contains the HTTP transport boundary for the
// simulator.  The package owns the wire DTOs so that the HTTP layer does not
// expose matchsystem's private document IDs or compiler implementation types.
package simulatorapi

import (
	"context"
	"encoding/json"
	"errors"
)

const APIVersion = "v1"

// Service is the application seam used by the HTTP handler.  A runtime
// adapter should translate internal/simulator values into these detached
// transport DTOs.  Keeping this interface here makes the HTTP package easy to
// test with httptest and keeps simulator internals out of the wire contract.
type Service interface {
	Health(context.Context) (HealthResponse, error)
	Capabilities(context.Context) (CapabilitiesResponse, error)
	GetScenario(context.Context) (ScenarioResponse, error)
	ReplaceScenario(context.Context, ScenarioRequest) (ScenarioResponse, error)
	ValidateRule(context.Context, ValidateRuleRequest) (ValidationResponse, error)
	Topology(context.Context) (TopologyResponse, error)
	ListTickets(context.Context, TicketListQuery) (TicketPage, error)
	CreateTicket(context.Context, TicketCreateRequest) (TicketView, error)
	CreateCustomTickets(context.Context, CustomTicketsRequest) (TicketBatchResponse, error)
	CreateTicketsBatch(context.Context, TicketBatchRequest) (TicketBatchResponse, error)
	DeleteTicket(context.Context, string) (DeleteTicketResponse, error)
	RunRound(context.Context, RoundRequest) (RoundResponse, error)
	ListMatches(context.Context, MatchListQuery) (MatchPage, error)
	SubscribeEvents(context.Context, EventQuery) (<-chan Event, error)
}

// LogicalNodeFactService is the optional application seam for querying the
// Fact contract of one LogicalNode. It is kept separate from Service so
// existing embedders that only implement the original simulator endpoints
// remain source-compatible; the built-in SimulatorAdapter implements it.
type LogicalNodeFactService interface {
	GetLogicalNodeFacts(context.Context, LogicalNodeFactsQuery) (LogicalNodeFactsResponse, error)
}

// MatchDetailService is the optional application seam for retrieving one
// retained Match with its detached member observations. It remains separate
// from Service so existing simulator embedders that only provide list
// observations remain source-compatible.
type MatchDetailService interface {
	GetMatch(context.Context, string) (MatchView, error)
}

// RuleKey and PlacementKey mirror identity.RuleKey and identity.LogicalNodeKey
// without making the transport package depend on the core identity package.
type RuleKey struct {
	Namespace string `json:"namespace,omitempty"`
	RuleID    int32  `json:"ruleId"`
}

type PlacementKey struct {
	Rule        RuleKey `json:"rule"`
	PlacementID string  `json:"placementId"`
}

type OwnerRef struct {
	PhysicalNodeID string       `json:"physicalNodeId"`
	LogicalNode    PlacementKey `json:"logicalNode"`
}

type RouteDecision struct {
	DecisionID string   `json:"decisionId,omitempty"`
	Owner      OwnerRef `json:"owner"`
	Endpoint   string   `json:"endpoint,omitempty"`
}

// TypedValues is shared by Ticket and Facts.  The names intentionally match
// the browser adapter and the JSON representation used by the simulator
// runtime (stringLists, uint64Lists, and int64Values).
type TypedValues struct {
	StringLists           map[string][]string `json:"stringLists,omitempty"`
	Uint64Lists           map[string][]uint64 `json:"uint64Lists,omitempty"`
	Int64Values           map[string]int64    `json:"int64Values,omitempty"`
	OmittedNumericSamples int                 `json:"omittedNumericSamples,omitempty"`
}

type Ticket struct {
	TypedValues
	TicketID uint64 `json:"ticketId"`
	// CreatedAt is a Unix timestamp in milliseconds on the HTTP boundary.
	CreatedAt int64 `json:"createdAt"`
}

type Facts = TypedValues

type TicketView struct {
	Ticket Ticket         `json:"ticket"`
	Facts  TypedValues    `json:"facts,omitempty"`
	Owner  OwnerRef       `json:"owner"`
	Route  *RouteDecision `json:"route,omitempty"`
	State  string         `json:"state"`
}

type MatchView struct {
	MatchID        string       `json:"matchId"`
	Round          uint64       `json:"round"`
	PhysicalNodeID string       `json:"physicalNodeId"`
	LogicalNode    PlacementKey `json:"logicalNode"`
	// Tickets is the compact, backwards-compatible member list. Members
	// carries the full detached TicketView observations for Match detail
	// consumers, including object Facts, owner, route, and state.
	Tickets   []Ticket     `json:"tickets"`
	Members   []TicketView `json:"members"`
	Facts     TypedValues  `json:"facts,omitempty"`
	CreatedAt int64        `json:"createdAt"`
	// DurationMs is the oldest member's queue wait duration at match commit,
	// not matching engine execution time.
	DurationMs int64 `json:"durationMs"`
}

type HealthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service,omitempty"`
	Version string `json:"version,omitempty"`
}

type CapabilitiesResponse struct {
	SchemaVersions   []string             `json:"schemaVersions"`
	Selectors        []string             `json:"selectors"`
	CandidateScorers []string             `json:"candidateScorers"`
	SeedSelections   []string             `json:"seedSelections"`
	FactTypes        []string             `json:"factTypes"`
	ExpressionOps    []string             `json:"expressionOps,omitempty"`
	BitmapOps        []string             `json:"bitmapOps,omitempty"`
	IndexTypes       []string             `json:"indexTypes,omitempty"`
	FactScopes       []string             `json:"factScopes,omitempty"`
	ScalarOperators  []OperatorCapability `json:"scalarOperators"`
	BitmapOperators  []OperatorCapability `json:"bitmapOperators"`
	Limits           map[string]int       `json:"limits,omitempty"`
}

// OperatorCapability is the transport form of the closed operator catalog.
// Inputs describe accepted result types and Fields are exact JSON keys.
type OperatorCapability struct {
	Name       string   `json:"name"`
	ResultType string   `json:"resultType"`
	Inputs     []string `json:"inputs,omitempty"`
	Fields     []string `json:"fields"`
}

type ScenarioResponse struct {
	Revision string          `json:"revision,omitempty"`
	Scenario json.RawMessage `json:"scenario"`
}

type ScenarioRequest struct {
	Scenario json.RawMessage `json:"scenario"`
}

type ValidateRuleRequest struct {
	Rule json.RawMessage `json:"rule"`
}

type ValidationIssue struct {
	Path     string `json:"path,omitempty"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity,omitempty"`
}

type ValidationResponse struct {
	Valid       bool              `json:"valid"`
	Issues      []ValidationIssue `json:"issues,omitempty"`
	Fingerprint string            `json:"fingerprint,omitempty"`
}

type LogicalStatus struct {
	Key         PlacementKey `json:"key"`
	State       string       `json:"state"`
	TicketCount int          `json:"ticketCount"`
}

type PhysicalNodeStatus struct {
	PhysicalNodeID string          `json:"physicalNodeId"`
	Endpoint       string          `json:"endpoint,omitempty"`
	Enabled        bool            `json:"enabled"`
	LogicalNodes   []LogicalStatus `json:"logicalNodes,omitempty"`
}

type TopologyResponse struct {
	PhysicalNodes []PhysicalNodeStatus `json:"physicalNodes"`
}

// LogicalNodeFactsQuery identifies a LogicalNode using its stable RuleKey and
// placement. Namespace may be omitted only when the runtime's rule ID is
// unambiguous; callers that know it should send it explicitly.
type LogicalNodeFactsQuery struct {
	RuleNamespace string
	RuleID        int32
	PlacementID   string
}

// FactSpec is the transport representation of one Fact declaration. Type and
// scope are strings on the wire so clients do not need to know the core's
// numeric enum values.
type FactSpec struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Scope       string `json:"scope"`
	MaxValues   int    `json:"maxValues,omitempty"`
	Description string `json:"description,omitempty"`
}

// FactProviderDescriptor is the transport form of one provider-side startup
// handshake declaration. Scope is carried by the wrapper because the core
// descriptor itself intentionally describes only one provider's Fact set.
// Its Facts are independent from ContractFacts and RuntimeFacts.
type FactProviderDescriptor struct {
	ID      string     `json:"id"`
	Version string     `json:"version"`
	Facts   []FactSpec `json:"facts"`
}

// ProviderDescriptorSet keeps all three simulator provider boundaries
// explicit. A missing pointer means that no descriptor was configured for
// that scope (and is a validation error when the Contract declares Facts).
type ProviderDescriptorSet struct {
	Tick   *FactProviderDescriptor `json:"tick,omitempty"`
	Object *FactProviderDescriptor `json:"object,omitempty"`
	Match  *FactProviderDescriptor `json:"match,omitempty"`
}

// RuntimeFactValues is the simulator-owned value side of the Fact model. The
// Tick layer is static Scenario data; Object values belong to individual
// tickets and Match values belong to completed Match observations, so those
// layers remain on their respective endpoints instead of being merged into a
// provider descriptor.
type RuntimeFactValues struct {
	Tick TypedValues `json:"tick,omitempty"`
}

type LogicalNodeFactsResponse struct {
	LogicalNode PlacementKey `json:"logicalNode"`
	// Facts is retained as a compatibility alias for the rule-side Contract
	// declarations. New clients should use ContractFacts to make the source
	// explicit.
	Facts               []FactSpec            `json:"facts"`
	ContractFacts       []FactSpec            `json:"contractFacts"`
	ProviderDescriptors ProviderDescriptorSet `json:"providerDescriptors"`
	RuntimeFacts        RuntimeFactValues     `json:"runtimeFacts"`
}

type TicketCreateRequest struct {
	Ticket      Ticket      `json:"ticket"`
	Rule        RuleKey     `json:"rule"`
	PlacementID string      `json:"placementId,omitempty"`
	AffinityKey string      `json:"affinityKey,omitempty"`
	Facts       TypedValues `json:"facts,omitempty"`
}

// GeneratorConfig is reserved for future options. Unsupported generator
// knobs are intentionally absent so the strict HTTP decoder rejects them
// instead of accepting a configuration that has no effect.
type GeneratorConfig struct{}

type CustomTicketsRequest struct {
	Count          int                 `json:"count"`
	Seed           int64               `json:"seed"`
	Rule           RuleKey             `json:"rule"`
	PlacementID    string              `json:"placementId,omitempty"`
	StartTicketID  uint64              `json:"startTicketId,omitempty"`
	CreatedAtStart int64               `json:"createdAtStart,omitempty"`
	Template       *Ticket             `json:"template,omitempty"`
	Attributes     map[string][]string `json:"attributes,omitempty"`
	Generator      *GeneratorConfig    `json:"generator,omitempty"`
	// These fields allow callers using the runtime generator vocabulary to
	// pass deterministic choices without sending a large ticket array.
	StringChoices map[string][]string   `json:"stringChoices,omitempty"`
	Uint64Choices map[string][]uint64   `json:"uint64Choices,omitempty"`
	Int64Ranges   map[string]Int64Range `json:"int64Ranges,omitempty"`
}

type Int64Range struct {
	Min int64 `json:"min"`
	Max int64 `json:"max"`
}

type TicketBatchRequest struct {
	Tickets        []TicketCreateRequest `json:"tickets"`
	Atomic         bool                  `json:"atomic,omitempty"`
	Count          int                   `json:"count,omitempty"`
	Seed           int64                 `json:"seed,omitempty"`
	Rule           RuleKey               `json:"rule,omitempty"`
	PlacementID    string                `json:"placementId,omitempty"`
	StartTicketID  uint64                `json:"startTicketId,omitempty"`
	CreatedAtStart int64                 `json:"createdAtStart,omitempty"`
	Generator      *GeneratorConfig      `json:"generator,omitempty"`
	StringChoices  map[string][]string   `json:"stringChoices,omitempty"`
	Uint64Choices  map[string][]uint64   `json:"uint64Choices,omitempty"`
	Int64Ranges    map[string]Int64Range `json:"int64Ranges,omitempty"`
}

type TicketBatchResponse struct {
	Accepted    int               `json:"accepted"`
	Rejected    int               `json:"rejected,omitempty"`
	GeneratorID string            `json:"generatorId,omitempty"`
	Seed        int64             `json:"seed,omitempty"`
	Tickets     []TicketView      `json:"tickets,omitempty"`
	Issues      []ValidationIssue `json:"issues,omitempty"`
}

type DeleteTicketResponse struct {
	TicketID uint64 `json:"ticketId"`
	Deleted  bool   `json:"deleted"`
}

type TicketPage struct {
	Items      []TicketView `json:"items"`
	NextCursor string       `json:"nextCursor,omitempty"`
	Total      int          `json:"total,omitempty"`
}

type TicketListQuery struct {
	Cursor         string
	Limit          int
	Search         string
	State          string
	PhysicalNodeID string
	RuleNamespace  string
	RuleID         int32
}

type RoundRequest struct {
	// Now is a Unix timestamp in milliseconds. The adapter accepts legacy
	// Unix-nanosecond values and normalizes them before entering the simulator.
	Now        *int64 `json:"now,omitempty"`
	MaxMatches int    `json:"maxMatches,omitempty"`
	Seed       *int64 `json:"seed,omitempty"`
	MatchLimit int    `json:"matchLimit,omitempty"`
}

type RoundResponse struct {
	RoundID  string            `json:"roundId,omitempty"`
	Produced int               `json:"produced"`
	Matches  []MatchView       `json:"matches,omitempty"`
	Topology *TopologyResponse `json:"topology,omitempty"`
}

type MatchPage struct {
	Items      []MatchView `json:"items"`
	NextCursor string      `json:"nextCursor,omitempty"`
	// Total is always present, including zero, so clients can clear a cached
	// history after a scenario replacement or an empty retained window.
	Total int `json:"total"`
}

type MatchListQuery struct {
	Cursor string
	Limit  int
}

// Event is serialized as a single JSON object inside each SSE data frame;
// this is what the browser EventSource consumer parses from message.data.
type Event struct {
	ID      string         `json:"id,omitempty"`
	Type    string         `json:"type"`
	At      string         `json:"at"`
	Payload map[string]any `json:"payload,omitempty"`
}

type EventQuery struct {
	After string
}

// APIError is the stable structured body returned for every non-2xx response.
type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Path      string `json:"path,omitempty"`
	Details   any    `json:"details,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

type ErrorResponse struct {
	Error APIError `json:"error"`
}

// ServiceError lets an application adapter choose an HTTP status and stable
// error code without importing handler internals.
type ServiceError struct {
	Status  int
	Code    string
	Message string
	Path    string
	Details any
	Err     error
}

func (e *ServiceError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

func (e *ServiceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewServiceError(status int, code, message string) *ServiceError {
	return &ServiceError{Status: status, Code: code, Message: message}
}

var ErrServiceUnavailable = &ServiceError{
	Status:  503,
	Code:    "SERVICE_UNAVAILABLE",
	Message: "simulator service is not configured",
}

func asServiceError(err error) (*ServiceError, bool) {
	if err == nil {
		return nil, false
	}
	var serviceErr *ServiceError
	if errors.As(err, &serviceErr) && serviceErr != nil {
		return serviceErr, true
	}
	return nil, false
}

func ensureServiceError(err error) *ServiceError {
	if serviceErr, ok := asServiceError(err); ok {
		// Do not mutate a shared sentinel or an error owned by the adapter.
		copy := *serviceErr
		return &copy
	}
	return &ServiceError{Status: 500, Code: "INTERNAL_ERROR", Message: "internal simulator error", Err: err}
}

func requireService(service Service) error {
	if service == nil {
		return ErrServiceUnavailable
	}
	return nil
}

func invalidBody(path, message string) *ServiceError {
	return &ServiceError{Status: 400, Code: "INVALID_REQUEST", Message: message, Path: path}
}
