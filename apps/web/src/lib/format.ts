import type { FactSnapshot, Ticket, TypedAttributes } from '../types'

export const numberFormatter = new Intl.NumberFormat('zh-CN')
export const dateFormatter = new Intl.DateTimeFormat('zh-CN', {
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
})

export function formatNumber(value: number | undefined): string {
  return value === undefined ? '—' : numberFormatter.format(value)
}

export function formatDate(value: string | undefined): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? value : dateFormatter.format(date)
}

export function formatValues(values: string[] | number[] | number | undefined): string {
  if (values === undefined) return '—'
  if (Array.isArray(values)) return values.length === 0 ? '∅' : values.join(', ')
  return String(values)
}

export function flattenTypedAttributes(attributes: TypedAttributes): string {
  const fields = [
    ...Object.entries(attributes.strings).map(([key, values]) => `${key}: ${formatValues(values)}`),
    ...Object.entries(attributes.uint64s).map(([key, values]) => `${key}: ${formatValues(values)}`),
    ...Object.entries(attributes.int64).map(([key, value]) => `${key}: ${value}`),
  ]
  return fields.join(' · ') || '—'
}

export function flattenFacts(facts: FactSnapshot): string {
  return (
    Object.entries(facts)
      .map(([key, value]) => `${key}: ${formatValues(value)}`)
      .join(' · ') || '—'
  )
}

export function ticketSearchText(ticket: Ticket): string {
  return `${ticket.ticketId} ${flattenTypedAttributes(ticket.attributes)} ${flattenFacts(ticket.facts)}`.toLowerCase()
}
