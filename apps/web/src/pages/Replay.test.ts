import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import { buildReplayTimelineRows, EventTimeline, ReplayEventDetail } from './Replay'
import type { ReplayEvent } from '../lib/replay'

function textContent(markup: string) {
  return markup.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ')
}

it('按原始偏移和顺序映射轮次、调用及穿插的票据事件', () => {
  const events: ReplayEvent[] = [
    { sequence: 1, type: 'round_begin', round: 1, now: 0, offsetNs: 0 },
    {
      sequence: 2,
      type: 'produce_match',
      round: 1,
      now: 0,
      offsetNs: '9007199254740992',
      seedAttempts: 0,
      result: 'no_match',
    },
    {
      sequence: 3,
      type: 'ticket_withdraw',
      offsetNs: '9007199254740992',
      ticket: { TicketID: '18446744073709551615' },
    },
    {
      sequence: 4,
      type: 'produce_match',
      round: 1,
      now: 0,
      offsetNs: '9007199254740993',
      seedAttempts: 2,
      result: 'match',
    },
  ]
  const rows = buildReplayTimelineRows(events)
  expect(rows.map(({ event, call, offsetNs }) => [event.sequence, call, offsetNs])).toEqual([
    [1, undefined, 0],
    [2, 1, '9007199254740992'],
    [3, undefined, '9007199254740992'],
    [4, 2, '9007199254740993'],
  ])
  expect(rows.map((row) => row.kind)).toEqual(events.map((event) => event.type))
  expect(rows[2].label).toContain('票据主动退出 · 票据 18446744073709551615')
  const call = textContent(
    renderToStaticMarkup(createElement(ReplayEventDetail, { row: rows[1], result: false })),
  )
  expect(call).toContain('第 1 轮')
  expect(call).toContain('now=0')
  expect(call).toContain('尝试种子 0')
  expect(call).toContain('+9007199254.740992 ms')
  const withdrawal = textContent(
    renderToStaticMarkup(createElement(ReplayEventDetail, { row: rows[2], result: false })),
  )
  expect(withdrawal).toContain('最近轮次 第 1 轮')
  expect(withdrawal).toContain('票据 18446744073709551615')
})

it('映射全部事件并保留同刻事件各自的身份，缺失时间不伪造偏移', () => {
  const events: ReplayEvent[] = Array.from({ length: 101 }, (_, index) => ({
    sequence: index + 1,
    type: 'ticket_add',
    offsetNs: index === 100 ? undefined : 0,
    ticketId: index + 1,
  }))
  const rows = buildReplayTimelineRows(events)
  expect(rows).toHaveLength(101)
  expect(new Set(rows.map((row) => row.id)).size).toBe(101)
  expect(rows[0].offsetNs).toBe(0)
  expect(rows[99].offsetNs).toBe(0)
  expect(rows[100].offsetNs).toBeUndefined()
  expect(rows[100].label).toContain('票据 101')
  const markup = renderToStaticMarkup(createElement(EventTimeline, { events }))
  expect(markup).not.toContain('aria-label="事件详情"')
  expect(markup).not.toContain('查看完整事件')
})

it('选中结果后按序号对照源规则，保留两侧成员、种子次数及零值', () => {
  const source: ReplayEvent[] = [
    {
      sequence: 3,
      type: 'produce_match',
      round: 1,
      now: 100,
      result: 'match',
      seedAttempts: 2,
      matchTicketIds: ['11', '12'],
    },
  ]
  const events: ReplayEvent[] = [
    {
      sequence: '3',
      type: 'produce_match',
      round: '1',
      now: '100',
      offsetNs: '1000',
      status: 'matched',
      seedAttempts: 0,
      ticketIds: ['9', '10'],
    },
  ]
  const [row] = buildReplayTimelineRows(events, source, true)
  const details = textContent(
    renderToStaticMarkup(createElement(ReplayEventDetail, { row, result: true })),
  )
  expect(row.label).toContain('已成局')
  expect(details).toContain('尝试种子 0')
  expect(details).toContain('执行结果 已成局')
  expect(details).toContain('成员 9, 10')
  expect(details).toContain('源调用：已成局 · 尝试种子 2 · 成员 11, 12')
  expect(details).toContain('查看完整事件')
  expect(details).toContain('查看完整源事件')
})
