import { useEffect, useMemo, useRef, useState } from 'react'
import { EmptyState, ErrorState, LoadingState, PageHeader, StatusPill } from '../components/States'
import { NodeInspector } from '../components/NodeInspector'
import { RuleCanvas } from '../components/RuleCanvas'
import { ContractEditor } from '../components/ContractEditor'
import { ExpressionEditor } from '../components/ExpressionEditor'
import { NumberField } from '../components/RuleFormControls'
import { ScenarioSettingsEditor } from '../components/ScenarioSettingsEditor'
import { RuleSettingsEditor } from '../components/RuleSettingsEditor'
import { TickFactsEditor } from '../components/RuleFactsEditor'
import '../components/rule-forms.css'
import {
  useCapabilities,
  useImportScenario,
  useLogicalNodeFacts,
  useReplaceScenario,
  useRule,
  useScenario,
  useValidateRule,
} from '../lib/queries'
import { useRuleStore, type RulesTab } from '../lib/ruleStore'
import {
  importRuleDocument,
  portableRuleDocument,
  portableRuleFileName,
} from '../lib/ruleDocumentIO'
import {
  appendRuleDraft,
  documentFromDraft,
  draftRules,
  mergeRuleDraft,
  removeRuleDraft,
} from '../lib/scenarioDraft'
import { validateRuleDocument } from '../lib/validation'
import { resolveRuleFactSources, type RuleFactSources } from '../lib/factSources'
import { formatApiError } from '../lib/api'
import type {
  ApiRuleKey,
  Capabilities,
  FactSpec,
  FactScope,
  JsonObject,
  LogicalNodeFactsResponse,
  RuleDocument,
} from '../types'

const tabs: Array<{ id: RulesTab; label: string }> = [
  { id: 'settings', label: '种子、评分与预算' },
  { id: 'contract', label: '字段契约' },
  { id: 'prefilter', label: '预筛选' },
  { id: 'evaluation', label: '加入与成局' },
  { id: 'scenario', label: '场景与部署' },
  { id: 'facts', label: '全部 Facts' },
  { id: 'graph', label: '高级：规则图' },
]

function sameJson(left: unknown, right: unknown): boolean {
  if (Object.is(left, right)) return true
  if (Array.isArray(left) || Array.isArray(right)) {
    return (
      Array.isArray(left) &&
      Array.isArray(right) &&
      left.length === right.length &&
      left.every((value, index) => sameJson(value, right[index]))
    )
  }
  if (!left || !right || typeof left !== 'object' || typeof right !== 'object') return false
  const leftObject = left as Record<string, unknown>
  const rightObject = right as Record<string, unknown>
  const keys = Object.keys(leftObject)
  return (
    keys.length === Object.keys(rightObject).length &&
    keys.every((key) => key in rightObject && sameJson(leftObject[key], rightObject[key]))
  )
}

function FactsPanel({
  metadata,
  localFactSources,
  isLoading,
  isError,
  error,
  onRetry,
  hasIdentity,
  runtimeMetadataCurrent,
  rule,
  ruleKey,
  placementId,
  onRuntimeFactsChange,
}: {
  metadata?: LogicalNodeFactsResponse
  localFactSources?: RuleFactSources
  isLoading: boolean
  isError: boolean
  error: unknown
  onRetry: () => void
  hasIdentity: boolean
  runtimeMetadataCurrent: boolean
  rule?: ApiRuleKey
  ruleKey: string
  placementId: string
  onRuntimeFactsChange: (value: unknown) => void
}) {
  const identity = rule ? `${rule.namespace ? `${rule.namespace}/` : ''}${rule.ruleId}` : ruleKey
  const metadataObject =
    metadata?.logicalNode && typeof metadata.logicalNode === 'object'
      ? (metadata.logicalNode as Record<string, unknown>)
      : undefined
  const metadataRule =
    metadataObject?.rule && typeof metadataObject.rule === 'object'
      ? (metadataObject.rule as Record<string, unknown>)
      : undefined
  const metadataIdentityMatches =
    !metadataObject ||
    Boolean(
      metadataRule &&
      Number(metadataRule.ruleId) === rule?.ruleId &&
      String(metadataRule.namespace ?? '') === String(rule?.namespace ?? '') &&
      String(metadataObject.placementId ?? 'default') === placementId,
    )
  const displayMetadata = metadataIdentityMatches && runtimeMetadataCurrent ? metadata : undefined
  const resolvedFactSources = resolveRuleFactSources(localFactSources, displayMetadata)
  const { contractFacts, runtimeFacts } = resolvedFactSources
  const runtimeDescriptors = displayMetadata?.providerDescriptors ?? {}
  const runtimeHasDescriptors = Object.values(runtimeDescriptors).some(Boolean)
  const runtimeDescriptorSources = [
    ...new Set(
      Object.values(runtimeDescriptors)
        .map((descriptor) => descriptor?.source)
        .filter((source): source is NonNullable<typeof source> => Boolean(source)),
    ),
  ]
  const descriptors = runtimeDescriptors
  const runtimeTickFacts = runtimeFacts.tick
  const grouped = (['tick', 'object', 'match'] as FactScope[]).map((scope) => ({
    scope,
    facts: contractFacts.filter((fact) => fact.scope === scope),
  }))
  return (
    <div className="facts-panel">
      <div className="schema-callout">
        <span className="schema-badge">FACT</span>
        <div>
          <strong>LogicalNode Fact 分层</strong>
          <p>Contract、Provider 启动握手声明和模拟器运行时值是三个独立的数据来源。</p>
          <div className="facts-node-identity">
            <code>{identity}</code>
            <span>placement: {placementId}</span>
          </div>
        </div>
      </div>
      {!hasIdentity && !localFactSources ? (
        <EmptyState
          title="无法确定 LogicalNode"
          detail="当前规则缺少 API Rule ID，暂时无法查询 Fact 元数据。"
        />
      ) : isLoading && !localFactSources ? (
        <LoadingState label="正在读取 LogicalNode Fact 元数据…" />
      ) : isError && !localFactSources ? (
        <ErrorState error={error} onRetry={onRetry} />
      ) : (
        <>
          {isError ? (
            <div className="state-panel state-error" role="alert">
              <span className="state-icon">!</span>
              <div>
                <strong>运行时 Fact 元数据加载失败</strong>
                <p>{formatApiError(error, '无法读取 LogicalNode Fact 元数据')}</p>
                <button className="button button-ghost" onClick={onRetry} type="button">
                  重试
                </button>
              </div>
            </div>
          ) : null}
          <section className="facts-source-section">
            <div className="schema-callout">
              <span className="schema-badge">CONTRACT</span>
              <div>
                <strong>规则 Contract Facts（规则定义）</strong>
                <p>Description 只属于 Contract 文档，不参与 Provider 握手比较。</p>
              </div>
            </div>
            <div className="fact-scope-grid">
              {grouped.map((group) => (
                <div className="fact-scope-card" key={`contract-${group.scope}`}>
                  <div className="fact-scope-heading">
                    <span className={`scope-chip scope-${group.scope}`}>{group.scope}</span>
                    <span>{group.facts.length} fields</span>
                  </div>
                  {group.facts.length === 0 ? (
                    <span className="muted">未声明</span>
                  ) : (
                    group.facts.map((fact) => (
                      <div className="fact-row" key={fact.name}>
                        <div className="fact-row-copy">
                          <strong>{fact.name}</strong>
                          <span className="fact-meta">
                            {fact.type}
                            {fact.maxValues !== undefined ? ` · max ${fact.maxValues}` : ''}
                          </span>
                          {fact.description ? (
                            <p className="fact-description">{fact.description}</p>
                          ) : null}
                        </div>
                      </div>
                    ))
                  )}
                </div>
              ))}
            </div>
          </section>

          <section className="facts-source-section">
            <div className="schema-callout">
              <span className="schema-badge">HANDSHAKE</span>
              <div>
                <strong>Provider 握手声明（Provider Descriptor）</strong>
                <p>
                  启动时由 Provider 显式提供并与 Contract 校验；不会从 Contract 或运行时值自动生成。
                </p>
                <p className="field-hint">
                  {runtimeHasDescriptors
                    ? `当前显示 LogicalNode 的真实运行态握手结果（source: ${runtimeDescriptorSources.join(
                        ' / ',
                      ) || 'unknown'}）。脚本或 Go Provider 可声明比 Contract 更多的 Fact；这里只读展示能力。`
                    : !runtimeMetadataCurrent && localFactSources
                      ? '当前 Provider 配置尚未保存，运行态 Descriptor 暂不可用；保存场景后刷新 Facts。'
                      : '尚未加载运行态 Descriptor；能力由 Provider 启动握手提供，不会从 Contract 自动生成。'}
                </p>
              </div>
            </div>
            <div className="fact-scope-grid">
              {(['tick', 'object', 'match'] as FactScope[]).map((scope) => {
                const descriptor = descriptors[scope]
                return (
                  <div className="fact-scope-card" key={`descriptor-${scope}`}>
                    <div className="fact-scope-heading">
                      <span className={`scope-chip scope-${scope}`}>{scope}</span>
                      <span>
                        {descriptor
                          ? `${descriptor.id} · ${descriptor.version}${
                              descriptor.source ? ` · source: ${descriptor.source}` : ''
                            }`
                          : '未配置'}
                      </span>
                    </div>
                    {descriptor ? (
                      (descriptor.facts ?? []).length > 0 ? (
                        (descriptor.facts ?? []).map((fact) => (
                          <div className="fact-row" key={fact.name}>
                            <div className="fact-row-copy">
                              <strong>{fact.name}</strong>
                              <span className="fact-meta">
                                {fact.type}
                                {fact.maxValues !== undefined ? ` · max ${fact.maxValues}` : ''}
                              </span>
                            </div>
                          </div>
                        ))
                      ) : (
                        <span className="muted">声明为空</span>
                      )
                    ) : (
                      <span className="muted">
                        Contract 声明该 scope 时，启动会拒绝缺少 Descriptor 的场景。
                      </span>
                    )}
                  </div>
                )
              })}
            </div>
            <p className="field-hint">
              Provider Descriptor 是只读运行态元数据；场景不保存手工能力声明。
            </p>
          </section>

          <section className="facts-source-section">
            <div className="schema-callout">
              <span className="schema-badge">RUNTIME</span>
              <div>
                <strong>Simulator Runtime Fact Values（模拟器运行时值）</strong>
                <p>
                  这些值用于本地模拟，不是 Provider 握手声明。Tick 值可在这里编辑；Object 值随
                  Ticket，Match 值随成局记录。
                </p>
              </div>
            </div>
            <TickFactsEditor
              value={runtimeTickFacts}
              fields={contractFacts}
              onChange={onRuntimeFactsChange}
            />
          </section>
        </>
      )}
    </div>
  )
}

function RuleJsonActions({
  document,
  capabilities,
  scenario,
  onImported,
}: {
  document: RuleDocument
  capabilities: Capabilities
  scenario?: JsonObject
  onImported?: () => void
}) {
  const inputRef = useRef<HTMLInputElement>(null)
  const importDocument = useRuleStore((state) => state.importDocument)
  const clearGraphSession = useRuleStore((state) => state.clearGraphSession)
  const importScenario = useImportScenario()
  const [message, setMessage] = useState<{ kind: 'success' | 'error'; text: string }>()

  const exportJson = () => {
    try {
      const payload = scenario ?? portableRuleDocument(document)
      const blob = new Blob([`${JSON.stringify(payload, null, 2)}\n`], {
        type: 'application/json',
      })
      const url = URL.createObjectURL(blob)
      const anchor = window.document.createElement('a')
      anchor.href = url
      anchor.download = scenario
        ? `${String(scenario.scenarioId ?? scenario.id ?? 'simulator-scenario')}.json`
        : portableRuleFileName(document)
      anchor.click()
      window.setTimeout(() => URL.revokeObjectURL(url), 0)
      setMessage({
        kind: 'success',
        text: scenario
          ? 'simulator-scenario/v1 场景已导出（包含当前未保存草稿）。'
          : 'match-rule/v1 规则配置已导出（含当前未保存编辑）。',
      })
    } catch (error) {
      setMessage({
        kind: 'error',
        text: error instanceof Error ? error.message : 'JSON 导出失败',
      })
    }
  }

  const importJson = async (file: File | undefined) => {
    if (!file) return
    try {
      const parsed = JSON.parse(await file.text()) as unknown
      const parsedObject =
        parsed && typeof parsed === 'object' && !Array.isArray(parsed)
          ? (parsed as Record<string, unknown>)
          : undefined
      const rawScenario =
        parsedObject?.scenario &&
        typeof parsedObject.scenario === 'object' &&
        !Array.isArray(parsedObject.scenario)
          ? (parsedObject.scenario as JsonObject)
          : (parsedObject as JsonObject | undefined)
      if (rawScenario?.schemaVersion === 'simulator-scenario/v1') {
        if (!window.confirm('导入完整场景会替换当前运行场景并清空其运行态，是否继续？')) return
        await importScenario.mutateAsync(rawScenario)
        // A full scenario import can reuse the same rule/placement identity.
        // Drop the document and every in-memory graph snapshot before the
        // invalidated queries hydrate the imported scenario.
        clearGraphSession()
        onImported?.()
        setMessage({ kind: 'success', text: `完整场景 ${file.name} 已导入并启用。` })
        return
      }
      const next = importRuleDocument(parsed, document)
      const validation = validateRuleDocument(next, capabilities)
      if (!validation.valid) {
        const first = validation.errors[0]
        throw new Error(`${first.path}：${first.message}`)
      }
      importDocument(next)
      onImported?.()
      setMessage({ kind: 'success', text: `已导入 ${file.name}，等待保存。` })
    } catch (error) {
      setMessage({
        kind: 'error',
        text: error instanceof Error ? error.message : 'JSON 导入失败',
      })
    } finally {
      if (inputRef.current) inputRef.current.value = ''
    }
  }

  return (
    <div className="rule-json-actions">
      <input
        ref={inputRef}
        className="visually-hidden"
        type="file"
        accept="application/json,.json"
        aria-label="选择场景或规则 JSON 文件"
        onChange={(event) => void importJson(event.target.files?.[0])}
      />
      <button
        className="button button-ghost"
        type="button"
        onClick={() => inputRef.current?.click()}
      >
        导入 JSON
      </button>
      <button className="button button-ghost" type="button" onClick={exportJson}>
        {scenario ? '导出场景 JSON' : '导出规则 JSON'}
      </button>
      {message ? (
        <span className={`rule-json-message ${message.kind}`} role="status">
          {message.text}
        </span>
      ) : null}
    </div>
  )
}

function ValidationPanel({
  document,
  backend,
  capabilities,
}: {
  document?: RuleDocument
  backend: ReturnType<typeof useValidateRule>
  capabilities: Capabilities
}) {
  const local = useMemo(
    () => (document ? validateRuleDocument(document, capabilities) : { valid: false, errors: [] }),
    [capabilities, document],
  )
  const issues = [...local.errors, ...(backend.data?.errors ?? [])]
  return (
    <div className="validation-panel">
      <div className="validation-heading">
        <div>
          <span className="eyebrow">VALIDATION</span>
          <strong>
            {local.valid && !backend.data?.errors.length
              ? '当前编辑合法'
              : `${issues.length} 个问题待处理`}
          </strong>
        </div>
        <span
          className={`validation-dot ${local.valid && !backend.data?.errors.length ? 'valid' : 'invalid'}`}
        />
      </div>
      {issues.length === 0 ? (
        <p className="validation-ok">Schema、节点端口和图结构均通过本地快速校验。</p>
      ) : (
        <div className="issue-list">
          {issues.slice(0, 8).map((issue, index) => (
            <div className="issue-item" key={`${issue.path}-${index}`}>
              <span>!</span>
              <div>
                <strong>{issue.path}</strong>
                <p>{issue.message}</p>
              </div>
            </div>
          ))}
        </div>
      )}
      {backend.isSuccess && backend.data?.valid ? (
        <p className="success-note">
          Go backend validation 已通过{backend.data ? ' · fingerprint 由服务端管理' : ''}。
        </p>
      ) : null}
    </div>
  )
}

export function Rules() {
  const scenarioQuery = useScenario()
  const capabilitiesQuery = useCapabilities()
  const [ruleIndex, setRuleIndex] = useState(0)
  const [scenarioDraft, setScenarioDraft] = useState<JsonObject>()
  const [scenarioDirty, setScenarioDirty] = useState(false)
  const [saveError, setSaveError] = useState('')
  const document = useRuleStore((state) => state.document)
  const draftRows = scenarioDraft ? draftRules(scenarioDraft) : []
  const draftSelection = draftRows[ruleIndex] ? documentFromDraft(draftRows[ruleIndex]) : undefined
  const summary = scenarioQuery.data?.rules[ruleIndex] ?? scenarioQuery.data?.rules[0]
  const selectedRule = document ?? summary
  const currentSelection = draftSelection ?? summary
  const ruleQuery = useRule(
    scenarioQuery.data?.rawScenario ? undefined : summary?.ruleKey,
    summary?.placementId,
  )
  const documentMatchesSelectedRule = Boolean(
    document &&
    currentSelection &&
    document.ruleKey === currentSelection.ruleKey &&
    document.placementId === currentSelection.placementId,
  )
  const activeTab = useRuleStore((state) => state.activeTab)
  const factsQuery = useLogicalNodeFacts(
    currentSelection?.apiRule,
    currentSelection?.placementId,
    activeTab === 'facts',
  )
  const dirty = useRuleStore((state) => state.dirty)
  const setDocument = useRuleStore((state) => state.setDocument)
  const setActiveTab = useRuleStore((state) => state.setActiveTab)
  const setEnvelope = useRuleStore((state) => state.setEnvelope)
  const resetDirty = useRuleStore((state) => state.resetDirty)
  const validate = useValidateRule()
  const replaceScenario = useImportScenario()
  const replaceRuleOnly = useReplaceScenario()
  const savedRawRule =
    scenarioQuery.data?.rawScenario && Array.isArray(scenarioQuery.data.rawScenario.rules)
      ? (scenarioQuery.data.rawScenario.rules[ruleIndex] as JsonObject | undefined)
      : undefined
  const savedProvider = savedRawRule?.pythonProvider ?? selectedRule?.pythonProvider
  const runtimeMetadataCurrent = Boolean(
    documentMatchesSelectedRule &&
    // Changing the draft authority (script/scenario/auto) does not change
    // which script is currently running. Keep the loaded metadata visible
    // while the user edits the other source; changing the executable binding
    // still hides it until the save/reload has installed a new worker.
    sameJson(
      document?.pythonProvider,
      savedProvider,
    ),
  )
  const syncScenarioProviderChanges = (nextDraft: JsonObject) => {
    if (!scenarioDraft || !document) return
    const previousRow = draftRules(scenarioDraft)[ruleIndex]
    const nextRow = draftRules(nextDraft)[ruleIndex]
    if (!previousRow || !nextRow) return
    const nextDocument = documentFromDraft(nextRow)
    if (!sameJson(previousRow.pythonProvider, nextRow.pythonProvider))
      setEnvelope('pythonProvider', nextDocument.pythonProvider)

  }
  useEffect(() => {
    validate.reset()
  }, [document])

  // Only pass a local source bundle when the document belongs to the selected
  // rule. During a rule switch the old document must not leak into Contract,
  // Provider Descriptor, or Runtime sections of the Facts tab.
  const localFactSources: RuleFactSources | undefined = documentMatchesSelectedRule
      ? {
        contractFacts: document!.contract.facts,
        runtimeFacts: {
          tick:
            document!.tickFacts ??
            currentSelection?.tickFacts ??
            scenarioQuery.data?.tickFacts ??
            {},
        },
      }
    : undefined

  useEffect(() => {
    const raw = scenarioQuery.data?.rawScenario
    if (!raw) {
      if (ruleQuery.data) setDocument(ruleQuery.data)
      return
    }
    const next = structuredClone(raw)
    setScenarioDraft(next)
    setScenarioDirty(false)
    const rows = draftRules(next)
    const index = rows.length ? Math.min(Math.max(ruleIndex, 0), rows.length - 1) : -1
    setRuleIndex(index)
    if (rows[index]) setDocument(documentFromDraft(rows[index]))
    else {
      useRuleStore.getState().clearGraphSession()
      setActiveTab('scenario')
    }
  }, [scenarioQuery.data?.revision, ruleQuery.data, setDocument, setActiveTab])

  const flushDraft = () =>
    mergeRuleDraft(scenarioDraft ?? scenarioQuery.data?.rawScenario ?? {}, ruleIndex, document)
  const loadDraft = (next: JsonObject, index: number, changed = true) => {
    setScenarioDraft(next)
    setRuleIndex(index)
    setScenarioDirty(scenarioDirty || dirty || changed)
    const row = draftRules(next)[index]
    if (row) setDocument(documentFromDraft(row))
    else {
      useRuleStore.getState().clearGraphSession()
      setActiveTab('scenario')
    }
  }
  const addRule = (copyIndex?: number) => {
    const next = appendRuleDraft(flushDraft(), copyIndex)
    loadDraft(next, draftRules(next).length - 1)
    setActiveTab('settings')
  }
  const deleteRule = (index: number) => {
    const next = removeRuleDraft(flushDraft(), index, ruleIndex)
    loadDraft(next.draft, next.selected)
  }
  const selectRule = (index: number) => {
    if (scenarioDraft) loadDraft(flushDraft(), index, false)
    else setRuleIndex(index)
  }
  const ruleOptions = scenarioQuery.data?.rawScenario
    ? draftRows.map((row, index) => {
        const logical = row.logicalNode as JsonObject
        const key = logical.rule as JsonObject
        return {
          value: index,
          label: `${key.namespace ? `${key.namespace}/` : ''}${key.ruleId} · ${logical.placementId}`,
        }
      })
    : (scenarioQuery.data?.rules ?? []).map((rule, index) => ({
        value: index,
        label: rule.displayName,
      }))

  const submitValidation = () => {
    if (document) validate.mutate(document)
  }

  const saveScenario = async () => {
    try {
      if (!scenarioQuery.data) return
      if (document) {
        const current = validateRuleDocument(document, capabilitiesQuery.data)
        if (!current.valid) {
          setSaveError(`当前规则：${current.errors[0]?.message}`)
          return
        }
      }
      if (!scenarioQuery.data!.rawScenario) {
        if (!document) return
        const local = validateRuleDocument(document, capabilitiesQuery.data)
        if (!local.valid) return
        await replaceRuleOnly.mutateAsync({ scenario: scenarioQuery.data, rule: document })
        resetDirty()
        return
      }
      const payload = flushDraft()
      for (const [index, row] of draftRules(payload).entries()) {
        const candidate = documentFromDraft(row)
        const local = validateRuleDocument(candidate, capabilitiesQuery.data)
        if (!local.valid) {
          setSaveError(`规则 ${index + 1}：${local.errors[0]?.message}`)
          return
        }
      }
      const invalidNumber = (value: unknown): boolean =>
        typeof value === 'number'
          ? !Number.isFinite(value)
          : !!value && typeof value === 'object' && Object.values(value).some(invalidNumber)
      if (invalidNumber(payload)) {
        setSaveError('请修正场景表单中的无效数值。')
        return
      }
      setSaveError('')
      await replaceScenario.mutateAsync(payload)
      resetDirty()
      setScenarioDirty(false)
    } catch (error) {
      setSaveError(formatApiError(error, '保存失败，草稿已保留，请修正后重试。'))
    }
  }

  if (scenarioQuery.isLoading || capabilitiesQuery.isLoading)
    return (
      <div className="page-stack">
        <LoadingState label="正在加载规则能力与场景…" />
      </div>
    )
  if (scenarioQuery.isError)
    return (
      <div className="page-stack">
        <ErrorState error={scenarioQuery.error} onRetry={() => scenarioQuery.refetch()} />
      </div>
    )
  if (capabilitiesQuery.isError)
    return (
      <div className="page-stack">
        <ErrorState error={capabilitiesQuery.error} onRetry={() => capabilitiesQuery.refetch()} />
      </div>
    )
  return (
    <div className="page-stack rules-page">
      <PageHeader
        eyebrow="SIMULATOR / RULE DESIGN"
        title="规则配置"
        description="编辑 match-rule/v1 单一规则配置及其图结构，并在保存前获得本地与后端双重校验。"
        actions={
          <>
            <select
              className="rule-select"
              value={String(ruleIndex)}
              onChange={(event) => selectRule(Number(event.target.value))}
              aria-label="选择规则"
            >
              {ruleOptions.length === 0 && <option value="-1">尚无规则</option>}
              {ruleOptions.map((rule) => (
                <option value={String(rule.value)} key={rule.value}>
                  {rule.label}
                </option>
              ))}
            </select>
            {scenarioQuery.data?.rawScenario && (
              <>
                <button type="button" className="button button-ghost" onClick={() => addRule()}>
                  + 新建规则
                </button>
                <button
                  type="button"
                  className="button button-ghost"
                  disabled={!document}
                  onClick={() => addRule(ruleIndex)}
                >
                  复制当前规则
                </button>
                <button
                  type="button"
                  className="button button-ghost"
                  disabled={!document}
                  onClick={() => deleteRule(ruleIndex)}
                >
                  删除当前规则
                </button>
              </>
            )}
            {document ? (
              <RuleJsonActions
                document={document}
                capabilities={capabilitiesQuery.data!}
                scenario={scenarioQuery.data?.rawScenario ? flushDraft() : undefined}
              />
            ) : null}
            <button
              className="button button-ghost"
              type="button"
              onClick={submitValidation}
              disabled={!document || validate.isPending}
            >
              {validate.isPending ? '服务端校验中…' : '校验规则'}
            </button>
            <button
              className="button button-primary"
              type="button"
              onClick={() => void saveScenario()}
              disabled={
                (!document && !scenarioQuery.data?.rawScenario) ||
                validate.isPending ||
                replaceScenario.isPending ||
                replaceRuleOnly.isPending
              }
            >
              {replaceScenario.isPending ? '保存中…' : '保存场景'}
            </button>
          </>
        }
      />
      {scenarioQuery.data?.rawScenario && (
        <p className="field-hint">
          新规则默认不会成局；复制沿用源规则条件并生成新身份。删除仅从草稿移除，保存场景后生效。保存后规则生效，并重置等待队列与比赛历史。
        </p>
      )}
      {ruleQuery.isLoading ? <LoadingState label="正在加载规则文档…" /> : null}
      {ruleQuery.isError ? (
        <ErrorState error={ruleQuery.error} onRetry={() => ruleQuery.refetch()} />
      ) : null}
      {saveError && (
        <p className="form-error" role="alert">
          {saveError}
        </p>
      )}
      {(replaceScenario.isSuccess || replaceRuleOnly.isSuccess) && !dirty && !scenarioDirty && (
        <p className="success-note" role="status">
          场景已保存，全部规则草稿已应用。
        </p>
      )}
      {replaceScenario.isError && !saveError ? (
        <div className="state-panel state-error">
          <span className="state-icon">!</span>
          <span>
            场景保存失败：
            {formatApiError(replaceScenario.error, '请求失败')}
          </span>
        </div>
      ) : null}
      {!document && scenarioDraft && scenarioQuery.data?.rawScenario ? (
        <section className="panel">
          <p className="rule-form-panel field-hint">
            场景尚无规则。点击“新建规则”即可从安全空规则开始；无物理节点时会建立可编辑的本地节点。
          </p>
          <ScenarioSettingsEditor
            draft={scenarioDraft}
            onChange={(value) => {
              setScenarioDraft(value)
              setScenarioDirty(true)
            }}
            onAddRule={addRule}
            onDeleteRule={deleteRule}
            onSelectRule={(index) => {
              selectRule(index)
              setActiveTab('settings')
            }}
          />
        </section>
      ) : null}
      {document ? (
        <>
          <section className="rule-summary-strip">
            <div>
              <span className="eyebrow">LOGICAL NODE</span>
              <strong>
                {document.ruleKey} / {document.placementId}
              </strong>
            </div>
            <div className="rule-summary-tags">
              <span className="type-chip">Match Rule v1</span>
              <span className="type-chip">Contract / Prefilter / Evaluation v3</span>
              {dirty || scenarioDirty ? (
                <span className="dirty-label">● 未保存</span>
              ) : (
                <StatusPill status="healthy" label="已加载" />
              )}
            </div>
          </section>
          <div className="rules-layout">
            <section className="panel rule-main-panel">
              <div className="rule-tabs" role="tablist">
                {tabs.map((tab) => (
                  <button
                    className={activeTab === tab.id ? 'active' : ''}
                    type="button"
                    role="tab"
                    aria-selected={activeTab === tab.id}
                    onClick={() => setActiveTab(tab.id)}
                    key={tab.id}
                  >
                    {tab.label}
                  </button>
                ))}
              </div>
              {activeTab === 'graph' ? (
                <RuleCanvas document={document} capabilities={capabilitiesQuery.data!} />
              ) : null}
              {activeTab === 'contract' ? (
                <ContractEditor
                  contract={document.contract}
                  onChange={(value) => setEnvelope('contract', value)}
                />
              ) : null}
              {activeTab === 'prefilter' ? (
                <div className="rule-form-panel">
                  <p className="field-hint">
                    Prefilter（预筛选）从隔离索引产生候选集。先声明索引，再选择对应查询操作。
                  </p>
                  <ExpressionEditor
                    value={document.prefilter.bitmap.expr}
                    type="bitmap"
                    contract={document.contract}
                    onChange={(expr) =>
                      setEnvelope('prefilter', {
                        ...document.prefilter,
                        bitmap: { ...document.prefilter.bitmap, expr },
                      })
                    }
                  />
                  <details>
                    <summary>高级：索引探测阈值</summary>
                    <NumberField
                      label="包含探测阈值 (containsProbeThreshold)"
                      optional
                      min={0}
                      value={document.prefilter.runtime?.containsProbeThreshold}
                      help="留空使用核心默认值；0 也表示使用默认值。只改变查询执行策略。"
                      onChange={(value) =>
                        setEnvelope('prefilter', {
                          ...document.prefilter,
                          runtime:
                            value === undefined
                              ? {}
                              : { ...document.prefilter.runtime, containsProbeThreshold: value },
                        })
                      }
                    />
                  </details>
                </div>
              ) : null}
              {activeTab === 'evaluation' ? (
                <div className="rule-form-panel">
                  {(['canJoin', 'canComplete'] as const).map((key) => (
                    <ExpressionEditor
                      key={key}
                      label={key === 'canJoin' ? '候选可加入 (canJoin)' : '可以成局 (canComplete)'}
                      value={document.evaluation[key].expr}
                      type="bool"
                      contract={document.contract}
                      onChange={(expr) =>
                        setEnvelope('evaluation', {
                          ...document.evaluation,
                          [key]: { ...document.evaluation[key], expr },
                        })
                      }
                    />
                  ))}
                </div>
              ) : null}
              {activeTab === 'settings' ? (
                <RuleSettingsEditor document={document} onChange={setEnvelope} />
              ) : null}
              {activeTab === 'scenario' && !scenarioQuery.data!.rawScenario ? (
                <p className="rule-form-panel field-hint">
                  演示模式没有可编辑的宿主部署，请连接真实模拟器 API。
                </p>
              ) : null}
              {activeTab === 'scenario' && scenarioQuery.data!.rawScenario && scenarioDraft ? (
                <ScenarioSettingsEditor
                  onAddRule={addRule}
                  onDeleteRule={deleteRule}
                  onSelectRule={(index) => {
                    selectRule(index)
                    setActiveTab('settings')
                  }}
                  draft={scenarioDraft}
                  onChange={(value) => {
                    syncScenarioProviderChanges(value)
                    setScenarioDraft(value)
                    setScenarioDirty(true)
                  }}
                />
              ) : null}
              {activeTab === 'facts' ? (
                <FactsPanel
                  metadata={factsQuery.data}
                  localFactSources={localFactSources}
                  isLoading={factsQuery.isLoading}
                  isError={factsQuery.isError}
                  error={factsQuery.error}
                  onRetry={() => void factsQuery.refetch()}
                  hasIdentity={Boolean(currentSelection?.apiRule && currentSelection.placementId)}
                  runtimeMetadataCurrent={runtimeMetadataCurrent}
                  rule={currentSelection?.apiRule}
                  ruleKey={currentSelection?.ruleKey ?? document.ruleKey}
                  placementId={currentSelection?.placementId ?? document.placementId}
                  onRuntimeFactsChange={(value) => {
                    setEnvelope('tickFacts', value)
                  }}
                />
              ) : null}
            </section>
            <aside className="rules-side-column">
              {activeTab === 'graph' ? (
                <section className="panel inspector-panel">
                  <NodeInspector contract={document.contract} />
                </section>
              ) : null}
              <section className="panel">
                <ValidationPanel
                  document={document}
                  backend={validate}
                  capabilities={capabilitiesQuery.data!}
                />
              </section>
            </aside>
          </div>
        </>
      ) : null}
    </div>
  )
}
