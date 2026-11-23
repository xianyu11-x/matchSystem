package simulatorapi

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/xianyu11-x/matchSystem/internal/simulator"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

// 覆盖浏览器安全整数以上的身份，确保相邻值不因 JSON 精度而合并。
func TestHTTPIdentityUint64Boundary(t *testing.T) {
	const maximum uint64 = math.MaxUint64
	const previous uint64 = maximum - 1
	maxText := strconv.FormatUint(maximum, 10)
	previousText := strconv.FormatUint(previous, 10)
	const alias = "实际/别名:0"
	rules := []simulator.RuleSpec{
		simulator.NewRuleSpec(identity.LogicalNodeKey{
			Rule: identity.RuleKey{Namespace: alias, RuleID: maximum}, PlacementID: identity.PlacementID(maximum),
		}, identity.PhysicalNodeID(maximum), apiRuleJSON(alias, maximum)),
		simulator.NewRuleSpec(identity.LogicalNodeKey{
			Rule: identity.RuleKey{Namespace: alias, RuleID: previous}, PlacementID: identity.PlacementID(previous),
		}, identity.PhysicalNodeID(previous), apiRuleJSON(alias, previous)),
	}
	runtime, err := simulator.NewSimulator(simulator.Scenario{
		PhysicalNodes: []simulator.PhysicalNodeSpec{
			simulator.NewPhysicalNodeSpec(identity.PhysicalNodeID(maximum), "inproc://max"),
			simulator.NewPhysicalNodeSpec(identity.PhysicalNodeID(previous), "inproc://previous"),
		},
		Rules: rules,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	defer server.Close()

	for _, test := range []struct {
		id        string
		rule      string
		placement string
	}{
		{"1", `"` + maxText + `"`, `"` + maxText + `"`},
		{"2", previousText, previousText},
		{"3", maxText, maxText},
	} {
		body := `{"ticket":{"ticketId":` + test.id + `,"createdAt":100},"rule":{"namespace":"请求别名","ruleId":` + test.rule + `},"placementId":` + test.placement + `}`
		response := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/v1/tickets", body)
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusCreated || !strings.Contains(string(data), `"ruleId":"`+strings.Trim(test.rule, `"`)+`"`) || !strings.Contains(string(data), `"placementId":"`+strings.Trim(test.placement, `"`)+`"`) || !strings.Contains(string(data), `"namespace":"`+alias+`"`) {
			t.Fatalf("创建 Ticket：status=%d body=%s", response.StatusCode, data)
		}
	}

	response, err := server.Client().Get(server.URL + "/api/v1/tickets?ruleNamespace=ignored%2Falias&ruleId=" + maxText + "&physicalNodeId=" + maxText)
	if err != nil {
		t.Fatal(err)
	}
	var page TicketPage
	decodeResponse(t, response, &page)
	if len(page.Items) != 2 || page.Items[0].Owner.PhysicalNodeID != identity.PhysicalNodeID(maximum) || page.Items[1].Owner.PhysicalNodeID != identity.PhysicalNodeID(maximum) {
		t.Fatalf("最大身份筛选结果错误：%#v", page)
	}
	response, err = server.Client().Get(server.URL + "/api/v1/logical-nodes/facts?ruleNamespace=" + maxText + "&ruleId=" + maxText + "&placementId=" + maxText)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("最大身份查询 Facts：status=%d body=%s", response.StatusCode, data)
	}
	var facts LogicalNodeFactsResponse
	if err := json.NewDecoder(response.Body).Decode(&facts); err != nil || facts.LogicalNode.Rule.RuleID != maximum || facts.LogicalNode.Rule.Namespace != alias {
		t.Fatalf("Facts 身份错误：%#v err=%v", facts.LogicalNode, err)
	}
	for _, target := range []string{
		"/api/v1/tickets?ruleId=18446744073709551616",
		"/api/v1/tickets?physicalNodeId=18446744073709551616",
		"/api/v1/logical-nodes/facts?ruleId=" + maxText + "&placementId=18446744073709551616",
	} {
		response, err := server.Client().Get(server.URL + target)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("超出 uint64 的查询 %s：status=%d", target, response.StatusCode)
		}
	}
}

func TestHTTPRuleAliasDoesNotAffectRoutingOrQueries(t *testing.T) {
	runtime, err := simulator.NewSimulator(apiScenario())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	defer server.Close()
	client := server.Client()
	create := func(alias, ruleID string) TicketView {
		t.Helper()
		body := `{"ticket":{"ticketId":41,"createdAt":100},"rule":{"namespace":` + strconv.Quote(alias) + `,"ruleId":` + ruleID + `},"affinityKey":"123"}`
		response := doJSON(t, client, http.MethodPost, server.URL+"/api/v1/tickets", body)
		if response.StatusCode != http.StatusCreated {
			data, _ := io.ReadAll(response.Body)
			response.Body.Close()
			t.Fatalf("创建请求失败: status=%d body=%s", response.StatusCode, data)
		}
		var view TicketView
		decodeResponse(t, response, &view)
		return view
	}
	first := create("api", `"1"`)
	removed := doJSON(t, client, http.MethodDelete, server.URL+"/api/v1/tickets/41", "")
	var deleted DeleteTicketResponse
	decodeResponse(t, removed, &deleted)
	if !deleted.Deleted {
		t.Fatal("删除首次创建的 Ticket 失败")
	}
	second := create("其他/别名:01", `1`)
	if first.Route == nil || second.Route == nil || first.Route.DecisionID != second.Route.DecisionID || first.Owner != second.Owner || second.Owner.LogicalNode.Rule.Namespace != "api" {
		t.Fatalf("规则别名影响路由或响应未使用实际部署别名: first=%#v second=%#v", first, second)
	}
	response, err := client.Get(server.URL + "/api/v1/tickets?ruleId=1&ruleNamespace=not%2Fa%2Fnumber")
	if err != nil {
		t.Fatal(err)
	}
	var page TicketPage
	decodeResponse(t, response, &page)
	if len(page.Items) != 1 || page.Items[0].Ticket.TicketID != 41 || page.Items[0].Owner.LogicalNode.Rule.Namespace != "api" {
		t.Fatalf("旧查询参数影响规则筛选: %#v", page)
	}
	body := `{"ticket":{"ticketId":42},"rule":{"namespace":"不同别名","ruleId":1},"placementId":"17745680053102999587"}`
	response = doJSON(t, client, http.MethodPost, server.URL+"/api/v1/tickets", body)
	var exact TicketView
	decodeResponse(t, response, &exact)
	if exact.Owner.PhysicalNodeID != testPhysical("p1") || exact.Owner.LogicalNode.Rule.Namespace != "api" {
		t.Fatalf("指定部署未忽略请求别名: %#v", exact.Owner)
	}
}

func TestRequestIdentityDecodeForms(t *testing.T) {
	const maximum = uint64(math.MaxUint64)
	maxText := strconv.FormatUint(maximum, 10)
	for _, encoded := range []string{maxText, strconv.Quote(maxText)} {
		rule := `{"namespace":"0/别名:1","ruleId":` + encoded + `}`
		fields := `"rule":` + rule + `,"placementId":` + encoded
		var ticket TicketCreateRequest
		if err := json.Unmarshal([]byte(`{"ticket":{"ticketId":1},`+fields+`}`), &ticket); err != nil {
			t.Fatalf("单条身份 %s: %v", encoded, err)
		}
		if ticket.Rule.RuleID != maximum || ticket.Rule.Namespace != "0/别名:1" || uint64(ticket.PlacementID) != maximum {
			t.Fatalf("单条身份丢失精度: %#v", ticket)
		}
		var custom CustomTicketsRequest
		if err := json.Unmarshal([]byte(`{"count":1,`+fields+`}`), &custom); err != nil || custom.Rule.RuleID != maximum || uint64(custom.PlacementID) != maximum {
			t.Fatalf("自定义批量身份 %s: %#v, %v", encoded, custom, err)
		}
		var batch TicketBatchRequest
		if err := json.Unmarshal([]byte(`{"count":1,`+fields+`}`), &batch); err != nil || batch.Rule.RuleID != maximum || uint64(batch.PlacementID) != maximum {
			t.Fatalf("批量身份 %s: %#v, %v", encoded, batch, err)
		}
		if err := json.Unmarshal([]byte(`{"tickets":[{"ticket":{"ticketId":1},`+fields+`}]} `), &batch); err != nil || len(batch.Tickets) != 1 || batch.Tickets[0].Rule.RuleID != maximum || uint64(batch.Tickets[0].PlacementID) != maximum {
			t.Fatalf("批量内单条身份 %s: %#v, %v", encoded, batch, err)
		}
		var traffic TrafficRequest
		if err := json.Unmarshal([]byte(`{"generator":{`+fields+`}}`), &traffic); err != nil || traffic.Generator.Rule.RuleID != maximum || uint64(traffic.Generator.PlacementID) != maximum {
			t.Fatalf("持续流量身份 %s: %#v, %v", encoded, traffic, err)
		}
		var physical PhysicalNodeDeclaration
		if err := json.Unmarshal([]byte(`{"id":`+encoded+`,"enabled":true}`), &physical); err != nil || uint64(physical.ID) != maximum {
			t.Fatalf("物理节点身份 %s: %#v, %v", encoded, physical, err)
		}
		output, err := json.Marshal(ticket)
		if err != nil || !strings.Contains(string(output), `"ruleId":"`+maxText+`"`) || !strings.Contains(string(output), `"placementId":"`+maxText+`"`) {
			t.Fatalf("身份输出不是规范字符串: %s, %v", output, err)
		}
	}
}

func TestHTTPRejectsOverflowingRoutingIdentity(t *testing.T) {
	runtime, err := simulator.NewSimulator(apiScenario())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	defer server.Close()

	for _, test := range []struct{ path, body, field string }{
		{"/api/v1/tickets", `{"ticket":{"ticketId":1},"rule":{"namespace":"api","ruleId":1},"affinityKey":"18446744073709551616"}`, "affinityKey"},
		{"/api/v1/tickets", `{"ticket":{"ticketId":1},"rule":{"namespace":"api","ruleId":1},"placementId":"17745680053102999587","affinityKey":"18446744073709551616"}`, "affinityKey"},
		{"/api/v1/tickets/custom", `{"count":1,"rule":{"namespace":"api","ruleId":1},"affinityPrefix":"18446744073709551615"}`, "affinityPrefix"},
		{"/api/v1/tickets/batch", `{"count":1,"rule":{"namespace":"api","ruleId":1},"requestIdPrefix":"18446744073709551615"}`, "requestIdPrefix"},
		{"/api/v1/traffic", `{"config":{"distribution":"constant","rate":1,"matchIntervalMs":100,"maxMatches":1},"generator":{"rule":{"namespace":"api","ruleId":1},"affinityPrefix":"18446744073709551615"}}`, "generator.affinityPrefix"},
	} {
		response := doJSON(t, server.Client(), http.MethodPost, server.URL+test.path, test.body)
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), test.field) {
			t.Fatalf("%s 身份溢出：status=%d body=%s", test.path, response.StatusCode, data)
		}
	}
}

func TestHTTPRejectsNoncanonicalIdentities(t *testing.T) {
	runtime, err := simulator.NewSimulator(apiScenario())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	server := httptest.NewServer(NewHandler(NewSimulatorAdapter(runtime)))
	defer server.Close()

	for _, test := range []struct{ path, body, field string }{
		{"/api/v1/tickets", `{"ticket":{"ticketId":1},"rule":{"namespace":1,"ruleId":1}}`, "$"},
		{"/api/v1/tickets", `{"ticket":{"ticketId":1},"rule":{"namespace":null,"ruleId":1}}`, "$"},
		{"/api/v1/tickets", `{"ticket":{"ticketId":1},"rule":{"namespace":"api","ruleId":"01"}}`, "$"},
		{"/api/v1/tickets", `{"ticket":{"ticketId":1},"rule":{"ruleId":1,"unexpected":true}}`, "$"},
		{"/api/v1/tickets", `{"ticket":{"ticketId":1},"rule":{"ruleId":1},"unexpected":true}`, "$"},
		{"/api/v1/tickets", `{"ticket":{"ticketId":1},"rule":{"ruleId":1},"placementId":"01"}`, "$"},
		{"/api/v1/tickets", `{"ticket":{"ticketId":1},"rule":{"namespace":"api","ruleId":1},"placementId":"p1"}`, "$"},
		{"/api/v1/tickets", `{"ticket":{"ticketId":1},"rule":{"namespace":"api","ruleId":1},"affinityKey":"01"}`, "affinityKey"},
		{"/api/v1/tickets", `{"ticket":{"ticketId":1},"rule":{"namespace":"api","ruleId":1},"affinityKey":"legacy"}`, "affinityKey"},
		{"/api/v1/tickets/custom", `{"count":1,"rule":{"namespace":"api","ruleId":1},"affinityPrefix":"route-"}`, "affinityPrefix"},
		{"/api/v1/tickets/custom", `{"count":1,"rule":{"ruleId":"01"}}`, "$"},
		{"/api/v1/tickets/custom", `{"count":1,"rule":{"ruleId":1},"unexpected":true}`, "$"},
		{"/api/v1/tickets/batch", `{"count":1,"rule":{"namespace":"api","ruleId":1},"requestIdPrefix":"0"}`, "requestIdPrefix"},
		{"/api/v1/tickets/batch", `{"count":1,"rule":{"ruleId":1},"placementId":"01"}`, "$"},
		{"/api/v1/tickets/batch", `{"tickets":[{"ticket":{"ticketId":1},"rule":{"ruleId":"01"}}]}`, "$"},
		{"/api/v1/tickets/batch", `{"count":3,"startTicketId":14,"rule":{"namespace":"api","ruleId":1},"requestIdPrefix":"184467440737095516"}`, "requestIdPrefix"},
		{"/api/v1/traffic", `{"config":{"distribution":"constant","rate":1,"matchIntervalMs":100,"maxMatches":1},"generator":{"rule":{"namespace":"api","ruleId":1},"affinityPrefix":"0"}}`, "generator.affinityPrefix"},
		{"/api/v1/traffic", `{"config":{},"generator":{"rule":{"ruleId":"01"}}}`, "$"},
		{"/api/v1/physical-nodes", `{"physicalNode":{"id":"01","endpoint":"inproc://1","enabled":true}}`, "$"},
		{"/api/v1/physical-nodes", `{"physicalNode":{"id":1,"endpoint":"inproc://1","enabled":true,"unexpected":true}}`, "$"},
	} {
		response := doJSON(t, server.Client(), http.MethodPost, server.URL+test.path, test.body)
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), test.field) {
			t.Fatalf("%s 错误接受身份：status=%d body=%s", test.path, response.StatusCode, data)
		}
	}
	for _, target := range []string{
		"/api/v1/tickets?physicalNodeId=01",
		"/api/v1/tickets?ruleId=01",
		"/api/v1/logical-nodes/facts?ruleId=01&placementId=1",
		"/api/v1/logical-nodes/facts?ruleId=1&placementId=p1",
		"/api/v1/logical-nodes/facts?ruleId=1&placementId=%201%20",
	} {
		response, err := server.Client().Get(server.URL + target)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("查询 %s 错误接受身份：status=%d", target, response.StatusCode)
		}
	}
	for _, id := range []string{"legacy", "01", "0"} {
		response := doJSON(t, server.Client(), http.MethodDelete, server.URL+"/api/v1/physical-nodes?physicalNodeId="+id, "")
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("删除物理节点错误接受身份 %q：status=%d", id, response.StatusCode)
		}
	}
	response, err := server.Client().Get(server.URL + "/api/v1/tickets")
	if err != nil {
		t.Fatal(err)
	}
	var page TicketPage
	decodeResponse(t, response, &page)
	if page.Total != 0 {
		t.Fatalf("无效请求留下 Ticket: %+v", page)
	}
}
