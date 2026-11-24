package common

import "testing"

func TestTicketInt64AttributeReadsBuiltinWithoutMap(t *testing.T) {
	for _, value := range []int64{-10, 0, 123} {
		ticket := &Ticket{CreatedAt: value, Int64Values: map[string]int64{"rank": 7, CreateTimeAttributeName: 999}}
		if got, ok := ticket.Int64Attribute(CreateTimeAttributeName); !ok || got != value {
			t.Fatalf("内置创建时间 = %d, %v；期望 %d", got, ok, value)
		}
		if got, ok := ticket.Int64Attribute("rank"); !ok || got != 7 {
			t.Fatalf("业务属性 = %d, %v；期望 7", got, ok)
		}
		if _, ok := ticket.Int64Attribute("missing"); ok {
			t.Fatal("缺失业务属性被识别为存在")
		}
	}
	var ticket *Ticket
	if _, ok := ticket.Int64Attribute(CreateTimeAttributeName); ok {
		t.Fatal("nil Ticket 不应返回内置属性")
	}
}
