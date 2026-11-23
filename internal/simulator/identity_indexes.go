package simulator

import "github.com/xianyu11-x/matchSystem/pkg/identity"

// 索引只使用数值身份；Namespace 由原始记录保留供展示。
type logicalIndexKey struct {
	ruleID      identity.RuleID
	placementID identity.PlacementID
}

type ownerIndexKey struct {
	logical    logicalIndexKey
	physicalID identity.PhysicalNodeID
}

func logicalIndex(key identity.LogicalNodeKey) logicalIndexKey {
	return logicalIndexKey{ruleID: key.Rule.RuleID, placementID: key.PlacementID}
}

func ownerIndex(owner identity.OwnerRef) ownerIndexKey {
	return ownerIndexKey{logical: logicalIndex(owner.LogicalNode), physicalID: owner.PhysicalNodeID}
}
