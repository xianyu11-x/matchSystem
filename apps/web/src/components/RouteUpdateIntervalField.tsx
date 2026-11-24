import { useEffect, useRef, useState } from 'react'
import {
  routeUpdateIntervalFromSeconds,
  routeUpdateIntervalSeconds,
} from '../lib/routeUpdateInterval'

export function RouteUpdateIntervalField({
  value,
  onChange,
}: {
  value: number | undefined
  onChange: (value: number | undefined) => void
}) {
  const [text, setText] = useState(routeUpdateIntervalSeconds(value))
  const lastEmitted = useRef(value)
  useEffect(() => {
    if (!Object.is(value, lastEmitted.current) && !Number.isNaN(value))
      setText(routeUpdateIntervalSeconds(value))
    lastEmitted.current = value
  }, [value])

  let error = ''
  try {
    routeUpdateIntervalFromSeconds(text)
  } catch (caught) {
    error = caught instanceof Error ? caught.message : '同步间隔无效'
  }

  return (
    <label className="field-label">
      路由快照同步间隔（秒）
      <input
        className="text-input"
        inputMode="decimal"
        placeholder="默认 10 秒"
        value={text}
        aria-invalid={!!error}
        onChange={(event) => {
          const next = event.target.value
          setText(next)
          try {
            const milliseconds = routeUpdateIntervalFromSeconds(next)
            lastEmitted.current = milliseconds
            onChange(milliseconds)
          } catch {
            lastEmitted.current = NaN
            onChange(NaN)
          }
        }}
      />
      <span className="field-hint">留空或填 0 使用默认 10 秒；最多保留 3 位小数。</span>
      {error ? (
        <span className="form-error" role="alert">
          {error}
        </span>
      ) : null}
    </label>
  )
}
