package prefilter

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"matchSystem/internal/matchsystem/contract"
	"matchSystem/internal/matchsystem/fact"
)

func adaptContractError(err error) error {
	if err == nil {
		return nil
	}
	var contractErr *contract.Error
	if errors.As(err, &contractErr) {
		return &Error{Phase: "compile", Path: contractErr.Path, Code: contractErr.Code, Err: contractErr.Err}
	}
	return err
}

// Fingerprint identifies the immutable Prefilter plan and the Contract
// boundary used to compile it. Runtime values, provider implementations and
// candidate scorers are deliberately outside this identity.
type Fingerprint string

const fingerprintSchema = "prefilter-fingerprint/v5"

// Plan contains Prefilter's private bitmap topology, physical lookup
// sidecars, and opaque scalar programs. No expression Bitmap IR, instruction
// handle or runtime expression program is exposed here.
type Plan struct {
	fingerprint  Fingerprint
	requirements Requirements

	nodes   []bitmapNode
	root    bitmapNodeID
	queries []bitmapQuery

	indexSpecs             []indexSpec
	factValidator          *fact.Validator
	attributeValidator     *contract.AttributeValidator
	containsProbeThreshold uint64
}

func (p *Plan) Fingerprint() Fingerprint {
	if p == nil {
		return ""
	}
	return p.fingerprint
}

func (p *Plan) Requirements() Requirements {
	if p == nil {
		return Requirements{}
	}
	return cloneRequirements(p.requirements)
}

// buildPlan finalizes the runtime Plan directly from the compiler's single
// build state.  There is intentionally no compiled bitmap product between the
// parser/compiler and Plan; the state is transferred once and never mutated
// afterwards.
func (c *bitmapCompiler) buildPlan(state *bitmapCompileState, root bitmapNodeID, runtimeThreshold uint64, requirements Requirements) (*Plan, error) {
	if c == nil || state == nil {
		return nil, compileError("$", "NIL_PLAN", "bitmap compiler state is nil")
	}
	schema := c.contract
	factValidator, err := fact.NewValidator(schema.FactSpecs())
	if err != nil {
		return nil, adaptFactError(err)
	}
	attributeValidator, err := schema.CompileAttributeValidator()
	if err != nil {
		return nil, adaptContractError(err)
	}
	indexSpecs := make([]indexSpec, len(schema.Indexes))
	for i, spec := range schema.Indexes {
		indexSpecs[i] = compileIndexSpec(spec)
	}
	plan := &Plan{
		requirements:           requirements,
		nodes:                  state.nodes,
		root:                   root,
		queries:                state.queries,
		indexSpecs:             indexSpecs,
		factValidator:          factValidator,
		attributeValidator:     attributeValidator,
		containsProbeThreshold: runtimeThreshold,
	}
	bitmapCanonical := "prefilter/v3|bitmap=" + canonicalBitmapNode(plan, root) +
		"|limits=" + canonicalBitmapLimits(c.limits) +
		fmt.Sprintf("|probe=%d|requirements=", runtimeThreshold) + canonicalRequirements(requirements)

	// Keep fingerprint inputs explicit. The canonical bitmap includes every
	// logical node and scalar operand, while Requirements and effective limits
	// are repeated here as independent invalidation dimensions. This makes it
	// impossible for a future canonicalizer change to accidentally omit an
	// index/fact/limit dependency from the identity.
	canonical := strings.Join([]string{
		fingerprintSchema,
		"contract=" + canonicalContract(schema),
		"prefilter=" + prefilterSchemaVersion,
		"bitmap=" + bitmapCanonical,
		"requirements=" + canonicalRequirements(plan.requirements),
		"limits=" + canonicalBitmapLimits(c.limits),
		fmt.Sprintf("containsProbeThreshold=%d", runtimeThreshold),
	}, "|")
	hash := sha256.Sum256([]byte(canonical))
	plan.fingerprint = Fingerprint(hex.EncodeToString(hash[:]))
	return plan, nil
}

// canonicalContract is a deterministic contract identity. Declarations are
// sorted by their globally unique names because their source order has no
// runtime meaning; all effective limits remain part of the identity.
func canonicalContract(schema contract.Contract) string {
	attributes := append([]contract.AttributeSpec(nil), schema.Attributes...)
	sort.Slice(attributes, func(i, j int) bool { return attributes[i].Name < attributes[j].Name })
	attributeParts := make([]string, len(attributes))
	for i, attribute := range attributes {
		attributeParts[i] = fmt.Sprintf("%q:%d:%d", attribute.Name, attribute.Type, attribute.MaxValues)
	}
	facts := append([]fact.Spec(nil), schema.Facts...)
	sort.Slice(facts, func(i, j int) bool { return facts[i].Name < facts[j].Name })
	factParts := make([]string, len(facts))
	for i, value := range facts {
		factParts[i] = fmt.Sprintf("%q:%d:%d:%s", value.Name, value.Type, value.MaxValues, value.Scope)
	}
	indexes := append([]contract.IndexSpec(nil), schema.Indexes...)
	sort.Slice(indexes, func(i, j int) bool { return indexes[i].Name < indexes[j].Name })
	indexParts := make([]string, len(indexes))
	for i, index := range indexes {
		indexParts[i] = fmt.Sprintf("%q:%s:%s:%d:%d", index.Name, index.Type, index.KeyType, index.MaxDocumentValues, index.MaxQueryValues)
	}
	limits := schema.Limits
	defaults := contract.DefaultLimits()
	if limits.MaxBytes == 0 {
		limits.MaxBytes = defaults.MaxBytes
	}
	if limits.MaxDepth == 0 {
		limits.MaxDepth = defaults.MaxDepth
	}
	if limits.MaxChildren == 0 {
		limits.MaxChildren = defaults.MaxChildren
	}
	if limits.MaxStringBytes == 0 {
		limits.MaxStringBytes = defaults.MaxStringBytes
	}
	if limits.MaxIndexes == 0 {
		limits.MaxIndexes = defaults.MaxIndexes
	}
	if limits.MaxAttributes == 0 {
		limits.MaxAttributes = defaults.MaxAttributes
	}
	if limits.MaxFacts == 0 {
		limits.MaxFacts = defaults.MaxFacts
	}
	if limits.MaxValues == 0 {
		limits.MaxValues = defaults.MaxValues
	}
	if limits.MaxDocumentValues == 0 {
		limits.MaxDocumentValues = defaults.MaxDocumentValues
	}
	if limits.MaxQueryValues == 0 {
		limits.MaxQueryValues = defaults.MaxQueryValues
	}
	return strings.Join([]string{
		contract.SchemaVersion,
		"attributes=[" + strings.Join(attributeParts, ",") + "]",
		"facts=[" + strings.Join(factParts, ",") + "]",
		"indexes=[" + strings.Join(indexParts, ",") + "]",
		"limits=" + strconv.Itoa(limits.MaxBytes) + "," + strconv.Itoa(limits.MaxDepth) + "," + strconv.Itoa(limits.MaxChildren) + "," + strconv.Itoa(limits.MaxStringBytes) + "," + strconv.Itoa(limits.MaxIndexes) + "," + strconv.Itoa(limits.MaxAttributes) + "," + strconv.Itoa(limits.MaxFacts) + "," + strconv.Itoa(limits.MaxValues) + "," + strconv.Itoa(limits.MaxDocumentValues) + "," + strconv.Itoa(limits.MaxQueryValues),
	}, "|")
}

func cloneRequirements(in Requirements) Requirements {
	return Requirements{
		Indexes:    append([]RequiredIndex(nil), in.Indexes...),
		Facts:      append([]fact.Spec(nil), in.Facts...),
		Attributes: append([]contract.AttributeSpec(nil), in.Attributes...),
	}
}
