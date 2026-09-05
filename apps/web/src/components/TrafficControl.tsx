import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { trafficRequest, type TrafficConfig } from '../lib/api'
import type { BatchGeneratorSpec } from '../types'

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
  const [config, setConfig] = useState<TrafficConfig>({
    distribution: 'constant',
    rate: 10,
    burstSize: 50,
    burstIntervalMs: 1000,
    matchIntervalMs: 1000,
    maxMatches: 10,
    seed: 42,
  })
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
    if (running && status.data?.config) setConfig(status.data.config)
  }, [running, status.data?.config])
  return (
    <section className="traffic-section">
      <h3>持续流量与定时匹配</h3>
      <p className="muted">
        按到达节奏持续注入，并按指定间隔尝试匹配。每轮局数为上限，实际产出由规则和候选对象决定。
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
                          ? 100
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
        </fieldset>
        <button
          className="button button-primary"
          disabled={running || action.isPending || !status.data}
          type="submit"
        >
          开始持续注入
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
        <p role="status">
          状态：
          {(
            { idle: '未启动', running: '运行中', stopped: '已停止', failed: '失败' } as Record<
              string,
              string
            >
          )[status.data.state] ?? status.data.state}{' '}
          · 已注入 {status.data.injected} · 已产出 {status.data.produced} · 匹配轮数{' '}
          {status.data.rounds} · 下一 TicketID {status.data.nextTicketId} · 调度延迟{' '}
          {status.data.lagMs} ms
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
          {status.error?.message ?? action.error?.message ?? status.data?.error}
        </p>
      )}
    </section>
  )
}
