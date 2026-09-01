import { describe, expect, it } from 'vitest'
import {
  isEditableRuleGraphTarget,
  isInteractiveRuleGraphTarget,
  isRuleGraphCanvasContext,
  isRuleGraphDeleteKey,
} from './ruleGraphKeyboard'

describe('Rule Graph delete shortcut guards', () => {
  const target = (value: object) => value as unknown as EventTarget

  it('recognizes Delete and the legacy Del spelling', () => {
    expect(isRuleGraphDeleteKey({ key: 'Delete', code: 'Delete' })).toBe(true)
    expect(isRuleGraphDeleteKey({ key: 'Del', code: '' })).toBe(true)
    expect(isRuleGraphDeleteKey({ key: 'Backspace', code: 'Backspace' })).toBe(false)
  })

  it('does not claim editable controls or contenteditable descendants', () => {
    for (const tagName of ['INPUT', 'TEXTAREA', 'SELECT'])
      expect(isEditableRuleGraphTarget(target({ tagName }))).toBe(true)
    expect(isEditableRuleGraphTarget(target({ tagName: 'DIV', isContentEditable: true }))).toBe(true)
    expect(
      isEditableRuleGraphTarget(target({
        tagName: 'SPAN',
        closest: (selector: string) =>
          selector.includes('[contenteditable="true"]') ? {} : null,
      })),
    ).toBe(true)
    expect(isEditableRuleGraphTarget(target({ tagName: 'DIV' }))).toBe(false)
  })

  it('does not delete from palette buttons or links', () => {
    expect(isInteractiveRuleGraphTarget(target({ tagName: 'BUTTON' }))).toBe(true)
    expect(isInteractiveRuleGraphTarget(target({ tagName: 'A' }))).toBe(true)
    expect(
      isInteractiveRuleGraphTarget(
        target({ tagName: 'SPAN', closest: () => ({}) }),
      ),
    ).toBe(true)
  })

  it('requires the Delete event or focus to belong to the canvas', () => {
    const canvas = {
      contains: (value: EventTarget | null) => value === canvasTarget || value === canvasChild,
    }
    const canvasTarget = target({ tagName: 'DIV' })
    const canvasChild = target({ tagName: 'DIV' })
    const paletteButton = target({ tagName: 'BUTTON' })
    expect(isRuleGraphCanvasContext(canvasChild, paletteButton, canvas)).toBe(true)
    expect(isRuleGraphCanvasContext(paletteButton, paletteButton, canvas)).toBe(false)
    expect(isRuleGraphCanvasContext(paletteButton, canvasTarget, canvas)).toBe(true)
  })
})
