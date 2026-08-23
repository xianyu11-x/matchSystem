package common

import "matchSystem/internal/identity"

type RouteRequest struct {
	Rule        identity.RuleKey
	TicketID    string
	AffinityKey string
	RequestID   string
}

type RouteDecision struct {
	DecisionID string
	Owner      identity.OwnerRef
	Endpoint   Endpoint
}

type ResolvedOwner struct {
	Owner    identity.OwnerRef
	Endpoint Endpoint
}
