import type { ReactNode } from 'react'

export function LoadingState({ label = '正在读取模拟器状态…' }: { label?: string }) {
  return (
    <div className="state-panel" role="status">
      <span className="spinner" aria-hidden="true" />
      <span>{label}</span>
    </div>
  )
}

export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const message = error instanceof Error ? error.message : '服务暂时不可用，请稍后重试。'
  return (
    <div className="state-panel state-error" role="alert">
      <span className="state-icon">!</span>
      <div>
        <strong>加载失败</strong>
        <p>{message}</p>
        {onRetry ? (
          <button className="button button-ghost" onClick={onRetry} type="button">
            重试
          </button>
        ) : null}
      </div>
    </div>
  )
}

export function EmptyState({
  title,
  detail,
  action,
}: {
  title: string
  detail?: string
  action?: ReactNode
}) {
  return (
    <div className="state-panel state-empty">
      <span className="empty-mark" aria-hidden="true">
        ◌
      </span>
      <strong>{title}</strong>
      {detail ? <p>{detail}</p> : null}
      {action}
    </div>
  )
}

export function MetricCard({
  label,
  value,
  detail,
  tone = 'neutral',
}: {
  label: string
  value: string | number
  detail?: string
  tone?: 'neutral' | 'positive' | 'warning'
}) {
  return (
    <article className={`metric-card metric-${tone}`}>
      <span className="eyebrow">{label}</span>
      <strong>{value}</strong>
      {detail ? <span className="metric-detail">{detail}</span> : null}
    </article>
  )
}

export function StatusPill({
  status,
  label,
}: {
  status:
    | 'healthy'
    | 'degraded'
    | 'stopped'
    | 'waiting'
    | 'matched'
    | 'expired'
    | 'rejected'
    | 'running'
    | 'completed'
    | 'failed'
  label?: string
}) {
  const labels: Record<typeof status, string> = {
    healthy: '健康',
    degraded: '有压力',
    stopped: '已停止',
    waiting: '等待中',
    matched: '已匹配',
    expired: '已过期',
    rejected: '已拒绝',
    running: '运行中',
    completed: '已完成',
    failed: '失败',
  }
  return <span className={`status-pill status-${status}`}>{label ?? labels[status]}</span>
}

export function PageHeader({
  eyebrow,
  title,
  description,
  actions,
}: {
  eyebrow: string
  title: string
  description: string
  actions?: ReactNode
}) {
  return (
    <header className="page-header">
      <div>
        <span className="eyebrow">{eyebrow}</span>
        <h1>{title}</h1>
        <p>{description}</p>
      </div>
      {actions ? <div className="page-actions">{actions}</div> : null}
    </header>
  )
}

export function SectionTitle({
  title,
  detail,
  action,
}: {
  title: string
  detail?: string
  action?: ReactNode
}) {
  return (
    <div className="section-title">
      <div>
        <h2>{title}</h2>
        {detail ? <span>{detail}</span> : null}
      </div>
      {action}
    </div>
  )
}
