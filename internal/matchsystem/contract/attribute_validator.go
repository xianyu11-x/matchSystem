package contract

import (
	"matchSystem/internal/common"
	"matchSystem/internal/matchsystem/fact"
)

// AttributeValidator is an immutable compiled index over a Contract's closed
// Ticket attribute namespace.
type AttributeValidator struct {
	byName map[string]AttributeSpec
}

func (c Contract) CompileAttributeValidator() (*AttributeValidator, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	byName := make(map[string]AttributeSpec, len(c.Attributes))
	for _, spec := range c.Attributes {
		byName[spec.Name] = spec
	}
	return &AttributeValidator{byName: byName}, nil
}

// ValidateTicket validates every value actually present on ticket. Metadata
// fields such as TicketID and CreatedAt are outside the attribute namespace.
func (v *AttributeValidator) ValidateTicket(path string, ticket *common.Ticket) error {
	if ticket == nil {
		return compileError(path, "NIL_TICKET", "ticket is nil")
	}
	fields, _, err := fact.Inspect(path, fact.Values{StringLists: ticket.StringLists, Uint64Lists: ticket.Uint64Lists, Int64Values: ticket.Int64Values})
	if err != nil {
		return compileError(path, "ATTRIBUTE_TYPE_COLLISION", "%v", err)
	}
	for _, field := range fields {
		fieldPath := path + "." + field.Name
		spec, ok := v.byName[field.Name]
		if !ok {
			return compileError(fieldPath, "UNDECLARED_ATTRIBUTE", "attribute %q is not declared by the contract", field.Name)
		}
		if spec.Type != field.Type {
			return compileError(fieldPath, "ATTRIBUTE_TYPE_MISMATCH", "attribute %q has a value in the wrong type map", field.Name)
		}
		if spec.MaxValues > 0 && field.ValueCount > spec.MaxValues {
			return compileError(fieldPath, "ATTRIBUTE_VALUE_LIMIT", "attribute %q contains %d values; maximum is %d", field.Name, field.ValueCount, spec.MaxValues)
		}
	}
	return nil
}
