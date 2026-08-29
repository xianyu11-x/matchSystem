package simulatorapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMaxBodyBytes int64 = 8 << 20
	defaultPageLimit          = 100
	maxPageLimit              = 1000
)

// HandlerOption changes transport behavior without changing the simulator
// service contract.
type HandlerOption func(*Handler)

// WithAllowedOrigins restricts CORS to the supplied origins. An empty list
// allows every origin with a wildcard response.
func WithAllowedOrigins(origins ...string) HandlerOption {
	return func(handler *Handler) {
		handler.allowedOrigins = append([]string(nil), origins...)
	}
}

func WithAllowCredentials(enabled bool) HandlerOption {
	return func(handler *Handler) { handler.allowCredentials = enabled }
}

func WithMaxBodyBytes(max int64) HandlerOption {
	return func(handler *Handler) {
		if max > 0 {
			handler.maxBodyBytes = max
		}
	}
}

// Handler is the standard-library HTTP transport for the simulator.
type Handler struct {
	service          Service
	maxBodyBytes     int64
	allowedOrigins   []string
	allowCredentials bool
}

// NewHandler constructs an API handler. A nil Service is handled as a
// structured 503 at request time; it is never a fake runtime.
func NewHandler(service Service, options ...HandlerOption) *Handler {
	handler := &Handler{service: service, maxBodyBytes: defaultMaxBodyBytes}
	for _, option := range options {
		if option != nil {
			option(handler)
		}
	}
	return handler
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.applyCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	switch r.URL.Path {
	case "/api/v1/health":
		h.handleHealth(w, r)
	case "/api/v1/capabilities":
		h.handleCapabilities(w, r)
	case "/api/v1/scenario":
		h.handleScenario(w, r)
	case "/api/v1/rules/validate":
		h.handleValidateRule(w, r)
	case "/api/v1/topology":
		h.handleTopology(w, r)
	case "/api/v1/tickets":
		h.handleTickets(w, r)
	case "/api/v1/tickets/custom":
		h.handleCustomTickets(w, r)
	case "/api/v1/tickets/batch":
		h.handleTicketBatch(w, r)
	case "/api/v1/rounds":
		h.handleRounds(w, r)
	case "/api/v1/matches":
		h.handleMatches(w, r)
	case "/api/v1/events":
		h.handleEvents(w, r)
	default:
		if strings.HasPrefix(r.URL.Path, "/api/v1/tickets/") {
			h.handleTicketByID(w, r)
			return
		}
		h.writeError(w, r, &ServiceError{
			Status:  http.StatusNotFound,
			Code:    "NOT_FOUND",
			Message: "endpoint not found",
		}, r.URL.Path)
	}
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.methodNotAllowed(w, r, http.MethodGet)
		return
	}
	if err := requireService(h.service); err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	response, err := h.service.Health(r.Context())
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.methodNotAllowed(w, r, http.MethodGet)
		return
	}
	if err := requireService(h.service); err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	response, err := h.service.Capabilities(r.Context())
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) handleScenario(w http.ResponseWriter, r *http.Request) {
	if err := requireService(h.service); err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	switch r.Method {
	case http.MethodGet:
		response, err := h.service.GetScenario(r.Context())
		if err != nil {
			h.writeError(w, r, err, r.URL.Path)
			return
		}
		h.writeJSON(w, http.StatusOK, response)
	case http.MethodPut:
		var request ScenarioRequest
		if err := h.decodeJSON(w, r, &request); err != nil {
			return
		}
		if err := requireJSONObject(request.Scenario, "scenario"); err != nil {
			h.writeError(w, r, err, r.URL.Path)
			return
		}
		response, err := h.service.ReplaceScenario(r.Context(), request)
		if err != nil {
			h.writeError(w, r, err, r.URL.Path)
			return
		}
		h.writeJSON(w, http.StatusOK, response)
	default:
		h.methodNotAllowed(w, r, http.MethodGet, http.MethodPut)
	}
}

func (h *Handler) handleValidateRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.methodNotAllowed(w, r, http.MethodPost)
		return
	}
	if err := requireService(h.service); err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	var request ValidateRuleRequest
	if err := h.decodeJSON(w, r, &request); err != nil {
		return
	}
	for name, document := range map[string]json.RawMessage{
		"contract": request.Contract, "prefilter": request.Prefilter, "evaluation": request.Evaluation,
	} {
		if err := requireJSONDocument(document, name); err != nil {
			h.writeError(w, r, err, r.URL.Path)
			return
		}
	}
	response, err := h.service.ValidateRule(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) handleTopology(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.methodNotAllowed(w, r, http.MethodGet)
		return
	}
	if err := requireService(h.service); err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	response, err := h.service.Topology(r.Context())
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) handleTickets(w http.ResponseWriter, r *http.Request) {
	if err := requireService(h.service); err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	switch r.Method {
	case http.MethodGet:
		query, err := parseTicketListQuery(r)
		if err != nil {
			h.writeError(w, r, err, r.URL.Path)
			return
		}
		response, err := h.service.ListTickets(r.Context(), query)
		if err != nil {
			h.writeError(w, r, err, r.URL.Path)
			return
		}
		h.writeJSON(w, http.StatusOK, response)
	case http.MethodPost:
		var request TicketCreateRequest
		if err := h.decodeJSON(w, r, &request); err != nil {
			return
		}
		if err := validateTicketCreateRequest(request); err != nil {
			h.writeError(w, r, err, r.URL.Path)
			return
		}
		response, err := h.service.CreateTicket(r.Context(), request)
		if err != nil {
			h.writeError(w, r, err, r.URL.Path)
			return
		}
		h.writeJSON(w, http.StatusCreated, response)
	default:
		h.methodNotAllowed(w, r, http.MethodGet, http.MethodPost)
	}
}

func (h *Handler) handleCustomTickets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.methodNotAllowed(w, r, http.MethodPost)
		return
	}
	if err := requireService(h.service); err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	var request CustomTicketsRequest
	if err := h.decodeJSON(w, r, &request); err != nil {
		return
	}
	if request.Count <= 0 {
		h.writeError(w, r, invalidBody("count", "count must be at least 1"), r.URL.Path)
		return
	}
	if request.Rule.RuleID <= 0 {
		h.writeError(w, r, invalidBody("rule.ruleId", "ruleId must be positive"), r.URL.Path)
		return
	}
	if err := validateGeneratedWireTicketIDs(request.StartTicketID, request.Count); err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	if request.Template != nil {
		if request.Template.TicketID == 0 {
			h.writeError(w, r, invalidBody("template.ticketId", "ticketId must be positive"), r.URL.Path)
			return
		}
		if request.Template.TicketID > MaxWireTicketID {
			h.writeError(w, r, invalidBody("template.ticketId", "ticketId must be at most 9007199254740991 for JSON/JavaScript safety"), r.URL.Path)
			return
		}
	}
	response, err := h.service.CreateCustomTickets(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	h.writeJSON(w, http.StatusCreated, response)
}

func (h *Handler) handleTicketBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.methodNotAllowed(w, r, http.MethodPost)
		return
	}
	if err := requireService(h.service); err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	var request TicketBatchRequest
	if err := h.decodeJSON(w, r, &request); err != nil {
		return
	}
	if request.Tickets == nil {
		if request.Count <= 0 {
			h.writeError(w, r, invalidBody("tickets", "tickets is required or count must be at least 1"), r.URL.Path)
			return
		}
		if err := validateGeneratedWireTicketIDs(request.StartTicketID, request.Count); err != nil {
			h.writeError(w, r, err, r.URL.Path)
			return
		}
	} else {
		if len(request.Tickets) == 0 {
			h.writeError(w, r, invalidBody("tickets", "tickets must contain at least one item"), r.URL.Path)
			return
		}
		if request.Count > 0 {
			h.writeError(w, r, invalidBody("count", "count must be omitted when tickets are provided"), r.URL.Path)
			return
		}
	}
	for index, ticket := range request.Tickets {
		if err := validateTicketCreateRequest(ticket); err != nil {
			err.Path = fmt.Sprintf("tickets[%d].%s", index, err.Path)
			h.writeError(w, r, err, r.URL.Path)
			return
		}
	}
	response, err := h.service.CreateTicketsBatch(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	h.writeJSON(w, http.StatusCreated, response)
}

func (h *Handler) handleTicketByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		h.methodNotAllowed(w, r, http.MethodDelete)
		return
	}
	if err := requireService(h.service); err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	id, err := parseTicketID(r.URL.Path)
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	response, err := h.service.DeleteTicket(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) handleRounds(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.methodNotAllowed(w, r, http.MethodPost)
		return
	}
	if err := requireService(h.service); err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	var request RoundRequest
	if err := h.decodeOptionalJSON(w, r, &request); err != nil {
		return
	}
	if request.MaxMatches < 0 || request.MatchLimit < 0 {
		h.writeError(w, r, invalidBody("maxMatches", "maxMatches must not be negative"), r.URL.Path)
		return
	}
	response, err := h.service.RunRound(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) handleMatches(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.methodNotAllowed(w, r, http.MethodGet)
		return
	}
	if err := requireService(h.service); err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	query, err := parseMatchListQuery(r)
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	response, err := h.service.ListMatches(r.Context(), query)
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.methodNotAllowed(w, r, http.MethodGet)
		return
	}
	if err := requireService(h.service); err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	after := r.URL.Query().Get("after")
	if after == "" {
		after = r.Header.Get("Last-Event-ID")
	}
	events, err := h.service.SubscribeEvents(r.Context(), EventQuery{After: after})
	if err != nil {
		h.writeError(w, r, err, r.URL.Path)
		return
	}
	if events == nil {
		h.writeError(w, r, NewServiceError(http.StatusInternalServerError, "INVALID_EVENT_STREAM", "event stream is nil"), r.URL.Path)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.writeError(w, r, NewServiceError(http.StatusInternalServerError, "SSE_UNSUPPORTED", "response writer does not support SSE"), r.URL.Path)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()

	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepAlive.C:
			_, _ = io.WriteString(w, ": keep-alive\n\n")
			flusher.Flush()
		case event, open := <-events:
			if !open {
				return
			}
			if err := writeSSE(w, event); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSE(w io.Writer, event Event) error {
	if event.ID != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", sanitizeSSEField(event.ID)); err != nil {
			return err
		}
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(payload), "\r\n", "\n"), "\n") {
		if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
			return err
		}
	}
	_, err = io.WriteString(w, "\n")
	return err
}

func sanitizeSSEField(value string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(value)
}

func (h *Handler) decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	if r.Body == nil {
		err := invalidBody("$", "request body is required")
		h.writeError(w, r, err, r.URL.Path)
		return err
	}
	limited := &io.LimitedReader{R: r.Body, N: h.maxBodyBytes + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		serviceErr := invalidBody("$", "invalid JSON body")
		serviceErr.Details = err.Error()
		h.writeError(w, r, serviceErr, r.URL.Path)
		return serviceErr
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		serviceErr := invalidBody("$", "request body must contain exactly one JSON value")
		if err != nil {
			serviceErr.Details = err.Error()
		}
		h.writeError(w, r, serviceErr, r.URL.Path)
		return serviceErr
	}
	if limited.N <= 0 {
		serviceErr := invalidBody("$", "request body exceeds the configured size limit")
		h.writeError(w, r, serviceErr, r.URL.Path)
		return serviceErr
	}
	return nil
}

func (h *Handler) decodeOptionalJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	if r.Body == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, h.maxBodyBytes+1))
	if err != nil {
		serviceErr := invalidBody("$", "failed to read request body")
		serviceErr.Details = err.Error()
		h.writeError(w, r, serviceErr, r.URL.Path)
		return serviceErr
	}
	if int64(len(data)) > h.maxBodyBytes {
		serviceErr := invalidBody("$", "request body exceeds the configured size limit")
		h.writeError(w, r, serviceErr, r.URL.Path)
		return serviceErr
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	return h.decodeJSON(w, r, destination)
}

func (h *Handler) methodNotAllowed(w http.ResponseWriter, r *http.Request, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	h.writeError(w, r, &ServiceError{
		Status:  http.StatusMethodNotAllowed,
		Code:    "METHOD_NOT_ALLOWED",
		Message: "method is not allowed",
	}, r.URL.Path)
}

func (h *Handler) applyCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}
	allowed := len(h.allowedOrigins) == 0
	responseOrigin := "*"
	for _, candidate := range h.allowedOrigins {
		if candidate == "*" || candidate == origin {
			allowed = true
			if candidate != "*" {
				responseOrigin = origin
			}
			break
		}
	}
	if !allowed {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", responseOrigin)
	w.Header().Add("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Last-Event-ID, X-Request-ID")
	w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
	if h.allowCredentials && responseOrigin != "*" {
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		h.writeRawError(w, http.StatusInternalServerError, ErrorResponse{Error: APIError{
			Code: "RESPONSE_ENCODING_ERROR", Message: "failed to encode response",
		}})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error, path string) {
	serviceErr := ensureServiceError(err)
	status := serviceErr.Status
	if errors.Is(err, context.Canceled) {
		status = 499
		serviceErr.Code = "REQUEST_CANCELED"
		serviceErr.Message = "request was canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusGatewayTimeout
		serviceErr.Code = "REQUEST_TIMEOUT"
		serviceErr.Message = "request timed out"
	}
	if status < 100 || status > 599 {
		status = http.StatusInternalServerError
	}
	code := serviceErr.Code
	if code == "" {
		code = "INTERNAL_ERROR"
	}
	message := serviceErr.Message
	if message == "" {
		message = http.StatusText(status)
	}
	if path == "" && r != nil && r.URL != nil {
		path = r.URL.Path
	}
	response := ErrorResponse{Error: APIError{
		Code: code, Message: message, Path: serviceErr.Path,
		Details: serviceErr.Details,
	}}
	if r != nil {
		response.Error.RequestID = r.Header.Get("X-Request-ID")
	}
	if response.Error.Path == "" {
		response.Error.Path = path
	}
	h.writeJSON(w, status, response)
}

func (h *Handler) writeRawError(w http.ResponseWriter, status int, response ErrorResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	data, _ := json.Marshal(response)
	_, _ = w.Write(append(data, '\n'))
}

func requireJSONDocument(document json.RawMessage, path string) error {
	if len(bytes.TrimSpace(document)) == 0 {
		return invalidBody(path, path+" is required")
	}
	if !json.Valid(document) {
		return invalidBody(path, path+" must be valid JSON")
	}
	return nil
}

func requireJSONObject(document json.RawMessage, path string) error {
	if err := requireJSONDocument(document, path); err != nil {
		return err
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(document, &value); err != nil || value == nil {
		return invalidBody(path, path+" must be a JSON object")
	}
	return nil
}

func validateTicketCreateRequest(request TicketCreateRequest) *ServiceError {
	if request.Rule.RuleID <= 0 {
		return invalidBody("rule.ruleId", "ruleId must be positive")
	}
	if request.Ticket.TicketID == 0 {
		return invalidBody("ticket.ticketId", "ticketId must be positive")
	}
	if request.Ticket.TicketID > MaxWireTicketID {
		return invalidBody("ticket.ticketId", "ticketId must be at most 9007199254740991 for JSON/JavaScript safety")
	}
	return nil
}

func parseTicketID(path string) (string, error) {
	value := strings.TrimPrefix(path, "/api/v1/tickets/")
	if value == "" || strings.Contains(value, "/") {
		return "", &ServiceError{Status: 400, Code: "INVALID_TICKET_ID", Message: "ticketId must be a single positive integer", Path: "ticketId"}
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return "", &ServiceError{Status: 400, Code: "INVALID_TICKET_ID", Message: "ticketId must be a single positive integer", Path: "ticketId", Details: value}
	}
	if strings.TrimLeft(value, "0") == "" {
		return "", &ServiceError{Status: 400, Code: "INVALID_TICKET_ID", Message: "ticketId must be positive", Path: "ticketId", Details: value}
	}
	if parsed > MaxWireTicketID {
		return "", &ServiceError{Status: 400, Code: "UNSAFE_TICKET_ID", Message: "ticketId must be at most 9007199254740991 for JSON/JavaScript safety", Path: "ticketId", Details: value}
	}
	return value, nil
}

func parseTicketListQuery(r *http.Request) (TicketListQuery, error) {
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		return TicketListQuery{}, err
	}
	query := r.URL.Query()
	result := TicketListQuery{
		Cursor: query.Get("cursor"), Limit: limit, Search: query.Get("search"),
		State: query.Get("state"), PhysicalNodeID: query.Get("physicalNodeId"),
		RuleNamespace: query.Get("ruleNamespace"),
	}
	if result.State == "" {
		result.State = query.Get("status")
	}
	if value := query.Get("ruleId"); value != "" {
		ruleID, parseErr := strconv.ParseInt(value, 10, 32)
		if parseErr != nil || ruleID <= 0 {
			return TicketListQuery{}, &ServiceError{Status: 400, Code: "INVALID_QUERY", Message: "ruleId must be a positive integer", Path: "ruleId", Details: value}
		}
		result.RuleID = int32(ruleID)
	}
	return result, nil
}

func parseMatchListQuery(r *http.Request) (MatchListQuery, error) {
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		return MatchListQuery{}, err
	}
	return MatchListQuery{Cursor: r.URL.Query().Get("cursor"), Limit: limit}, nil
}

func parseLimit(value string) (int, error) {
	if value == "" {
		return defaultPageLimit, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 || limit > maxPageLimit {
		return 0, &ServiceError{Status: 400, Code: "INVALID_QUERY", Message: fmt.Sprintf("limit must be between 1 and %d", maxPageLimit), Path: "limit", Details: value}
	}
	return limit, nil
}
