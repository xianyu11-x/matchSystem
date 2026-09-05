import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { trafficRequest, type TrafficConfig } from '../lib/api'
import type { BatchGeneratorSpec } from '../types'

export function TrafficControl({ buildSpec }: { buildSpec: () => BatchGeneratorSpec | undefined }) {
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
      const spec = buildSpec()
      if (method === 'POST' && !spec) throw new Error('请先选择规则并配置生成器')
      return trafficRequest(method, config, spec)
    },
    onSuccess: (result) => {
      cache.setQueryData(['traffic'], result)
      void cache.invalidateQueries({ queryKey: ['tickets'] })
    },
  })
  const running = status.data?.state === 'running'
  return (
    <section>
      <h3>持续流量与定时匹配</h3>
      <p>
        复用上方生成器属性、随机种子与起始
        TicketID；数量由到达分布决定。每次匹配数量是规则允许时的上限。启动后配置固定，停止后可修改并重新启动；重启请使用状态中的下一
        TicketID。
      </p>
      <fieldset disabled={running || action.isPending} className="form-grid">
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
          ['rate', 'burstSize', 'burstIntervalMs', 'matchIntervalMs', 'maxMatches', 'seed'] as const
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
                value={config[key]}
                onChange={(e) => setConfig({ ...config, [key]: Number(e.target.value) })}
              />
            </label>
          ))}
      </fieldset>
      <button
        className="button button-primary"
        disabled={running || action.isPending || !status.data}
        onClick={() => action.mutate('POST')}
      >
        开始持续注入
      </button>
      <button
        className="button button-secondary"
        disabled={!running || action.isPending}
        onClick={() => action.mutate('DELETE')}
      >
        停止
      </button>
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
      {(status.error || action.error || status.data?.error) && (
        <p role="alert" className="form-error">
          {status.error?.message ?? action.error?.message ?? status.data?.error}
        </p>
      )}
    </section>
  )
}
