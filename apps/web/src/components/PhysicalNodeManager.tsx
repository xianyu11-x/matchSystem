import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { formatApiError, isDemoMode } from '../lib/api'
import { formatNumber } from '../lib/format'
import {
  hasOriginalIdentity,
  keyFromSelection,
  newDeploymentForm,
  newPhysicalNodeForm,
  physicalNodeFormFromScenario,
  physicalNodeRuleOptions,
  type DeploymentForm,
  type PhysicalNodeForm,
} from '../lib/physicalNodes'
import { ruleIdentityLabel } from '../lib/ruleIdentity'
import {
  useAddPhysicalNode,
  useClearPhysicalNodeTickets,
  useDeletePhysicalNode,
  useRuleLibrary,
  useUpdatePhysicalNode,
} from '../lib/queries'
import type { MatchRuleDocument, Scenario, Topology } from '../types'
import { EmptyState, StatusPill } from './States'
import { RouteUpdateIntervalField } from './RouteUpdateIntervalField'

export function PhysicalNodeEditor({
  form,
  editing,
  busy,
  libraryReady,
  rules,
  onChange,
  onCancel,
  onSubmit,
}: {
  form: PhysicalNodeForm
  editing: boolean
  busy: boolean
  libraryReady: boolean
  rules: MatchRuleDocument[]
  onChange: (form: PhysicalNodeForm) => void
  onCancel: () => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
}) {
  const options = physicalNodeRuleOptions(rules)
  const updateDeployment = (rowId: string, patch: Partial<DeploymentForm>) =>
    onChange({
      ...form,
      deployments: form.deployments.map((row) =>
        row.rowId === rowId ? { ...row, ...patch } : row,
      ),
    })

  return (
    <form className="physical-node-add" onSubmit={onSubmit}>
      <h3>{editing ? `编辑物理节点 ${form.id}` : '添加物理节点'}</h3>
      <fieldset disabled={isDemoMode || busy}>
        <div className="physical-node-form-grid">
          <label className="field-label">
            物理节点标识
            <input
              className="text-input"
              required
              readOnly={editing}
              inputMode="numeric"
              placeholder="无前导零的正 uint64 十进制数"
              value={form.id}
              onChange={(event) => onChange({ ...form, id: event.target.value })}
            />
          </label>
          <label className="field-label">
            节点内逻辑节点调度算法
            <select
              className="text-input"
              value={form.selector}
              onChange={(event) => onChange({ ...form, selector: event.target.value })}
            >
              <option value="round_robin">轮询</option>
              <option value="largest_queue">最大队列优先</option>
              <option value="oldest_waiting">最长等待优先</option>
              <option value="smooth_weighted_round_robin">平滑加权轮询</option>
            </select>
          </label>
          <RouteUpdateIntervalField
            value={form.routeUpdateIntervalMs}
            onChange={(routeUpdateIntervalMs) => onChange({ ...form, routeUpdateIntervalMs })}
          />
        </div>
        <label className="physical-node-enabled">
          <input
            type="checkbox"
            checked={form.enabled}
            onChange={(event) => onChange({ ...form, enabled: event.target.checked })}
          />{' '}
          启用物理节点路由
        </label>
        <div className="deployment-heading">
          <strong>逻辑节点部署</strong>
          <button
            type="button"
            className="button button-ghost"
            onClick={() =>
              onChange({ ...form, deployments: [...form.deployments, newDeploymentForm()] })
            }
          >
            + 添加逻辑节点
          </button>
        </div>
        {form.deployments.length === 0 ? (
          <p className="field-hint">没有规则部署的节点不会接收匹配对象。</p>
        ) : null}
        {form.deployments.map((row, index) => {
          const unavailable = !!row.ruleId && !options.some((option) => option.value === row.ruleId)
          const missing = libraryReady && unavailable
          const originalMissing =
            !!row.originalRuleId && !options.some((option) => option.value === row.originalRuleId)
          const fallbackId = originalMissing ? row.originalRuleId! : unavailable ? row.ruleId : ''
          const missingKey =
            originalMissing && row.originalRule ? row.originalRule : keyFromSelection(fallbackId)
          const unchanged = hasOriginalIdentity(row)
          return (
            <section
              className="physical-deployment"
              key={row.rowId}
              aria-label={`逻辑节点部署 ${index + 1}`}
            >
              <div className="deployment-heading">
                <strong>逻辑节点 {index + 1}</strong>
                <button
                  type="button"
                  className="button button-ghost"
                  aria-label={`移除逻辑节点部署 ${index + 1}`}
                  onClick={() =>
                    onChange({
                      ...form,
                      deployments: form.deployments.filter((item) => item.rowId !== row.rowId),
                    })
                  }
                >
                  移除部署
                </button>
              </div>
              <div className="physical-node-form-grid">
                <label className="field-label">
                  规则
                  <select
                    className="text-input"
                    required
                    disabled={!libraryReady}
                    value={row.ruleId}
                    onChange={(event) =>
                      updateDeployment(row.rowId, { ruleId: event.target.value })
                    }
                  >
                    <option value="">请选择规则</option>
                    {fallbackId ? (
                      <option value={fallbackId}>
                        {missingKey ? ruleIdentityLabel(missingKey) : fallbackId}
                        {libraryReady ? ' · 当前快照（规则已不在规则库）' : ' · 当前快照'}
                      </option>
                    ) : null}
                    {options.map((option) => (
                      <option key={option.value} value={option.value}>
                        {option.label}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="field-label">
                  部署标识（PlacementID）
                  <input
                    className="text-input"
                    required
                    inputMode="numeric"
                    placeholder="无前导零的正 uint64 十进制数"
                    value={row.placementId}
                    onChange={(event) =>
                      updateDeployment(row.rowId, { placementId: event.target.value })
                    }
                  />
                </label>
                <label className="field-label">
                  路由/调度权重
                  <input
                    className="text-input"
                    inputMode="numeric"
                    required
                    value={row.weight}
                    onChange={(event) =>
                      updateDeployment(row.rowId, { weight: event.target.value })
                    }
                  />
                </label>
                <label className="physical-node-enabled">
                  <input
                    type="checkbox"
                    checked={row.enabled}
                    onChange={(event) =>
                      updateDeployment(row.rowId, { enabled: event.target.checked })
                    }
                  />{' '}
                  启用规则路由
                </label>
              </div>
              {unchanged ? (
                <label className="physical-node-enabled">
                  <input
                    type="checkbox"
                    checked={row.applyLatest}
                    disabled={(!libraryReady || missing) && !row.applyLatest}
                    onChange={(event) =>
                      updateDeployment(row.rowId, { applyLatest: event.target.checked })
                    }
                  />{' '}
                  应用规则库中的最新版本
                </label>
              ) : null}
              {missing && unchanged ? (
                <p className="field-hint">该规则已从规则库移除；保存时可继续使用当前运行快照。</p>
              ) : null}
              <label className="field-label">
                Tick Facts（JSON，可选）
                <textarea
                  className="text-input physical-node-json"
                  rows={4}
                  value={row.tickFacts}
                  placeholder={'{"int64s":{"wait":3}}'}
                  onChange={(event) =>
                    updateDeployment(row.rowId, { tickFacts: event.target.value })
                  }
                />
              </label>
              <p className="field-hint">
                大整数显示可能被舍入；不修改时保留服务端原值。手动修改时数值须为 JavaScript
                安全范围内的整数。
              </p>
              <label className="physical-node-enabled">
                <input
                  type="checkbox"
                  checked={row.pythonEnabled}
                  onChange={(event) =>
                    updateDeployment(row.rowId, { pythonEnabled: event.target.checked })
                  }
                />{' '}
                启用此逻辑节点的 Python Provider
              </label>
              {row.pythonEnabled ? (
                <div className="physical-node-form-grid">
                  <label className="field-label">
                    Provider 脚本
                    <input
                      className="text-input"
                      required
                      value={row.script}
                      onChange={(event) =>
                        updateDeployment(row.rowId, { script: event.target.value })
                      }
                    />
                  </label>
                  <label className="field-label">
                    Python 解释器（可选）
                    <input
                      className="text-input"
                      value={row.python}
                      onChange={(event) =>
                        updateDeployment(row.rowId, { python: event.target.value })
                      }
                    />
                  </label>
                  <label className="field-label">
                    RPC Runner（可选）
                    <input
                      className="text-input"
                      value={row.runner}
                      onChange={(event) =>
                        updateDeployment(row.rowId, { runner: event.target.value })
                      }
                    />
                  </label>
                  <label className="field-label">
                    RPC 超时毫秒（可选）
                    <input
                      className="text-input"
                      inputMode="numeric"
                      value={row.timeoutMs}
                      onChange={(event) =>
                        updateDeployment(row.rowId, { timeoutMs: event.target.value })
                      }
                    />
                  </label>
                </div>
              ) : null}
            </section>
          )
        })}
        <p className="field-hint">
          每个物理节点中同一规则只能部署一次；同一规则的 PlacementID 在整个场景内必须唯一。
        </p>
        {editing ? (
          <p className="field-hint">
            保留的部署默认继续使用当前规则快照；移除部署、修改身份、Facts 或
            Provider、应用不同的规则版本会移除该部署的等待对象，比赛历史保留。
          </p>
        ) : null}
        <div className="deployment-heading">
          <button
            className="button button-primary"
            type="submit"
            disabled={!libraryReady && form.deployments.length > 0}
          >
            {busy ? '保存中…' : editing ? '保存节点' : '添加节点'}
          </button>
          <button className="button button-ghost" type="button" onClick={onCancel}>
            取消
          </button>
        </div>
      </fieldset>
    </form>
  )
}

export function PhysicalNodeManager({
  topology,
  scenario,
}: {
  topology: Topology
  scenario?: Scenario
}) {
  const [active, setActive] = useState<{ editing: boolean; form: PhysicalNodeForm }>()
  const [message, setMessage] = useState<{
    kind: 'success' | 'error'
    text: string
    nodeId?: string
  }>()
  const controlsRef = useRef<HTMLDivElement>(null)
  const library = useRuleLibrary()
  const add = useAddPhysicalNode()
  const update = useUpdatePhysicalNode()
  const remove = useDeletePhysicalNode()
  const clear = useClearPhysicalNodeTickets()
  const busy = add.isPending || update.isPending || remove.isPending || clear.isPending

  useEffect(() => {
    if (active) controlsRef.current?.scrollIntoView({ block: 'start', behavior: 'smooth' })
  }, [active?.editing, active?.form.baseline])

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!active) return
    setMessage(undefined)
    try {
      await (active.editing ? update.mutateAsync(active.form) : add.mutateAsync(active.form))
      setMessage({
        kind: 'success',
        text: `物理节点 ${active.form.id} 已${active.editing ? '更新' : '添加'}。`,
      })
      setActive(undefined)
    } catch (error) {
      setMessage({ kind: 'error', text: formatApiError(error, '保存节点失败') })
    }
  }

  const editNode = (id: string) => {
    setMessage(undefined)
    try {
      if (!scenario?.rawScenario) throw new Error('无法读取当前场景，请刷新后重试')
      setActive({ editing: true, form: physicalNodeFormFromScenario(scenario.rawScenario, id) })
    } catch (error) {
      setMessage({ kind: 'error', text: formatApiError(error, '读取节点失败') })
    }
  }

  const deleteNode = async (id: string) => {
    setMessage(undefined)
    try {
      await remove.mutateAsync(id)
      if (active?.editing && active.form.id === id) setActive(undefined)
      setMessage({ kind: 'success', text: `物理节点 ${id} 已删除。` })
    } catch (error) {
      setMessage({ kind: 'error', text: formatApiError(error, '删除节点失败') })
    }
  }

  const clearTickets = async (id: string) => {
    if (!window.confirm(`确定清空物理节点 ${id} 的所有等待 Ticket？持续流量可能再次注入。`)) return
    setMessage(undefined)
    try {
      const result = await clear.mutateAsync(id)
      setMessage({
        kind: 'success',
        text: `物理节点 ${id} 已清空 ${formatNumber(result.removedTickets)} 个等待 Ticket。`,
        nodeId: id,
      })
    } catch (error) {
      setMessage({ kind: 'error', text: formatApiError(error, '清空等待 Ticket 失败'), nodeId: id })
    }
  }

  return (
    <>
      <div className="physical-node-controls" ref={controlsRef}>
        {isDemoMode ? (
          <p className="field-hint">演示模式仅展示拓扑，连接模拟器 API 后可修改节点。</p>
        ) : null}
        {library.isError ? (
          <p className="form-error" role="alert">
            规则库读取失败：{formatApiError(library.error)}{' '}
            <button
              className="button button-ghost"
              type="button"
              onClick={() => void library.refetch()}
            >
              重试
            </button>
          </p>
        ) : null}
        {library.data && library.data.rules.length === 0 ? (
          <p className="field-hint">
            规则库为空，可先到<Link to="/rules">规则配置</Link>创建规则。
          </p>
        ) : null}
        {active ? (
          <PhysicalNodeEditor
            form={active.form}
            editing={active.editing}
            busy={busy}
            libraryReady={!!library.data}
            rules={library.data?.rules ?? []}
            onChange={(form) => setActive({ ...active, form })}
            onCancel={() => setActive(undefined)}
            onSubmit={(event) => void submit(event)}
          />
        ) : (
          <button
            className="button button-ghost physical-node-open"
            type="button"
            disabled={isDemoMode || busy}
            onClick={() => {
              setMessage(undefined)
              setActive({ editing: false, form: newPhysicalNodeForm() })
            }}
          >
            + 添加物理节点
          </button>
        )}
        {message &&
        (!message.nodeId || !topology.physicalNodes.some((node) => node.id === message.nodeId)) ? (
          <p
            className={message.kind === 'error' ? 'form-error' : 'success-note'}
            role={message.kind === 'error' ? 'alert' : 'status'}
          >
            {message.text}
          </p>
        ) : null}
      </div>
      {topology.physicalNodes.length === 0 ? (
        <EmptyState title="暂无 PhysicalNode" detail="添加物理节点后，节点会出现在这里。" />
      ) : (
        <div className="topology-overview">
          <div className="router-node">
            <span className="node-glyph">R</span>
            <div>
              <strong>Router</strong>
              <span>路由入口</span>
            </div>
          </div>
          <div className="topology-links" aria-hidden="true">
            {topology.physicalNodes.map((physical) => (
              <span key={physical.id} />
            ))}
          </div>
          <div className="physical-nodes">
            {topology.physicalNodes.map((physical) => {
              const ticketCount = physical.logicalNodes.reduce(
                (sum, node) => sum + node.ticketCount,
                0,
              )
              return (
                <div className="physical-node" key={physical.id}>
                  <div className="node-heading">
                    <span
                      className={`node-dot node-dot-${physical.enabled ? 'healthy' : 'stopped'}`}
                    />
                    <strong>{physical.id}</strong>
                    <StatusPill
                      status={physical.enabled ? 'healthy' : 'stopped'}
                      label={physical.enabled ? '已启用' : '未启用'}
                    />
                  </div>
                  {physical.endpoint ? (
                    <span className="node-endpoint">{physical.endpoint}</span>
                  ) : null}
                  {physical.logicalNodes.length === 0 ? (
                    <p className="node-empty">暂无规则部署，不能接收匹配对象。</p>
                  ) : (
                    <ul className="node-logical-list">
                      {physical.logicalNodes.map((node) => (
                        <li className="node-logical" key={node.id}>
                          <div className="node-heading">
                            <strong>
                              {node.ruleKey} · {node.placementId}
                            </strong>
                            <StatusPill status={node.state} />
                          </div>
                          <span>{formatNumber(node.ticketCount)} 个等待对象</span>
                        </li>
                      ))}
                    </ul>
                  )}
                  <div className="node-actions">
                    <p>
                      删除将移除该节点的 {formatNumber(physical.logicalNodes.length)} 个部署和当前{' '}
                      {formatNumber(ticketCount)} 个等待对象，保留比赛历史。
                    </p>
                    <button
                      type="button"
                      className="button button-ghost"
                      aria-label={`清空物理节点 ${physical.id} 的等待 Ticket`}
                      disabled={isDemoMode || busy || ticketCount === 0}
                      onClick={() => void clearTickets(physical.id)}
                    >
                      {clear.isPending && clear.variables === physical.id
                        ? '清空中…'
                        : '清空等待 Ticket'}
                    </button>
                    <button
                      type="button"
                      className="button button-ghost"
                      aria-label={`编辑物理节点 ${physical.id}`}
                      disabled={isDemoMode || busy || !scenario?.rawScenario}
                      onClick={() => editNode(physical.id)}
                    >
                      编辑节点
                    </button>
                    <button
                      type="button"
                      className="button button-ghost"
                      aria-label={`删除物理节点 ${physical.id}`}
                      disabled={isDemoMode || busy}
                      onClick={() => void deleteNode(physical.id)}
                    >
                      {remove.isPending && remove.variables === physical.id
                        ? '删除中…'
                        : '删除节点'}
                    </button>
                  </div>
                  {message?.nodeId === physical.id ? (
                    <p
                      className={message.kind === 'error' ? 'form-error' : 'success-note'}
                      role={message.kind === 'error' ? 'alert' : 'status'}
                    >
                      {message.text}
                    </p>
                  ) : null}
                </div>
              )
            })}
          </div>
        </div>
      )}
    </>
  )
}
