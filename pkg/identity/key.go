// Package identity 定义路由客户端和匹配核心共用的稳定、可比较的身份。
package identity

import (
	"fmt"
	"strconv"
)

type PhysicalNodeID uint64
type PlacementID uint64
type RuleID = uint64

func (id PhysicalNodeID) Validate() error {
	if id == 0 {
		return fmt.Errorf("PhysicalNodeID is required")
	}
	return nil
}

func (id PlacementID) Validate() error {
	if id == 0 {
		return fmt.Errorf("PlacementID is required")
	}
	return nil
}

// RuleKey 是跨路由与匹配核心共享的规则身份。
type RuleKey struct {
	Namespace string `json:"namespace,omitempty"`
	RuleID    RuleID `json:"ruleId,string"`
}

func (k RuleKey) Validate() error {
	if k.RuleID == 0 {
		return fmt.Errorf("RuleID must be positive")
	}
	return nil
}

func (k RuleKey) String() string {
	return joinParts(strconv.FormatUint(k.RuleID, 10))
}

type LogicalNodeKey struct {
	Rule        RuleKey     `json:"rule"`
	PlacementID PlacementID `json:"placementId,string"`
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
	return joinParts(strconv.FormatUint(k.Rule.RuleID, 10), strconv.FormatUint(uint64(k.PlacementID), 10))
}

type OwnerRef struct {
	LogicalNode    LogicalNodeKey `json:"logicalNode"`
	PhysicalNodeID PhysicalNodeID `json:"physicalNodeId,string"`
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
	return joinParts(r.LogicalNode.String(), strconv.FormatUint(uint64(r.PhysicalNodeID), 10))
}

func joinParts(parts ...string) string {
	result := ""
	for _, part := range parts {
		result += strconv.Itoa(len(part)) + ":" + part
	}
	return result
}
