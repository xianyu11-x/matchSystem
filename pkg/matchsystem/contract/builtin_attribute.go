package contract

import (
	"github.com/xianyu11-x/matchSystem/pkg/common"
	"github.com/xianyu11-x/matchSystem/pkg/matchsystem/fact"
)

// BuiltinAttribute 描述由核心提供、无需业务写入的 Ticket 属性。
type BuiltinAttribute struct {
	Name       string
	Type       fact.Type
	ReadOnly   bool
	Source     string
	IndexTypes []IndexType
}

// BuiltinAttributes 返回内置属性目录的独立副本。
func BuiltinAttributes() []BuiltinAttribute {
	return []BuiltinAttribute{{
		Name:       common.CreateTimeAttributeName,
		Type:       fact.TypeInt64,
		ReadOnly:   true,
		Source:     "ticket.createdAt",
		IndexTypes: []IndexType{IndexTypeInt64Range},
	}}
}

func isBuiltinAttribute(name string) bool {
	for _, attribute := range BuiltinAttributes() {
		if attribute.Name == name {
			return true
		}
	}
	return false
}
