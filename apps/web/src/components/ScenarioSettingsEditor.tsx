import { scenarioPayload } from '../lib/api'
import { ChoiceField, NumberField, TextField } from './RuleFormControls'
import type { JsonObject, JsonValue, RuleDocument, Scenario } from '../types'

const object = (value: JsonValue | undefined): JsonObject =>
  value && typeof value === 'object' && !Array.isArray(value) ? value : {}
const array = (value: JsonValue | undefined): JsonObject[] =>
  Array.isArray(value) ? value.map(object) : []
const identity = (rule: JsonObject) => {
  const logical = object(rule.logicalNode)
  const key = object(logical.rule)
  return `${key.namespace ?? ''}/${key.ruleId}/${logical.placementId}`
}

/** Merge current rule edits before applying deployment identity changes. */
export function scenarioSettingsPayload(
  scenario: Scenario,
  draft: JsonObject,
  document: RuleDocument,
): JsonObject {
  const original = array(scenario.rawScenario?.rules)
  const edited = array(scenarioPayload(scenario, document).rules)
  const key = `${document.apiRule?.namespace ?? ''}/${document.apiRule?.ruleId}/${document.placementId}`
  const index = original.findIndex((rule) => identity(rule) === key)
  const next = structuredClone(draft)
  const rules = array(next.rules)
  if (index >= 0 && rules[index]) {
    for (const field of [
      'rule',
      'tickFacts',
      'factProviderDescriptor',
      'objectFactProviderDescriptor',
      'matchFactProviderDescriptor',
    ]) {
      delete rules[index][field]
      if (edited[index][field] !== undefined) rules[index][field] = edited[index][field]
    }
  }
  for (const rule of rules)
    rule.rule = { ...object(rule.rule), ruleKey: object(rule.logicalNode).rule }
  next.rules = rules
  return next
}

export function ScenarioSettingsEditor({
  draft,
  onChange: setDraft,
}: {
  draft: JsonObject
  onChange: (value: JsonObject) => void
}) {
  const physical = array(draft.physicalNodes)
  const rules = array(draft.rules)
  const updatePhysical = (index: number, patch: JsonObject) =>
    setDraft({
      ...draft,
      physicalNodes: physical.map((node, i) => (i === index ? { ...node, ...patch } : node)),
    })
  const updateRule = (index: number, patch: JsonObject) =>
    setDraft({
      ...draft,
      rules: rules.map((rule, i) => (i === index ? { ...rule, ...patch } : rule)),
    })
  return (
    <div className="rule-form-panel">
      <p className="field-hint">
        这里配置模拟器宿主。保存会重建运行态并清空等待队列与历史；当前规则页草稿会一并保存，其他规则配置保持原值。
      </p>
      <NumberField
        label="匹配历史保留数量"
        optional
        min={0}
        value={draft.matchHistoryLimit as number | undefined}
        help="留空或 0 使用服务端默认上限。"
        onChange={(value) => {
          const next = { ...draft }
          if (value === undefined) delete next.matchHistoryLimit
          else next.matchHistoryLimit = value
          setDraft(next)
        }}
      />
      <section>
        <h3>物理节点（PhysicalNode）</h3>
        {physical.map((node, index) => (
          <fieldset className="rule-expression" key={index}>
            <legend>物理节点 {index + 1}</legend>
            <div className="rule-form-grid">
              <TextField
                label="物理节点标识"
                required
                value={String(node.id ?? '')}
                onChange={(id) => {
                  setDraft({
                    ...draft,
                    physicalNodes: physical.map((item, i) =>
                      i === index ? { ...item, id } : item,
                    ),
                    rules: rules.map((rule) =>
                      rule.physicalNodeId === node.id ? { ...rule, physicalNodeId: id } : rule,
                    ),
                  })
                }}
                help="重命名时同步更新当前场景的规则部署引用。"
              />
              <TextField
                label="节点端点 (Endpoint)"
                required
                value={String(node.endpoint ?? '')}
                onChange={(endpoint) => updatePhysical(index, { endpoint })}
              />
              <ChoiceField
                label="逻辑节点调度算法"
                value={String(node.selector ?? '')}
                options={[
                  { value: '', label: '默认轮询' },
                  { value: 'round_robin', label: '轮询 (round_robin)' },
                  { value: 'largest_queue', label: '最大队列优先 (largest_queue)' },
                  { value: 'oldest_waiting', label: '最长等待优先 (oldest_waiting)' },
                  {
                    value: 'smooth_weighted_round_robin',
                    label: '平滑加权轮询 (smooth_weighted_round_robin)',
                  },
                ]}
                onChange={(selector) => updatePhysical(index, { selector })}
              />
              <label className="field-label">
                <span>
                  <input
                    type="checkbox"
                    checked={node.enabled === true}
                    onChange={(event) => updatePhysical(index, { enabled: event.target.checked })}
                  />{' '}
                  启用物理节点路由
                </span>
              </label>
            </div>
            {physical.some((other, i) => i !== index && other.id === node.id) && (
              <p className="form-error">物理节点标识重复。</p>
            )}
            <button
              type="button"
              className="button button-ghost"
              disabled={rules.some((rule) => rule.physicalNodeId === node.id)}
              onClick={() =>
                setDraft({ ...draft, physicalNodes: physical.filter((_, i) => i !== index) })
              }
            >
              删除未被规则引用的节点
            </button>
          </fieldset>
        ))}
        <button
          type="button"
          className="button button-ghost"
          onClick={() =>
            setDraft({
              ...draft,
              physicalNodes: [
                ...physical,
                { id: '', endpoint: '', enabled: true, selector: 'round_robin' },
              ],
            })
          }
        >
          + 添加物理节点
        </button>
      </section>
      <section>
        <h3>规则部署与路由</h3>
        {rules.map((rule, index) => {
          const logical = object(rule.logicalNode)
          const key = object(logical.rule)
          const updateKey = (patch: JsonObject) =>
            updateRule(index, { logicalNode: { ...logical, rule: { ...key, ...patch } } })
          return (
            <fieldset className="rule-expression" key={index}>
              <legend>规则部署 {index + 1}</legend>
              <div className="rule-form-grid">
                <TextField
                  label="规则命名空间（可选）"
                  value={String(key.namespace ?? '')}
                  onChange={(namespace) => updateKey({ namespace })}
                />
                <NumberField
                  label="规则 ID"
                  value={key.ruleId as number}
                  min={1}
                  onChange={(ruleId) => updateKey({ ruleId: ruleId! })}
                />
                <TextField
                  label="部署标识 (Placement ID)"
                  required
                  value={String(logical.placementId ?? '')}
                  onChange={(placementId) =>
                    updateRule(index, { logicalNode: { ...logical, placementId } })
                  }
                />
                <ChoiceField
                  label="所属物理节点"
                  value={String(rule.physicalNodeId ?? '')}
                  options={physical
                    .filter((node) => node.id)
                    .map((node) => ({ value: String(node.id), label: String(node.id) }))}
                  onChange={(physicalNodeId) => updateRule(index, { physicalNodeId })}
                />
                <NumberField
                  label="路由/调度权重"
                  min={1}
                  max={4294967295}
                  value={rule.weight as number}
                  onChange={(weight) => updateRule(index, { weight: weight! })}
                />
                <label className="field-label">
                  <span>
                    <input
                      type="checkbox"
                      checked={rule.enabled === true}
                      onChange={(event) => updateRule(index, { enabled: event.target.checked })}
                    />{' '}
                    启用规则路由
                  </span>
                </label>
              </div>
              {rules.some((other, i) => i !== index && identity(other) === identity(rule)) && (
                <p className="form-error">规则与部署标识组合重复。</p>
              )}
            </fieldset>
          )
        })}
      </section>
    </div>
  )
}
