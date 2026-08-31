import { useEffect, useMemo, useRef, useState } from 'react'
import { EmptyState, ErrorState, LoadingState, PageHeader, StatusPill } from '../components/States'
import { NodeInspector } from '../components/NodeInspector'
import { RuleCanvas } from '../components/RuleCanvas'
import { ContractEditor } from '../components/ContractEditor'
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
import { validateRuleDocument } from '../lib/validation'
import type {
  ApiRuleKey,
  Capabilities,
  FactSpec,
  FactScope,
  JsonObject,
  RuleDocument,
} from '../types'

const tabs: Array<{ id: RulesTab; label: string }> = [
  { id: 'graph', label: 'Rule Graph' },
  { id: 'contract', label: 'Contract' },
  { id: 'prefilter', label: 'Prefilter' },
  { id: 'evaluation', label: 'Evaluation' },
  { id: 'facts', label: '全部 Facts' },
]

function JsonEditorPanel({ value, onApply }: { value: unknown; onApply: (next: unknown) => void }) {
  const [text, setText] = useState(() => JSON.stringify(value, null, 2))
  const [parseError, setParseError] = useState<string>()
  useEffect(() => {
    setText(JSON.stringify(value, null, 2))
    setParseError(undefined)
  }, [value])
  const apply = () => {
    try {
      onApply(JSON.parse(text) as unknown)
      setParseError(undefined)
    } catch (error) {
      setParseError(error instanceof Error ? error.message : 'JSON 语法错误')
    }
  }
  return (
    <div className="json-editor-wrap">
      <textarea
        className="json-editor"
        value={text}
        onChange={(event) => setText(event.target.value)}
        spellCheck={false}
        aria-label="JSON 编辑器"
      />
      <div className="json-editor-footer">
        <span className="muted">应用后会重新生成 Rule Graph</span>
        <button className="button button-ghost" type="button" onClick={apply}>
          应用 JSON
        </button>
      </div>
      {parseError ? <p className="form-error">JSON 无法解析：{parseError}</p> : null}
    </div>
  )
}

function FactsPanel({
  facts,
  isLoading,
  isError,
  error,
  onRetry,
  hasIdentity,
  rule,
  ruleKey,
  placementId,
  tickFacts,
}: {
  facts?: FactSpec[]
  isLoading: boolean
  isError: boolean
  error: unknown
  onRetry: () => void
  hasIdentity: boolean
  rule?: ApiRuleKey
  ruleKey: string
  placementId: string
  tickFacts: Record<string, unknown>
}) {
  const identity = rule
    ? `${rule.namespace ? `${rule.namespace}/` : ''}${rule.ruleId}`
    : ruleKey
  const groups = facts ?? []
  const grouped = (['tick', 'object', 'match'] as FactScope[]).map((scope) => ({
    scope,
    facts: groups.filter((fact) => fact.scope === scope),
  }))
  return (
    <div className="facts-panel">
      <div className="schema-callout">
        <span className="schema-badge">FACT</span>
        <div>
          <strong>LogicalNode Fact Provider Descriptor</strong>
          <p>以下元数据来自当前 LogicalNode 的启动握手，描述 Provider 可提供的全部 Fact。</p>
          <div className="facts-node-identity">
            <code>{identity}</code>
            <span>placement: {placementId}</span>
          </div>
        </div>
      </div>
      {!hasIdentity ? (
        <EmptyState
          title="无法确定 LogicalNode"
          detail="当前规则缺少 API Rule ID，暂时无法查询 Fact Provider Descriptor。"
        />
      ) : isLoading ? (
        <LoadingState label="正在读取 LogicalNode Fact 定义…" />
      ) : isError ? (
        <ErrorState error={error} onRetry={onRetry} />
      ) : groups.length === 0 ? (
        <EmptyState
          title="当前 LogicalNode 未声明 Fact"
          detail="Provider Descriptor 返回了空的 Fact 列表。"
        />
      ) : (
        <div className="fact-scope-grid">
          {grouped.map((group) => (
            <div className="fact-scope-card" key={group.scope}>
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
                    {group.scope === 'tick' && tickFacts[fact.name] !== undefined ? (
                      <code title={JSON.stringify(tickFacts[fact.name])}>
                        {JSON.stringify(tickFacts[fact.name])}
                      </code>
                    ) : (
                      <span className="muted">provider</span>
                    )}
                  </div>
                ))
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function RuleJsonActions({
  document,
  capabilities,
}: {
  document: RuleDocument
  capabilities: Capabilities
}) {
  const inputRef = useRef<HTMLInputElement>(null)
  const importDocument = useRuleStore((state) => state.importDocument)
  const importScenario = useImportScenario()
  const [message, setMessage] = useState<{ kind: 'success' | 'error'; text: string }>()

  const exportJson = () => {
    try {
      const payload = portableRuleDocument(document)
      const blob = new Blob([`${JSON.stringify(payload, null, 2)}\n`], {
        type: 'application/json',
      })
      const url = URL.createObjectURL(blob)
      const anchor = window.document.createElement('a')
      anchor.href = url
      anchor.download = portableRuleFileName(document)
      anchor.click()
      window.setTimeout(() => URL.revokeObjectURL(url), 0)
      setMessage({ kind: 'success', text: 'match-rule/v1 规则配置已导出（含当前未保存编辑）。' })
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
        导出 JSON
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
  const selectedRule = scenarioQuery.data?.rules[ruleIndex] ?? scenarioQuery.data?.rules[0]
  const ruleQuery = useRule(selectedRule?.ruleKey, selectedRule?.placementId)
  const document = useRuleStore((state) => state.document)
  const activeTab = useRuleStore((state) => state.activeTab)
  const factsQuery = useLogicalNodeFacts(
    selectedRule?.apiRule,
    selectedRule?.placementId,
    activeTab === 'facts',
  )
  const dirty = useRuleStore((state) => state.dirty)
  const setDocument = useRuleStore((state) => state.setDocument)
  const setActiveTab = useRuleStore((state) => state.setActiveTab)
  const setEnvelope = useRuleStore((state) => state.setEnvelope)
  const resetDirty = useRuleStore((state) => state.resetDirty)
  const validate = useValidateRule()
  const replaceScenario = useReplaceScenario()

  useEffect(() => {
    if (ruleQuery.data) setDocument(ruleQuery.data)
  }, [ruleQuery.data, setDocument])

  const submitValidation = () => {
    if (document) validate.mutate(document)
  }

  const saveScenario = async () => {
    if (!document || !scenarioQuery.data) return
    const local = validateRuleDocument(document, capabilitiesQuery.data)
    if (!local.valid) {
      validate.reset()
      return
    }
    const backendResult = await validate.mutateAsync(document)
    if (!backendResult.valid) return
    await replaceScenario.mutateAsync({ scenario: scenarioQuery.data, rule: document })
    resetDirty()
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
  if (!scenarioQuery.data?.rules.length)
    return (
      <div className="page-stack">
        <EmptyState
          title="场景没有规则"
          detail="先在 simulator API 中配置一个 LogicalNode rule。"
        />
      </div>
    )

  return (
    <div className="page-stack rules-page">
      <PageHeader
        eyebrow="SIMULATOR / RULE DESIGN"
        title="Rules"
        description="编辑 match-rule/v1 单一规则配置及其图结构，并在保存前获得本地与后端双重校验。"
        actions={
          <>
            <select
              className="rule-select"
              value={String(Math.max(0, scenarioQuery.data.rules.indexOf(selectedRule!)))}
              onChange={(event) => setRuleIndex(Number(event.target.value))}
              aria-label="选择规则"
            >
              {scenarioQuery.data.rules.map((rule, index) => (
                <option value={String(index)} key={`${rule.ruleKey}/${rule.placementId}`}>
                  {rule.displayName}
                </option>
              ))}
            </select>
            {document ? (
              <RuleJsonActions
                document={document}
                capabilities={capabilitiesQuery.data!}
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
              disabled={!document || validate.isPending || replaceScenario.isPending}
            >
              {replaceScenario.isPending ? '保存中…' : '保存场景'}
            </button>
          </>
        }
      />
      {ruleQuery.isLoading ? <LoadingState label="正在加载规则文档…" /> : null}
      {ruleQuery.isError ? (
        <ErrorState error={ruleQuery.error} onRetry={() => ruleQuery.refetch()} />
      ) : null}
      {replaceScenario.isError ? (
        <div className="state-panel state-error">
          <span className="state-icon">!</span>
          <span>
            场景保存失败：
            {replaceScenario.error instanceof Error ? replaceScenario.error.message : '请求失败'}
          </span>
        </div>
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
              {dirty ? (
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
                <JsonEditorPanel
                  value={document.prefilter}
                  onApply={(value) => setEnvelope('prefilter', value)}
                />
              ) : null}
              {activeTab === 'evaluation' ? (
                <JsonEditorPanel
                  value={document.evaluation}
                  onApply={(value) => setEnvelope('evaluation', value)}
                />
              ) : null}
              {activeTab === 'facts' ? (
                <FactsPanel
                  facts={factsQuery.data}
                  isLoading={factsQuery.isLoading}
                  isError={factsQuery.isError}
                  error={factsQuery.error}
                  onRetry={() => void factsQuery.refetch()}
                  hasIdentity={Boolean(selectedRule?.apiRule && selectedRule.placementId)}
                  rule={selectedRule?.apiRule}
                  ruleKey={selectedRule?.ruleKey ?? document.ruleKey}
                  placementId={selectedRule?.placementId ?? document.placementId}
                  tickFacts={document.tickFacts ?? scenarioQuery.data.tickFacts}
                />
              ) : null}
            </section>
            <aside className="rules-side-column">
              <section className="panel inspector-panel">
                <NodeInspector contract={document.contract} />
              </section>
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
