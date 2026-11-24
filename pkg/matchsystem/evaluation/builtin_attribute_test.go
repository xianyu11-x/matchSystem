package evaluation

import (
	"strings"
	"testing"

	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem/contract"
)

func TestBuiltinCreateTimeInStandaloneEvaluation(t *testing.T) {
	json := []byte(`{
		"schemaVersion":"evaluation/v3",
		"canJoin":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{
			"op":"int64_lt",
			"left":{"op":"int64_ref","source":"seed_attributes","name":"createTime"},
			"right":{"op":"int64_ref","source":"candidate_attributes","name":"createTime"}
		}},
		"canComplete":{"schemaVersion":"expression-scalar/v3","resultType":"bool","expr":{"op":"bool_literal","value":true}}
	}`)
	predicates, err := CompileJSON(json, contract.Contract{})
	if err != nil {
		t.Fatal(err)
	}
	input := CanJoinInput{SeedAttributes: &common.Ticket{TicketID: 1, CreatedAt: -1}, Candidate: &common.Ticket{TicketID: 2, CreatedAt: 0}}
	if got, err := predicates.CanJoin(input); err != nil || !got {
		t.Fatalf("创建时间比较结果=%v, err=%v", got, err)
	}
	input.Candidate.CreatedAt = -2
	if got, err := predicates.CanJoin(input); err != nil || got {
		t.Fatalf("反向创建时间比较结果=%v, err=%v", got, err)
	}
	input.Candidate.Int64Values = map[string]int64{common.CreateTimeAttributeName: 9}
	if _, err := predicates.CanJoin(input); err == nil || !strings.Contains(err.Error(), "READ_ONLY_ATTRIBUTE") {
		t.Fatalf("Evaluation 未拒绝只读属性写入: %v", err)
	}
}
