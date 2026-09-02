---
name: create-match-rule-json
description: Convert natural-language matchmaking requirements into a current project match-rule/v1 JSON file, asking the user to resolve every missing semantic decision or applying the reserved inert default only when explicitly requested. Use for creating, revising, or validating MatchSystem rule JSON; do not use for archived pre-v3 formats or whole simulator scenarios unless explicitly requested.
---

# Create Match Rule JSON

Create one production-shaped `match-rule/v1` document from the user's rule description. Preserve the user's intent exactly: ask questions for missing decisions instead of choosing business semantics, limits, ordering, identifiers, provider behavior, or output locations.

## Generation source of truth

Work from the current checkout, not remembered or archived examples.

1. Locate the repository root with `git rev-parse --show-toplevel` when needed.
2. Read the current `api/schema/match-rule/v1.schema.json` and every schema it references.
3. Read [references/requirements-and-mapping.md](references/requirements-and-mapping.md) for project-specific semantic constraints and the clarification checklist.
4. Consult the current non-archive docs linked there for semantics that JSON Schema cannot express.
5. The bundled validator is intentionally a lightweight, Skill-local format check. It is not a replacement for host-side compilation or provider checks.

The files above guide rule generation and semantic clarification only. The validation command below reads only the target JSON and the rules embedded in this Skill; it does not load repository schemas or project packages.

Never derive current behavior from `doc/archive/`.

## Workflow

### 1. Build a requirement ledger

Translate the request into explicit decisions for:

- output file path;
- complete `ruleKey` identity;
- Contract attributes, facts, indexes, and limits policy;
- Prefilter candidate-set semantics;
- `canJoin` and `canComplete` predicates;
- candidate scoring and seed selection;
- the runtime limits, including the candidate scoring-pool cap and the retained Top-L cap;
- required Tick/Object/Match Fact Providers.

Mark each item as confirmed, mechanically implied, or unresolved. Constants imposed by the schemas, such as `schemaVersion`, `resultType`, required empty `params`, and JSON syntax, are mechanically implied. A project default is not user confirmation unless the user explicitly activates the default-rule mode below.

### Default-rule mode

Use [assets/default-rule.json](assets/default-rule.json) only when the user explicitly asks to use the default rule for a blank/empty configuration, for example “空白配置使用默认规则”, “生成空白配置并采用默认规则”, or an equally unambiguous request. Merely asking for a blank rule, omitting details, or saying “随便生成” does not activate this mode.

When activated:

1. Copy the asset as the complete starting RuleJSON. Do not ask for fields already supplied by the default profile.
2. Apply only overrides the user explicitly provides. If an override is ambiguous, ask only about that override.
3. If no destination is supplied, write `rules/default-rule.json`.
4. Validate the exact written file with the normal validator before reporting success.

The reserved default is deliberately inert: `prefilter.none`, `canJoin: false`, and `canComplete: false` mean it compiles and loads but cannot create a Match. It declares no Attribute, Fact, Index, or Provider obligation. Its identity is `namespace: "default"`, `ruleId: 1`; its remaining fixed values are documented in [references/requirements-and-mapping.md](references/requirements-and-mapping.md). State this behavior in the handoff so “default” is never mistaken for match-all behavior.

### 2. Ask before deciding

Outside default-rule mode, if any semantic item is unresolved, stop before writing the final JSON file and ask focused, numbered questions. Group related questions and explain valid choices in project terms. Ask only what remains unresolved, but do not silently choose:

- names, types, scopes, cardinality limits, or index bounds;
- match compatibility, completion thresholds, or empty/missing-value behavior;
- scoring type/direction/weight or seed ordering;
- runtime capacities and attempt budgets;
- identifiers, namespace behavior, or destination path;
- whether optional field/contract defaults are accepted;
- whether required Fact Providers already exist and what exact values they expose.

Do not create a placeholder-filled `.json` file while waiting. A draft shown for discussion must be clearly labeled non-final and must not be saved as the requested artifact.

### 3. Construct only confirmed semantics

Generate a single, formatted JSON object using the current `match-rule/v1` envelope. Derive declarations that are mechanically required by confirmed expressions, but never invent their bounds or behavior. Keep these invariants:

- `PlacementID` is deployment topology and never belongs in RuleJSON.
- The RuleJSON `ruleKey` must exactly match the `LogicalNodeKey.Rule` used by the host.
- Every referenced attribute, fact, and index is declared in `contract` with matching type and scope.
- Prefilter operands use complete `expression-scalar/v3` envelopes; Evaluation contains complete Bool roots.
- `prefilter` is index-only. `none` means an empty candidate set, not “all candidates”; never fabricate a broad anchor.
- `int64_field` scoring and `int64_priority` seed selection reference declared `int64` attributes.
- `attemptLimitPerProduceMatch` does not exceed `attemptLimitPerMatchRound`.
- Fact declarations do not implement providers. Record external provider obligations in the handoff.

Do not wrap the rule in a simulator scenario unless the user explicitly asks for a scenario.

### 4. Validate the exact file

Run from the repository root:

```powershell
python Skills/create-match-rule-json/scripts/validate_rule.py <path-to-rule.json>
```

The script uses only Python's standard library and the format rules in this Skill; it does not import project packages or require Go. It checks JSON syntax, required/unknown fields, schema versions, basic types/enums, expression envelopes, and runtime number relationships. It deliberately does not compile expressions, resolve every declaration reference, or verify runtime/provider behavior. Fix mechanical formatting mistakes directly. If a fix would alter semantics, field types, bounds, predicates, defaults, or runtime behavior, ask the user instead.

Do not claim the rule is host-ready solely because the format check returns `"valid": true`. If the rule uses facts, separately state the required provider bindings; this validator cannot prove that the production host supplies them correctly.

### 5. Hand off

Report the saved path, format-validation result and fingerprint, the rule behavior in plain language, and any external Fact Provider obligations. Distinguish “RuleJSON format is valid” from “host/provider integration was verified.”
