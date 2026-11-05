import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { formatApiError, trafficRequest, type TrafficConfig } from '../lib/api'
import type { BatchGeneratorSpec } from '../types'

const defaultTrafficConfig: TrafficConfig = {
  mode: 'realtime',
  distribution: 'constant',
  rate: 10,
  burstSize: 50,
  burstIntervalMs: 1000,
  matchIntervalMs: 1000,
  maxMatches: 10,
  seed: 42,
  steps: 100,
  stepMs: 1000,
  startTimeMs: Date.now(),
}

const fixedFieldLabels = {
  steps: '虚拟步数',
  stepMs: '步长（毫秒）',
  startTimeMs: '虚拟起始时间（Unix ms）',
} as const

function formatVirtualTime(value: number | undefined) {
  if (value === undefined || !Number.isSafeInteger(value) || value <= 0) return '未设置'
  const date = new Date(value)
  const display = Number.isNaN(date.getTime())
    ? '日期超出浏览器范围'
    : date.toLocaleString('zh-CN', { hour12: false })
  return `${value}（${display}）`
}

export function fixedVirtualEnd(
  startTimeMs: number | undefined,
  steps: number | undefined,
  stepMs: number | undefined,
): number | undefined {
  if (
    typeof startTimeMs !== 'number' ||
    typeof steps !== 'number' ||
    typeof stepMs !== 'number' ||
    !Number.isSafeInteger(startTimeMs) ||
    !Number.isSafeInteger(steps) ||
    !Number.isSafeInteger(stepMs) ||
    startTimeMs < 0 ||
    steps < 1 ||
    stepMs < 1
  )
    return undefined
  const spanSteps = steps - 1
  if (spanSteps > Math.floor(Number.MAX_SAFE_INTEGER / stepMs)) return undefined
  const span = spanSteps * stepMs
  if (startTimeMs > Number.MAX_SAFE_INTEGER - span) return undefined
  return startTimeMs + span
}

export function TrafficControl({
  buildSpec,
  onRunningChange,
  onNextId,
}: {
  buildSpec: () => BatchGeneratorSpec | undefined
  onRunningChange?: (running: boolean) => void
  onNextId?: (id: number) => void
}) {
  const cache = useQueryClient()
  const [config, setConfig] = useState<TrafficConfig>(defaultTrafficConfig)
  const status = useQuery({
    queryKey: ['traffic'],
    queryFn: () => trafficRequest('GET'),
    refetchInterval: 1000,
    retry: false,
  })
  const action = useMutation({
    mutationFn: async (method: 'POST' | 'DELETE') => {
      const spec = method === 'POST' ? buildSpec() : undefined
      if (method === 'POST' && !spec) throw new Error('请先选择规则并配置生成器')
      if (method === 'POST' && !Number.isSafeInteger(config.seed))
        throw new Error('到达随机种子必须是安全整数')
      if (method === 'POST' && config.mode === 'fixed') {
        const { steps, stepMs, startTimeMs } = config
        if (fixedVirtualEnd(startTimeMs, steps, stepMs) === undefined)
          throw new Error('固定步长虚拟时间范围必须保持为安全整数')
        if (
          typeof steps !== 'number' ||
          !Number.isSafeInteger(steps) ||
          steps < 1 ||
          steps > 10000000
        )
          throw new Error('固定步长运行的虚拟步数必须是 1 到 10000000 的安全整数')
        if (
          typeof stepMs !== 'number' ||
          !Number.isSafeInteger(stepMs) ||
          stepMs < 1 ||
          stepMs > 86400000
        )
          throw new Error('固定步长运行的步长必须是 1 到 86400000 毫秒的安全整数')
        if (
          typeof startTimeMs !== 'number' ||
          !Number.isSafeInteger(startTimeMs) ||
          startTimeMs < 0 ||
          startTimeMs > Number.MAX_SAFE_INTEGER
        )
          throw new Error('虚拟起始时间必须是安全整数')
      }
      return trafficRequest(method, config, spec)
    },
    onSuccess: (result) => {
      cache.setQueryData(['traffic'], result)
      void cache.invalidateQueries({ queryKey: ['tickets'] })
    },
  })
  const running = status.data?.state === 'running'
  useEffect(() => {
    onRunningChange?.(running)
  }, [running, onRunningChange])
  useEffect(() => {
    if (running && status.data?.config)
      setConfig({ ...defaultTrafficConfig, ...status.data.config })
  }, [running, status.data?.config])
  const fixedRun = config.mode === 'fixed'
  const fixedSteps = config.steps
  const fixedStepMs = config.stepMs
  const fixedStartTimeMs = config.startTimeMs
  const fixedEnd = fixedRun ? fixedVirtualEnd(fixedStartTimeMs, fixedSteps, fixedStepMs) : undefined
  return (
    <section className="traffic-section">
      <h3>持续流量与定时匹配</h3>
      <p className="muted">
        实时模式按墙上时钟注入；固定步长模式逐步推进虚拟时间，Python RPC 的耗时不会改变模拟时间。
      </p>
      <form
        onSubmit={(event) => {
          event.preventDefault()
          action.mutate('POST')
        }}
      >
        <fieldset disabled={running || action.isPending} className="form-grid composer-fields">
          <legend className="sr-only">流量与匹配节奏</legend>
          <label className="field-label">
            模拟时钟
            <select
              className="text-input"
              value={config.mode ?? 'realtime'}
              onChange={(e) =>
                setConfig({ ...config, mode: e.target.value as TrafficConfig['mode'] })
              }
            >
              <option value="realtime">实时运行</option>
              <option value="fixed">固定步长运行</option>
            </select>
          </label>
          <label className="field-label">
            到达分布
            <select
              className="text-input"
              value={config.distribution}
              onChange={(e) =>
                setConfig({
                  ...config,
                  distribution: e.target.value as TrafficConfig['distribution'],
                })
              }
            >
              <option value="constant">恒定</option>
              <option value="poisson">Poisson（泊松）</option>
              <option value="burst">周期突发</option>
            </select>
          </label>
          {(
            [
              'rate',
              'burstSize',
              'burstIntervalMs',
              'matchIntervalMs',
              'maxMatches',
              'seed',
            ] as const
          )
            .filter((key) =>
              config.distribution === 'burst'
                ? key !== 'rate'
                : key !== 'burstSize' && key !== 'burstIntervalMs',
            )
            .map((key) => (
              <label className="field-label" key={key}>
                {
                  {
                    rate: '到达速率（条/秒）',
                    burstSize: '每次突发条数',
                    burstIntervalMs: '突发间隔（毫秒）',
                    matchIntervalMs: '匹配间隔（毫秒）',
                    maxMatches: '每次最多产出 Match（局）',
                    seed: '到达随机种子',
                  }[key]
                }
                <input
                  className="text-input"
                  type="number"
                  required
                  min={
                    key === 'seed'
                      ? undefined
                      : key === 'rate'
                        ? 0.001
                        : key.endsWith('Ms')
                          ? fixedRun && key === 'matchIntervalMs'
                            ? 1
                            : 100
                          : 1
                  }
                  max={
                    key === 'seed' ? Number.MAX_SAFE_INTEGER : key.endsWith('Ms') ? 86400000 : 10000
                  }
                  step={key === 'rate' ? 'any' : 1}
                  value={config[key]}
                  onChange={(e) => setConfig({ ...config, [key]: Number(e.target.value) })}
                />
              </label>
            ))}
          {fixedRun &&
            (['steps', 'stepMs', 'startTimeMs'] as const).map((key) => (
              <label className="field-label" key={key}>
                {fixedFieldLabels[key]}
                <input
                  className="text-input"
                  type="number"
                  required
                  min={key === 'startTimeMs' ? 0 : 1}
                  max={key === 'steps' ? 10000000 : Number.MAX_SAFE_INTEGER}
                  step={1}
                  value={config[key] ?? ''}
                  onChange={(e) => setConfig({ ...config, [key]: Number(e.target.value) })}
                />
              </label>
            ))}
        </fieldset>
        <button
          className="button button-primary"
          disabled={running || action.isPending || !status.data}
          type="submit"
        >
          {fixedRun ? '开始固定步长运行' : '开始实时注入'}
        </button>
        <button
          className="button button-secondary"
          disabled={!running || action.isPending}
          type="button"
          onClick={() => action.mutate('DELETE')}
        >
          停止
        </button>
      </form>
      {status.data && (
        <div className="traffic-status">
          <p role="status">
            状态：
            {(
              {
                idle: '未启动',
                running: '运行中',
                stopped: '已停止',
                completed: '已完成',
                failed: '失败',
              } as Record<string, string>
            )[status.data.state] ?? status.data.state}{' '}
            · 已注入 {status.data.injected} · 已产出 {status.data.produced} · 匹配轮数{' '}
            {status.data.rounds} · 下一 TicketID {status.data.nextTicketId} · 调度延迟{' '}
            {status.data.lagMs} ms
          </p>
          {status.data.config.mode === 'fixed' && (
            <p role="status">
              固定进度 {status.data.step ?? 0}/
              {status.data.totalSteps ?? status.data.config.steps ?? 0} · 虚拟时间{' '}
              {formatVirtualTime(status.data.virtualNow)} · 墙上耗时{' '}
              {status.data.wallElapsedMs ?? 0} ms
            </p>
          )}
        </div>
      )}
      {fixedRun && !running && (
        <p className="muted">
          虚拟范围：{formatVirtualTime(config.startTimeMs)} → {formatVirtualTime(fixedEnd)}；共{' '}
          {config.steps ?? 0} 步，每步 {config.stepMs ?? 0} ms。
        </p>
      )}
      {!running && status.data && status.data.nextTicketId > 0 && onNextId && (
        <button
          className="button button-ghost"
          type="button"
          onClick={() => onNextId(status.data!.nextTicketId)}
        >
          接续编号 {status.data.nextTicketId}
        </button>
      )}
      {running && (
        <p className="muted">
          运行配置已锁定。停止后可修改属性与节奏；再次启动前可使用“接续编号”避免重复 ID。
        </p>
      )}
      {(status.error || action.error || status.data?.error) && (
        <p role="alert" className="form-error">
          {status.error
            ? formatApiError(status.error, '读取流量状态失败')
            : action.error
              ? formatApiError(action.error, '流量运行失败')
              : status.data?.error}
        </p>
      )}
    </section>
  )
}
