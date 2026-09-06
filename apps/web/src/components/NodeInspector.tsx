import { useRuleStore } from '../lib/ruleStore'
import { astInputSlots, nodeTypeForOp } from '../lib/graphBuilder'
import { expressionLabels } from '../lib/expressionForm'
import { typeLabel } from '../lib/graph'
import { ExpressionEditor } from './ExpressionEditor'
import type { JsonObject, LogicalNodeContract } from '../types'

export function NodeInspector({ contract }: { contract: LogicalNodeContract }) {
  const document = useRuleStore((state) => state.document)
  const selectedNodeId = useRuleStore((state) => state.selectedNodeId)
  const updateNodeData = useRuleStore((state) => state.updateNodeData)
  const removeNode = useRuleStore((state) => state.removeNode)
  const selectNode = useRuleStore((state) => state.selectNode)
  const node = document?.graph.nodes.find((item) => item.id === selectedNodeId)
  if (!node)
    return (
      <div className="inspector-empty">
        <strong>选择一个节点</strong>
        <p>从图中选择节点，在这里编辑属性和输入。</p>
      </div>
    )
  const fixed = ['evaluation.join', 'evaluation.complete', 'prefilter.output'].includes(
    node.data.nodeType,
  )
  const update = (config: JsonObject) => {
    const op = String(config.op)
    const slots = astInputSlots(op, config)
    const variadic = [
      'and',
      'or',
      'bool_and',
      'bool_or',
      'strings_union',
      'uint64s_union',
    ].includes(op)
    updateNodeData(node.id, {
      config,
      nodeType: nodeTypeForOp(op, node.data.outputType, String(config.source ?? '')),
      label: expressionLabels[op] ?? op,
      inputTypes: slots.map((slot) => slot.expectedType),
      requiredInputs: slots.length,
      maxInputs: variadic ? 16 : slots.length,
      variadic,
      variadicInputType: variadic ? node.data.outputType : undefined,
    })
  }
  return (
    <div className="inspector-content">
      <div className="inspector-heading">
        <h3>{node.data.label}</h3>
        <button
          className="icon-button danger"
          type="button"
          aria-label="删除节点"
          disabled={fixed}
          onClick={() => removeNode(node.id)}
        >
          ×
        </button>
      </div>
      <p className="field-hint">输出：{typeLabel[node.data.outputType]}</p>
      <label className="field-label">
        节点注释
        <textarea
          className="text-input text-area"
          rows={3}
          value={node.data.comment ?? ''}
          onChange={(event) => updateNodeData(node.id, { comment: event.target.value })}
        />
      </label>
      {fixed ? (
        <p className="field-hint">固定出口；从图中连接表达式，或在预筛选、加入与成局表单中编辑。</p>
      ) : (
        <ExpressionEditor
          key={node.id}
          label="节点配置"
          value={node.data.config}
          type={node.data.outputType}
          contract={contract}
          onChange={update}
          onNavigateInput={(slot) => {
            const index = astInputSlots(String(node.data.config.op), node.data.config).findIndex(
              (input) => input.key === slot,
            )
            const edge = document?.graph.edges.find(
              (item) => item.target === node.id && item.targetHandle === `input-${index}`,
            )
            if (!edge) return false
            selectNode(edge.source)
            return true
          }}
        />
      )}
    </div>
  )
}
