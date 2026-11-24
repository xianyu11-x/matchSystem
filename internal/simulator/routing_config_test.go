package simulator

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestRoutingConfigValidation(t *testing.T) {
	tests := []struct {
		name   string
		config RoutingConfig
		path   string
	}{
		{"默认路由", RoutingConfig{}, ""},
		{"显式加权 Rendezvous", RoutingConfig{Algorithm: RoutingWeightedRendezvous}, ""},
		{"队列加权随机", RoutingConfig{Algorithm: RoutingQueueWeightedRandom}, ""},
		{"有效优先阈值", RoutingConfig{Algorithm: RoutingWeightedPriority, TicketLimit: 1}, ""},
		{"其他算法允许非零阈值", RoutingConfig{Algorithm: RoutingQueueWeightedRandom, TicketLimit: 5}, ""},
		{"未知算法", RoutingConfig{Algorithm: "unknown"}, "$.routing.algorithm"},
		{"负数阈值", RoutingConfig{TicketLimit: -1}, "$.routing.ticketLimit"},
		{"缺少优先阈值", RoutingConfig{Algorithm: RoutingWeightedPriority}, "$.routing.ticketLimit"},
		{"负数优先阈值", RoutingConfig{Algorithm: RoutingWeightedPriority, TicketLimit: -1}, "$.routing.ticketLimit"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := ValidateRoutingConfig(test.config)
			if test.path == "" {
				if !report.Valid {
					t.Fatalf("有效配置被拒绝: %+v", report)
				}
				return
			}
			if report.Valid || len(report.Issues) != 1 || report.Issues[0].Path != test.path {
				t.Fatalf("校验结果不符合预期: %+v", report)
			}
			scenario, _ := testScenario()
			scenario.Routing = test.config
			if report := ValidateScenario(scenario); report.Valid || !hasIssueCode(report.Issues, reportIssueCode(test.path)) {
				t.Fatalf("场景未报告路由配置错误: %+v", report)
			}
		})
	}
}

func reportIssueCode(path string) string {
	if path == "$.routing.algorithm" {
		return "UNKNOWN_ROUTING_ALGORITHM"
	}
	return "INVALID_TICKET_LIMIT"
}

func TestRouteUpdateIntervalValidation(t *testing.T) {
	for _, test := range []struct {
		value int64
		valid bool
	}{
		{0, true}, {1, true}, {10000, true}, {9223372036854, true},
		{-1, false}, {9223372036855, false},
	} {
		t.Run(fmt.Sprint(test.value), func(t *testing.T) {
			scenario, _ := testScenario()
			scenario.PhysicalNodes[0].RouteUpdateIntervalMS = test.value
			report := ValidateScenario(scenario)
			if report.Valid != test.valid {
				t.Fatalf("间隔 %d 的校验结果不正确: %+v", test.value, report)
			}
			if !test.valid && (len(report.Issues) != 1 || report.Issues[0].Path != "$.physicalNodes[0].routeUpdateIntervalMs" || report.Issues[0].Code != "INVALID_ROUTE_UPDATE_INTERVAL") {
				t.Fatalf("错误路径不正确: %+v", report.Issues)
			}
		})
	}
}

func TestScenarioRoutingJSONRoundTripAndStrictDecoding(t *testing.T) {
	scenario := Scenario{
		PhysicalNodes: []PhysicalNodeSpec{NewPhysicalNodeSpec(1, "inproc://1")},
		Routing:       RoutingConfig{Algorithm: RoutingWeightedPriority, TicketLimit: 42},
	}
	scenario.PhysicalNodes[0].RouteUpdateIntervalMS = 1234
	data, err := json.Marshal(scenario)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"routing":{"algorithm":"weighted_priority","ticketLimit":42}`, `"routeUpdateIntervalMs":1234`} {
		if !strings.Contains(string(data), value) {
			t.Fatalf("场景 JSON 未保留配置 %s: %s", value, data)
		}
	}
	decoded, err := ParseScenarioJSON(data)
	if err != nil || decoded.Routing != scenario.Routing || decoded.PhysicalNodes[0].RouteUpdateIntervalMS != 1234 || decoded.Clone().Routing != scenario.Routing {
		t.Fatalf("场景配置往返失败: %+v, %v", decoded, err)
	}
	for _, field := range []string{
		`"routing":null`, `"routing":{"unknown":1}`, `"routing":{"algorithm":null}`,
		`"routing":{"ticketLimit":null}`, `"routing":{"ticketLimit":1.5}`,
		`"routing":{"ticketLimit":"10"}`, `"routing":{"algorithm":5}`, `"routing":{"Algorithm":"weighted_rendezvous"}`,
	} {
		input := `{"schemaVersion":"simulator-scenario/v1","physicalNodes":[],"rules":[],` + field + `}`
		if _, err := ParseScenarioJSON([]byte(input)); err == nil {
			t.Fatalf("错误路由 JSON 未被拒绝: %s", input)
		}
	}
	for _, interval := range []string{"null", "1.5", `"10"`} {
		input := `{"schemaVersion":"simulator-scenario/v1","physicalNodes":[{"id":"1","endpoint":"inproc://1","enabled":true,"routeUpdateIntervalMs":` + interval + `}],"rules":[]}`
		if _, err := ParseScenarioJSON([]byte(input)); err == nil {
			t.Fatalf("错误节点间隔未被拒绝: %s", input)
		}
	}
}
