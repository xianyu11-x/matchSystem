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

import { parseInputValue, parseSafeInteger, validateBatch } from '../lib/composerValidation'

const selectionKey = (ruleKey: string, placementId: string) => `${ruleKey}@@${placementId}`

function TicketComposer() {
  const scenarioQuery = useScenario()
  const createTicket = useCreateTicket()
  const createBatch = useCreateBatch()
  const [ruleSelection, setRuleSelection] = useState('')
  const [mode, setMode] = useState<'single' | 'batch'>('batch')
  const [inputError, setInputError] = useState('')
  const [trafficRunning, setTrafficRunning] = useState(false)
  const [ticketId, setTicketId] = useState('')
  const [createdAt, setCreatedAt] = useState('')
  const [affinityKey, setAffinityKey] = useState('')
  const [batchFacts, setBatchFacts] = useState<Record<string, string>>({})
  const [batchOptions, setBatchOptions] = useState({
    createdAtStart: '',
    createdAtStep: '1',
    affinityPrefix: '',
    requestIdPrefix: '',
    atomic: true,
  })
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

  const contractKey = JSON.stringify([activeRule?.ruleKey, activeRule?.placementId, contract])
  useEffect(() => {
    if (!activeRule) return
    const key = selectionKey(activeRule.ruleKey, activeRule.placementId)
    setRuleSelection((current) => (current === key ? current : key))
    setAttributeDraft(() =>
      Object.fromEntries(activeRule.contract.attributes.map((field) => [field.name, ''])),
    )
    setFactDraft(() =>
      Object.fromEntries(
        activeRule.contract.facts
          .filter((field) => field.scope === 'object')
          .map((field) => [field.name, '']),
      ),
    )
    setGenerators({})
    setBatchFacts({})
    setInputError('')
  }, [contractKey])

  const buildAttributes = (): TypedAttributes => {
    const result: TypedAttributes = { strings: {}, uint64s: {}, int64: {} }
    for (const field of contract?.attributes ?? []) {
      const raw = (attributeDraft[field.name] ?? '').trim()
      if (!raw) continue
      const value = parseInputValue(raw, field.type, field.name)
      if (field.type === 'strings') result.strings[field.name] = value as string[]
      else if (field.type === 'uint64s') result.uint64s[field.name] = value as number[]
      else result.int64[field.name] = value as number
    }
    return result
  }

  const buildFacts = (): FactSnapshot => {
    const result: FactSnapshot = {}
    for (const field of objectFacts) {
      const raw = (factDraft[field.name] ?? '').trim()
      if (!raw) continue
      result[field.name] = parseInputValue(raw, field.type, field.name)
    }
    return result
  }

  const buildBatchSpec = (continuous = false): BatchGeneratorSpec | undefined => {
    if (!activeRule) return undefined
    const spec: BatchGeneratorSpec = {
      count: batch.count,
      seed: batch.seed,
      startTicketId: batch.startTicketId,
      ruleKey: activeRule.ruleKey,
      rule: activeRule.apiRule,
      placementId: activeRule.placementId,
      attributeGenerators: generators,
      ...(!continuous
        ? {
            createdAtStart: batchOptions.createdAtStart.trim()
              ? parseSafeInteger(batchOptions.createdAtStart, '批量创建时间', 0)
              : undefined,
            createdAtStep: parseSafeInteger(batchOptions.createdAtStep, '创建时间步长'),
            atomic: batchOptions.atomic,
          }
        : {}),
      affinityPrefix: batchOptions.affinityPrefix || undefined,
      requestIdPrefix: batchOptions.requestIdPrefix || undefined,
      objectFacts: { stringLists: {}, uint64Lists: {}, int64Values: {} },
    }
    for (const fact of objectFacts) {
      const raw = (batchFacts[fact.name] ?? '').trim()
      if (!raw) continue
      const value = parseInputValue(raw, fact.type, fact.name)
      if (fact.type === 'strings') spec.objectFacts!.stringLists[fact.name] = value as string[]
      else if (fact.type === 'uint64s') spec.objectFacts!.uint64Lists[fact.name] = value as number[]
      else spec.objectFacts!.int64Values[fact.name] = value as number
    }
    validateBatch(spec, continuous)
    return spec
  }

  const submitTicket = () => {
    if (!activeRule) return
    setInputError('')
    try {
      if (ticketId.trim()) parseSafeInteger(ticketId, 'Ticket ID', 1)
      const input: TicketInput = {
        ticketId: ticketId.trim() || undefined,
        createdAt: createdAt.trim() ? parseSafeInteger(createdAt, '创建时间', 0) : undefined,
        affinityKey: affinityKey || undefined,
        rule: activeRule.apiRule,
        placementId: activeRule.placementId,
        attributes: buildAttributes(),
        facts: buildFacts(),
      }
      createTicket.mutate(input)
    } catch (error) {
      setInputError(error instanceof Error ? error.message : '请检查输入')
    }
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
      <div className="composer-heading">
        <div className="mode-switch" role="group" aria-label="输入方式">
          <button
            type="button"
            className={mode === 'single' ? 'active' : ''}
            aria-pressed={mode === 'single'}
            onClick={() => {
              setMode('single')
              setInputError('')
            }}
          >
            单条输入
          </button>
          <button
            type="button"
            className={mode === 'batch' ? 'active' : ''}
            aria-pressed={mode === 'batch'}
            onClick={() => {
              setMode('batch')
              setInputError('')
            }}
          >
            批量与持续流量{trafficRunning ? ' · 运行中' : ''}
          </button>
        </div>
        <label className="field-label">
          目标规则与节点
          <select
            className="text-input"
            value={ruleSelection}
            disabled={trafficRunning}
            onChange={(e) => setRuleSelection(e.target.value)}
          >
            {rules.map((rule) => (
              <option
                key={selectionKey(rule.ruleKey, rule.placementId)}
                value={selectionKey(rule.ruleKey, rule.placementId)}
              >
                {rule.displayName}
              </option>
            ))}
          </select>
        </label>
      </div>
      {inputError && (
        <p className="form-error" role="alert">
          {inputError}
        </p>
      )}
      <div className="subsection" hidden={mode !== 'single'}>
        <SectionTitle
          title="填写单条 Ticket（匹配对象）"
          detail="填写需要提交的属性；空白字段不提交。整数支持浏览器安全范围，超大值请使用批量生成。"
        />
        <div className="form-grid">
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
        <details className="advanced-options">
          <summary>更多选项 · 创建时间与路由</summary>
          <div className="form-grid">
            <label className="field-label">
              创建时间（Unix 毫秒）
              <input
                className="text-input"
                inputMode="numeric"
                value={createdAt}
                onChange={(e) => setCreatedAt(e.target.value)}
                placeholder="留空使用提交时的当前时间"
              />
            </label>
            <label className="field-label">
              路由亲和键（Affinity Key）
              <input
                className="text-input"
                value={affinityKey}
                onChange={(e) => setAffinityKey(e.target.value)}
                placeholder="可选，用于路由选择"
              />
            </label>
          </div>
        </details>
        <div className="form-divider">
          <span>对象属性</span>
          <small>严格按 Contract 的名称和类型提交</small>
        </div>
        <div className="form-grid">
          {contract?.attributes.map((field) => (
            <label className="field-label" key={field.name}>
              {field.name} / {field.type}
              <input
                className="text-input"
                type="text"
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
          <span>对象事实（Object Facts）</span>
          <small>随对象提交的事实值，按规则声明填写</small>
        </div>
        <div className="form-grid">
          {objectFacts.map((fact) => (
            <label className="field-label" key={fact.name}>
              {fact.name} / {fact.type}
              <input
                className="text-input"
                type="text"
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

      <div className="subsection batch-panel" hidden={mode !== 'batch'}>
        <fieldset className="composer-fields" disabled={trafficRunning || createBatch.isPending}>
          <SectionTitle
            title="批量生成"
            detail="先设置数量和起始编号，再启用所需属性。持续流量也使用这组属性配置。"
          />
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
          </div>
          <details className="advanced-options">
            <summary>更多选项 · 批量时间、路由与失败处理</summary>
            <div className="form-grid">
              <label className="field-label">
                批量创建时间（Unix 毫秒）
                <input
                  className="text-input"
                  inputMode="numeric"
                  value={batchOptions.createdAtStart}
                  onChange={(e) =>
                    setBatchOptions({ ...batchOptions, createdAtStart: e.target.value })
                  }
                  placeholder="留空使用当前时间"
                />
              </label>
              <label className="field-label">
                每条时间递增（毫秒）
                <input
                  className="text-input"
                  inputMode="numeric"
                  value={batchOptions.createdAtStep}
                  onChange={(e) =>
                    setBatchOptions({ ...batchOptions, createdAtStep: e.target.value })
                  }
                />
              </label>
              <label className="field-label">
                路由亲和键前缀
                <input
                  className="text-input"
                  value={batchOptions.affinityPrefix}
                  onChange={(e) =>
                    setBatchOptions({ ...batchOptions, affinityPrefix: e.target.value })
                  }
                  placeholder="可选，自动附加 Ticket ID"
                />
              </label>
              <label className="field-label">
                请求标识前缀
                <input
                  className="text-input"
                  value={batchOptions.requestIdPrefix}
                  onChange={(e) =>
                    setBatchOptions({ ...batchOptions, requestIdPrefix: e.target.value })
                  }
                  placeholder="可选，自动附加 Ticket ID"
                />
              </label>
              <label>
                <input
                  type="checkbox"
                  checked={batchOptions.atomic}
                  onChange={(e) => setBatchOptions({ ...batchOptions, atomic: e.target.checked })}
                />{' '}
                批量任一条失败时撤回本批已加入的对象
              </label>
            </div>
            <p className="muted">
              时间步长为 0 时按 1
              毫秒处理。持续流量使用实际到达时间，并逐次注入，不使用批量时间和整批撤回选项。
            </p>
          </details>
          <p className="muted">
            启用属性后配置生成规则。整数按十进制文本传输；未启用的属性不生成。
          </p>
          <div className="form-grid">
            {contract?.attributes.map((field) => {
              const g = generators[field.name]
              return (
                <fieldset className="generator-card" key={field.name}>
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
                      <label>
                        值来源
                        <select
                          className="text-input"
                          value={g.source ?? 'sample'}
                          onChange={(e) =>
                            setGenerator(field.name, {
                              type: g.type,
                              source: e.target.value as AttributeGenerator['source'],
                              ...(e.target.value === 'sample'
                                ? g.type === 'int64'
                                  ? { min: '0', max: '100' }
                                  : g.type === 'strings'
                                    ? { values: ['a', 'b'] }
                                    : { set: '1-100' }
                                : {}),
                            })
                          }
                        >
                          <option value="sample">按分布抽样</option>
                          <option value="ticketId">使用当前 Ticket ID</option>
                          <option value="shared">共享另一属性的值</option>
                        </select>
                      </label>
                      {g.source === 'shared' && (
                        <label>
                          共享来源属性
                          <select
                            className="text-input"
                            value={g.ref ?? ''}
                            onChange={(e) =>
                              setGenerator(field.name, { ...g, ref: e.target.value })
                            }
                          >
                            <option value="">请选择已启用的同类型属性</option>
                            {Object.entries(generators)
                              .filter(
                                ([name, other]) => name !== field.name && other.type === g.type,
                              )
                              .map(([name]) => (
                                <option key={name} value={name}>
                                  {name}
                                </option>
                              ))}
                          </select>
                        </label>
                      )}
                      {g.source === 'ticketId' && (
                        <small>使用本条 Ticket ID；多值属性生成单元素列表。</small>
                      )}
                      {(g.source === undefined || g.source === 'sample') && (
                        <>
                          {g.type === 'strings' && (
                            <label>
                              候选值（逗号分隔）
                              <input
                                className="text-input"
                                value={g.values?.join(',') ?? ''}
                                onChange={(e) =>
                                  setGenerator(field.name, {
                                    ...g,
                                    values: e.target.value.split(','),
                                  })
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
                                onChange={(e) =>
                                  setGenerator(field.name, { ...g, set: e.target.value })
                                }
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
                                  distribution: e.target
                                    .value as AttributeGenerator['distribution'],
                                })
                              }
                            >
                              <option value="uniform">均匀</option>
                              <option value="low">偏向较小值 / 列表前部</option>
                              <option value="high">偏向较大值 / 列表后部</option>
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
                                    setGenerator(field.name, {
                                      ...g,
                                      count: Number(e.target.value),
                                    })
                                  }
                                />
                              </label>
                              <label>
                                <input
                                  type="checkbox"
                                  checked={g.replacement ?? false}
                                  onChange={(e) =>
                                    setGenerator(field.name, {
                                      ...g,
                                      replacement: e.target.checked,
                                    })
                                  }
                                />
                                允许重复抽取
                              </label>
                            </>
                          )}
                        </>
                      )}
                    </>
                  )}
                </fieldset>
              )
            })}
          </div>
          <details className="advanced-options">
            <summary>共同对象事实 · {objectFacts.length} 个可配置字段</summary>
            <p className="muted">为每条生成对象附加相同事实，也应用于持续流量。空白字段不提交。</p>
            <div className="form-grid">
              {objectFacts.map((fact) => (
                <label className="field-label" key={fact.name}>
                  {fact.name} / {fact.type}
                  <input
                    className="text-input"
                    value={batchFacts[fact.name] ?? ''}
                    onChange={(e) =>
                      setBatchFacts((values) => ({ ...values, [fact.name]: e.target.value }))
                    }
                    placeholder={fact.type === 'int64' ? '整数' : '逗号分隔'}
                  />
                </label>
              ))}
              {!objectFacts.length && (
                <p className="muted">当前规则未声明对象事实，可在规则配置中添加。</p>
              )}
            </div>
          </details>
          <button
            className="button button-secondary"
            type="button"
            onClick={() => {
              setInputError('')
              try {
                const spec = buildBatchSpec()
                if (spec)
                  createBatch.mutate(spec, {
                    onSuccess: () =>
                      setBatch((current) => ({
                        ...current,
                        startTicketId: (spec.startTicketId ?? 1) + spec.count,
                      })),
                  })
              } catch (error) {
                setInputError(error instanceof Error ? error.message : '请检查生成配置')
              }
            }}
            disabled={createBatch.isPending}
          >
            {createBatch.isPending ? '生成中…' : `生成 ${formatNumber(batch.count)} 条 Ticket`}
          </button>
        </fieldset>
        <TrafficControl
          buildSpec={() => buildBatchSpec(true)}
          onRunningChange={setTrafficRunning}
          onNextId={(id) => setBatch((current) => ({ ...current, startTicketId: id }))}
        />
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
  const [pageCursors, setPageCursors] = useState<string[]>([''])
  const [pageSize, setPageSize] = useState(100)
  const query = useTickets({
    limit: pageSize,
    cursor: pageCursors.at(-1) || undefined,
    search,
    status,
  })
  const deleteTicket = useDeleteTicket()
  const [showComposer, setShowComposer] = useState(false)

  return (
    <div className="page-stack">
      <PageHeader
        eyebrow="SIMULATOR / OBSERVATION"
        title="匹配对象"
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

      {
        <section className="panel composer-panel" hidden={!showComposer}>
          <TicketComposer />
        </section>
      }

      <section className="panel ticket-panel">
        <SectionTitle
          title="对象列表"
          detail={
            query.data?.total === undefined
              ? '按页浏览对象，可通过搜索和状态缩小范围'
              : `${formatNumber(query.data.total)} 条符合条件`
          }
        />
        <div className="toolbar">
          <label className="search-box">
            <span aria-hidden="true">⌕</span>
            <input
              value={search}
              placeholder="搜索 Ticket ID、Attribute、Fact…"
              onChange={(event) => {
                setSearch(event.target.value)
                setPageCursors([''])
              }}
            />
          </label>
          <select
            className="filter-select"
            value={status}
            onChange={(event) => {
              setStatus(event.target.value)
              setPageCursors([''])
            }}
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
        <div className="ticket-pagination" aria-label="对象分页">
          <label>
            每页{' '}
            <select
              className="filter-select"
              aria-label="每页对象数量"
              value={pageSize}
              onChange={(e) => {
                setPageSize(Number(e.target.value))
                setPageCursors([''])
              }}
            >
              <option value={25}>25 条</option>
              <option value={100}>100 条</option>
              <option value={250}>250 条</option>
            </select>
          </label>
          <span role="status">
            第 {pageCursors.length} 页 · {query.data?.items.length ?? 0} 条
          </span>
          <button
            className="button button-ghost"
            disabled={pageCursors.length < 2 || query.isFetching}
            onClick={() => setPageCursors([''])}
          >
            首页
          </button>
          <button
            className="button button-ghost"
            disabled={pageCursors.length < 2 || query.isFetching}
            onClick={() => setPageCursors((pages) => pages.slice(0, -1))}
          >
            上一页
          </button>
          <button
            className="button button-ghost"
            disabled={!query.data?.nextCursor || query.isFetching || query.isError}
            onClick={() => {
              const next = query.data?.nextCursor
              if (next) setPageCursors((pages) => [...pages, next])
            }}
          >
            下一页
          </button>
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
