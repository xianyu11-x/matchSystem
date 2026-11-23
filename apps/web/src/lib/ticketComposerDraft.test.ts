import { describe, expect, it } from 'vitest'
import {
  loadTicketComposerDraft,
  saveTicketComposerDraft,
  ticketComposerDraftKey,
} from './ticketComposerDraft'
import { validateBatch } from './composerValidation'

function memoryStorage(initial: string | null = null) {
  let stored = initial
  return {
    getItem: (key: string) => (key === ticketComposerDraftKey ? stored : null),
    setItem: (key: string, value: string) => {
      if (key === ticketComposerDraftKey) stored = value
    },
  }
}

describe('Ticket 生成草稿', () => {
  it('恢复目标、未提交表单、生成器和持续流量，并保留空数值的校验行为', () => {
    const storage = memoryStorage()
    const draft = loadTicketComposerDraft(storage, 1000)
    draft.contractKey = '["规则1",{"attributes":[]}]'
    draft.targetMode = 'node'
    draft.targetSelections = { rule: '规则1', node: '["2","规则1","3"]' }
    draft.mode = 'single'
    draft.ticketId = ' 123 '
    draft.createdAt = '未完成的时间'
    draft.affinityKey = '1234'
    draft.attributeDraft = { 地区: ' 上海,北京 ' }
    draft.factDraft = { 延迟: '-1' }
    draft.batchFacts = { 等级: '7' }
    draft.batchOptions = {
      createdAtStart: '123',
      createdAtStep: '25',
      affinityPrefix: '5',
      requestIdPrefix: '6',
      atomic: false,
    }
    draft.generators = {
      地区: { type: 'strings', source: 'sample', values: ['', 'a,b', '  x  '], count: 2 },
      等级: {
        type: 'int64',
        source: 'sample',
        min: '0',
        max: '100',
        distribution: 'normal',
        mean: NaN,
        stdDev: NaN,
      },
    }
    draft.batch = { count: 30, seed: -7, startTicketId: undefined }
    draft.traffic = {
      mode: 'fixed',
      distribution: 'burst',
      rate: 1.5,
      burstSize: 9,
      burstIntervalMs: 1100,
      matchIntervalMs: 500,
      maxMatches: 3,
      seed: 12,
      steps: 40,
      stepMs: 50,
      startTimeMs: 5000,
    }

    saveTicketComposerDraft(draft, storage)
    const restored = loadTicketComposerDraft(storage, 2000)
    expect(restored).toEqual(draft)
    expect(Number.isNaN(restored.generators.等级.mean)).toBe(true)
    expect(Number.isNaN(restored.generators.等级.stdDev)).toBe(true)
    expect(() =>
      validateBatch({ ...restored.batch, ruleKey: '1', attributeGenerators: restored.generators }),
    ).toThrow('均值必须位于最小/最大值之间')
  })

  it('存储缺失、不可用、损坏或版本不兼容时回退到默认值', () => {
    const blocked = {
      getItem: () => {
        throw new Error('storage denied')
      },
      setItem: () => {
        throw new Error('storage denied')
      },
    }
    const defaults = loadTicketComposerDraft(memoryStorage(), 1000)
    expect(defaults.batch.startTicketId).toBe(1000000)
    expect(defaults.traffic.startTimeMs).toBe(1000)
    expect(loadTicketComposerDraft(blocked, 1000)).toEqual(defaults)
    expect(loadTicketComposerDraft(memoryStorage('{'), 1000)).toEqual(defaults)
    expect(loadTicketComposerDraft(memoryStorage('{"version":2,"draft":{}}'), 1000)).toEqual(
      defaults,
    )
    expect(loadTicketComposerDraft(memoryStorage('{"version":1,"draft":[]}'), 1000)).toEqual(
      defaults,
    )
    expect(() => saveTicketComposerDraft(defaults, blocked)).not.toThrow()
  })

  it('过滤损坏字段，同时保留其他可用草稿和待修正的非法输入', () => {
    const storage = memoryStorage(
      JSON.stringify({
        version: 1,
        draft: {
          contractKey: 42,
          targetMode: 'unknown',
          targetSelections: { rule: '1', node: 123 },
          mode: 'single',
          ticketId: '尚未填写完',
          attributeDraft: { valid: '待修正', bad: ['错误'] },
          batchOptions: { atomic: 'false', createdAtStep: '0' },
          batch: { count: -1, seed: '坏数据' },
          generators: {
            valid: { type: 'strings', values: ['保留'], count: 0 },
            invalid: { type: 'unknown', values: [1] },
          },
          traffic: { distribution: 'bad', rate: -1, burstSize: null },
        },
      }),
    )
    const restored = loadTicketComposerDraft(storage, 1000)
    expect(restored.contractKey).toBeUndefined()
    expect(restored.targetMode).toBe('rule')
    expect(restored.targetSelections).toEqual({ rule: '1' })
    expect(restored.mode).toBe('single')
    expect(restored.ticketId).toBe('尚未填写完')
    expect(restored.attributeDraft).toEqual({ valid: '待修正' })
    expect(restored.batchOptions.atomic).toBe(true)
    expect(restored.batchOptions.createdAtStep).toBe('0')
    expect(restored.batch.count).toBe(-1)
    expect(restored.batch.seed).toBe(20260829)
    expect(restored.generators).toEqual({ valid: { type: 'strings', values: ['保留'], count: 0 } })
    expect(restored.traffic.distribution).toBe('constant')
    expect(restored.traffic.rate).toBe(-1)
    expect(restored.traffic.burstSize).toBe(50)
  })
})
