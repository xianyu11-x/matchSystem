import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  ErrorState,
  EmptyState,
  LoadingState,
  MetricCard,
  PageHeader,
  SectionTitle,
  StatusPill,
} from '../components/States'
import { RunMetricsChart } from '../components/Chart'
import { MatchDetailsDrawer } from '../components/MatchDetailsDrawer'
import { useMatches, useScenario, useStartRound, useTopology } from '../lib/queries'
import { flattenFacts, formatDate, formatNumber } from '../lib/format'

function TopologyOverview({
  nodes,
}: {
  nodes: NonNullable<ReturnType<typeof useTopology>['data']>['nodes']
}) {
  if (nodes.length === 0) {
    return (
      <EmptyState title="暂无 PhysicalNode" detail="启动模拟器场景后，拓扑节点会出现在这里。" />
    )
  }
  return (
    <div className="topology-overview">
      <div className="router-node">
        <span className="node-glyph">R</span>
        <div>
          <strong>Router</strong>
          <span>路由入口</span>
        </div>
      </div>
      <div className="topology-links" aria-hidden="true">
        {nodes.map((node) => (
          <span key={node.id} style={{ opacity: Math.max(0.3, node.load) }} />
        ))}
      </div>
      <div className="physical-nodes">
        {nodes.map((node) => (
          <div className="physical-node" key={node.id}>
            <div className="node-heading">
              <span className={`node-dot node-dot-${node.state}`} />
              <strong>{node.name}</strong>
              <StatusPill status={node.state} />
            </div>
            <span className="node-rule">
              {node.ruleKey} · {node.placementId}
            </span>
            <div className="node-stat-row">
              <span>{formatNumber(node.ticketCount)} Tickets</span>
              <span>{Math.round(node.load * 100)}% 负载</span>
            </div>
            <div className="load-track">
              <span style={{ width: `${node.load * 100}%` }} />
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

export function Dashboard() {
  const scenarioQuery = useScenario()
  const topologyQuery = useTopology()
  const matchesQuery = useMatches()
  const startRound = useStartRound()
  // The public API uses Unix milliseconds for both Ticket.createdAt and the
  // round clock. Keep the editable value in that same unit.
  const [roundNow, setRoundNow] = useState(String(Date.now()))
  const [matchLimit, setMatchLimit] = useState('100')
  const [selectedMatchId, setSelectedMatchId] = useState<string>()

  useEffect(() => {
    if (scenarioQuery.isFetching || scenarioQuery.isError) setSelectedMatchId(undefined)
  }, [scenarioQuery.isError, scenarioQuery.isFetching])

  useEffect(() => {
    if (!selectedMatchId) return
    if (
      matchesQuery.isError ||
      (!matchesQuery.isFetching && !matchesQuery.data) ||
      (matchesQuery.data &&
        !matchesQuery.data.items.some((match) => match.matchId === selectedMatchId))
    )
      setSelectedMatchId(undefined)
  }, [matchesQuery.data, matchesQuery.isError, matchesQuery.isFetching, selectedMatchId])

  const nodeCount = topologyQuery.data?.nodes.length ?? 0
  const ticketCount =
    topologyQuery.data?.nodes.reduce((sum, node) => sum + node.ticketCount, 0) ?? 0
  const matchCount = matchesQuery.data?.total ?? matchesQuery.data?.items.length ?? 0
  const health = useMemo(() => {
    const nodes = topologyQuery.data?.nodes ?? []
    return nodes.length > 0 && nodes.every((node) => node.state === 'healthy')
      ? 'healthy'
      : 'degraded'
  }, [topologyQuery.data?.nodes])

  const runRound = () => {
    const parsedNow = Number(roundNow)
    const parsedLimit = Number(matchLimit)
    if (
      !Number.isInteger(parsedNow) ||
      parsedNow < 0 ||
      !Number.isInteger(parsedLimit) ||
      parsedLimit < 1
    )
      return
    startRound.mutate({ now: parsedNow, matchLimit: parsedLimit })
  }

  return (
    <div className="page-stack">
      <PageHeader
        eyebrow="SIMULATOR / OVERVIEW"
        title="运行总览"
        description="观察 Router 到 PhysicalNode 的流量、当前轮次和最近的匹配结果。"
        actions={
          <span className="live-indicator">
            <i /> LIVE · SSE 事件
          </span>
        }
      />

      {scenarioQuery.isLoading ? <LoadingState label="正在读取场景…" /> : null}
      {scenarioQuery.isError ? (
        <ErrorState error={scenarioQuery.error} onRetry={() => scenarioQuery.refetch()} />
      ) : null}
      {scenarioQuery.data ? (
        <section className="scenario-banner">
          <div>
            <span className="eyebrow">ACTIVE SCENARIO</span>
            <strong>{scenarioQuery.data.name}</strong>
            <span>
              {scenarioQuery.data.scenarioId} · 更新于 {formatDate(scenarioQuery.data.updatedAt)}
            </span>
          </div>
          <Link className="button button-ghost" to="/rules">
            查看规则 →
          </Link>
        </section>
      ) : null}

      <section className="metric-grid">
        <MetricCard
          label="PhysicalNode"
          value={formatNumber(nodeCount)}
          detail="多节点路由拓扑"
          tone={health === 'healthy' ? 'positive' : 'warning'}
        />
        <MetricCard
          label="等待中 Tickets"
          value={formatNumber(ticketCount)}
          detail="跨 LogicalNode 隔离统计"
        />
        <MetricCard
          label="最近 Matches"
          value={formatNumber(matchCount)}
          detail="服务端保留的历史结果"
          tone="positive"
        />
        <MetricCard
          label="运行健康度"
          value={health === 'healthy' ? '稳定' : '关注'}
          detail="基于节点状态汇总"
          tone={health === 'healthy' ? 'positive' : 'warning'}
        />
      </section>

      <section className="content-grid dashboard-grid">
        <div className="panel panel-wide">
          <SectionTitle
            title="拓扑 / Routing"
            detail={
              topologyQuery.data ? `更新于 ${formatDate(topologyQuery.data.updatedAt)}` : undefined
            }
          />
          {topologyQuery.isLoading ? <LoadingState label="正在读取拓扑…" /> : null}
          {topologyQuery.isError ? (
            <ErrorState error={topologyQuery.error} onRetry={() => topologyQuery.refetch()} />
          ) : null}
          {topologyQuery.data ? <TopologyOverview nodes={topologyQuery.data.nodes} /> : null}
        </div>

        <div className="panel run-panel">
          <SectionTitle title="运行下一轮" detail="服务端 deterministic seed" />
          <label className="field-label" htmlFor="round-now">
            模拟时间（Unix ms）
          </label>
          <input
            id="round-now"
            className="text-input"
            inputMode="numeric"
            value={roundNow}
            onChange={(event) => setRoundNow(event.target.value)}
          />
          <label className="field-label" htmlFor="match-limit">
            Match 上限
          </label>
          <input
            id="match-limit"
            className="text-input"
            inputMode="numeric"
            value={matchLimit}
            onChange={(event) => setMatchLimit(event.target.value)}
          />
          <button
            className="button button-primary button-block"
            type="button"
            onClick={runRound}
            disabled={startRound.isPending}
          >
            {startRound.isPending ? '运行中…' : '开始 Match Round'}
          </button>
          {startRound.isError ? (
            <p className="form-error">
              {startRound.error instanceof Error ? startRound.error.message : '运行失败'}
            </p>
          ) : null}
          {startRound.data ? (
            <div className="success-note">
              Round {startRound.data.round.roundId} 已完成 · {startRound.data.round.matchCount}{' '}
              Matches
            </div>
          ) : null}
        </div>

        <div className="panel panel-wide chart-panel">
          <SectionTitle title="轮次趋势" detail="服务端聚合 · 最近 30 分钟" />
          <RunMetricsChart />
        </div>

        <div className="panel matches-panel">
          <SectionTitle
            title="最近 Matches"
            detail={
              matchesQuery.data
                ? `${formatNumber(
                    matchesQuery.data.total ?? matchesQuery.data.items.length,
                  )} 条 immutable records`
                : 'immutable match events'
            }
            action={
              <Link className="text-link" to="/tickets">
                查看 Tickets →
              </Link>
            }
          />
          {matchesQuery.isLoading ? <LoadingState label="正在读取 Matches…" /> : null}
          {matchesQuery.isError ? (
            <ErrorState error={matchesQuery.error} onRetry={() => matchesQuery.refetch()} />
          ) : null}
          {matchesQuery.data?.items.length === 0 ? (
            <EmptyState title="还没有 Match" detail="运行一轮后，匹配结果会出现在这里。" />
          ) : null}
          {matchesQuery.data && matchesQuery.data.items.length > 0 ? (
            <div className="match-list">
              {matchesQuery.data.items.map((match) => (
                <button
                  className="match-row match-row-button"
                  key={match.matchId}
                  type="button"
                  onClick={() => setSelectedMatchId(match.matchId)}
                  aria-label={`打开 ${match.matchId} 详情`}
                >
                  <div>
                    <strong>{match.matchId}</strong>
                    <span>
                      {match.ruleKey} · {match.placementId}
                    </span>
                    <span title={flattenFacts(match.facts ?? {})}>
                      Match Facts · {flattenFacts(match.facts ?? {})}
                    </span>
                  </div>
                  <div className="match-meta">
                    <strong>{match.memberCount} 人</strong>
                    <span>{formatDate(match.createdAt)}</span>
                    <span className="match-open-hint">查看详情 →</span>
                  </div>
                </button>
              ))}
            </div>
          ) : null}
        </div>
      </section>
      <MatchDetailsDrawer
        matchId={selectedMatchId}
        onClose={() => setSelectedMatchId(undefined)}
      />
    </div>
  )
}
