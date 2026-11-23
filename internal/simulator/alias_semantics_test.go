package simulator

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAliasRoutingAndObservationUseRuleID(t *testing.T) {
	ctx := context.Background()
	scenario, key := testScenario()
	sim, err := NewSimulator(scenario)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()

	other := key.Rule
	other.Namespace = "other/别名:0"
	view, err := sim.AddTicket(ctx, TicketInput{Rule: other, TicketID: 42})
	if err != nil || view.Owner.LogicalNode.Rule.Namespace != key.Rule.Namespace {
		t.Fatalf("不同别名未路由到原部署: view=%+v err=%v", view, err)
	}
	lookup := view.Owner
	lookup.LogicalNode.Rule.Namespace = "another"
	if _, found, err := sim.GetTicket(ctx, lookup, 42); err != nil || !found {
		t.Fatalf("不同别名无法查询已有 Ticket: found=%v err=%v", found, err)
	}
	if _, err := sim.AddTicket(ctx, TicketInput{Rule: other, TicketID: 42}); !errors.Is(err, ErrTicketAlreadyObserved) {
		t.Fatalf("不同别名重复 Ticket 未被拒绝: %v", err)
	}
	page, err := sim.ListTickets(ctx, TicketQuery{Rule: &other})
	if err != nil || page.Total != 1 || page.Items[0].Owner.LogicalNode.Rule.Namespace != key.Rule.Namespace {
		t.Fatalf("RuleID 查询未返回真实部署别名: %+v err=%v", page, err)
	}
}

func TestScenarioDeduplicatesByRuleID(t *testing.T) {
	scenario, _ := testScenario()
	second := scenario.Rules[0]
	second.LogicalNode.Rule.Namespace = "new-alias"
	second.LogicalNode.PlacementID = testPlacementID("p2")
	second.PhysicalNodeID = testPhysicalID("p2")
	second.RuleJSON = bytes.Replace(second.RuleJSON, []byte(`"namespace":"11495104353665842533"`), []byte(`"namespace":"new-alias"`), 1)
	scenario.PhysicalNodes = append(scenario.PhysicalNodes, NewPhysicalNodeSpec(second.PhysicalNodeID, "inproc://p2"))
	scenario.Rules = append(scenario.Rules, second)
	if report := ValidateScenario(scenario); !report.Valid {
		t.Fatalf("不同节点相同规则不同别名应能部署: %+v", report.Issues)
	}
	duplicate := scenario.Clone()
	duplicate.Rules[1].PhysicalNodeID = duplicate.Rules[0].PhysicalNodeID
	if report := ValidateScenario(duplicate); report.Valid || !hasIssueCode(report.Issues, "DUPLICATE_RULE_ON_PHYSICAL_NODE") {
		t.Fatalf("同物理节点接受了重复 RuleID: %+v", report.Issues)
	}
	duplicate.Rules[1].LogicalNode.PlacementID = duplicate.Rules[0].LogicalNode.PlacementID
	if report := ValidateScenario(duplicate); report.Valid || !hasIssueCode(report.Issues, "DUPLICATE_LOGICAL_NODE") {
		t.Fatalf("不同别名的相同逻辑部署未拒绝: %+v", report.Issues)
	}
	mismatch := scenario.Clone()
	mismatch.Rules[1].RuleJSON = bytes.Replace(mismatch.Rules[1].RuleJSON, []byte(`"maxPlayers":8`), []byte(`"maxPlayers":7`), 1)
	if report := ValidateScenario(mismatch); report.Valid || !hasIssueCode(report.Issues, "RULE_CONFIG_MISMATCH") {
		t.Fatalf("相同 RuleID 的不同业务规则未拒绝: %+v", report.Issues)
	}
}

func hasIssueCode(issues []ValidationIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func TestIdentityJSONAcceptsNumericValuesAndPreservesAlias(t *testing.T) {
	for _, input := range []string{
		`{"rule":{"namespace":"0/:名称","ruleId":18446744073709551615},"placementId":18446744073709551615}`,
		`{"rule":{"namespace":"0/:名称","ruleId":"18446744073709551615"},"placementId":"18446744073709551615"}`,
	} {
		key, err := DecodeLogicalNodeKeyJSON([]byte(input))
		if err != nil || key.Rule.Namespace != "0/:名称" || key.Rule.RuleID != ^uint64(0) || uint64(key.PlacementID) != ^uint64(0) {
			t.Fatalf("无法读取身份: %s key=%+v err=%v", input, key, err)
		}
	}
	for _, invalid := range []string{
		`{"rule":{"namespace":1,"ruleId":1},"placementId":1}`,
		`{"rule":{"namespace":null,"ruleId":1},"placementId":1}`,
		`{"rule":{"ruleId":"01"},"placementId":1}`,
		`{"rule":{"ruleId":1},"placementId":"01"}`,
		`{"rule":{"ruleId":null},"placementId":1}`,
		`{"rule":{"ruleId":1},"placementId":18446744073709551616}`,
	} {
		if _, err := DecodeLogicalNodeKeyJSON([]byte(invalid)); err == nil {
			t.Fatalf("错误接受身份: %s", invalid)
		}
	}
	_, err := DecodeRuleKeyJSON([]byte(`{"namespace":"任意","ruleId":"01"}`))
	if err == nil || !strings.Contains(err.Error(), "ruleId") {
		t.Fatalf("规则 ID 错误没有定位: %v", err)
	}
}
