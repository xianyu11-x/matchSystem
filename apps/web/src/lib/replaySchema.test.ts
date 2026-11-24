import Ajv2020 from 'ajv/dist/2020'
import { expect, it } from 'vitest'
import eventSchema from '../../../../api/schema/match-timeline-event/v1.schema.json'
import timelineSchema from '../../../../api/schema/match-timeline/v1.schema.json'

it('时间线 Schema 引用完整事件并约束 Object Fact 快照', () => {
  const ajv = new Ajv2020({ strict: false })
  ajv.addFormat('date-time', true)
  ajv.addSchema(eventSchema)
  const validate = ajv.compile(timelineSchema)
  const instanceId = '0123456789abcdef0123456789abcdef'
  const common = {
    version: 'match-timeline/v1',
    logicalNode: { rule: { ruleId: '1' }, placementId: '1' },
    instanceId,
    baseTime: '2026-11-24T00:00:00Z',
    offsetNs: 0,
    round: 0,
    now: 0,
    seedAttempts: 0,
    initialEmpty: true,
  }
  const document = {
    schemaVersion: 'match-timeline/v1',
    instanceId,
    events: [
      {
        ...common,
        sequence: 1,
        type: 'stream_start',
        ruleJson: {},
        providerDescriptors: { Tick: null, Object: null, Match: null },
      },
      {
        ...common,
        sequence: 2,
        type: 'ticket_add',
        ticket: { TicketID: 1, CreatedAt: 0, StringLists: {}, Uint64Lists: {}, Int64Values: {} },
      },
    ],
    facts: [
      {
        instanceId,
        afterSequence: 2,
        order: 1,
        ticketId: 1,
        addSequence: 2,
        facts: { strings: { tags: null }, int64s: { score: -1 } },
      },
    ],
  }
  expect(validate(document), JSON.stringify(validate.errors)).toBe(true)
  const noTicket = structuredClone(document)
  Reflect.deleteProperty(noTicket.events[1], 'ticket')
  expect(validate(noTicket)).toBe(false)
  const noOrder = structuredClone(document)
  Reflect.deleteProperty(noOrder.facts[0], 'order')
  expect(validate(noOrder)).toBe(false)
})
