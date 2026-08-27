package matchsystem

import nodecontract "matchSystem/internal/matchsystem/contract"

// ParseLogicalNodeContractJSON is the only top-level Contract entry point.
// It accepts exactly logical-node-contract/v3 and applies the bounded default
// limits. LogicalNodeSpec itself accepts only ContractJSON and never a typed
// Contract; the parsed result is intended for read-only inspection or for
// internal domain compilation.
func ParseLogicalNodeContractJSON(data []byte) (nodecontract.Contract, error) {
	return nodecontract.Parse(data, nodecontract.DefaultLimits())
}
