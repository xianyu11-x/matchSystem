import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import { ScenarioSettingsEditor } from './ScenarioSettingsEditor'
import type { JsonObject } from '../types'

type Element = { props: Record<string, unknown> }

function elements(value: unknown): Element[] {
  if (Array.isArray(value)) return value.flatMap(elements)
  if (!value || typeof value !== 'object' || !('props' in value)) return []
  const element = value as Element
  return [element, ...elements(element.props.children)]
}

it('场景设置自动生成端点，修改标识时保留自定义端点', () => {
  let draft: JsonObject = {
    physicalNodes: [
      { id: '1', endpoint: 'inproc://1', enabled: true },
      { id: '2', endpoint: 'custom://node', enabled: true },
    ],
    rules: [{ physicalNodeId: '1' }, { physicalNodeId: 2 }],
  }
  const render = () =>
    elements(ScenarioSettingsEditor({ draft, onChange: (value) => (draft = value) }))
  const changeId = (index: number, id: string) => {
    const field = render().filter((node) => node.props.label === '物理节点标识')[index]
    expect(field).toBeDefined()
    ;(field.props.onChange as (value: string) => void)(id)
  }
  const nodeAt = (index: number) => (draft.physicalNodes as JsonObject[])[index]

  const add = render().find((node) => node.props.children === '+ 添加物理节点')
  expect(add).toBeDefined()
  ;(add!.props.onClick as () => void)()
  expect(nodeAt(2)).toEqual({
    id: '3',
    endpoint: 'inproc://3',
    enabled: true,
    selector: 'round_robin',
  })

  const maxId = '18446744073709551615'
  changeId(0, maxId)
  expect(nodeAt(0)).toMatchObject({ id: maxId, endpoint: `inproc://${maxId}` })
  expect((draft.rules as JsonObject[])[0].physicalNodeId).toBe(maxId)
  changeId(1, '4')
  expect(nodeAt(1)).toMatchObject({ id: '4', endpoint: 'custom://node' })
  expect((draft.rules as JsonObject[])[1].physicalNodeId).toBe('4')
  changeId(2, '5')
  expect(nodeAt(2)).toMatchObject({ id: '5', endpoint: 'inproc://5' })
})

it('场景设置不显示端点输入框', () => {
  const draft: JsonObject = {
    physicalNodes: [{ id: '1', endpoint: 'custom://node', enabled: true }],
    rules: [],
  }
  const html = renderToStaticMarkup(
    createElement(ScenarioSettingsEditor, { draft, onChange: () => undefined }),
  )
  expect(html).toContain('物理节点标识')
  expect(html).not.toContain('节点端点')
  expect(html).not.toContain('custom://node')
})

it('修改标识时补齐旧草稿中缺失或为空的端点', () => {
  let draft: JsonObject = {
    physicalNodes: [
      { id: '1', enabled: true },
      { id: '2', endpoint: '', enabled: true },
    ],
    rules: [],
  }
  for (const [index, id] of [
    [0, '8'],
    [1, '9'],
  ] as const) {
    const nodes = elements(ScenarioSettingsEditor({ draft, onChange: (value) => (draft = value) }))
    const field = nodes.filter((node) => node.props.label === '物理节点标识')[index]
    ;(field.props.onChange as (value: string) => void)(id)
    expect((draft.physicalNodes as JsonObject[])[index].endpoint).toBe(`inproc://${id}`)
  }
})
