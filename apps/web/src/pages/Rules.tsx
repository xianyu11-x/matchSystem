import { useEffect, useMemo, useRef, useState } from 'react'
import { ContractEditor } from '../components/ContractEditor'
import { ExpressionEditor } from '../components/ExpressionEditor'
import { NodeInspector } from '../components/NodeInspector'
import { RuleCanvas } from '../components/RuleCanvas'
import { NumberField, TextField } from '../components/RuleFormControls'
import { RuleSettingsEditor } from '../components/RuleSettingsEditor'
import { EmptyState, ErrorState, LoadingState, PageHeader, StatusPill } from '../components/States'
import '../components/rule-forms.css'
import { ApiError, formatApiError } from '../lib/api'
import { demoCapabilities } from '../lib/mockData'
import {
  useCapabilities,
  useReplaceRuleLibrary,
  useRuleLibrary,
  useValidateRule,
} from '../lib/queries'
import { portableRuleDocument, portableRuleFileName } from '../lib/ruleDocumentIO'
import { parseRuleIdentityJson, parseRuleKey } from '../lib/ruleIdentity'
import {
  bindRuleLibraryEditor,
  parseRuleLibraryImport,
  ruleIdentity,
  ruleIdentityLabel,
  useLibraryWorkspace,
  validateLibraryDocuments,
} from '../lib/ruleLibrary'
import { useRuleStore, type RulesTab } from '../lib/ruleStore'
import { validateRuleDocument } from '../lib/validation'
import type { ApiRuleKey, Capabilities, RuleDocument } from '../types'

const tabs: Array<{ id: RulesTab; label: string }> = [
  { id: 'settings', label: '种子、评分与预算' },
  { id: 'contract', label: '字段契约' },
  { id: 'prefilter', label: '预筛选' },
  { id: 'evaluation', label: '加入与成局' },
  { id: 'graph', label: '高级：规则图' },
]

function ValidationPanel({
  document,
  backend,
  capabilities,
}: {
  document: RuleDocument
  backend: ReturnType<typeof useValidateRule>
  capabilities?: Capabilities
}) {
  const local = useMemo(
    () => validateRuleDocument(document, capabilities),
    [capabilities, document],
  )
  const identityError = !parseRuleKey(document.apiRule)
    ? '规则 ID 必须为无前导零的正 uint64 十进制数，别名须为文本。'
    : ''
  const issues = [...local.errors, ...(backend.data?.errors ?? [])]
  const valid = !identityError && local.valid && !backend.data?.errors.length
  return (
    <div className="validation-panel">
      <div className="validation-heading">
        <div>
          <span className="eyebrow">VALIDATION</span>
          <strong>
            {valid ? '当前编辑合法' : `${issues.length + (identityError ? 1 : 0)} 个问题待处理`}
          </strong>
        </div>
        <span className={`validation-dot ${valid ? 'valid' : 'invalid'}`} />
      </div>
      {valid ? <p className="validation-ok">规则结构和图结构均通过本地快速校验。</p> : null}
      {identityError ? <p className="form-error">{identityError}</p> : null}
      {issues.length ? (
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
      ) : null}
      {backend.isError ? (
        <p className="form-error">{formatApiError(backend.error, '服务端校验失败')}</p>
      ) : null}
      {backend.isSuccess && backend.data?.valid ? (
        <p className="success-note">Go 服务端校验已通过。</p>
      ) : null}
    </div>
  )
}

function downloadJson(value: unknown, fileName: string) {
  const hasInvalidNumber = (item: unknown, path = ''): boolean =>
    typeof item === 'number'
      ? !Number.isFinite(item) ||
        (!path.includes('/scoring/params/') && !Number.isSafeInteger(item))
      : !!item &&
        typeof item === 'object' &&
        Object.entries(item).some(([key, child]) => hasInvalidNumber(child, `${path}/${key}`))
  if (hasInvalidNumber(value)) throw new Error('草稿包含无效或无法精确表示的数值，请修正后再导出。')
  const blob = new Blob([`${JSON.stringify(value, null, 2)}\n`], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const anchor = window.document.createElement('a')
  anchor.href = url
  anchor.download = fileName
  anchor.click()
  window.setTimeout(() => URL.revokeObjectURL(url), 0)
}

export function Rules() {
  const libraryQuery = useRuleLibrary()
  const capabilitiesQuery = useCapabilities()
  const replaceLibrary = useReplaceRuleLibrary()
  const validate = useValidateRule()
  const inputRef = useRef<HTMLInputElement>(null)
  const [message, setMessage] = useState<{ kind: 'success' | 'error'; text: string }>()
  const document = useRuleStore((state) => state.document)
  const activeTab = useRuleStore((state) => state.activeTab)
  const setEnvelope = useRuleStore((state) => state.setEnvelope)
  const setActiveTab = useRuleStore((state) => state.setActiveTab)
  const rules = useLibraryWorkspace((state) => state.rules)
  const selected = useLibraryWorkspace((state) => state.selected)
  const revision = useLibraryWorkspace((state) => state.revision)
  const dirty = useLibraryWorkspace((state) => state.dirty)
  const serverChanged = useLibraryWorkspace((state) => state.serverChanged)
  const loadToken = useLibraryWorkspace((state) => state.loadToken)

  useEffect(bindRuleLibraryEditor, [])

  useEffect(() => {
    if (libraryQuery.data) useLibraryWorkspace.getState().hydrate(libraryQuery.data)
  }, [libraryQuery.data])

  useEffect(() => {
    const state = useLibraryWorkspace.getState()
    const selectedDocument = state.rules[state.selected]
    if (selectedDocument) useRuleStore.getState().setDocument(selectedDocument)
    else useRuleStore.getState().clearGraphSession()
    if (!tabs.some((tab) => tab.id === useRuleStore.getState().activeTab))
      useRuleStore.getState().setActiveTab('settings')
  }, [loadToken])

  useEffect(() => {
    validate.reset()
  }, [document])

  const flush = () => {
    const state = useRuleStore.getState()
    if (state.document) useLibraryWorkspace.getState().updateCurrent(state.document, state.dirty)
  }

  const selectRule = (index: number) => {
    flush()
    useLibraryWorkspace.getState().select(index)
    setMessage(undefined)
  }

  const addRule = (copy: boolean) => {
    try {
      flush()
      useLibraryWorkspace.getState().add(copy)
      setActiveTab('settings')
      setMessage(undefined)
    } catch (error) {
      setMessage({ kind: 'error', text: formatApiError(error, '复制规则失败') })
    }
  }

  const updateIdentity = (patch: Partial<ApiRuleKey>) => {
    const state = useRuleStore.getState()
    if (!state.document) return
    const key = { ...state.document.apiRule!, ...patch }
    if (key.namespace === '') delete key.namespace
    const next = { ...state.document, apiRule: key, ruleKey: ruleIdentity(key) }
    useRuleStore.setState({
      document: next,
      dirty: true,
      graphCache: {
        ...state.graphCache,
        [JSON.stringify([next.ruleKey, next.placementId])]: next.graph,
      },
    })
  }

  const save = async () => {
    try {
      flush()
      const workspace = useLibraryWorkspace.getState()
      if (workspace.revision === undefined) return
      const problem = validateLibraryDocuments(workspace.rules)
      if (problem) throw new Error(problem)
      for (const [index, rule] of workspace.rules.entries()) {
        const result = validateRuleDocument(rule, capabilitiesQuery.data)
        if (!result.valid) throw new Error(`规则 ${index + 1}：${result.errors[0]?.message}`)
      }
      const snapshot = {
        revision: workspace.revision,
        rules: workspace.rules.map(portableRuleDocument),
      }
      const sentVersion = workspace.version
      const saved = await replaceLibrary.mutateAsync(snapshot)
      const latest = useLibraryWorkspace.getState()
      if (latest.version === sentVersion && latest.rules.length === saved.rules.length) {
        const graphCache = { ...useRuleStore.getState().graphCache }
        latest.rules.forEach((rule, index) => {
          const key = parseRuleKey(saved.rules[index].ruleKey)
          if (key) graphCache[JSON.stringify([ruleIdentity(key), rule.placementId])] = rule.graph
        })
        useRuleStore.setState({ graphCache })
      }
      useLibraryWorkspace.getState().saved(saved, sentVersion)
      if (!useLibraryWorkspace.getState().dirty) useRuleStore.getState().resetDirty()
      setMessage({
        kind: 'success',
        text: useLibraryWorkspace.getState().dirty
          ? '规则库已保存；保存期间的新编辑仍在草稿中。'
          : '规则库已保存。请在物理节点中选择规则部署。',
      })
    } catch (error) {
      const conflict = error instanceof ApiError && error.status === 409
      setMessage({
        kind: 'error',
        text: conflict
          ? '规则库已被其他操作修改，当前草稿仍保留。可先导出草稿，再重新加载服务端规则库。'
          : formatApiError(error, '保存失败，草稿已保留。'),
      })
    }
  }

  const importJson = async (file?: File) => {
    if (!file) return
    try {
      flush()
      const imported = parseRuleLibraryImport(parseRuleIdentityJson(await file.text()))
      const current = useLibraryWorkspace.getState()
      const overlaps =
        imported.kind === 'single' &&
        current.rules.some(
          (rule) =>
            rule.apiRule && ruleIdentity(rule.apiRule) === ruleIdentity(imported.rules[0].ruleKey),
        )
      if (
        (imported.kind === 'library' || overlaps) &&
        current.rules.length &&
        !window.confirm(
          imported.kind === 'library'
            ? '导入规则库将替换当前规则草稿，是否继续？'
            : '导入将替换同身份的规则草稿，是否继续？',
        )
      )
        return
      useLibraryWorkspace.getState().importRules(imported)
      setActiveTab('settings')
      setMessage({ kind: 'success', text: `已导入 ${file.name}，请保存规则库。` })
    } catch (error) {
      setMessage({ kind: 'error', text: formatApiError(error, '导入失败') })
    } finally {
      if (inputRef.current) inputRef.current.value = ''
    }
  }

  const exportJson = (all: boolean) => {
    try {
      flush()
      const workspace = useLibraryWorkspace.getState()
      if (all) {
        downloadJson(
          {
            schemaVersion: 'match-rule-library/v1',
            rules: workspace.rules.map(portableRuleDocument),
          },
          'match-rule-library.json',
        )
      } else {
        const current = workspace.rules[workspace.selected]
        if (!current) return
        downloadJson(portableRuleDocument(current), portableRuleFileName(current))
      }
      setMessage({ kind: 'success', text: all ? '已导出规则库草稿。' : '已导出当前规则草稿。' })
    } catch (error) {
      setMessage({ kind: 'error', text: formatApiError(error, '导出失败') })
    }
  }

  const reload = async () => {
    if (dirty && !window.confirm('重新加载会放弃当前未保存的规则草稿，是否继续？')) return
    const result = await libraryQuery.refetch()
    if (!result.isError && result.data) {
      useRuleStore.getState().clearGraphSession()
      useLibraryWorkspace.getState().hydrate(result.data, true)
      setMessage({ kind: 'success', text: '已重新加载服务端规则库。' })
    } else {
      setMessage({ kind: 'error', text: formatApiError(result.error, '重新加载失败') })
    }
  }

  if (revision === undefined && libraryQuery.isLoading)
    return (
      <div className="page-stack">
        <LoadingState label="正在加载规则库…" />
      </div>
    )
  if (revision === undefined && libraryQuery.isError)
    return (
      <div className="page-stack">
        <ErrorState error={libraryQuery.error} onRetry={() => void libraryQuery.refetch()} />
      </div>
    )
  const capabilities = capabilitiesQuery.data ?? demoCapabilities
  const selectedDocument = selected >= 0 ? document : undefined

  return (
    <div className="page-stack rules-page">
      <PageHeader
        eyebrow="SIMULATOR / RULE LIBRARY"
        title="规则配置"
        description="独立编辑和保存 match-rule/v1 规则；在物理节点中选择规则并部署。"
        actions={
          <>
            <select
              className="rule-select"
              value={String(selected)}
              onChange={(event) => selectRule(Number(event.target.value))}
              aria-label="选择规则"
            >
              {rules.length === 0 ? <option value="-1">尚无规则</option> : null}
              {rules.map((rule, index) => (
                <option value={String(index)} key={rule.placementId}>
                  {rule.apiRule ? ruleIdentityLabel(rule.apiRule) : rule.ruleKey}
                </option>
              ))}
            </select>
            <button type="button" className="button button-ghost" onClick={() => addRule(false)}>
              + 新建规则
            </button>
            <button
              type="button"
              className="button button-ghost"
              disabled={!selectedDocument}
              onClick={() => addRule(true)}
            >
              复制当前规则
            </button>
            <button
              type="button"
              className="button button-ghost"
              disabled={!selectedDocument}
              onClick={() => {
                flush()
                useLibraryWorkspace.getState().remove()
              }}
            >
              删除当前规则
            </button>
            <div className="rule-json-actions">
              <input
                ref={inputRef}
                className="visually-hidden"
                type="file"
                accept="application/json,.json"
                aria-label="选择规则或规则库 JSON 文件"
                onChange={(event) => void importJson(event.target.files?.[0])}
              />
              <button
                type="button"
                className="button button-ghost"
                onClick={() => inputRef.current?.click()}
              >
                导入 JSON
              </button>
              <button
                type="button"
                className="button button-ghost"
                disabled={!selectedDocument}
                onClick={() => exportJson(false)}
              >
                导出规则
              </button>
              <button
                type="button"
                className="button button-ghost"
                onClick={() => exportJson(true)}
              >
                导出规则库
              </button>
            </div>
            <button
              className="button button-ghost"
              type="button"
              disabled={!selectedDocument || validate.isPending}
              onClick={() => {
                if (!selectedDocument) return
                if (!parseRuleKey(selectedDocument.apiRule)) {
                  setMessage({
                    kind: 'error',
                    text: '规则身份无效，无法执行服务端校验。',
                  })
                  return
                }
                validate.mutate(selectedDocument)
              }}
            >
              {validate.isPending ? '服务端校验中…' : '校验规则'}
            </button>
            <button
              className="button button-primary"
              type="button"
              disabled={replaceLibrary.isPending}
              onClick={() => void save()}
            >
              {replaceLibrary.isPending ? '保存中…' : '保存规则库'}
            </button>
          </>
        }
      />
      <p className="field-hint">
        新规则默认不会加入或成局。规则库只保存规则定义；Tick Facts 和 Provider 在部署时配置。
      </p>
      {capabilitiesQuery.isError ? (
        <p className="form-error">能力列表加载失败，暂使用内置编辑能力；服务端校验可能不可用。</p>
      ) : null}
      {serverChanged ? (
        <p className="form-error">服务端规则库已有更新，当前草稿已保留。可导出草稿后重新加载。</p>
      ) : null}
      {serverChanged || (message?.kind === 'error' && message.text.includes('其他操作')) ? (
        <button
          type="button"
          className="button button-ghost"
          disabled={replaceLibrary.isPending}
          onClick={() => void reload()}
        >
          重新加载服务端规则库
        </button>
      ) : null}
      {message ? (
        <p
          className={message.kind === 'error' ? 'form-error' : 'success-note'}
          role={message.kind === 'error' ? 'alert' : 'status'}
        >
          {message.text}
        </p>
      ) : null}
      {!selectedDocument ? (
        <EmptyState title="规则库为空" detail="新建或导入规则后即可编辑。" />
      ) : (
        <>
          <section className="rule-summary-strip">
            <div>
              <span className="eyebrow">MATCH RULE</span>
              <strong>
                {selectedDocument.apiRule
                  ? ruleIdentityLabel(selectedDocument.apiRule)
                  : selectedDocument.ruleKey}
              </strong>
            </div>
            <div className="rule-summary-tags">
              <span className="type-chip">Match Rule v1</span>
              {dirty ? (
                <span className="dirty-label">● 未保存</span>
              ) : (
                <StatusPill status="healthy" label="已保存" />
              )}
            </div>
          </section>
          <div className="rules-layout">
            <section className="panel rule-main-panel">
              <div className="rule-form-panel rule-form-grid">
                <TextField
                  label="规则别名（可选）"
                  value={String(selectedDocument.apiRule?.namespace ?? '')}
                  onChange={(namespace) => updateIdentity({ namespace })}
                  help="可使用任意文本；规则由规则 ID 唯一标识。"
                />
                <TextField
                  label="规则 ID"
                  required
                  help="请输入无前导零的正 uint64 十进制数。"
                  value={String(selectedDocument.apiRule?.ruleId ?? '')}
                  onChange={(ruleId) => updateIdentity({ ruleId })}
                />
              </div>
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
                <RuleCanvas document={selectedDocument} capabilities={capabilities} />
              ) : null}
              {activeTab === 'contract' ? (
                <ContractEditor
                  contract={selectedDocument.contract}
                  onChange={(value) => setEnvelope('contract', value)}
                />
              ) : null}
              {activeTab === 'prefilter' ? (
                <div className="rule-form-panel">
                  <p className="field-hint">
                    预筛选从隔离索引产生候选集。先声明索引，再选择对应查询操作。
                  </p>
                  <ExpressionEditor
                    value={selectedDocument.prefilter.bitmap.expr}
                    type="bitmap"
                    contract={selectedDocument.contract}
                    onChange={(expr) =>
                      setEnvelope('prefilter', {
                        ...selectedDocument.prefilter,
                        bitmap: { ...selectedDocument.prefilter.bitmap, expr },
                      })
                    }
                  />
                  <details>
                    <summary>高级：索引探测阈值</summary>
                    <NumberField
                      label="包含探测阈值 (containsProbeThreshold)"
                      optional
                      min={0}
                      value={selectedDocument.prefilter.runtime?.containsProbeThreshold}
                      help="留空或 0 使用核心默认值。"
                      onChange={(value) =>
                        setEnvelope('prefilter', {
                          ...selectedDocument.prefilter,
                          runtime:
                            value === undefined
                              ? {}
                              : {
                                  ...selectedDocument.prefilter.runtime,
                                  containsProbeThreshold: value,
                                },
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
                      value={selectedDocument.evaluation[key].expr}
                      type="bool"
                      contract={selectedDocument.contract}
                      onChange={(expr) =>
                        setEnvelope('evaluation', {
                          ...selectedDocument.evaluation,
                          [key]: { ...selectedDocument.evaluation[key], expr },
                        })
                      }
                    />
                  ))}
                </div>
              ) : null}
              {activeTab === 'settings' ? (
                <RuleSettingsEditor document={selectedDocument} onChange={setEnvelope} />
              ) : null}
            </section>
            <aside className="rules-side-column">
              {activeTab === 'graph' ? (
                <section className="panel inspector-panel">
                  <NodeInspector contract={selectedDocument.contract} />
                </section>
              ) : null}
              <section className="panel">
                <ValidationPanel
                  document={selectedDocument}
                  backend={validate}
                  capabilities={capabilitiesQuery.data}
                />
              </section>
            </aside>
          </div>
        </>
      )}
    </div>
  )
}
