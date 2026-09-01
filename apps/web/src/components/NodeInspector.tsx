import { useRuleStore } from '../lib/ruleStore'
import { typeLabel } from '../lib/graph'
import type { JsonValue, LogicalNodeContract, ValueType } from '../types'

function asText(value: unknown, fallback = ''): string {
  return typeof value === 'string' ? value : fallback
}

function valuesText(value: unknown): string {
  return Array.isArray(value) ? value.join(', ') : ''
}

function stepsText(value: unknown): string {
  if (!Array.isArray(value)) return ''
  return value
    .map((step) => {
      if (!step || typeof step !== 'object' || Array.isArray(step)) return ''
      const candidate = step as Record<string, unknown>
      return Number.isInteger(candidate.at) && Number.isInteger(candidate.value)
        ? `${candidate.at}:${candidate.value}`
        : ''
    })
    .filter(Boolean)
    .join(', ')
}

function parseSteps(value: string): Array<{ at: number; value: number }> {
  return value
    .split(',')
    .map((part) => part.trim())
    .filter(Boolean)
    .map((part) => {
      const [atText, valueText] = part.split(':').map((item) => item.trim())
      return { at: Number(atText), value: Number(valueText) }
    })
    .filter((step) => Number.isInteger(step.at) && Number.isInteger(step.value))
}

const stringOperators = [
  'strings_eq',
  'strings_neq',
  'strings_is_empty',
  'strings_contains',
  'strings_contains_any',
  'strings_contains_all',
  'strings_intersects',
] as const
const uint64Operators = [
  'uint64s_eq',
  'uint64s_neq',
  'uint64s_is_empty',
  'uint64s_contains',
  'uint64s_contains_any',
  'uint64s_contains_all',
  'uint64s_intersects',
] as const
const knownConfigOperations = new Set<string>([
  ...stringOperators,
  ...uint64Operators,
  'int64_step',
])

function referenceType(operation: string): 'strings' | 'int64' | 'uint64s' | undefined {
  if (operation === 'strings_ref') return 'strings'
  if (operation === 'int64_ref') return 'int64'
  if (operation === 'uint64s_ref') return 'uint64s'
  return undefined
}

function referenceFields(contract: LogicalNodeContract, operation: string, source: string) {
  const expectedType = referenceType(operation)
  if (!expectedType) return []
  if (source.endsWith('_attributes'))
    return contract.attributes.filter((field) => field.type === expectedType)
  const scope = source === 'tick_facts' ? 'tick' : source === 'match_facts' ? 'match' : 'object'
  return contract.facts.filter((field) => field.type === expectedType && field.scope === scope)
}

function inputTypesForOperation(operation: string): ValueType[] | undefined {
  if (operation === 'strings_is_empty' || operation === 'strings_contains') return ['strings']
  if (
    operation === 'strings_eq' ||
    operation === 'strings_neq' ||
    operation === 'strings_contains_any' ||
    operation === 'strings_contains_all' ||
    operation === 'strings_intersects'
  )
    return ['strings', 'strings']
  if (operation === 'uint64s_is_empty' || operation === 'uint64s_contains') return ['uint64s']
  if (
    operation === 'uint64s_eq' ||
    operation === 'uint64s_neq' ||
    operation === 'uint64s_contains_any' ||
    operation === 'uint64s_contains_all' ||
    operation === 'uint64s_intersects'
  )
    return ['uint64s', 'uint64s']
  if (operation === 'int64_step') return ['int64']
  return undefined
}

function variadicTypeForOperation(operation: string): ValueType | undefined {
  if (operation === 'bool_and' || operation === 'bool_or') return 'bool'
  if (operation === 'and' || operation === 'or') return 'bitmap'
  if (operation === 'strings_union') return 'strings'
  if (operation === 'uint64s_union') return 'uint64s'
  return undefined
}

export function NodeInspector({ contract }: { contract: LogicalNodeContract }) {
  const document = useRuleStore((state) => state.document)
  const selectedNodeId = useRuleStore((state) => state.selectedNodeId)
  const updateNodeData = useRuleStore((state) => state.updateNodeData)
  const removeNode = useRuleStore((state) => state.removeNode)
  const node = document?.graph.nodes.find((item) => item.id === selectedNodeId)

  if (!node) {
    return (
      <div className="inspector-empty">
        <span className="inspector-empty-mark">↖</span>
        <strong>选择一个节点</strong>
        <p>从图中选择节点，在这里编辑属性并获得实时校验。</p>
      </div>
    )
  }

  const config = node.data.config
  const operation = asText(config.op)
  const isStringContains = operation === 'strings_contains'
  const isUint64Contains = operation === 'uint64s_contains'
  const isStringEmpty = operation === 'strings_is_empty'
  const isUint64Empty = operation === 'uint64s_is_empty'
  const isInt64Step = operation === 'int64_step'
  const isEvaluationRoot =
    node.data.nodeType === 'evaluation.join' || node.data.nodeType === 'evaluation.complete'
  const isFixedOutput = isEvaluationRoot || node.data.nodeType === 'prefilter.output'
  const update = (patch: Record<string, JsonValue>) =>
    updateNodeData(node.id, { config: { ...config, ...patch } })
  const updateOperation = (nextOperation: string) => {
    const nextConfig: Record<string, JsonValue> = { ...config, op: nextOperation }
    if (nextOperation.startsWith('strings_')) {
      delete nextConfig.needle
      delete nextConfig.other
      if (
        nextOperation === 'strings_contains_any' ||
        nextOperation === 'strings_contains_all' ||
        nextOperation === 'strings_eq' ||
        nextOperation === 'strings_neq' ||
        nextOperation === 'strings_intersects'
      )
        if (config.other !== undefined) nextConfig.other = config.other
    }
    if (nextOperation.startsWith('uint64s_')) {
      delete nextConfig.needle
      delete nextConfig.other
      if (
        nextOperation === 'uint64s_contains_any' ||
        nextOperation === 'uint64s_contains_all' ||
        nextOperation === 'uint64s_eq' ||
        nextOperation === 'uint64s_neq' ||
        nextOperation === 'uint64s_intersects'
      )
        if (config.other !== undefined) nextConfig.other = config.other
    }
    if (nextOperation === 'int64_step') {
      delete nextConfig.left
      delete nextConfig.right
      delete nextConfig.value
      delete nextConfig.min
      delete nextConfig.max
      delete nextConfig.source
      delete nextConfig.name
    } else if (nextOperation.startsWith('int64_')) delete nextConfig.steps
    if (nextOperation !== 'strings_contains' && nextOperation !== 'uint64s_contains')
      delete nextConfig.needle
    if (nextOperation !== 'int64_step') delete nextConfig.steps
    if (nextOperation === 'strings_contains' && typeof nextConfig.needle !== 'string')
      nextConfig.needle = ''
    if (
      nextOperation === 'uint64s_contains' &&
      (typeof nextConfig.needle !== 'number' ||
        !Number.isInteger(nextConfig.needle) ||
        nextConfig.needle < 0)
    )
      nextConfig.needle = 0
    if (nextOperation === 'int64_step' && !Array.isArray(nextConfig.steps))
      nextConfig.steps = [{ at: 0, value: 0 }]
    const inputTypes = inputTypesForOperation(nextOperation)
    const variadicInputType = variadicTypeForOperation(nextOperation)
    updateNodeData(node.id, {
      config: nextConfig,
      ...(variadicInputType
        ? {
            inputTypes: [],
            maxInputs: 16,
            requiredInputs: 0,
            variadic: true,
            variadicInputType,
          }
        : inputTypes
          ? {
              inputTypes,
              maxInputs: inputTypes.length,
              requiredInputs: inputTypes.length,
              variadic: false,
              variadicInputType: undefined,
            }
          : {}),
    })
  }
  const sourceOptions =
    node.data.nodeType === 'source.attribute' ? contract.attributes : contract.facts
  const selectedSource = sourceOptions.find((item) => item.name === config.name)
  const selectedFact =
    node.data.nodeType === 'source.fact'
      ? contract.facts.find((item) => item.name === config.name)
      : undefined
  const allowedSources =
    node.data.nodeType === 'source.attribute'
      ? ['seed_attributes', 'candidate_attributes']
      : selectedFact?.scope === 'tick'
        ? ['tick_facts']
        : selectedFact?.scope === 'match'
          ? ['match_facts']
          : selectedFact?.scope === 'object'
            ? ['seed_facts', 'candidate_facts']
            : ['seed_facts', 'candidate_facts', 'tick_facts', 'match_facts']
  const selectedWireSource = asText(config.source)
  const safeWireSource = allowedSources.includes(selectedWireSource)
    ? selectedWireSource
    : (allowedSources[0] ?? '')
  const isGenericReference =
    referenceType(operation) !== undefined &&
    node.data.nodeType !== 'source.attribute' &&
    node.data.nodeType !== 'source.fact'
  const referenceSourceOptions = [
    'seed_attributes',
    'candidate_attributes',
    'seed_facts',
    'candidate_facts',
    'tick_facts',
    'match_facts',
  ]
  const safeReferenceSource = referenceSourceOptions.includes(selectedWireSource)
    ? selectedWireSource
    : 'candidate_attributes'
  const genericReferenceFields = referenceFields(contract, operation, safeReferenceSource)
  return (
    <div className="inspector-content">
      <div className="inspector-heading">
        <div>
          <span className="eyebrow">NODE INSPECTOR</span>
          <h3>{node.data.label}</h3>
        </div>
        <button
          className="icon-button danger"
          type="button"
          aria-label="删除节点"
          onClick={() => removeNode(node.id)}
          disabled={isFixedOutput}
          title={isFixedOutput ? '最终输出节点不能删除' : '删除节点'}
        >
          ×
        </button>
      </div>
      <div className="inspector-meta">
        <span>{node.data.nodeType}</span>
        <span>out · {typeLabel[node.data.outputType]}</span>
      </div>
      <label className="field-label node-comment-field">
        节点注释
        <textarea
          className="text-input text-area"
          rows={3}
          value={node.data.comment ?? ''}
          placeholder="说明这个节点的用途、业务含义或特殊约束"
          onChange={(event) => updateNodeData(node.id, { comment: event.target.value })}
        />
        <span className="field-hint">注释只属于当前 Rule Graph 节点，不会改变运行时表达式。</span>
      </label>
      <div className="inspector-fields">
        {node.data.nodeType === 'source.attribute' || node.data.nodeType === 'source.fact' ? (
          <>
            <label className="field-label">
              Source
              <select
                className="text-input"
                value={safeWireSource}
                onChange={(event) => update({ source: event.target.value })}
              >
                {allowedSources.map((source) => (
                  <option value={source} key={source}>
                    {source}
                  </option>
                ))}
              </select>
            </label>
            <label className="field-label">
              名称
              <select
                className="text-input"
                value={asText(config.name)}
                onChange={(event) => {
                  const selected = sourceOptions.find((item) => item.name === event.target.value)
                  const nextSource =
                    node.data.nodeType === 'source.fact' && selected && 'scope' in selected
                      ? selected.scope === 'tick'
                        ? 'tick_facts'
                        : selected.scope === 'match'
                          ? 'match_facts'
                          : 'candidate_facts'
                      : safeWireSource
                  const nextOp =
                    selected?.type === 'int64'
                      ? 'int64_ref'
                      : selected?.type === 'uint64s'
                        ? 'uint64s_ref'
                        : 'strings_ref'
                  updateNodeData(node.id, {
                    config: { ...config, op: nextOp, name: event.target.value, source: nextSource },
                    outputType: selected?.type ?? node.data.outputType,
                  })
                }}
              >
                <option value="">选择字段…</option>
                {sourceOptions.map((item) => (
                  <option value={item.name} key={item.name}>
                    {item.name} · {item.type}
                    {'scope' in item ? ` · ${item.scope}` : ''}
                  </option>
                ))}
              </select>
            </label>
            {selectedSource ? (
              <div className="field-hint">
                Contract 类型：{selectedSource.type}
                {'scope' in selectedSource ? ` · ${selectedSource.scope}-scope` : ''}
              </div>
            ) : null}
          </>
        ) : null}
        {node.data.nodeType === 'literal.string' || node.data.nodeType === 'literal.uint64' ? (
          <label className="field-label">
            Values（逗号分隔）
            <input
              className="text-input"
              value={valuesText(config.values)}
              onChange={(event) =>
                update({
                  values: event.target.value
                    .split(',')
                    .map((value) =>
                      node.data.nodeType === 'literal.uint64' ? Number(value.trim()) : value.trim(),
                    )
                    .filter((value) => value !== ''),
                })
              }
            />
          </label>
        ) : null}
        {node.data.nodeType === 'literal.int64' ? (
          <label className="field-label">
            Value
            <input
              className="text-input"
              type="number"
              value={typeof config.value === 'number' ? config.value : 0}
              onChange={(event) => update({ value: Number(event.target.value) })}
            />
          </label>
        ) : null}
        {node.data.nodeType === 'literal.bool' ? (
          <label className="field-label">
            Value
            <select
              className="text-input"
              value={String(config.value ?? true)}
              onChange={(event) => update({ value: event.target.value === 'true' })}
            >
              <option value="true">true</option>
              <option value="false">false</option>
            </select>
          </label>
        ) : null}
        {node.data.nodeType === 'compare.int64' ? (
          <label className="field-label">
            Operator
            <select
              className="text-input"
              value={asText(config.op, 'int64_eq')}
              onChange={(event) => updateOperation(event.target.value)}
            >
              <option value="int64_eq">等于 =</option>
              <option value="int64_neq">不等于 ≠</option>
              <option value="int64_lt">小于 &lt;</option>
              <option value="int64_lte">小于等于 ≤</option>
              <option value="int64_gt">大于 &gt;</option>
              <option value="int64_gte">大于等于 ≥</option>
            </select>
          </label>
        ) : null}
        {node.data.nodeType === 'compare.strings' ? (
          <label className="field-label">
            Operator
            <select
              className="text-input"
              value={asText(config.op, 'strings_contains_any')}
              onChange={(event) => updateOperation(event.target.value)}
            >
              <option value="strings_eq">完全相等</option>
              <option value="strings_neq">不相等</option>
              <option value="strings_is_empty">为空</option>
              <option value="strings_contains">包含指定值</option>
              <option value="strings_contains_any">包含任一</option>
              <option value="strings_contains_all">包含全部</option>
              <option value="strings_intersects">存在交集</option>
            </select>
          </label>
        ) : null}
        {node.data.nodeType === 'compare.uint64' ? (
          <label className="field-label">
            Operator
            <select
              className="text-input"
              value={asText(config.op, 'uint64s_contains_any')}
              onChange={(event) => updateOperation(event.target.value)}
            >
              <option value="uint64s_eq">完全相等</option>
              <option value="uint64s_neq">不相等</option>
              <option value="uint64s_is_empty">为空</option>
              <option value="uint64s_contains">包含指定值</option>
              <option value="uint64s_contains_any">包含任一</option>
              <option value="uint64s_contains_all">包含全部</option>
              <option value="uint64s_intersects">存在交集</option>
            </select>
          </label>
        ) : null}
        {(node.data.nodeType === 'expression.generic' ||
          node.data.nodeType === 'prefilter.generic') &&
        !knownConfigOperations.has(operation) ? (
          <label className="field-label">
            Operation
            <input
              className="text-input"
              value={asText(config.op)}
              onChange={(event) => updateOperation(event.target.value)}
            />
          </label>
        ) : null}
        {isStringContains ? (
          <label className="field-label">
            Needle
            <input
              className="text-input"
              value={asText(config.needle)}
              onChange={(event) => update({ needle: event.target.value })}
            />
            <span className="field-hint">
              连接唯一的 Strings values 输入；needle 是节点配置中的固定字符串。
            </span>
          </label>
        ) : null}
        {isUint64Contains ? (
          <label className="field-label">
            Needle
            <input
              className="text-input"
              type="number"
              min={0}
              step={1}
              value={typeof config.needle === 'number' ? config.needle : 0}
              onChange={(event) => update({ needle: Number(event.target.value) })}
            />
            <span className="field-hint">
              连接唯一的 Uint64s values 输入；needle 必须是非负整数。
            </span>
          </label>
        ) : null}
        {isInt64Step ? (
          <label className="field-label">
            Steps（at:value，逗号分隔）
            <input
              className="text-input"
              value={stepsText(config.steps)}
              placeholder="0:0, 100:10"
              onChange={(event) => update({ steps: parseSteps(event.target.value) })}
            />
            <span className="field-hint">
              int64_step 只有一个 input；每个 step 需要整数 at 和 value。
            </span>
          </label>
        ) : null}
        {isStringEmpty || isUint64Empty ? (
          <div className="field-hint">
            {isStringEmpty ? 'Strings' : 'Uint64s'} is_empty 只能连接一个输入 values 端口。
          </div>
        ) : null}
        {node.data.nodeType === 'prefilter.combine' ? (
          <label className="field-label">
            Bitmap operator
            <select
              className="text-input"
              value={asText(config.op, 'and') === 'or' ? 'or' : 'and'}
              onChange={(event) => update({ op: event.target.value })}
            >
              <option value="and">AND · 交集</option>
              <option value="or">OR · 并集</option>
            </select>
            <span className="field-hint">输入数量由连入该节点的 Bitmap 端口决定。</span>
          </label>
        ) : null}
        {node.data.nodeType === 'prefilter.exclude' ? (
          <div className="field-hint">
            Exclude 会从 Prefilter 候选集中排除连接进来的 Bitmap 结果。
          </div>
        ) : null}
        {node.data.nodeType === 'prefilter.lookup' ? (
          <>
            <label className="field-label">
              Lookup kind
              <select
                className="text-input"
                value={
                  config.op === 'lookup_uint64'
                    ? 'uint64s'
                    : config.op === 'lookup_range'
                      ? 'int64'
                      : 'strings'
                }
                onChange={(event) => {
                  const valueType = event.target.value
                  const op =
                    valueType === 'uint64s'
                      ? 'lookup_uint64'
                      : valueType === 'int64'
                        ? 'lookup_range'
                        : 'lookup_string'
                  const nextConfig: Record<string, JsonValue> = { ...config, op }
                  delete nextConfig.valueType
                  delete nextConfig.values
                  delete nextConfig.min
                  delete nextConfig.max
                  updateNodeData(node.id, {
                    config: nextConfig,
                    inputTypes:
                      valueType === 'int64'
                        ? ['int64', 'int64']
                        : valueType === 'uint64s'
                          ? ['uint64s']
                          : ['strings'],
                    maxInputs: valueType === 'int64' ? 2 : 1,
                    requiredInputs: 0,
                  })
                }}
              >
                <option value="strings">Strings</option>
                <option value="uint64s">Uint64s</option>
                <option value="int64">Int64 range</option>
              </select>
            </label>
            <label className="field-label">
              Index
              <select
                className="text-input"
                value={asText(config.index)}
                onChange={(event) => update({ index: event.target.value })}
              >
                <option value="">选择索引…</option>
                {contract.indexes.map((index) => (
                  <option value={index.name} key={index.name}>
                    {index.name} · {index.type}
                  </option>
                ))}
              </select>
            </label>
            <div className="field-hint">
              连接相应类型的 Scalar 节点作为查询值；Range 需要 min 和 max 两个输入。
            </div>
          </>
        ) : null}
        {isGenericReference ? (
          <>
            <label className="field-label">
              Source
              <select
                className="text-input"
                value={safeReferenceSource}
                onChange={(event) => {
                  const source = event.target.value
                  const first = referenceFields(contract, operation, source)[0]
                  updateNodeData(node.id, {
                    config: {
                      ...config,
                      source,
                      name: first?.name ?? '',
                    },
                    outputType: first?.type ?? node.data.outputType,
                  })
                }}
              >
                {referenceSourceOptions.map((source) => (
                  <option value={source} key={source}>
                    {source}
                  </option>
                ))}
              </select>
            </label>
            <label className="field-label">
              名称
              <select
                className="text-input"
                value={asText(config.name)}
                onChange={(event) =>
                  updateNodeData(node.id, {
                    config: { ...config, source: safeReferenceSource, name: event.target.value },
                  })
                }
              >
                <option value="">选择字段…</option>
                {genericReferenceFields.map((field) => (
                  <option value={field.name} key={field.name}>
                    {field.name} · {field.type}
                    {'scope' in field ? ` · ${field.scope}` : ''}
                  </option>
                ))}
              </select>
            </label>
            <div className="field-hint">
              引用类型：{referenceType(operation)}；仅显示与当前 Source scope 兼容的 Contract 字段。
            </div>
          </>
        ) : null}
        {isEvaluationRoot ? (
          <div className="field-hint">
            固定出口：{asText(config.field)}。选择 Palette 节点可替换该出口的根表达式。
          </div>
        ) : null}
        {node.data.nodeType === 'prefilter.output' ? (
          <div className="field-hint output-node-hint">
            这是 Prefilter 的最终结果出口。将 Bitmap 节点连接到左侧输入即可定义最终候选集。
          </div>
        ) : null}
      </div>
      <details className="raw-config">
        <summary>查看节点 JSON</summary>
        <pre>{JSON.stringify(node.data.config, null, 2)}</pre>
      </details>
    </div>
  )
}
