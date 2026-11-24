import { describe, expect, it, vi, afterEach } from 'vitest'
import { api } from './api'
import { parseReplayJson, readReplayTimeline, replayOffset, replayRows } from './replay'

afterEach(() => vi.unstubAllGlobals())

describe('时间线精度', () => {
  it('保留不安全整数的原文，同时正确处理字符串内的数字和转义', () => {
    const raw = String.raw`{"large":18446744073709551615,"negative":-9223372036854775808,"small":7,"text":"quote \" 18446744073709551615","values":[9007199254740991,9007199254740992]}`
    expect(parseReplayJson(raw)).toEqual({
      large: '18446744073709551615',
      negative: '-9223372036854775808',
      small: 7,
      text: 'quote " 18446744073709551615',
      values: [9007199254740991, '9007199254740992'],
    })
    expect(replayOffset('9007199254740992')).toBe('+9007199254.740992 ms')
    expect(replayOffset(9007199254740992)).toBe('—')
    expect(replayOffset('-1234567')).toBe('-1.234567 ms')
  })

  it('保留调用之间的票据事件顺序，并分别编号每轮调用', () => {
    const timeline = readReplayTimeline(
      '{"schemaVersion":"match-timeline/v1","instanceId":"node","events":[{"sequence":1,"type":"round_begin","round":1},{"sequence":2,"type":"produce_match","round":1},{"sequence":3,"type":"ticket_withdraw"},{"sequence":4,"type":"produce_match","round":1},{"sequence":5,"type":"round_begin","round":2},{"sequence":6,"type":"produce_match","round":2}]}',
    )
    expect(replayRows(timeline.events).map(({ event, call }) => [event.sequence, call])).toEqual([
      [1, undefined],
      [2, 1],
      [3, undefined],
      [4, 2],
      [5, undefined],
      [6, 1],
    ])
    expect(() => readReplayTimeline('{"schemaVersion":"unknown"}')).toThrow('时间线格式无效')
  })

  it('提取和回放原样传输时间线而不经 Number 转换', async () => {
    const timeline =
      '{"schemaVersion":"match-timeline/v1","instanceId":"node","events":[{"sequence":18446744073709551615,"type":"ticket_add"}]}'
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ instances: [], timelineJson: timeline }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
      .mockResolvedValueOnce(
        new Response(
          '{"instanceId":"node","targetRuleId":"3","status":"completed","rounds":0,"calls":0,"matches":0,"seedAttempts":0,"events":[{"sequence":18446744073709551615,"type":"ticket_add","status":"applied"}]}',
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        ),
      )
    vi.stubGlobal('fetch', fetchMock)
    const extracted = await api.extractTimeline({ instanceId: 'node', logs: '原始日志' })
    expect(extracted.timelineJson).toBe(timeline)
    const result = await api.replayTimeline({
      timelineJson: extracted.timelineJson!,
      targetRuleId: '3',
    })
    expect(result.events[0].sequence).toBe('18446744073709551615')
    expect(JSON.parse(fetchMock.mock.calls[1][1].body).timelineJson).toBe(timeline)
  })
})
