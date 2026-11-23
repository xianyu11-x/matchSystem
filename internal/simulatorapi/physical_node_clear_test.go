package simulatorapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xianyu11-x/matchSystem/internal/simulator"
	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

type clearPhysicalNodeFakeService struct {
	fakeService
	id string
}

func (f *clearPhysicalNodeFakeService) ClearPhysicalNodeTickets(_ context.Context, id string) (ClearPhysicalNodeTicketsResponse, error) {
	f.id = id
	return ClearPhysicalNodeTicketsResponse{RemovedTickets: 7}, f.err
}

func TestClearPhysicalNodeTicketsHTTPValidation(t *testing.T) {
	service := &clearPhysicalNodeFakeService{}
	handler := NewHandler(service)
	const valid = "/api/v1/physical-nodes/tickets?physicalNodeId=17745680053102999587"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, valid, nil))
	var result ClearPhysicalNodeTicketsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || recorder.Code != http.StatusOK || result.RemovedTickets != 7 || service.id != "17745680053102999587" {
		t.Fatalf("清空响应不正确: status=%d body=%s id=%s err=%v", recorder.Code, recorder.Body.String(), service.id, err)
	}
	for _, test := range []struct {
		name, method, path string
		status             int
	}{
		{"缺失", http.MethodDelete, "/api/v1/physical-nodes/tickets", http.StatusBadRequest},
		{"空值", http.MethodDelete, "/api/v1/physical-nodes/tickets?physicalNodeId=", http.StatusBadRequest},
		{"零值", http.MethodDelete, "/api/v1/physical-nodes/tickets?physicalNodeId=0", http.StatusBadRequest},
		{"非数字", http.MethodDelete, "/api/v1/physical-nodes/tickets?physicalNodeId=node", http.StatusBadRequest},
		{"重复", http.MethodDelete, "/api/v1/physical-nodes/tickets?physicalNodeId=1&physicalNodeId=2", http.StatusBadRequest},
		{"无效查询", http.MethodDelete, "/api/v1/physical-nodes/tickets?physicalNodeId=1;bad=1", http.StatusBadRequest},
		{"无效方法", http.MethodGet, valid, http.StatusMethodNotAllowed},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
			if recorder.Code != test.status {
				t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), test.status)
			}
		})
	}
	for _, test := range []struct {
		name    string
		service Service
		status  int
	}{
		{"旧接口", &fakeService{}, http.StatusNotImplemented},
		{"空接口", nil, http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			NewHandler(test.service).ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, valid, nil))
			if recorder.Code != test.status {
				t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), test.status)
			}
		})
	}
}

func TestClearPhysicalNodeTicketsRuntimeHTTP(t *testing.T) {
	runtime, err := simulator.NewSimulator(apiScenario())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	ctx := context.Background()
	rule := identity.RuleKey{Namespace: "api", RuleID: 1}
	owners := []identity.OwnerRef{
		{PhysicalNodeID: testPhysical("p1"), LogicalNode: identity.LogicalNodeKey{Rule: rule, PlacementID: testPlacement("p1")}},
		{PhysicalNodeID: testPhysical("p2"), LogicalNode: identity.LogicalNodeKey{Rule: rule, PlacementID: testPlacement("p2")}},
	}
	for _, owner := range owners {
		if _, err := runtime.AddRoutedTicket(ctx, common.RouteDecision{Owner: owner}, simulator.TicketInput{Rule: rule, TicketID: 1}); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewHandler(NewSimulatorAdapter(runtime))
	path := fmt.Sprintf("/api/v1/physical-nodes/tickets?physicalNodeId=%d", owners[0].PhysicalNodeID)
	for _, want := range []int{1, 0} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, path, nil))
		var result ClearPhysicalNodeTicketsResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || recorder.Code != http.StatusOK || result.RemovedTickets != want {
			t.Fatalf("清空响应不正确: status=%d body=%s err=%v want=%d", recorder.Code, recorder.Body.String(), err, want)
		}
	}
	page, err := runtime.ListTickets(ctx, simulator.TicketQuery{})
	if err != nil || page.Total != 1 || page.Items[0].Owner != owners[1] {
		t.Fatalf("其他节点的同 ID Ticket 被清理: %+v %v", page, err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/physical-nodes/tickets?physicalNodeId=1", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("未知节点 status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
