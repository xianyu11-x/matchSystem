import { useEffect, useMemo, useState } from 'react'
import {
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  SectionTitle,
  StatusPill,
} from '../components/States'
import { NodeInspector } from '../components/NodeInspector'
import { RuleCanvas } from '../components/RuleCanvas'
import {
  useCapabilities,
  useReplaceScenario,
  useRule,
  useScenario,
  useValidateRule,
} from '../lib/queries'
import { useRuleStore, type RulesTab } from '../lib/ruleStore'
import { validateRuleDocument } from '../lib/validation'
import type { FactScope, LogicalNodeContract, RuleDocument } from '../types'

const tabs: Array<{ id: RulesTab; label: string }> = [
  { id: 'graph', label: 'Rule Graph' },
  { id: 'contract', label: 'Contract' },
  { id: 'prefilter', label: 'Prefilter' },
  { id: 'evaluation', label: 'Evaluation' },
  { id: 'facts', label: '全部 Facts' },
]

function JsonPanel({ value }: { value: unknown }) {
  return <pre className="json-panel">{JSON.stringify(value, null, 2)}</pre>
}

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

function ContractPanel({
  contract,
  onApply,
}: {
  contract: LogicalNodeContract
  onApply: (next: unknown) => void
}) {
  return (
    <div className="detail-panel-stack">
      <div className="schema-callout">
        <span className="schema-badge">v3</span>
        <div>
          <strong>logical-node-contract/v3</strong>
          <p>限制 Attribute、Fact、Index 以及运行时复杂度，保存前由 Go 再次编译确认。</p>
        </div>
      </div>
      <div className="detail-table-wrap">
        <table className="detail-table">
          <thead>
            <tr>
              <th>Attribute</th>
              <th>类型</th>
              <th>最大值数</th>
            </tr>
          </thead>
          <tbody>
            {contract.attributes.map((item) => (
              <tr key={item.name}>
                <td>
                  <strong>{item.name}</strong>
                </td>
                <td>
                  <span className="type-chip">{item.type}</span>
                </td>
                <td>{item.maxValues ?? '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="detail-table-wrap">
        <table className="detail-table">
          <thead>
            <tr>
              <th>Fact</th>
              <th>类型</th>
              <th>Scope</th>
              <th>最大值数</th>
            </tr>
          </thead>
          <tbody>
            {contract.facts.map((item) => (
              <tr key={`${item.scope}-${item.name}`}>
                <td>
                  <strong>{item.name}</strong>
                </td>
                <td>
                  <span className="type-chip">{item.type}</span>
                </td>
                <td>
                  <span className={`scope-chip scope-${item.scope}`}>{item.scope}</span>
                </td>
                <td>{item.maxValues ?? '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="detail-table-wrap">
        <table className="detail-table">
          <thead>
            <tr>
              <th>Index</th>
              <th>类型</th>
              <th>键类型</th>
            </tr>
          </thead>
          <tbody>
            {contract.indexes.map((item) => (
              <tr key={item.name}>
                <td>
                  <strong>{item.name}</strong>
                </td>
                <td>
                  <span className="type-chip">{item.type}</span>
                </td>
                <td>{item.keyType ?? '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <details className="raw-json-details">
        <summary>编辑 Contract JSON</summary>
        <JsonEditorPanel value={contract} onApply={onApply} />
      </details>
    </div>
  )
}

function FactsPanel({
  contract,
  tickFacts,
}: {
  contract: LogicalNodeContract
  tickFacts: Record<string, unknown>
}) {
  const grouped = (['tick', 'object', 'match'] as FactScope[]).map((scope) => ({
    scope,
    facts: contract.facts.filter((fact) => fact.scope === scope),
  }))
  return (
    <div className="facts-panel">
      <div className="schema-callout">
        <span className="schema-badge">FACT</span>
        <div>
          <strong>Fact registry · 全部 scope</strong>
          <p>Object Facts 随 Ticket 观测；Tick/Match Facts 由模拟器运行时提供。</p>
        </div>
      </div>
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
                  <div>
                    <strong>{fact.name}</strong>
                    <span>
                      {fact.type}
                      {fact.maxValues ? ` · max ${fact.maxValues}` : ''}
                    </span>
                  </div>
                  {group.scope === 'tick' && tickFacts[fact.name] !== undefined ? (
                    <code>{JSON.stringify(tickFacts[fact.name])}</code>
                  ) : (
                    <span className="muted">provider</span>
                  )}
                </div>
              ))
            )}
          </div>
        ))}
      </div>
    </div>
  )
}

function ValidationPanel({
  document,
  backend,
}: {
  document?: RuleDocument
  backend: ReturnType<typeof useValidateRule>
}) {
  const local = useMemo(
    () => (document ? validateRuleDocument(document) : { valid: false, errors: [] }),
    [document],
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
    const local = validateRuleDocument(document)
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
        description="查看四种 v3 envelope，编辑规则图，并在保存前获得本地与后端双重校验。"
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
              <span className="type-chip">Contract v3</span>
              <span className="type-chip">Prefilter v3</span>
              <span className="type-chip">Evaluation v3</span>
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
                <ContractPanel
                  contract={document.contract}
                  onApply={(value) => setEnvelope('contract', value)}
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
                  contract={document.contract}
                  tickFacts={document.tickFacts ?? scenarioQuery.data.tickFacts}
                />
              ) : null}
            </section>
            <aside className="rules-side-column">
              <section className="panel inspector-panel">
                <NodeInspector contract={document.contract} />
              </section>
              <section className="panel">
                <ValidationPanel document={document} backend={validate} />
              </section>
            </aside>
          </div>
        </>
      ) : null}
    </div>
  )
}
