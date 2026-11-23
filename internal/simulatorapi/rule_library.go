package simulatorapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/xianyu11-x/matchSystem/internal/simulator"
)

// RuleLibraryService 是规则库查询与条件替换的可选接口。
type RuleLibraryService interface {
	RuleLibrary(context.Context) (simulator.RuleLibrarySnapshot, error)
	ReplaceRuleLibrary(context.Context, uint64, []json.RawMessage) (simulator.RuleLibrarySnapshot, error)
}

type ruleLibraryRequest struct {
	Revision *string              `json:"revision"`
	Rules    ruleLibraryDocuments `json:"rules"`
}

type ruleLibraryDocuments []json.RawMessage

func (rules *ruleLibraryDocuments) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("rules must be an array")
	}
	var decoded []json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*rules = decoded
	return nil
}

var _ RuleLibraryService = (*SimulatorAdapter)(nil)

func (a *SimulatorAdapter) RuleLibrary(ctx context.Context) (simulator.RuleLibrarySnapshot, error) {
	if err := a.check(); err != nil {
		return simulator.RuleLibrarySnapshot{}, err
	}
	snapshot, err := a.runtime.RuleLibrary(ctx)
	return snapshot, adaptRuntimeError(err)
}

func (a *SimulatorAdapter) ReplaceRuleLibrary(ctx context.Context, revision uint64, rules []json.RawMessage) (simulator.RuleLibrarySnapshot, error) {
	if err := a.check(); err != nil {
		return simulator.RuleLibrarySnapshot{}, err
	}
	snapshot, err := a.runtime.ReplaceRuleLibrary(ctx, revision, rules)
	return snapshot, adaptRuntimeError(err)
}

func (h *Handler) handleRuleLibrary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		h.methodNotAllowed(w, r, http.MethodGet, http.MethodPut)
		return
	}
	if err := requireService(h.service); err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	service, ok := h.service.(RuleLibraryService)
	if !ok {
		h.writeError(w, r, &ServiceError{Status: http.StatusNotImplemented, Code: "NOT_IMPLEMENTED", Message: "rule library is not supported by this service"}, r.URL.Path)
		return
	}
	var snapshot simulator.RuleLibrarySnapshot
	var err error
	if r.Method == http.MethodGet {
		snapshot, err = service.RuleLibrary(r.Context())
	} else {
		var request ruleLibraryRequest
		if err := h.decodeJSON(w, r, &request); err != nil {
			return
		}
		if request.Revision == nil || *request.Revision == "" {
			h.writeError(w, r, invalidBody("revision", "revision is required as a decimal string"), r.URL.Path)
			return
		}
		for _, character := range *request.Revision {
			if character < '0' || character > '9' {
				h.writeError(w, r, invalidBody("revision", "revision must be a decimal string"), r.URL.Path)
				return
			}
		}
		revision, parseErr := strconv.ParseUint(*request.Revision, 10, 64)
		if parseErr != nil {
			h.writeError(w, r, invalidBody("revision", "revision must fit uint64"), r.URL.Path)
			return
		}
		if request.Rules == nil {
			h.writeError(w, r, invalidBody("rules", "rules is required as an array"), r.URL.Path)
			return
		}
		for i, rule := range request.Rules {
			if err := requireJSONObject(rule, fmt.Sprintf("rules[%d]", i)); err != nil {
				h.writeError(w, r, err, r.URL.Path)
				return
			}
		}
		snapshot, err = service.ReplaceRuleLibrary(r.Context(), revision, request.Rules)
	}
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	if snapshot.Rules == nil {
		snapshot.Rules = []json.RawMessage{}
	}
	h.writeJSON(w, http.StatusOK, snapshot)
}
