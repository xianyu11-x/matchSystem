import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent } from 'react'
import {
  applyEdgeChanges,
  applyNodeChanges,
  Background,
  Controls,
  Handle,
  MiniMap,
  Panel,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useReactFlow,
  type Connection,
  type EdgeChange,
  type NodeChange,
  type NodeProps,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { isConnectionValid, typeLabel, validateGraph } from '../lib/graph'
import { capabilityTooltip, describeRuleGraphNode } from '../lib/capabilityDescriptions'
import {
  isInteractiveRuleGraphTarget,
  isRuleGraphCanvasContext,
  isRuleGraphDeleteKey,
} from '../lib/ruleGraphKeyboard'
import { useRuleStore } from '../lib/ruleStore'
import type {
  CapabilityNode,
  Capabilities,
  JsonValue,
  RuleDocument,
  RuleGraphEdge,
  RuleGraphNode,
} from '../types'

function RuleNodeView({ data, selected }: NodeProps<RuleGraphNode>) {
  const inputTypes =
    data.variadic && data.inputTypes.length < (data.maxInputs ?? 16)
      ? [...data.inputTypes, data.variadicInputType ?? data.inputTypes.at(-1) ?? data.outputType]
      : data.inputTypes
  return (
    <div
      className={`rule-node-card node-${data.nodeType.replaceAll('.', '-')} ${selected ? 'is-selected' : ''} ${data.valid === false ? 'has-error' : ''}`}
      title={describeRuleGraphNode(data)}
    >
      {inputTypes.map((inputType, index) => (
        <Handle
          key={`${data.nodeType}-input-${index}`}
          type="target"
          position={Position.Left}
          id={`input-${index}`}
          style={{ top: `${((index + 1) / (inputTypes.length + 1)) * 100}%` }}
        />
      ))}
      <div className="rule-node-topline">
        <span className="node-type-dot" /> <span>{data.nodeType}</span>
      </div>
      <strong>{data.label}</strong>
      <div className="rule-node-type">out · {typeLabel[data.outputType]}</div>
      {data.comment ? <p className="rule-node-comment">{data.comment}</p> : null}
      {data.error ? <div className="rule-node-error">{data.error}</div> : null}
      {data.nodeType === 'prefilter.output' ||
      data.nodeType === 'evaluation.join' ||
      data.nodeType === 'evaluation.complete' ? null : (
        <Handle type="source" position={Position.Right} id="output" />
      )}
    </div>
  )
}

export function RuleCanvas(props: { document: RuleDocument; capabilities: Capabilities }) {
  return (
    <ReactFlowProvider>
      <RuleCanvasContent {...props} />
    </ReactFlowProvider>
  )
}

const nodeTypes = { rule: RuleNodeView }

const categoryLabels: Record<CapabilityNode['category'], string> = {
  source: '数据源',
  literal: '常量',
  expression: 'Expression',
  prefilter: 'Prefilter',
  evaluation: 'Evaluation',
}

const defaultConfig = (
  capability: CapabilityNode,
  document: RuleDocument,
): Record<string, JsonValue> => {
  const op = capability.op
  if (op === 'strings_contains') return { op, needle: '' }
  if (op === 'uint64s_contains') return { op, needle: 0 }
  if (op === 'int64_step') return { op, steps: [{ at: 0, value: 0 }] }
  if (op === 'and' || op === 'or' || op === 'bool_and' || op === 'bool_or')
    return { op, children: [] }
  if (capability.type === 'source.attribute') {
    const attribute = document.contract.attributes[0]
    return {
      op:
        attribute?.type === 'int64'
          ? 'int64_ref'
          : attribute?.type === 'uint64s'
            ? 'uint64s_ref'
            : 'strings_ref',
      source: 'candidate_attributes',
      name: attribute?.name ?? '',
    }
  }
  if (capability.type === 'source.fact') {
    const fact = document.contract.facts[0]
    return {
      op:
        fact?.type === 'int64'
          ? 'int64_ref'
          : fact?.type === 'uint64s'
            ? 'uint64s_ref'
            : 'strings_ref',
      source:
        fact?.scope === 'tick'
          ? 'tick_facts'
          : fact?.scope === 'match'
            ? 'match_facts'
            : 'candidate_facts',
      name: fact?.name ?? '',
    }
  }
  if (capability.type === 'literal.string')
    return { op: 'strings_literal', values: ['ap-southeast'] }
  if (capability.type === 'literal.uint64') return { op: 'uint64s_literal', values: [1] }
  if (capability.type === 'literal.bool') return { op: 'bool_literal', value: true }
  if (capability.type === 'literal.int64') return { op: 'int64_literal', value: 0 }
  if (capability.type === 'compare.int64')
    return { op: capability.op?.startsWith('int64_') ? capability.op : 'int64_eq' }
  if (capability.type === 'compare.strings')
    return { op: capability.op?.startsWith('strings_') ? capability.op : 'strings_contains_any' }
  if (capability.type === 'compare.uint64')
    return { op: capability.op?.startsWith('uint64s_') ? capability.op : 'uint64s_contains_any' }
  if (capability.type === 'logic.and') return { op: 'bool_and', children: [] }
  if (capability.type === 'logic.or') return { op: 'bool_or', children: [] }
  if (capability.type === 'logic.not') return { op: 'bool_not' }
  if (capability.type === 'prefilter.lookup') {
    const op =
      capability.op === 'lookup_uint64' || capability.op === 'lookup_range'
        ? capability.op
        : 'lookup_string'
    return {
      op,
      index: document.contract.indexes[0]?.name ?? '',
      valueType: op === 'lookup_uint64' ? 'uint64s' : op === 'lookup_range' ? 'int64' : 'strings',
    }
  }
  if (capability.type === 'prefilter.exclude') return { op: 'exclude', value: { op: 'none' } }
  if (capability.type === 'prefilter.combine') return { op: 'and', children: [] }
  if (capability.type === 'expression.generic' || capability.type === 'prefilter.generic')
    return { op: capability.op ?? capability.label }
  if (capability.type === 'evaluation.join') return { field: 'canJoin' }
  if (capability.type === 'evaluation.complete') return { field: 'canComplete' }
  return {}
}

function createNode(
  capability: CapabilityNode,
  document: RuleDocument,
  index: number,
  position?: { x: number; y: number },
): RuleGraphNode {
  const id = `${capability.type.replaceAll('.', '-')}-${Date.now()}-${index}`
  const selectedField =
    capability.type === 'source.attribute'
      ? document.contract.attributes[0]
      : capability.type === 'source.fact'
        ? document.contract.facts[0]
        : undefined
  return {
    id,
    type: 'rule',
    position: position ?? { x: 150 + (index % 3) * 220, y: 120 + Math.floor(index / 3) * 140 },
    data: {
      label: capability.label,
      nodeType: capability.type,
      outputType: selectedField?.type ?? capability.outputType,
      inputTypes: capability.variadic
        ? []
        : capability.maxInputs > capability.inputTypes.length && capability.variadic !== false
          ? []
          : capability.inputTypes,
      maxInputs: capability.maxInputs,
      requiredInputs:
        capability.variadic ||
        (capability.maxInputs > capability.inputTypes.length && capability.variadic !== false)
          ? 0
          : capability.inputTypes.length,
      variadic: capability.variadic ?? capability.maxInputs > capability.inputTypes.length,
      variadicInputType:
        capability.variadicInputType ??
        (capability.variadic || capability.maxInputs > capability.inputTypes.length
          ? capability.inputTypes.at(-1)
          : undefined),
      config: defaultConfig(capability, document),
      comment: '',
    },
  }
}

function RuleCanvasContent({
  document,
  capabilities,
}: {
  document: RuleDocument
  capabilities: Capabilities
}) {
  const { screenToFlowPosition } = useReactFlow()
  const nodes = useRuleStore((state) => state.document?.graph.nodes ?? [])
  const edges = useRuleStore((state) => state.document?.graph.edges ?? [])
  const setGraph = useRuleStore((state) => state.setGraph)
  const connectGraph = useRuleStore((state) => state.connectGraph)
  const selectNode = useRuleStore((state) => state.selectNode)
  const removeNode = useRuleStore((state) => state.removeNode)
  const removeGraphEdge = useRuleStore((state) => state.removeGraphEdge)
  const notice = useRuleStore((state) => state.notice)
  const selectedNodeId = useRuleStore((state) => state.selectedNodeId)
  const addNode = useRuleStore((state) => state.addNode)
  const [connectionError, setConnectionError] = useState<string>()
  const [paletteOpen, setPaletteOpen] = useState(true)
  const canvasRef = useRef<HTMLDivElement>(null)
  const dragTypeKey = 'application/x-matchscope-rule-node'

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (
        !isRuleGraphDeleteKey(event) ||
        event.defaultPrevented ||
        isInteractiveRuleGraphTarget(event.target) ||
        !isRuleGraphCanvasContext(event.target, window.document.activeElement, canvasRef.current) ||
        !selectedNodeId
      )
        return
      const selectedNode = nodes.find((node) => node.id === selectedNodeId)
      if (!selectedNode) return

      // Output sinks are part of the rule envelope and are deliberately
      // permanent. Keep the browser from performing any default action while
      // leaving the selected sink visible.
      event.preventDefault()
      if (
        selectedNode.data.nodeType === 'prefilter.output' ||
        selectedNode.data.nodeType === 'evaluation.join' ||
        selectedNode.data.nodeType === 'evaluation.complete'
      )
        return
      removeNode(selectedNode.id)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [nodes, removeNode, selectedNodeId])

  const addPaletteNode = useCallback(
    (capability: CapabilityNode, position?: { x: number; y: number }) => {
      addNode(createNode(capability, document, nodes.length, position), { standalone: true })
    },
    [addNode, document, nodes.length],
  )

  const onPaletteDragStart = useCallback(
    (event: DragEvent<HTMLButtonElement>, capability: CapabilityNode) => {
      event.dataTransfer.effectAllowed = 'copy'
      event.dataTransfer.setData(
        dragTypeKey,
        JSON.stringify({ type: capability.type, op: capability.op ?? '' }),
      )
    },
    [],
  )

  const onCanvasDrop = useCallback(
    (event: DragEvent<HTMLDivElement>) => {
      event.preventDefault()
      const raw = event.dataTransfer.getData(dragTypeKey)
      if (!raw) return
      let descriptor: { type?: string; op?: string }
      try {
        descriptor = JSON.parse(raw) as { type?: string; op?: string }
      } catch {
        return
      }
      const capability = capabilities.nodeTypes.find(
        (item) => item.type === descriptor.type && (item.op ?? '') === (descriptor.op ?? ''),
      )
      if (!capability) return
      const position = screenToFlowPosition({ x: event.clientX, y: event.clientY })
      addPaletteNode(capability, position)
    },
    [addPaletteNode, capabilities.nodeTypes, screenToFlowPosition],
  )

  const onCanvasDragOver = useCallback((event: DragEvent<HTMLDivElement>) => {
    event.preventDefault()
    event.dataTransfer.dropEffect = 'copy'
  }, [])

  const graphResult = useMemo(
    () => validateGraph({ nodes, edges }, document.contract),
    [nodes, edges, document.contract],
  )
  const nodeErrorMap = useMemo(() => {
    const map = new Map<string, string>()
    for (const error of graphResult.errors) {
      const match = error.path.match(/^\/graph\/nodes\/([^/]+)/)
      if (match && !map.has(match[1])) map.set(match[1], error.message)
    }
    return map
  }, [graphResult.errors])
  const displayNodes = useMemo(
    () =>
      nodes.map((node) => ({
        ...node,
        selected: node.id === selectedNodeId,
        data: { ...node.data, valid: !nodeErrorMap.has(node.id), error: nodeErrorMap.get(node.id) },
      })),
    [nodes, nodeErrorMap, selectedNodeId],
  )

  const onNodesChange = useCallback(
    (changes: NodeChange<RuleGraphNode>[]) => {
      const removals = changes.filter((change) => change.type === 'remove')
      removals.forEach((change) => removeNode(change.id))
      const visualChanges = changes.filter((change) => change.type !== 'remove')
      if (visualChanges.length > 0)
        setGraph(applyNodeChanges(visualChanges, nodes) as RuleGraphNode[], edges)
    },
    [edges, nodes, removeNode, setGraph],
  )
  const onEdgesChange = useCallback(
    (changes: EdgeChange<RuleGraphEdge>[]) => {
      const removals = changes.filter((change) => change.type === 'remove')
      removals.forEach((change) => removeGraphEdge(change.id))
      const visualChanges = changes.filter((change) => change.type !== 'remove')
      if (visualChanges.length > 0)
        setGraph(nodes, applyEdgeChanges(visualChanges, edges) as RuleGraphEdge[])
    },
    [edges, nodes, removeGraphEdge, setGraph],
  )
  const connectionCheck = useCallback(
    (connection: Connection | RuleGraphEdge) =>
      isConnectionValid(
        {
          source: connection.source,
          target: connection.target,
          sourceHandle: connection.sourceHandle ?? null,
          targetHandle: connection.targetHandle ?? null,
        },
        nodes,
        edges,
      ).valid,
    [edges, nodes],
  )
  const onConnect = useCallback(
    (connection: Connection) => {
      const result = isConnectionValid(connection, nodes, edges)
      if (!result.valid) {
        setConnectionError(result.reason)
        return
      }
      setConnectionError(undefined)
      const source = nodes.find((node) => node.id === connection.source)
      const edge: RuleGraphEdge = {
        ...connection,
        id: `edge-${connection.source}-${connection.target}-${Date.now()}`,
        type: 'smoothstep',
        data: { valueType: source?.data.outputType ?? 'bool' },
      }
      const targetIndex = Number((connection.targetHandle ?? '').replace('input-', ''))
      connectGraph(
        connection.source,
        connection.target,
        Number.isInteger(targetIndex) ? targetIndex : 0,
        edge,
      )
    },
    [connectGraph, edges, nodes],
  )

  const categories = useMemo(
    () =>
      Object.keys(categoryLabels)
        .map((category) => ({
          category: category as CapabilityNode['category'],
          items: capabilities.nodeTypes.filter(
            (item) =>
              item.category === category &&
              item.type !== 'evaluation.join' &&
              item.type !== 'evaluation.complete',
          ),
        }))
        .filter((group) => group.items.length > 0),
    [capabilities.nodeTypes],
  )

  return (
    <div className="rule-editor-stage">
      <aside className={`node-palette ${paletteOpen ? '' : 'palette-collapsed'}`}>
        <button
          className="palette-toggle"
          type="button"
          onClick={() => setPaletteOpen((value) => !value)}
        >
          {paletteOpen ? '收起 palette' : '打开 palette'}
        </button>
        {paletteOpen ? (
          <>
            <div className="palette-heading">
              <span className="eyebrow">NODE PALETTE</span>
              <span>{capabilities.nodeTypes.length} types</span>
            </div>
            {categories.map((group) => (
              <div className="palette-group" key={group.category}>
                <span className="palette-group-title">{categoryLabels[group.category]}</span>
                {group.items.map((capability) => (
                  <button
                    className="palette-item"
                    type="button"
                    key={`${capability.category}-${capability.type}-${capability.op ?? ''}`}
                    draggable
                    onDragStart={(event) => onPaletteDragStart(event, capability)}
                    onClick={() => addPaletteNode(capability)}
                    title={capabilityTooltip(capability)}
                  >
                    <span className="palette-item-mark">+</span>
                    <span>
                      <strong>{capability.label}</strong>
                      <small>{typeLabel[capability.outputType]} output</small>
                    </span>
                  </button>
                ))}
              </div>
            ))}
            <p className="palette-hint">
              拖动节点到画布后，再从输出端口连到任意兼容输入；节点可先独立配置和添加注释。
            </p>
          </>
        ) : null}
      </aside>
      <div
        ref={canvasRef}
        className="flow-canvas"
        tabIndex={0}
        aria-label="Rule Graph 画布"
        onDrop={onCanvasDrop}
        onDragOver={onCanvasDragOver}
      >
        <ReactFlow
          nodes={displayNodes}
          edges={edges}
          nodeTypes={nodeTypes}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onConnect={onConnect}
          isValidConnection={connectionCheck}
          onNodeClick={(_, node) => {
            selectNode(node.id)
            canvasRef.current?.focus()
          }}
          onPaneClick={() => {
            selectNode(undefined)
            canvasRef.current?.focus()
          }}
          deleteKeyCode={null}
          fitView
          fitViewOptions={{ padding: 0.2 }}
          minZoom={0.25}
          maxZoom={1.5}
          proOptions={{ hideAttribution: true }}
        >
          <Background color="#dce2ec" gap={20} size={1} />
          <Controls showInteractive={false} />
          <MiniMap
            pannable
            zoomable
            nodeColor={(node) => (node.id === selectedNodeId ? '#3567f0' : '#bbc7d9')}
          />
          <Panel position="top-right">
            <div className={`graph-health ${graphResult.valid ? 'is-valid' : 'is-invalid'}`}>
              <span />
              {graphResult.valid ? '图结构合法' : `${graphResult.errors.length} 个问题`}
            </div>
          </Panel>
          {connectionError ? (
            <Panel position="bottom-center">
              <div className="connection-error">{connectionError}</div>
            </Panel>
          ) : null}
          {notice ? (
            <Panel position="bottom-left">
              <div className="connection-error">{notice}</div>
            </Panel>
          ) : null}
        </ReactFlow>
      </div>
    </div>
  )
}
