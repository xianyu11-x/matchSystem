package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/client"
	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/identity"
)

func TestScenarioRoutesWithUnifiedMaxUint64IDs(t *testing.T) {
	ctx := context.Background()
	scenario, _ := testScenario()
	max := uint64(math.MaxUint64)
	key := identity.LogicalNodeKey{
		Rule:        identity.RuleKey{Namespace: "high-node", RuleID: max},
		PlacementID: identity.PlacementID(max),
	}
	physical := identity.PhysicalNodeID(max)
	scenario.PhysicalNodes[0].ID = physical
	scenario.Rules[0].LogicalNode = key
	scenario.Rules[0].PhysicalNodeID = physical
	scenario.Rules[0].RuleJSON = bytes.Replace(scenario.Rules[0].RuleJSON,
		[]byte(`"ruleKey":{"namespace":"11495104353665842533","ruleId":1}`),
		[]byte(`"ruleKey":{"namespace":"high-node","ruleId":18446744073709551615}`), 1)
	encoded, err := json.Marshal(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`:18446744073709551615`)) || !bytes.Contains(encoded, []byte(`"ruleId":"18446744073709551615"`)) {
		t.Fatalf("场景身份没有以精确十进制字符串输出: %s", encoded)
	}
	var roundTrip Scenario
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip.Rules[0].LogicalNode != key || roundTrip.Rules[0].PhysicalNodeID != physical {
		t.Fatalf("场景身份往返丢失精度: %+v", roundTrip.Rules[0])
	}
	numericInput := bytes.ReplaceAll(encoded, []byte(`"18446744073709551615"`), []byte(`18446744073709551615`))
	var fromNumbers Scenario
	if err := json.Unmarshal(numericInput, &fromNumbers); err != nil || fromNumbers.Rules[0].LogicalNode != key || fromNumbers.Rules[0].PhysicalNodeID != physical {
		t.Fatalf("无法读取完整 uint64 数值场景: %+v, %v", fromNumbers, err)
	}

	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()
	input := TicketInput{Rule: key.Rule, TicketID: max, AffinityKey: "18446744073709551615", RequestID: "18446744073709551615"}
	added, err := sim.AddTicket(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	wantOwner := identity.OwnerRef{LogicalNode: key, PhysicalNodeID: physical}
	if added.Decision.Owner != wantOwner || added.Decision.DecisionID == 0 {
		t.Fatalf("模拟器未保留路由身份: %+v", added.Decision)
	}
	table, err := client.NewRouteTable(client.RouteTableConfig{
		PhysicalNodes: []client.PhysicalRoute{{
			PhysicalNodeID: physical, Endpoint: scenario.PhysicalNodes[0].Endpoint,
			Enabled: scenario.PhysicalNodes[0].Enabled,
		}},
		Rules: []client.RuleRoute{{
			LogicalNode: key, PhysicalNodeID: physical,
			Weight: scenario.Rules[0].Weight, Enabled: scenario.Rules[0].Enabled,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	router, err := client.NewRouter(table)
	if err != nil {
		t.Fatal(err)
	}
	want, err := router.RouteNew(ctx, common.RouteRequest{Rule: key.Rule, TicketID: max, AffinityKey: max, RequestID: max})
	if err != nil || added.Decision != want {
		t.Fatalf("模拟器和客户端路由结果不同: %+v / %+v, %v", added.Decision, want, err)
	}
	if resolved, err := sim.ResolveOwner(wantOwner); err != nil || resolved.Owner != wantOwner || resolved.Endpoint != added.Decision.Endpoint {
		t.Fatalf("无法解析原始 Owner: %+v, %v", resolved, err)
	}
	unknown := wantOwner
	unknown.LogicalNode.PlacementID--
	if _, err := sim.ResolveOwner(unknown); !errors.Is(err, client.ErrOwnerNotFound) {
		t.Fatalf("未知 Owner 应拒绝: %v", err)
	}
	if _, err := sim.AddTicket(ctx, TicketInput{Rule: key.Rule, TicketID: 1, RequestID: "18446744073709551616"}); err == nil {
		t.Fatal("溢出的十进制 RequestID 未被拒绝")
	}
}

func TestScenarioJSONCanonicalizesOnlyTopLevelRuleKey(t *testing.T) {
	scenario, key := testScenario()
	before := append(json.RawMessage(nil), scenario.Rules[0].RuleJSON...)
	canonical, err := canonicalRuleKeyJSON(before)
	if err != nil {
		t.Fatal(err)
	}
	wireKey, err := json.Marshal(key.Rule)
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Replace(before, []byte(`{"namespace":"11495104353665842533","ruleId":1}`), wireKey, 1)
	if !bytes.Equal(canonical, want) {
		t.Fatalf("规则规范化修改了其他原始内容: %s", canonical)
	}
	encoded, err := json.Marshal(scenario)
	if err != nil || !bytes.Contains(encoded, append([]byte(`"ruleKey":`), wireKey...)) {
		t.Fatalf("场景规则键未规范化: %s, %v", encoded, err)
	}
	numeric := bytes.ReplaceAll(encoded, wireKey, []byte(`{"namespace":"11495104353665842533","ruleId":1}`))
	var decoded Scenario
	if err := json.Unmarshal(numeric, &decoded); err != nil || decoded.Rules[0].LogicalNode != key || decoded.Rules[0].PhysicalNodeID != testPhysicalID("p1") {
		t.Fatalf("数字身份无法解析: %+v, %v", decoded, err)
	}
	for _, invalidID := range []string{`"p1"`, `"01"`} {
		invalid := bytes.ReplaceAll(encoded, []byte(`"`+strconv.FormatUint(uint64(testPhysicalID("p1")), 10)+`"`), []byte(invalidID))
		if err := json.Unmarshal(invalid, &Scenario{}); err == nil {
			t.Fatalf("场景错误接受身份 %s", invalidID)
		}
	}
	decoded.Rules[0].ObjectFactProvider = scenario.Rules[0].ObjectFactProvider
	sim, err := NewSimulator(decoded)
	if err != nil {
		t.Fatalf("数字身份场景无法构造模拟器: %v", err)
	}
	defer sim.Close()
	decision, err := sim.RouteNew(context.Background(), common.RouteRequest{Rule: key.Rule, TicketID: 1})
	if err != nil || decision.Owner.LogicalNode != key {
		t.Fatalf("数字身份场景无法路由: %+v, %v", decision, err)
	}
	for _, invalid := range []string{
		`{"logicalNode":{"rule":{"ruleId":1,"unexpected":true},"placementId":"1"}}`,
		`{"logicalNode":{"rule":{"ruleId":1},"placementId":"1","unexpected":true}}`,
	} {
		if err := json.Unmarshal([]byte(invalid), &RuleSpec{}); err == nil {
			t.Fatalf("嵌套未知字段未被拒绝: %s", invalid)
		}
	}
}
