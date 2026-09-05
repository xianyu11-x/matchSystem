package simulatorapi

import (
	"context"
	"matchSystem/internal/simulator"
	"net/http"
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
	if request.Generator.PlacementID != "" {
		return simulator.TrafficStatus{}, invalidBody("generator.placementId", "traffic routes by rule; placementId is unsupported")
	}
	status, err := a.runtime.StartTraffic(request.Config, generatorSpec(request.Generator))
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
		var request TrafficRequest
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
