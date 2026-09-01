/** Return true for the browser keys used to remove a selected graph node. */
export function isRuleGraphDeleteKey(event: Pick<KeyboardEvent, 'key' | 'code'>): boolean {
  return event.key === 'Delete' || event.key === 'Del' || event.code === 'Delete'
}

/**
 * Interactive controls own Delete. A graph-level shortcut must never remove a
 * node while a button, link, form control, or rich-text field has focus,
 * including when the event target is a child of a contenteditable.
 */
export function isInteractiveRuleGraphTarget(target: EventTarget | null): boolean {
  if (!target || typeof target !== 'object') return false
  const element = target as {
    tagName?: unknown
    isContentEditable?: unknown
    closest?: (selector: string) => unknown
  }
  const tagName = typeof element.tagName === 'string' ? element.tagName.toUpperCase() : ''
  if (
    tagName === 'INPUT' ||
    tagName === 'TEXTAREA' ||
    tagName === 'SELECT' ||
    tagName === 'BUTTON' ||
    tagName === 'A' ||
    tagName === 'OPTION' ||
    tagName === 'SUMMARY'
  )
    return true
  if (element.isContentEditable === true) return true
  return (
    element.closest?.(
      'button,a,input,textarea,select,option,summary,[contenteditable="true"],[role="button"],[role="link"]',
    ) != null
  )
}

/** Backward-compatible name for callers that only care about form editing. */
export const isEditableRuleGraphTarget = isInteractiveRuleGraphTarget

export interface RuleGraphCanvasContainer {
  contains(target: EventTarget | null): boolean
}

/** Delete is valid only when the event or active focus belongs to the canvas. */
export function isRuleGraphCanvasContext(
  target: EventTarget | null,
  activeElement: EventTarget | null,
  canvas: RuleGraphCanvasContainer | null,
): boolean {
  if (!canvas) return false
  const contains = (value: EventTarget | null) => {
    try {
      return canvas.contains(value)
    } catch {
      // HTML Element#contains rejects non-Node EventTargets such as Window.
      return false
    }
  }
  return contains(target) || contains(activeElement)
}
