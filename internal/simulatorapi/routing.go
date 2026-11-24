package simulatorapi

import (
	"context"
	"net/http"

	"github.com/xianyu11-x/matchSystem/internal/simulator"
)

// RoutingService 是独立更新客户端路由配置的可选服务接口。
type RoutingService interface {
	UpdateRouting(context.Context, simulator.RoutingConfig) (simulator.RoutingConfig, error)
}

var _ RoutingService = (*SimulatorAdapter)(nil)

// UpdateRouting 返回当前请求成功提交的路由配置。
func (a *SimulatorAdapter) UpdateRouting(ctx context.Context, config simulator.RoutingConfig) (simulator.RoutingConfig, error) {
	if err := a.check(); err != nil {
		return simulator.RoutingConfig{}, err
	}
	committed, err := a.runtime.UpdateRouting(ctx, config)
	return committed, adaptRuntimeError(err)
}

func (h *Handler) handleRouting(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		h.methodNotAllowed(w, r, http.MethodPut)
		return
	}
	if err := requireService(h.service); err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	service, ok := h.service.(RoutingService)
	if !ok {
		h.writeError(w, r, &ServiceError{Status: http.StatusNotImplemented, Code: "NOT_IMPLEMENTED", Message: "routing configuration is not supported by this service"}, r.URL.Path)
		return
	}
	var request simulator.RoutingConfig
	if err := h.decodeJSON(w, r, &request); err != nil {
		return
	}
	response, err := service.UpdateRouting(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	h.writeJSON(w, http.StatusOK, response)
}
