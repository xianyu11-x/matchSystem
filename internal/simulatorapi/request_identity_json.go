package simulatorapi

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/xianyu11-x/matchSystem/internal/simulator"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

// decodeRequestIdentities 只处理请求中的规则和部署身份，保留统一的十进制校验。
func decodeRequestIdentities(rule, placement json.RawMessage) (identity.RuleKey, identity.PlacementID, error) {
	var key identity.RuleKey
	if len(rule) > 0 {
		var err error
		key, err = simulator.DecodeRuleKeyJSON(rule)
		if err != nil {
			return identity.RuleKey{}, 0, fmt.Errorf("invalid rule: %w", err)
		}
	}
	var id identity.PlacementID
	if len(placement) > 0 {
		value, err := simulator.DecodeUint64JSON(placement)
		if err != nil {
			return identity.RuleKey{}, 0, fmt.Errorf("invalid placementId: %w", err)
		}
		id = identity.PlacementID(value)
	}
	return key, id, nil
}

func (request *TicketCreateRequest) UnmarshalJSON(data []byte) error {
	type requestJSON TicketCreateRequest
	var wire struct {
		*requestJSON
		Rule        json.RawMessage `json:"rule"`
		PlacementID json.RawMessage `json:"placementId"`
	}
	wire.requestJSON = new(requestJSON)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	key, placement, err := decodeRequestIdentities(wire.Rule, wire.PlacementID)
	if err != nil {
		return err
	}
	wire.requestJSON.Rule, wire.requestJSON.PlacementID = key, placement
	*request = TicketCreateRequest(*wire.requestJSON)
	return nil
}

func (request *CustomTicketsRequest) UnmarshalJSON(data []byte) error {
	type requestJSON CustomTicketsRequest
	var wire struct {
		*requestJSON
		Rule        json.RawMessage `json:"rule"`
		PlacementID json.RawMessage `json:"placementId"`
	}
	wire.requestJSON = new(requestJSON)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	key, placement, err := decodeRequestIdentities(wire.Rule, wire.PlacementID)
	if err != nil {
		return err
	}
	wire.requestJSON.Rule, wire.requestJSON.PlacementID = key, placement
	*request = CustomTicketsRequest(*wire.requestJSON)
	return nil
}

func (request *TicketBatchRequest) UnmarshalJSON(data []byte) error {
	type requestJSON TicketBatchRequest
	var wire struct {
		*requestJSON
		Rule        json.RawMessage `json:"rule"`
		PlacementID json.RawMessage `json:"placementId"`
	}
	wire.requestJSON = new(requestJSON)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	key, placement, err := decodeRequestIdentities(wire.Rule, wire.PlacementID)
	if err != nil {
		return err
	}
	wire.requestJSON.Rule, wire.requestJSON.PlacementID = key, placement
	*request = TicketBatchRequest(*wire.requestJSON)
	return nil
}
