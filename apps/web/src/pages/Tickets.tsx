import { useEffect, useMemo, useState } from 'react'
import {
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  SectionTitle,
} from '../components/States'
import { TrafficControl } from '../components/TrafficControl'
import { TicketTable } from '../components/TicketTable'
import {
  useCreateBatch,
  useCreateTicket,
  useDeleteTicket,
  useScenario,
  useTickets,
} from '../lib/queries'
import type {
  AttributeGenerator,
  BatchGeneratorSpec,
  FactSnapshot,
  TicketInput,
  TypedAttributes,
} from '../types'
import { formatNumber } from '../lib/format'

const splitValues = (value: string) =>
  value
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)

const parseUint64Values = (value: string): number[] =>
  splitValues(value).flatMap((item) => {
    const parsed = Number(item)
    return Number.isSafeInteger(parsed) && parsed >= 0 ? [parsed] : []
  })

const parseInt64Value = (value: string): number | undefined => {
  if (!value.trim()) return undefined
  const parsed = Number(value)
  return Number.isSafeInteger(parsed) ? parsed : undefined
}

const selectionKey = (ruleKey: string, placementId: string) => `${ruleKey}@@${placementId}`

function TicketComposer() {
  const scenarioQuery = useScenario()
  const createTicket = useCreateTicket()
  const createBatch = useCreateBatch()
  const [ruleSelection, setRuleSelection] = useState('')
  const [ticketId, setTicketId] = useState('')
  const [attributeDraft, setAttributeDraft] = useState<Record<string, string>>({})
  const [factDraft, setFactDraft] = useState<Record<string, string>>({})
  const [generators, setGenerators] = useState<Record<string, AttributeGenerator>>({})
  const setGenerator = (name: string, value: AttributeGenerator) =>
    setGenerators((current) => ({ ...current, [name]: value }))
  const [batch, setBatch] = useState<Pick<BatchGeneratorSpec, 'count' | 'seed' | 'startTicketId'>>({
    count: 1000,
    seed: 20260829,
    startTicketId: Date.now() * 1000,
  })

  const rules = scenarioQuery.data?.rules ?? []
  const activeRule =
    rules.find((rule) => selectionKey(rule.ruleKey, rule.placementId) === ruleSelection) ?? rules[0]
  const contract = activeRule?.contract
  const objectFacts = useMemo(
    () => contract?.facts.filter((fact) => fact.scope === 'object') ?? [],
    [contract],
  )

  useEffect(() => {
    if (!activeRule) return
    const key = selectionKey(activeRule.ruleKey, activeRule.placementId)
    setRuleSelection((current) => (current === key ? current : key))
    setAttributeDraft((current) =>
      Object.fromEntries(
        activeRule.contract.attributes.map((field) => [field.name, current[field.name] ?? '']),
      ),
    )
    setFactDraft((current) =>
      Object.fromEntries(
        activeRule.contract.facts
          .filter((field) => field.scope === 'object')
          .map((field) => [field.name, current[field.name] ?? '']),
      ),
    )
    setGenerators({})
  }, [activeRule])

  const buildAttributes = (): TypedAttributes => {
    const result: TypedAttributes = { strings: {}, uint64s: {}, int64: {} }
    for (const field of contract?.attributes ?? []) {
      const raw = (attributeDraft[field.name] ?? '').trim()
      if (!raw) continue
      if (field.type === 'strings') result.strings[field.name] = splitValues(raw)
      else if (field.type === 'uint64s') result.uint64s[field.name] = parseUint64Values(raw)
      else {
        const parsed = parseInt64Value(raw)
        if (parsed !== undefined) result.int64[field.name] = parsed
      }
    }
    return result
  }

  const buildFacts = (): FactSnapshot => {
    const result: FactSnapshot = {}
    for (const field of objectFacts) {
      const raw = (factDraft[field.name] ?? '').trim()
      if (!raw) continue
      if (field.type === 'strings') result[field.name] = splitValues(raw)
      else if (field.type === 'uint64s') result[field.name] = parseUint64Values(raw)
      else {
        const parsed = parseInt64Value(raw)
        if (parsed !== undefined) result[field.name] = parsed
      }
    }
    return result
  }

  const buildBatchSpec = (): BatchGeneratorSpec | undefined => {
    if (!activeRule) return undefined
    return {
      count: Math.max(0, Math.trunc(batch.count)),
      seed: Math.trunc(batch.seed),
      startTicketId: batch.startTicketId ? Math.trunc(batch.startTicketId) : undefined,
      ruleKey: activeRule.ruleKey,
      rule: activeRule.apiRule,
      placementId: activeRule.placementId,
      attributeGenerators: generators,
    }
  }

  const submitTicket = () => {
    if (!activeRule) return
    const input: TicketInput = {
      ticketId: ticketId.trim() || undefined,
      rule: activeRule.apiRule,
      placementId: activeRule.placementId,
      attributes: buildAttributes(),
      facts: buildFacts(),
    }
    createTicket.mutate(input)
  }

  if (scenarioQuery.isLoading) return <LoadingState label="正在加载场景 Contract…" />
  if (scenarioQuery.isError)
    return <ErrorState error={scenarioQuery.error} onRetry={() => scenarioQuery.refetch()} />
  if (!activeRule)
    return (
      <EmptyState title="场景没有可用 Rule" detail="请先在 simulator API 配置 LogicalNode rule。" />
    )

  return (
    <div className="composer-stack">
      <div className="subsection">
        <SectionTitle title="自定义 Ticket" detail="字段完全来自当前 Rule 的 Contract" />
        <div className="form-grid">
          <label className="field-label">
            Rule / Placement
            <select
              className="text-input"
              value={ruleSelection}
              onChange={(event) => setRuleSelection(event.target.value)}
            >
              {rules.map((rule) => (
                <option
                  value={selectionKey(rule.ruleKey, rule.placementId)}
                  key={selectionKey(rule.ruleKey, rule.placementId)}
                >
                  {rule.displayName}
                </option>
              ))}
            </select>
          </label>
          <label className="field-label">
            Ticket ID
            <input
              className="text-input"
              value={ticketId}
              placeholder="留空由客户端生成安全整数 ID"
              onChange={(event) => setTicketId(event.target.value)}
            />
          </label>
        </div>
        <div className="form-divider">
          <span>Ticket Attributes</span>
          <small>严格按 Contract 的名称和类型提交</small>
        </div>
        <div className="form-grid">
          {contract?.attributes.map((field) => (
            <label className="field-label" key={field.name}>
              {field.name} / {field.type}
              <input
                className="text-input"
                type={field.type === 'int64' ? 'number' : 'text'}
                inputMode={
                  field.type === 'int64' || field.type === 'uint64s' ? 'numeric' : undefined
                }
                placeholder={field.type === 'int64' ? '整数' : '逗号分隔'}
                value={attributeDraft[field.name] ?? ''}
                onChange={(event) =>
                  setAttributeDraft((current) => ({ ...current, [field.name]: event.target.value }))
                }
              />
            </label>
          ))}
        </div>
        <div className="form-divider">
          <span>Object Facts</span>
          <small>随 Ticket 一起提交的 object-scope snapshot</small>
        </div>
        <div className="form-grid">
          {objectFacts.map((fact) => (
            <label className="field-label" key={fact.name}>
              {fact.name} / {fact.type}
              <input
                className="text-input"
                type={fact.type === 'int64' ? 'number' : 'text'}
                inputMode={fact.type === 'int64' || fact.type === 'uint64s' ? 'numeric' : undefined}
                placeholder={fact.type === 'int64' ? '整数' : '逗号分隔'}
                value={factDraft[fact.name] ?? ''}
                onChange={(event) =>
                  setFactDraft((current) => ({ ...current, [fact.name]: event.target.value }))
                }
              />
            </label>
          ))}
          {objectFacts.length === 0 ? (
            <span className="muted">当前 Contract 没有 object-scope Fact。</span>
          ) : null}
        </div>
        <button
          className="button button-primary"
          type="button"
          onClick={submitTicket}
          disabled={createTicket.isPending}
        >
          {createTicket.isPending ? '提交中…' : '加入等待队列'}
        </button>
        {createTicket.isError ? (
          <p className="form-error">
            {createTicket.error instanceof Error ? createTicket.error.message : 'Ticket 提交失败'}
          </p>
        ) : null}
        {createTicket.isSuccess ? (
          <p className="success-note">Ticket {createTicket.data.ticketId} 已路由到等待队列。</p>
        ) : null}
      </div>

      <div className="subsection batch-panel">
        <SectionTitle title="Batch Generator" detail="配置属性分布与多值抽样，由服务端生成" />
        <div className="form-grid form-grid-compact">
          <label className="field-label">
            数量
            <input
              className="text-input"
              type="number"
              min="1"
              max="1000000"
              value={batch.count}
              onChange={(event) => setBatch({ ...batch, count: Number(event.target.value) })}
            />
          </label>
          <label className="field-label">
            随机种子
            <input
              className="text-input"
              type="number"
              min="0"
              value={batch.seed}
              onChange={(event) => setBatch({ ...batch, seed: Number(event.target.value) })}
            />
          </label>
          <label className="field-label">
            起始 Ticket ID
            <input
              className="text-input"
              type="number"
              min="1"
              max="9007199254740991"
              value={batch.startTicketId ?? ''}
              onChange={(event) =>
                setBatch({
                  ...batch,
                  startTicketId: event.target.value ? Number(event.target.value) : undefined,
                })
              }
            />
          </label>
          <label className="field-label">
            Rule / Placement
            <input className="text-input" value={activeRule.displayName} readOnly />
          </label>
        </div>
        <p className="muted">启用属性后配置生成规则。整数按十进制文本传输；未启用的属性不生成。</p>
        <div className="form-grid">
          {contract?.attributes.map((field) => {
            const g = generators[field.name]
            return (
              <div className="field-label" key={field.name}>
                <label>
                  <input
                    type="checkbox"
                    checked={!!g}
                    onChange={(event) => {
                      if (event.target.checked)
                        setGenerator(field.name, {
                          type: field.type,
                          source: 'sample',
                          ...(field.type === 'int64'
                            ? { min: '0', max: '100' }
                            : field.type === 'strings'
                              ? { values: ['a', 'b'] }
                              : { set: '1-100' }),
                        })
                      else
                        setGenerators((current) =>
                          Object.fromEntries(
                            Object.entries(current).filter(([name]) => name !== field.name),
                          ),
                        )
                    }}
                  />{' '}
                  {field.name} / {field.type}
                </label>
                {g && (
                  <>
                    {g.type === 'strings' && (
                      <label>
                        候选值（逗号分隔）
                        <input
                          className="text-input"
                          value={g.values?.join(',') ?? ''}
                          onChange={(e) =>
                            setGenerator(field.name, { ...g, values: e.target.value.split(',') })
                          }
                        />
                      </label>
                    )}
                    {g.type === 'uint64s' && (
                      <label>
                        集合与闭区间
                        <input
                          className="text-input"
                          placeholder="1-100,200-400,18446744073709551615"
                          value={g.set ?? ''}
                          onChange={(e) => setGenerator(field.name, { ...g, set: e.target.value })}
                        />
                      </label>
                    )}
                    {g.type === 'int64' && (
                      <>
                        <label>
                          最小值
                          <input
                            className="text-input"
                            value={g.min ?? ''}
                            onChange={(e) =>
                              setGenerator(field.name, { ...g, min: e.target.value })
                            }
                          />
                        </label>
                        <label>
                          最大值
                          <input
                            className="text-input"
                            value={g.max ?? ''}
                            onChange={(e) =>
                              setGenerator(field.name, { ...g, max: e.target.value })
                            }
                          />
                        </label>
                      </>
                    )}
                    <label>
                      分布
                      <select
                        className="text-input"
                        value={g.distribution ?? 'uniform'}
                        onChange={(e) =>
                          setGenerator(field.name, {
                            ...g,
                            distribution: e.target.value as AttributeGenerator['distribution'],
                          })
                        }
                      >
                        <option value="uniform">均匀</option>
                        <option value="low">偏向前端 / 较小值</option>
                        <option value="high">偏向后端 / 较大值</option>
                        <option value="triangular">三角形 / 中间集中</option>
                      </select>
                    </label>
                    {g.type !== 'int64' && (
                      <>
                        <label>
                          抽取数量（0–4096）
                          <input
                            className="text-input"
                            type="number"
                            min="0"
                            max="4096"
                            value={g.count ?? 1}
                            onChange={(e) =>
                              setGenerator(field.name, { ...g, count: Number(e.target.value) })
                            }
                          />
                        </label>
                        <label>
                          <input
                            type="checkbox"
                            checked={g.replacement ?? false}
                            onChange={(e) =>
                              setGenerator(field.name, { ...g, replacement: e.target.checked })
                            }
                          />
                          允许重复抽取
                        </label>
                      </>
                    )}
                  </>
                )}
              </div>
            )
          })}
        </div>
        <button
          className="button button-secondary"
          type="button"
          onClick={() => {
            const spec = buildBatchSpec()
            if (spec)
              createBatch.mutate(spec, {
                onSuccess: () =>
                  setBatch((current) => ({
                    ...current,
                    startTicketId: (spec.startTicketId ?? 1) + spec.count,
                  })),
              })
          }}
          disabled={createBatch.isPending}
        >
          {createBatch.isPending ? '生成中…' : `生成 ${formatNumber(batch.count)} 条 Ticket`}
        </button>
        <TrafficControl buildSpec={buildBatchSpec} />
        {createBatch.isError ? (
          <p className="form-error">
            {createBatch.error instanceof Error ? createBatch.error.message : '批量生成失败'}
          </p>
        ) : null}
        {createBatch.data ? (
          <p className="success-note">
            已接受 {formatNumber(createBatch.data.accepted)} 条 · generator{' '}
            {createBatch.data.generatorId}
          </p>
        ) : null}
      </div>
    </div>
  )
}

export function Tickets() {
  const [search, setSearch] = useState('')
  const [status, setStatus] = useState('all')
  const query = useTickets({ limit: 100, search, status })
  const deleteTicket = useDeleteTicket()
  const [showComposer, setShowComposer] = useState(false)

  return (
    <div className="page-stack">
      <PageHeader
        eyebrow="SIMULATOR / OBSERVATION"
        title="Tickets"
        description="按服务端状态、搜索和分页查看 Ticket 及其 Object Facts。"
        actions={
          <button
            className="button button-primary"
            type="button"
            onClick={() => setShowComposer((value) => !value)}
          >
            {showComposer ? '收起输入面板' : '+ 新建 / 批量生成'}
          </button>
        }
      />

      {showComposer ? (
        <section className="panel composer-panel">
          <TicketComposer />
        </section>
      ) : null}

      <section className="panel ticket-panel">
        <SectionTitle
          title="Ticket Registry"
          detail={
            query.data?.total === undefined
              ? '浏览器只虚拟化当前服务端窗口'
              : `${formatNumber(query.data.total)} 条符合条件`
          }
        />
        <div className="toolbar">
          <label className="search-box">
            <span aria-hidden="true">⌕</span>
            <input
              value={search}
              placeholder="搜索 Ticket ID、Attribute、Fact…"
              onChange={(event) => setSearch(event.target.value)}
            />
          </label>
          <select
            className="filter-select"
            value={status}
            onChange={(event) => setStatus(event.target.value)}
            aria-label="筛选状态"
          >
            <option value="all">全部状态</option>
            <option value="waiting">等待中</option>
            <option value="matched">已匹配</option>
            <option value="expired">已过期</option>
            <option value="rejected">已拒绝</option>
          </select>
          {query.isFetching && !query.isLoading ? (
            <span className="refreshing">更新中…</span>
          ) : null}
        </div>
        {query.isLoading ? <LoadingState label="正在读取 Ticket registry…" /> : null}
        {query.isError ? <ErrorState error={query.error} onRetry={() => query.refetch()} /> : null}
        {query.data?.items.length === 0 ? (
          <EmptyState title="没有匹配的 Ticket" detail="可以调整搜索词或状态筛选。" />
        ) : null}
        {query.data && query.data.items.length > 0 ? (
          <TicketTable
            tickets={query.data.items}
            onDelete={(ticket) => {
              if (window.confirm(`删除 ${ticket.ticketId}？`)) deleteTicket.mutate(ticket.ticketId)
            }}
          />
        ) : null}
      </section>
    </div>
  )
}
