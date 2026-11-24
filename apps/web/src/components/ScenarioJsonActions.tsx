import { useRef, useState } from 'react'
import { api, formatApiError, isDemoMode } from '../lib/api'
import { uint64Text } from '../lib/ruleIdentity'
import { useImportScenarioText } from '../lib/queries'
import { saveJsonFile } from '../lib/saveJsonFile'
import type { JsonObject } from '../types'
import './scenario-json-actions.css'

export function parseScenarioJson(value: unknown): JsonObject {
  const object = (item: unknown): Record<string, unknown> | undefined =>
    item && typeof item === 'object' && !Array.isArray(item)
      ? (item as Record<string, unknown>)
      : undefined
  const root = object(value)
  const scenario = object(root?.scenario) ?? root
  if (scenario?.schemaVersion !== 'simulator-scenario/v1')
    throw new Error('请选择 simulator-scenario/v1 场景 JSON。')

  // JSON.parse 无法精确表示大于安全整数上限的数字身份；要求用字符串传输。
  const checkIdentity = (identity: unknown, path: string, positive = true) => {
    if (identity === undefined) return
    if (typeof identity === 'number' && !Number.isSafeInteger(identity))
      throw new Error(`${path} 不能使用不安全的 JSON 数值，请改用十进制字符串。`)
    if (uint64Text(identity, positive) === undefined)
      throw new Error(`${path} 必须为无前导零的${positive ? '正' : ''} uint64 十进制数。`)
  }
  const checkRule = (value: unknown, path: string) => {
    const rule = object(value)
    if (rule?.namespace !== undefined && typeof rule.namespace !== 'string')
      throw new Error(`${path}.namespace 必须为文本。`)
    checkIdentity(rule?.ruleId, `${path}.ruleId`)
  }
  if (Array.isArray(scenario.physicalNodes))
    scenario.physicalNodes.forEach((value, index) =>
      checkIdentity(object(value)?.id, `physicalNodes[${index}].id`),
    )
  if (Array.isArray(scenario.rules))
    scenario.rules.forEach((value, index) => {
      const row = object(value)
      const logical = object(row?.logicalNode)
      checkIdentity(row?.physicalNodeId, `rules[${index}].physicalNodeId`)
      checkIdentity(logical?.placementId, `rules[${index}].logicalNode.placementId`)
      checkRule(logical?.rule, `rules[${index}].logicalNode.rule`)
      checkRule(object(row?.rule)?.ruleKey, `rules[${index}].rule.ruleKey`)
    })
  return scenario as JsonObject
}

// 仅定位顶层 scenario 字段；具体 JSON 合法性由 JSON.parse 先行检查。
export function scenarioDocumentText(text: string): string {
  const root = JSON.parse(text) as unknown
  parseScenarioJson(root)
  const envelope =
    root && typeof root === 'object' && !Array.isArray(root)
      ? (root as Record<string, unknown>)
      : undefined
  if (
    !envelope?.scenario ||
    typeof envelope.scenario !== 'object' ||
    Array.isArray(envelope.scenario)
  )
    return text
  const skipSpace = (offset: number) => {
    while (/\s/.test(text[offset] ?? '') && offset < text.length) offset++
    return offset
  }
  const skipString = (offset: number) => {
    offset++
    while (offset < text.length) {
      if (text[offset] === '\\') offset += 2
      else if (text[offset++] === '"') return offset
    }
    throw new Error('JSON 字符串不完整。')
  }
  const skipValue = (offset: number) => {
    if (text[offset] === '"') return skipString(offset)
    if (text[offset] !== '{' && text[offset] !== '[') {
      while (offset < text.length && ![',', '}', ']'].includes(text[offset])) offset++
      return offset
    }
    const stack: string[] = []
    while (offset < text.length) {
      const current = text[offset]
      if (current === '"') {
        offset = skipString(offset)
        continue
      }
      if (current === '{' || current === '[') stack.push(current)
      if (current === '}' || current === ']') {
        stack.pop()
        if (!stack.length) return offset + 1
      }
      offset++
    }
    throw new Error('JSON 对象不完整。')
  }
  let offset = skipSpace(0) + 1
  let scenario: string | undefined
  while (offset < text.length) {
    offset = skipSpace(offset)
    if (text[offset] === '}') break
    const keyEnd = skipString(offset)
    const key = JSON.parse(text.slice(offset, keyEnd)) as string
    offset = skipSpace(keyEnd) + 1
    const start = skipSpace(offset)
    const end = skipValue(start)
    if (key === 'scenario') scenario = text.slice(start, end)
    offset = skipSpace(end)
    if (text[offset] === ',') offset++
  }
  if (!scenario) throw new Error('场景 JSON 缺少 scenario 对象。')
  return scenario
}

export function ScenarioJsonActions({ scenario }: { scenario?: JsonObject }) {
  const inputRef = useRef<HTMLInputElement>(null)
  const exportingRef = useRef(false)
  const replace = useImportScenarioText()
  const [exporting, setExporting] = useState(false)
  const [message, setMessage] = useState<{ kind: 'success' | 'error'; text: string }>()

  const importJson = async (file?: File) => {
    if (!file) return
    try {
      const payload = scenarioDocumentText(await file.text())
      if (
        !window.confirm(
          '导入会替换整个运行场景，清空等待队列和比赛历史。规则库不会更改，是否继续？',
        )
      )
        return
      await replace.mutateAsync(payload)
      setMessage({ kind: 'success', text: `场景 ${file.name} 已导入。` })
    } catch (error) {
      setMessage({ kind: 'error', text: formatApiError(error, '导入场景失败') })
    } finally {
      if (inputRef.current) inputRef.current.value = ''
    }
  }

  const exportJson = async () => {
    if (!scenario || exportingRef.current) return
    exportingRef.current = true
    setExporting(true)
    setMessage(undefined)
    try {
      const name = String(scenario.scenarioId ?? scenario.id ?? 'simulator-scenario')
      const saved = await saveJsonFile(`${name.replace(/[^a-zA-Z0-9._-]+/g, '-')}.json`, () =>
        api.getScenarioJsonText(),
      )
      if (saved !== null) setMessage({ kind: 'success', text: `场景已保存：${saved}` })
    } catch (error) {
      setMessage({ kind: 'error', text: formatApiError(error, '导出场景失败') })
    } finally {
      exportingRef.current = false
      setExporting(false)
    }
  }

  return (
    <div className="scenario-json-actions">
      <input
        ref={inputRef}
        className="visually-hidden"
        type="file"
        accept="application/json,.json"
        aria-label="选择场景 JSON 文件"
        onChange={(event) => void importJson(event.target.files?.[0])}
      />
      <button
        type="button"
        className="button button-ghost"
        disabled={isDemoMode || replace.isPending}
        onClick={() => inputRef.current?.click()}
      >
        {replace.isPending ? '导入中…' : '导入场景'}
      </button>
      <button
        type="button"
        className="button button-ghost"
        disabled={!scenario || isDemoMode || exporting}
        onClick={() => void exportJson()}
      >
        {exporting ? '导出中…' : '导出场景'}
      </button>
      {message ? (
        <p
          className={`${message.kind === 'error' ? 'form-error' : 'success-note'} export-result`}
          role={message.kind === 'error' ? 'alert' : 'status'}
        >
          {message.text}
        </p>
      ) : null}
    </div>
  )
}
