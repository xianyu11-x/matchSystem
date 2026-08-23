// Package identity defines stable, comparable keys shared by routing clients
// and MatchService's in-process physical node.
package identity

import (
	"fmt"
	"strconv"
)

type PhysicalNodeID string
type PlacementID string

func (id PhysicalNodeID) Validate() error {
	if id == "" {
		return fmt.Errorf("PhysicalNodeID is required")
	}
	return nil
}

func (id PlacementID) Validate() error {
	if id == "" {
		return fmt.Errorf("PlacementID is required")
	}
	return nil
}

type RuleKey struct {
	Namespace string
	RuleID    string
}

func (k RuleKey) Validate() error {
	if k.RuleID == "" {
		return fmt.Errorf("RuleID is required")
	}
	return nil
}

func (k RuleKey) String() string {
	return joinParts(k.Namespace, k.RuleID)
}

type LogicalNodeKey struct {
	Rule        RuleKey
	PlacementID PlacementID
}

func (k LogicalNodeKey) Validate() error {
	if err := k.Rule.Validate(); err != nil {
		return err
	}
	if err := k.PlacementID.Validate(); err != nil {
		return err
	}
	return nil
}

func (k LogicalNodeKey) String() string {
	return joinParts(k.Rule.Namespace, k.Rule.RuleID, string(k.PlacementID))
}

type OwnerRef struct {
	LogicalNode    LogicalNodeKey
	PhysicalNodeID PhysicalNodeID
}

func (r OwnerRef) Validate() error {
	if err := r.LogicalNode.Validate(); err != nil {
		return err
	}
	if err := r.PhysicalNodeID.Validate(); err != nil {
		return err
	}
	return nil
}

func (r OwnerRef) String() string {
	return joinParts(r.LogicalNode.String(), string(r.PhysicalNodeID))
}

func joinParts(parts ...string) string {
	result := ""
	for _, part := range parts {
		result += strconv.Itoa(len(part)) + ":" + part
	}
	return result
}
