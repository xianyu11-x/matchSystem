package common

import "github.com/xianyu11-x/matchSystem/pkg/identity"

type RouteRequest struct {
	Rule        identity.RuleKey
	TicketID    TicketID
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
