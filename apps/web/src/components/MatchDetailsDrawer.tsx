import { useCallback, useEffect, useRef } from 'react'
import { ApiError } from '../lib/api'
import { useMatch } from '../lib/queries'
import {
  flattenFacts,
  flattenTypedAttributes,
  formatDate,
  formatNumber,
  formatValues,
} from '../lib/format'
import type { MatchRecord, Ticket } from '../types'
import { EmptyState, ErrorState, LoadingState, StatusPill } from './States'

function MatchSummary({ match }: { match: MatchRecord }) {
  const round =
    match.round === undefined
      ? match.roundId || '—'
      : `${match.roundId || `round-${match.round}`} (#${formatNumber(match.round)})`
  return (
    <dl className="match-detail-summary">
      <div>
        <dt>Match ID</dt>
        <dd>
          <code>{match.matchId}</code>
        </dd>
      </div>
      <div>
        <dt>Created</dt>
        <dd>{formatDate(match.createdAt)}</dd>
      </div>
      <div>
        <dt>Round</dt>
        <dd>{round}</dd>
      </div>
      <div>
        <dt>PhysicalNode</dt>
        <dd>{match.physicalNodeId ?? '—'}</dd>
      </div>
      <div>
        <dt>Rule</dt>
        <dd>{match.ruleKey}</dd>
      </div>
      <div>
        <dt>LogicalNode</dt>
        <dd>{`${match.ruleKey} / ${match.placementId}`}</dd>
      </div>
      <div>
        <dt>Placement</dt>
        <dd>{match.placementId}</dd>
      </div>
      <div>
        <dt>Members</dt>
        <dd>{formatNumber(match.members?.length || match.memberCount)}</dd>
      </div>
    </dl>
  )
}

function MatchFacts({ match }: { match: MatchRecord }) {
  const facts = Object.entries(match.facts ?? {})
  return (
    <section className="match-detail-section">
      <div className="match-detail-section-heading">
        <h3>Match Facts</h3>
        <span>immutable snapshot</span>
      </div>
      {facts.length === 0 ? (
        <span className="muted">该 Match 没有记录 Match Facts。</span>
      ) : (
        <dl className="match-facts-list">
          {facts.map(([name, value]) => (
            <div key={name}>
              <dt>{name}</dt>
              <dd>
                <code>{formatValues(value)}</code>
              </dd>
            </div>
          ))}
        </dl>
      )}
    </section>
  )
}

function MemberValue({ label, value }: { label: string; value: string }) {
  return (
    <div className="match-member-value">
      <span>{label}</span>
      <code title={value}>{value}</code>
    </div>
  )
}

function MatchMember({ ticket }: { ticket: Ticket }) {
  const owner = ticket.routeDecision?.owner
  const route = ticket.routeDecision
  const ownerText = owner ? `${owner.physicalNodeId} · ${owner.logicalNodeKey}` : '—'
  const routeText = route
    ? [
        route.status,
        route.decisionId ? `decision ${route.decisionId}` : '',
        route.endpoint ?? '',
      ]
        .filter(Boolean)
        .join(' · ')
    : '—'
  return (
    <article className="match-member-card">
      <div className="match-member-heading">
        <div>
          <strong>{ticket.ticketId}</strong>
          <span>created {formatDate(ticket.createdAt)}</span>
        </div>
        <StatusPill status={ticket.status} />
      </div>
      <div className="match-member-grid">
        <MemberValue label="Owner" value={ownerText} />
        <MemberValue label="Route decision" value={routeText} />
        <MemberValue label="Attributes" value={flattenTypedAttributes(ticket.attributes)} />
        <MemberValue label="Object Facts" value={flattenFacts(ticket.facts)} />
      </div>
    </article>
  )
}

function MatchMembers({ match }: { match: MatchRecord }) {
  const members = match.members ?? []
  return (
    <section className="match-detail-section">
      <div className="match-detail-section-heading">
        <h3>成员 / Members</h3>
        <span>{members.length || match.memberCount} tickets</span>
      </div>
      {members.length === 0 ? (
        <EmptyState
          title="成员快照不可用"
          detail={
            match.ticketIds.length > 0
              ? `该 Match 仅保留了 ${match.ticketIds.length} 个 Ticket ID，服务端未返回成员明细。`
              : '服务端没有返回成员明细。'
          }
        />
      ) : (
        <div className="match-member-list">
          {members.map((ticket) => (
            <MatchMember key={ticket.ticketId} ticket={ticket} />
          ))}
        </div>
      )}
    </section>
  )
}

function MatchContent({ match }: { match: MatchRecord }) {
  return (
    <>
      <MatchSummary match={match} />
      <MatchFacts match={match} />
      <MatchMembers match={match} />
    </>
  )
}

const focusableSelector = [
  'a[href]',
  'area[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

function focusableElements(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>(focusableSelector)).filter(
    (element) => !element.hidden && element.getAttribute('aria-hidden') !== 'true',
  )
}

export function MatchDetailsDrawer({
  matchId,
  onClose,
}: {
  matchId?: string
  onClose: () => void
}) {
  const query = useMatch(matchId)
  const drawerRef = useRef<HTMLElement>(null)
  const closeButtonRef = useRef<HTMLButtonElement>(null)
  const restoreFocusRef = useRef<HTMLElement | null>(null)
  const wasOpenRef = useRef(false)
  const onCloseRef = useRef(onClose)

  useEffect(() => {
    onCloseRef.current = onClose
  }, [onClose])

  const restoreFocus = useCallback(() => {
    const element = restoreFocusRef.current
    restoreFocusRef.current = null
    if (element?.isConnected) element.focus()
  }, [])

  useEffect(() => {
    if (!matchId) {
      if (wasOpenRef.current) restoreFocus()
      wasOpenRef.current = false
      return
    }
    if (!wasOpenRef.current) {
      const activeElement = document.activeElement
      restoreFocusRef.current = activeElement instanceof HTMLElement ? activeElement : null
      wasOpenRef.current = true
    }
    closeButtonRef.current?.focus()
    if (!closeButtonRef.current) drawerRef.current?.focus()
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        onCloseRef.current()
        return
      }
      if (event.key !== 'Tab' || !drawerRef.current) return
      const focusable = focusableElements(drawerRef.current)
      if (focusable.length === 0) {
        event.preventDefault()
        drawerRef.current.focus()
        return
      }
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      const current = document.activeElement
      if (event.shiftKey && (current === first || !drawerRef.current.contains(current))) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && (current === last || !drawerRef.current.contains(current))) {
        event.preventDefault()
        first.focus()
      }
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [matchId, restoreFocus])

  if (!matchId) return null
  const notFound = query.error instanceof ApiError && query.error.status === 404
  return (
    <div className="match-drawer-layer">
      <button
        className="match-drawer-scrim"
        type="button"
        aria-label="关闭 Match 详情"
        onClick={onClose}
      />
      <aside
        ref={drawerRef}
        className="match-drawer"
        role="dialog"
        aria-modal="true"
        aria-labelledby="match-drawer-title"
        tabIndex={-1}
      >
        <header className="match-drawer-header">
          <div>
            <span className="eyebrow">MATCH DETAIL</span>
            <h2 id="match-drawer-title">{matchId}</h2>
          </div>
          <button
            ref={closeButtonRef}
            className="icon-button"
            type="button"
            aria-label="关闭详情"
            onClick={onClose}
          >
            ×
          </button>
        </header>
        <div className="match-drawer-body">
          {query.isLoading ? <LoadingState label="正在读取 Match 成员快照…" /> : null}
          {query.isError && notFound ? (
            <EmptyState
              title="Match 不存在或已被淘汰"
              detail={
                query.error instanceof Error ? query.error.message : '服务端没有保留该 Match。'
              }
              action={
                <button className="button button-ghost" type="button" onClick={onClose}>
                  关闭详情
                </button>
              }
            />
          ) : null}
          {query.isError && !notFound ? (
            <ErrorState error={query.error} onRetry={() => void query.refetch()} />
          ) : null}
          {query.isSuccess ? <MatchContent match={query.data} /> : null}
        </div>
      </aside>
    </div>
  )
}
