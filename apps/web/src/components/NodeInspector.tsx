import { useRuleStore } from '../lib/ruleStore'
import { typeLabel } from '../lib/graph'
import type { JsonValue, LogicalNodeContract } from '../types'

function asText(value: unknown, fallback = ''): string {
  return typeof value === 'string' ? value : fallback
}

function valuesText(value: unknown): string {
  return Array.isArray(value) ? value.join(', ') : ''
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
  const isEvaluationRoot =
    node.data.nodeType === 'evaluation.join' || node.data.nodeType === 'evaluation.complete'
  const update = (patch: Record<string, JsonValue>) =>
    updateNodeData(node.id, { config: { ...config, ...patch } })
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
          disabled={isEvaluationRoot}
          title={isEvaluationRoot ? 'Evaluation 根出口不能删除' : '删除节点'}
        >
          ×
        </button>
      </div>
      <div className="inspector-meta">
        <span>{node.data.nodeType}</span>
        <span>out · {typeLabel[node.data.outputType]}</span>
      </div>
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
              onChange={(event) => update({ op: event.target.value })}
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
              onChange={(event) => update({ op: event.target.value })}
            >
              <option value="strings_eq">完全相等</option>
              <option value="strings_neq">不相等</option>
              <option value="strings_contains_any">包含任一</option>
              <option value="strings_contains_all">包含全部</option>
              <option value="strings_intersects">存在交集</option>
            </select>
          </label>
        ) : null}
        {node.data.nodeType === 'compare.uint64' ||
        node.data.nodeType === 'expression.generic' ||
        node.data.nodeType === 'prefilter.generic' ? (
          <label className="field-label">
            Operation
            <input
              className="text-input"
              value={asText(config.op)}
              onChange={(event) => update({ op: event.target.value })}
            />
          </label>
        ) : null}
        {node.data.nodeType === 'prefilter.lookup' ? (
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
        ) : null}
        {isEvaluationRoot ? (
          <div className="field-hint">
            固定出口：{asText(config.field)}。选择 Palette 节点可替换该出口的根表达式。
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
