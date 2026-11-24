import { useEffect, useState, type FormEvent } from 'react'
import { formatApiError, isDemoMode } from '../lib/api'
import { useUpdateRouting } from '../lib/queries'
import {
  routingConfigFromForm,
  routingFormFromScenario,
  routingFormWithAlgorithm,
  type RoutingForm,
} from '../lib/routingSettings'
import type { JsonObject } from '../types'

const sameForm = (left: RoutingForm, right: RoutingForm) =>
  left.algorithm === right.algorithm && left.ticketLimit === right.ticketLimit

export function RoutingSettings({ scenario }: { scenario?: JsonObject }) {
  const source = routingFormFromScenario(scenario)
  const [baseline, setBaseline] = useState(source)
  const [form, setForm] = useState(source)
  const [message, setMessage] = useState<{ kind: 'success' | 'error'; text: string }>()
  const update = useUpdateRouting()
  const dirty = !sameForm(form, baseline)
  const stale = !sameForm(source, baseline)

  useEffect(() => {
    if (!dirty) {
      const current = { algorithm: source.algorithm, ticketLimit: source.ticketLimit }
      setBaseline(current)
      setForm(current)
    }
  }, [source.algorithm, source.ticketLimit, dirty])

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setMessage(undefined)
    try {
      const config = routingConfigFromForm(form)
      const saved = await update.mutateAsync(config)
      const next = routingFormFromScenario({ routing: { ...saved } })
      setBaseline(next)
      setForm(next)
      setMessage({ kind: 'success', text: 'Router 路由策略已应用。' })
    } catch (error) {
      setMessage({ kind: 'error', text: formatApiError(error, '应用路由策略失败') })
    }
  }

  return (
    <section className="routing-settings" aria-labelledby="routing-settings-title">
      <h3 id="routing-settings-title">路由策略</h3>
      <form onSubmit={(event) => void submit(event)}>
        <fieldset disabled={isDemoMode || update.isPending}>
          <div className="routing-settings-row">
            <label className="field-label routing-settings-algorithm">
              路由算法
              <select
                className="text-input"
                value={form.algorithm}
                onChange={(event) => {
                  setForm(routingFormWithAlgorithm(form, event.target.value))
                  setMessage(undefined)
                }}
              >
                {!['weighted_rendezvous', 'queue_weighted_random', 'weighted_priority'].includes(
                  form.algorithm,
                ) ? (
                  <option value={form.algorithm}>{form.algorithm}</option>
                ) : null}
                <option value="weighted_rendezvous">加权 Rendezvous（默认）</option>
                <option value="queue_weighted_random">按队列加权随机</option>
                <option value="weighted_priority">权重优先</option>
              </select>
            </label>
            {form.algorithm === 'weighted_priority' ? (
              <label className="field-label routing-settings-threshold">
                队列阈值
                <input
                  className="text-input"
                  inputMode="numeric"
                  value={form.ticketLimit}
                  placeholder="正整数"
                  aria-describedby="routing-threshold-hint"
                  onChange={(event) => {
                    setForm({ ...form, ticketLimit: event.target.value })
                    setMessage(undefined)
                  }}
                />
              </label>
            ) : null}
            <div className="routing-settings-actions">
              <button type="submit" className="button button-primary" disabled={!dirty || stale}>
                {update.isPending ? '应用中…' : '应用'}
              </button>
              {dirty || stale ? (
                <button
                  type="button"
                  className="button button-ghost"
                  onClick={() => {
                    setBaseline(source)
                    setForm(source)
                    setMessage(undefined)
                  }}
                >
                  恢复当前值
                </button>
              ) : null}
            </div>
          </div>
          {form.algorithm === 'weighted_priority' ? (
            <p id="routing-threshold-hint" className="field-hint routing-settings-hint">
              Ticket 数须为正整数，队列低于阈值时优先按权重选择。
            </p>
          ) : null}
          {stale && dirty ? (
            <p className="form-error" role="alert">
              路由配置已变化，请恢复当前值后重新编辑。
            </p>
          ) : null}
        </fieldset>
        {message ? (
          <p
            className={message.kind === 'error' ? 'form-error' : 'success-note'}
            role={message.kind === 'error' ? 'alert' : 'status'}
          >
            {message.text}
          </p>
        ) : null}
      </form>
    </section>
  )
}
