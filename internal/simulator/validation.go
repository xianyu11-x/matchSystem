package simulator

import (
	"errors"
	"fmt"
	"strings"

	"matchSystem/internal/identity"
	"matchSystem/internal/matchsystem"
	"matchSystem/internal/matchsystem/contract"
	"matchSystem/internal/matchsystem/evaluation"
	"matchSystem/internal/matchsystem/fact"
)

// ValidateScenario performs the same semantic checks used by runtime
// construction. A report is returned instead of exposing compiler internals,
// which makes it directly suitable for a validation HTTP endpoint.
func ValidateScenario(scenario Scenario) ValidationReport {
	issues := make([]ValidationIssue, 0)
	if scenario.SchemaVersion != "" && scenario.SchemaVersion != ScenarioSchemaVersion {
		issues = append(issues, ValidationIssue{
			Path:    "$.schemaVersion",
			Code:    "UNKNOWN_SCHEMA_VERSION",
			Message: fmt.Sprintf("unsupported scenario schemaVersion %q", scenario.SchemaVersion),
		})
	}

	physical := make(map[identity.PhysicalNodeID]int, len(scenario.PhysicalNodes))
	for index, node := range scenario.PhysicalNodes {
		path := fmt.Sprintf("$.physicalNodes[%d]", index)
		if err := node.ID.Validate(); err != nil {
			issues = append(issues, issueAt(path+".id", "INVALID_PHYSICAL_NODE", err))
			continue
		}
		if previous, exists := physical[node.ID]; exists {
			issues = append(issues, ValidationIssue{
				Path:    path + ".id",
				Code:    "DUPLICATE_PHYSICAL_NODE",
				Message: fmt.Sprintf("PhysicalNode %q is already declared at index %d", node.ID, previous),
			})
		} else {
			physical[node.ID] = index
		}
		if node.Endpoint == "" {
			issues = append(issues, ValidationIssue{Path: path + ".endpoint", Code: "MISSING_ENDPOINT", Message: "endpoint is required"})
		}
		if !isKnownSelector(node.Selector) {
			issues = append(issues, ValidationIssue{
				Path:    path + ".selector",
				Code:    "UNKNOWN_SELECTOR",
				Message: fmt.Sprintf("unsupported PhysicalNode selector %q", node.Selector),
			})
		}
	}

	logical := make(map[identity.LogicalNodeKey]int, len(scenario.Rules))
	ruleOnPhysical := make(map[string]int, len(scenario.Rules))
	fingerprintByRule := make(map[identity.RuleKey]string, len(scenario.Rules))
	for index, rule := range scenario.Rules {
		path := fmt.Sprintf("$.rules[%d]", index)
		if err := rule.LogicalNode.Validate(); err != nil {
			issues = append(issues, issueAt(path+".logicalNode", "INVALID_LOGICAL_NODE", err))
		}
		if err := rule.PhysicalNodeID.Validate(); err != nil {
			issues = append(issues, issueAt(path+".physicalNodeId", "INVALID_PHYSICAL_NODE", err))
		} else if _, exists := physical[rule.PhysicalNodeID]; !exists {
			issues = append(issues, ValidationIssue{
				Path:    path + ".physicalNodeId",
				Code:    "UNKNOWN_PHYSICAL_NODE",
				Message: fmt.Sprintf("PhysicalNode %q is not declared", rule.PhysicalNodeID),
			})
		}
		if rule.Weight == 0 {
			issues = append(issues, ValidationIssue{Path: path + ".weight", Code: "INVALID_WEIGHT", Message: "weight must be positive"})
		}
		if previous, exists := logical[rule.LogicalNode]; exists {
			issues = append(issues, ValidationIssue{
				Path:    path + ".logicalNode",
				Code:    "DUPLICATE_LOGICAL_NODE",
				Message: fmt.Sprintf("LogicalNode is already declared at rule index %d", previous),
			})
		} else if rule.LogicalNode.Validate() == nil {
			logical[rule.LogicalNode] = index
		}
		routeKey := string(rule.PhysicalNodeID) + "\x00" + rule.LogicalNode.Rule.String()
		if previous, exists := ruleOnPhysical[routeKey]; exists {
			issues = append(issues, ValidationIssue{
				Path:    path,
				Code:    "DUPLICATE_RULE_ON_PHYSICAL_NODE",
				Message: fmt.Sprintf("Rule is already loaded on this PhysicalNode at rule index %d", previous),
			})
		} else {
			ruleOnPhysical[routeKey] = index
		}

		// Parsing and compiling the complete Rule document here gives callers the same
		// fail-closed result that buildRuntime uses, even for a disabled route.
		compiled, err := compileRuleForValidation(rule)
		if err != nil {
			issues = append(issues, issueAt(path+".rule", "INVALID_RULE", err))
			continue
		}
		if previous, exists := fingerprintByRule[compiled.RuleKey()]; exists && previous != compiled.Fingerprint() {
			issues = append(issues, ValidationIssue{
				Path:    path + ".rule",
				Code:    "RULE_CONFIG_MISMATCH",
				Message: fmt.Sprintf("RuleKey %s is already bound to a different match-rule/v1 document", compiled.RuleKey()),
			})
			continue
		}
		fingerprintByRule[compiled.RuleKey()] = compiled.Fingerprint()
		schema := compiled.Contract()
		validator, err := fact.NewValidator(schema.Facts)
		if err != nil {
			issues = append(issues, issueAt(path+".rule.contract.facts", "INVALID_FACT_CONTRACT", err))
			continue
		}
		if err := validateFactSnapshot(path+".tickFacts", validator, rule.TickFacts, fact.ScopeTick); err != nil {
			issues = append(issues, issueAt(path+".tickFacts", "INVALID_TICK_FACTS", err))
		}
	}
	return ValidationReport{Valid: len(issues) == 0, Issues: issues}
}

// ValidateRuleJSON validates one complete match-rule/v1 document using the
// same compiler and LogicalNode construction boundary as runtime loading.
func ValidateRuleJSON(ruleJSON []byte) ValidationReport {
	compiled, err := matchsystem.CompileRuleJSON(ruleJSON)
	if err != nil {
		return ValidationReport{Issues: []ValidationIssue{issueAt("$", "INVALID_RULE", err)}}
	}
	rule := RuleSpec{
		LogicalNode:    identity.LogicalNodeKey{Rule: compiled.RuleKey(), PlacementID: "preview"},
		PhysicalNodeID: "preview",
		Weight:         1,
		Enabled:        true,
		RuleJSON:       append([]byte(nil), ruleJSON...),
	}
	if _, err := compileRuleForValidation(rule); err != nil {
		return ValidationReport{Issues: []ValidationIssue{issueAt("$", "INVALID_RULE", err)}}
	}
	return ValidationReport{Valid: true, Fingerprint: compiled.Fingerprint()}
}

func compileRuleForValidation(rule RuleSpec) (*matchsystem.CompiledRuleConfig, error) {
	compiled, err := matchsystem.CompileRuleJSON(rule.RuleJSON)
	if err != nil {
		return nil, fmt.Errorf("compile Rule JSON: %w", err)
	}
	schema := compiled.Contract()
	owner := identity.OwnerRef{LogicalNode: rule.LogicalNode, PhysicalNodeID: "preview"}
	spec := runtimeLogicalNodeSpec(rule, schema, NewObservationRegistry(), owner, nil)
	_, err = matchsystem.NewLogicalNode(spec)
	if err != nil {
		return nil, err
	}
	return compiled, nil
}

func validateFactSnapshot(path string, validator *fact.Validator, values FactSnapshot, scope fact.Scope) error {
	if validator == nil {
		return fmt.Errorf("Fact validator is nil")
	}
	_, err := validator.ValidateLayer(path, values.values(), scope)
	return err
}

func isKnownSelector(selector SelectorKind) bool {
	return normalizeSelector(selector) == SelectorRoundRobin ||
		normalizeSelector(selector) == SelectorLargestQueue ||
		normalizeSelector(selector) == SelectorOldestWaiting ||
		normalizeSelector(selector) == SelectorWeighted
}

func issueAt(path, code string, err error) ValidationIssue {
	issue := ValidationIssue{Path: path, Code: code, Message: "validation failed"}
	if err == nil {
		return issue
	}
	issue.Message = err.Error()
	var handshakeErr *matchsystem.ProviderHandshakeError
	if errors.As(err, &handshakeErr) {
		if handshakeErr.Provider != "" {
			issue.Path = joinValidationPath(path, "provider."+handshakeErr.Provider)
		}
		if handshakeErr.Name != "" {
			issue.Path = joinValidationPath(issue.Path, "facts."+handshakeErr.Name)
		}
		if handshakeErr.Code != "" {
			issue.Code = handshakeErr.Code
		}
		return issue
	}
	var ruleErr *matchsystem.RuleConfigError
	if errors.As(err, &ruleErr) {
		if ruleErr.Path != "" {
			issue.Path = joinValidationPath(path, ruleErr.Path)
		}
		if ruleErr.Code != "" {
			issue.Code = ruleErr.Code
		}
		return issue
	}
	var contractErr *contract.Error
	if errors.As(err, &contractErr) {
		if contractErr.Path != "" {
			issue.Path = joinValidationPath(path, contractErr.Path)
		}
		if contractErr.Code != "" {
			issue.Code = contractErr.Code
		}
		return issue
	}
	var evaluationErr *evaluation.Error
	if errors.As(err, &evaluationErr) {
		if evaluationErr.Path != "" {
			issue.Path = joinValidationPath(path, evaluationErr.Path)
		}
		if evaluationErr.Code != "" {
			issue.Code = evaluationErr.Code
		}
		return issue
	}
	var factErr *fact.Error
	if errors.As(err, &factErr) {
		if factErr.Path != "" {
			issue.Path = joinValidationPath(path, factErr.Path)
		}
		if factErr.Code != "" {
			issue.Code = factErr.Code
		}
	}
	return issue
}

func joinValidationPath(prefix, suffix string) string {
	suffix = strings.TrimSpace(suffix)
	if suffix == "" || suffix == "$" {
		return prefix
	}
	if strings.HasPrefix(suffix, "$") {
		return prefix + suffix[1:]
	}
	return prefix + "." + suffix
}
