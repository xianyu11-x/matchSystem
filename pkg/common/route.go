package common

import "github.com/xianyu11-x/matchSystem/pkg/identity"

type RouteRequest struct {
	Rule        identity.RuleKey
	TicketID    TicketID
	AffinityKey uint64
	RequestID   uint64
}

type RouteDecision struct {
	DecisionID uint64
	Owner      identity.OwnerRef
	Endpoint   Endpoint
}

type ResolvedOwner struct {
	Owner    identity.OwnerRef
	Endpoint Endpoint
}
