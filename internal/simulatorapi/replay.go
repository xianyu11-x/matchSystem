package simulatorapi

import (
	"context"
	"net/http"

	"github.com/xianyu11-x/matchSystem/internal/simulator"
)

// ReplayService 是时间线提取与隔离回放的可选接口。
type ReplayService interface {
	ExtractTimeline(context.Context, simulator.ReplayExtractRequest) (simulator.ReplayExtractResult, error)
	ReplayTimeline(context.Context, simulator.ReplayRequest) (simulator.ReplayResult, error)
}

var _ ReplayService = (*SimulatorAdapter)(nil)

func (a *SimulatorAdapter) ExtractTimeline(ctx context.Context, request simulator.ReplayExtractRequest) (simulator.ReplayExtractResult, error) {
	if err := a.check(); err != nil {
		return simulator.ReplayExtractResult{}, err
	}
	result, err := a.runtime.ExtractTimeline(ctx, request)
	return result, adaptRuntimeError(err)
}

func (a *SimulatorAdapter) ReplayTimeline(ctx context.Context, request simulator.ReplayRequest) (simulator.ReplayResult, error) {
	if err := a.check(); err != nil {
		return simulator.ReplayResult{}, err
	}
	result, err := a.runtime.ReplayTimeline(ctx, request)
	return result, adaptRuntimeError(err)
}

func (h *Handler) replayService(w http.ResponseWriter, r *http.Request) (ReplayService, bool) {
	if r.Method != http.MethodPost {
		h.methodNotAllowed(w, r, http.MethodPost)
		return nil, false
	}
	if err := requireService(h.service); err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return nil, false
	}
	service, ok := h.service.(ReplayService)
	if !ok {
		h.writeError(w, r, &ServiceError{Status: http.StatusNotImplemented, Code: "NOT_IMPLEMENTED", Message: "timeline replay is not supported by this service"}, r.URL.Path)
	}
	return service, ok
}

func (h *Handler) handleReplayExtract(w http.ResponseWriter, r *http.Request) {
	service, ok := h.replayService(w, r)
	if !ok {
		return
	}
	var request simulator.ReplayExtractRequest
	if err := h.decodeJSONWithLimit(w, r, &request, h.replayMaxBodyBytes); err != nil {
		return
	}
	result, err := service.ExtractTimeline(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleReplayRun(w http.ResponseWriter, r *http.Request) {
	service, ok := h.replayService(w, r)
	if !ok {
		return
	}
	var request simulator.ReplayRequest
	if err := h.decodeJSONWithLimit(w, r, &request, h.replayMaxBodyBytes); err != nil {
		return
	}
	result, err := service.ReplayTimeline(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
