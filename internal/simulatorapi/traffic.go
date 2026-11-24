package simulatorapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/xianyu11-x/matchSystem/internal/simulator"
)

type TrafficRequest struct {
	Config    simulator.TrafficConfig `json:"config"`
	Generator CustomTicketsRequest    `json:"generator"`
}
type TrafficService interface {
	Traffic(context.Context) (simulator.TrafficStatus, error)
	StartTraffic(context.Context, TrafficRequest) (simulator.TrafficStatus, error)
	StopTraffic(context.Context) (simulator.TrafficStatus, error)
}

func (a *SimulatorAdapter) Traffic(ctx context.Context) (simulator.TrafficStatus, error) {
	if err := a.check(); err != nil {
		return simulator.TrafficStatus{}, err
	}
	return a.runtime.Traffic(), nil
}
func (a *SimulatorAdapter) StopTraffic(ctx context.Context) (simulator.TrafficStatus, error) {
	if err := a.check(); err != nil {
		return simulator.TrafficStatus{}, err
	}
	return a.runtime.StopTraffic(), nil
}
func (a *SimulatorAdapter) StartTraffic(ctx context.Context, request TrafficRequest) (simulator.TrafficStatus, error) {
	if err := a.check(); err != nil {
		return simulator.TrafficStatus{}, err
	}
	if request.Generator.StartTicketID > MaxWireTicketID {
		return simulator.TrafficStatus{}, invalidBody("generator.startTicketId", "TicketID exceeds safe integer range")
	}
	firstID := request.Generator.StartTicketID
	if firstID == 0 {
		firstID = 1
	}
	for _, field := range []struct{ prefix, path string }{
		{request.Generator.AffinityPrefix, "generator.affinityPrefix"},
		{request.Generator.RequestIDPrefix, "generator.requestIdPrefix"},
	} {
		if field.prefix == "" {
			continue
		}
		if err := validateRouteIdentity(field.prefix+strconv.FormatUint(firstID, 10), field.path); err != nil {
			return simulator.TrafficStatus{}, err
		}
	}
	spec := generatorSpec(request.Generator)
	var status simulator.TrafficStatus
	var err error
	if request.Generator.PlacementID != 0 {
		status, err = a.runtime.StartTrafficAtPlacement(request.Config, spec, request.Generator.PlacementID)
	} else {
		status, err = a.runtime.StartTraffic(request.Config, spec)
	}
	return status, adaptRuntimeError(err)
}
func (h *Handler) handleTraffic(w http.ResponseWriter, r *http.Request) {
	service, ok := h.service.(TrafficService)
	if !ok {
		h.writeError(w, r, &ServiceError{Status: 503, Code: "UNAVAILABLE", Message: "traffic service unavailable"}, r.URL.Path)
		return
	}
	var status simulator.TrafficStatus
	var err error
	switch r.Method {
	case http.MethodGet:
		status, err = service.Traffic(r.Context())
	case http.MethodDelete:
		status, err = service.StopTraffic(r.Context())
	case http.MethodPost:
		// 与显式零区分：缺失或 null 保留非法哨兵，仍由服务端校验拒绝。
		request := TrafficRequest{Config: simulator.TrafficConfig{MaxMatches: -1}}
		if decodeErr := h.decodeJSON(w, r, &request); decodeErr != nil {
			return
		}
		status, err = service.StartTraffic(r.Context(), request)
	default:
		h.methodNotAllowed(w, r, http.MethodGet, http.MethodPost, http.MethodDelete)
		return
	}
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	h.writeJSON(w, http.StatusOK, status)
}
