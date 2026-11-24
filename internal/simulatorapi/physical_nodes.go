package simulatorapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/xianyu11-x/matchSystem/internal/simulator"
	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

// PhysicalNodeService 是物理节点动态管理的可选服务接口。
type PhysicalNodeService interface {
	AddPhysicalNode(context.Context, PhysicalNodeCreateRequest) error
	RemovePhysicalNode(context.Context, string) error
}

// PhysicalNodeTicketService 是物理节点等待 Ticket 清空的可选服务接口。
type PhysicalNodeTicketService interface {
	ClearPhysicalNodeTickets(context.Context, string) (ClearPhysicalNodeTicketsResponse, error)
}

// ClearPhysicalNodeTicketsResponse 返回已从匹配核心移除的等待 Ticket 数量。
type ClearPhysicalNodeTicketsResponse struct {
	RemovedTickets int `json:"removedTickets"`
}

// PhysicalNodeDeploymentService 是引用规则库增改节点的可选接口。
type PhysicalNodeDeploymentService interface {
	AddPhysicalNodeFromLibrary(context.Context, PhysicalNodeCreateRequest) error
	UpdatePhysicalNodeFromLibrary(context.Context, PhysicalNodeCreateRequest) error
}

// PhysicalNodeCreateRequest 包含物理节点及内联规则或规则库部署。
type PhysicalNodeCreateRequest struct {
	PhysicalNode *PhysicalNodeDeclaration    `json:"physicalNode"`
	Rules        physicalNodeRuleSpecs       `json:"rules,omitempty"`
	Deployments  physicalNodeDeploymentSpecs `json:"deployments,omitempty"`
}

// PhysicalNodeDeclaration 使用指针区分未提供 enabled 与显式禁用。
type PhysicalNodeDeclaration struct {
	ID                    identity.PhysicalNodeID `json:"id,string"`
	Endpoint              string                  `json:"endpoint"`
	Enabled               *bool                   `json:"enabled"`
	Selector              string                  `json:"selector,omitempty"`
	RouteUpdateIntervalMS int64                   `json:"routeUpdateIntervalMs,omitempty"`
}

func (physical *PhysicalNodeDeclaration) UnmarshalJSON(data []byte) error {
	type declarationJSON PhysicalNodeDeclaration
	var wire struct {
		*declarationJSON
		ID                    json.RawMessage `json:"id"`
		RouteUpdateIntervalMS json.RawMessage `json:"routeUpdateIntervalMs"`
	}
	wire.declarationJSON = new(declarationJSON)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	if len(wire.ID) > 0 {
		id, err := simulator.DecodeUint64JSON(wire.ID)
		if err != nil {
			return fmt.Errorf("invalid physicalNode.id: %w", err)
		}
		wire.declarationJSON.ID = identity.PhysicalNodeID(id)
	}
	if len(wire.RouteUpdateIntervalMS) > 0 {
		if bytes.Equal(bytes.TrimSpace(wire.RouteUpdateIntervalMS), []byte("null")) {
			return fmt.Errorf("physicalNode.routeUpdateIntervalMs must not be null")
		}
		if err := json.Unmarshal(wire.RouteUpdateIntervalMS, &wire.declarationJSON.RouteUpdateIntervalMS); err != nil {
			return fmt.Errorf("invalid physicalNode.routeUpdateIntervalMs: %w", err)
		}
	}
	*physical = PhysicalNodeDeclaration(*wire.declarationJSON)
	return nil
}

type physicalNodeRuleSpecs []simulator.RuleSpec
type physicalNodeDeploymentSpecs []simulator.RuleDeploymentSpec

func (rules *physicalNodeRuleSpecs) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("rules must be an array")
	}
	var decoded []simulator.RuleSpec
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*rules = decoded
	return nil
}

func (deployments *physicalNodeDeploymentSpecs) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("deployments must be an array")
	}
	var decoded []simulator.RuleDeploymentSpec
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*deployments = decoded
	return nil
}

var _ PhysicalNodeService = (*SimulatorAdapter)(nil)
var _ PhysicalNodeDeploymentService = (*SimulatorAdapter)(nil)
var _ PhysicalNodeTicketService = (*SimulatorAdapter)(nil)

func physicalNodeSpec(request PhysicalNodeCreateRequest) (simulator.PhysicalNodeSpec, error) {
	if request.PhysicalNode == nil {
		return simulator.PhysicalNodeSpec{}, invalidBody("physicalNode", "physicalNode is required")
	}
	physical := request.PhysicalNode
	if physical.Enabled == nil {
		return simulator.PhysicalNodeSpec{}, invalidBody("physicalNode.enabled", "enabled is required")
	}
	return simulator.PhysicalNodeSpec{
		ID:                    physical.ID,
		Endpoint:              common.Endpoint(physical.Endpoint),
		Enabled:               *physical.Enabled,
		Selector:              simulator.SelectorKind(physical.Selector),
		RouteUpdateIntervalMS: physical.RouteUpdateIntervalMS,
	}, nil
}

func (a *SimulatorAdapter) AddPhysicalNode(ctx context.Context, request PhysicalNodeCreateRequest) error {
	if err := a.check(); err != nil {
		return err
	}
	if request.Deployments != nil {
		return invalidBody("deployments", "deployments requires the rule library endpoint")
	}
	spec, err := physicalNodeSpec(request)
	if err != nil {
		return err
	}
	return adaptRuntimeError(a.runtime.AddPhysicalNode(ctx, spec, request.Rules))
}

func (a *SimulatorAdapter) AddPhysicalNodeFromLibrary(ctx context.Context, request PhysicalNodeCreateRequest) error {
	if err := a.check(); err != nil {
		return err
	}
	if request.Rules != nil || request.Deployments == nil {
		return invalidBody("deployments", "deployments is required and cannot be combined with rules")
	}
	spec, err := physicalNodeSpec(request)
	if err != nil {
		return err
	}
	return adaptRuntimeError(a.runtime.AddPhysicalNodeFromLibrary(ctx, spec, request.Deployments))
}

func (a *SimulatorAdapter) UpdatePhysicalNodeFromLibrary(ctx context.Context, request PhysicalNodeCreateRequest) error {
	if err := a.check(); err != nil {
		return err
	}
	if request.Rules != nil || request.Deployments == nil {
		return invalidBody("deployments", "deployments is required and cannot be combined with rules")
	}
	spec, err := physicalNodeSpec(request)
	if err != nil {
		return err
	}
	return adaptRuntimeError(a.runtime.UpdatePhysicalNodeFromLibrary(ctx, spec, request.Deployments))
}

func (a *SimulatorAdapter) RemovePhysicalNode(ctx context.Context, id string) error {
	if err := a.check(); err != nil {
		return err
	}
	if id == "" {
		return &ServiceError{Status: http.StatusBadRequest, Code: "INVALID_QUERY", Message: "physicalNodeId is required", Path: "physicalNodeId"}
	}
	value, err := parseQueryIdentity(id, "physicalNodeId", true)
	if err != nil {
		return err
	}
	return adaptRuntimeError(a.runtime.RemovePhysicalNode(ctx, identity.PhysicalNodeID(value)))
}

func (a *SimulatorAdapter) ClearPhysicalNodeTickets(ctx context.Context, id string) (ClearPhysicalNodeTicketsResponse, error) {
	if err := a.check(); err != nil {
		return ClearPhysicalNodeTicketsResponse{}, err
	}
	value, err := parseQueryIdentity(id, "physicalNodeId", true)
	if err != nil {
		return ClearPhysicalNodeTicketsResponse{}, err
	}
	removed, err := a.runtime.ClearPhysicalNodeTickets(ctx, identity.PhysicalNodeID(value))
	if err != nil {
		return ClearPhysicalNodeTicketsResponse{}, adaptRuntimeError(err)
	}
	return ClearPhysicalNodeTicketsResponse{RemovedTickets: removed}, nil
}

func (h *Handler) handlePhysicalNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodDelete {
		h.methodNotAllowed(w, r, http.MethodPost, http.MethodPut, http.MethodDelete)
		return
	}
	if err := requireService(h.service); err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	var err error
	switch r.Method {
	case http.MethodPost, http.MethodPut:
		var request PhysicalNodeCreateRequest
		if err := h.decodeJSON(w, r, &request); err != nil {
			return
		}
		if request.PhysicalNode == nil {
			h.writeError(w, r, invalidBody("physicalNode", "physicalNode is required"), r.URL.Path)
			return
		}
		if request.PhysicalNode.Enabled == nil {
			h.writeError(w, r, invalidBody("physicalNode.enabled", "enabled is required"), r.URL.Path)
			return
		}
		if request.Rules != nil && request.Deployments != nil {
			h.writeError(w, r, invalidBody("deployments", "deployments cannot be combined with rules"), r.URL.Path)
			return
		}
		if r.Method == http.MethodPut && (request.Deployments == nil || request.Rules != nil) {
			h.writeError(w, r, invalidBody("deployments", "deployments is required for updates and rules is not supported"), r.URL.Path)
			return
		}
		if request.Deployments != nil {
			service, ok := h.service.(PhysicalNodeDeploymentService)
			if !ok {
				h.writeError(w, r, physicalNodeNotImplemented(), r.URL.Path)
				return
			}
			if r.Method == http.MethodPut {
				err = service.UpdatePhysicalNodeFromLibrary(r.Context(), request)
			} else {
				err = service.AddPhysicalNodeFromLibrary(r.Context(), request)
			}
		} else {
			service, ok := h.service.(PhysicalNodeService)
			if !ok {
				h.writeError(w, r, physicalNodeNotImplemented(), r.URL.Path)
				return
			}
			err = service.AddPhysicalNode(r.Context(), request)
		}
	case http.MethodDelete:
		service, ok := h.service.(PhysicalNodeService)
		if !ok {
			h.writeError(w, r, physicalNodeNotImplemented(), r.URL.Path)
			return
		}
		var id string
		id, err = physicalNodeIDQuery(r)
		if err == nil {
			err = service.RemovePhysicalNode(r.Context(), id)
		}
	}
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handlePhysicalNodeTickets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		h.methodNotAllowed(w, r, http.MethodDelete)
		return
	}
	if err := requireService(h.service); err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	service, ok := h.service.(PhysicalNodeTicketService)
	if !ok {
		h.writeError(w, r, physicalNodeNotImplemented(), r.URL.Path)
		return
	}
	id, err := physicalNodeIDQuery(r)
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	response, err := service.ClearPhysicalNodeTickets(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	h.writeJSON(w, http.StatusOK, response)
}

func physicalNodeIDQuery(r *http.Request) (string, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", &ServiceError{Status: http.StatusBadRequest, Code: "INVALID_QUERY", Message: "query is invalid", Path: "physicalNodeId", Err: err}
	}
	ids := values["physicalNodeId"]
	if len(ids) != 1 || ids[0] == "" {
		return "", &ServiceError{Status: http.StatusBadRequest, Code: "INVALID_QUERY", Message: "physicalNodeId must be provided exactly once and must not be empty", Path: "physicalNodeId"}
	}
	if _, err := parseQueryIdentity(ids[0], "physicalNodeId", true); err != nil {
		return "", err
	}
	return ids[0], nil
}

func physicalNodeNotImplemented() *ServiceError {
	return &ServiceError{Status: http.StatusNotImplemented, Code: "NOT_IMPLEMENTED", Message: "physical node management is not supported by this service"}
}
