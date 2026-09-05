import { useEffect, useState } from 'react'

export function NumberField({
  label,
  value,
  onChange,
  min,
  max,
  exclusiveMin,
  integer = true,
  optional = false,
  help,
}: {
  label: string
  value?: number
  onChange: (value: number | undefined) => void
  min?: number
  max?: number
  exclusiveMin?: number
  integer?: boolean
  optional?: boolean
  help?: string
}) {
  const [text, setText] = useState(value === undefined ? '' : String(value))
  useEffect(() => {
    if (!Number.isNaN(value)) setText(value === undefined ? '' : String(value))
  }, [value])
  const number = Number(text)
  const error =
    text === ''
      ? optional
        ? ''
        : '请填写数值'
      : !Number.isFinite(number)
        ? '请填写有限数值'
        : integer && !Number.isSafeInteger(number)
          ? '请填写 JavaScript 可精确表示的安全整数'
          : exclusiveMin !== undefined && number <= exclusiveMin
            ? `必须大于 ${exclusiveMin}`
            : min !== undefined && number < min
              ? `不得小于 ${min}`
              : max !== undefined && number > max
                ? `不得大于 ${max}`
                : ''
  return (
    <label className="field-label">
      {label}
      <input
        className="text-input"
        inputMode={integer ? 'numeric' : 'decimal'}
        value={text}
        aria-invalid={!!error}
        onChange={(event) => {
          const raw = event.target.value
          setText(raw)
          const parsed = Number(raw)
          onChange(
            raw === '' && optional
              ? undefined
              : raw.trim() === '' ||
                  !Number.isFinite(parsed) ||
                  (integer && !Number.isSafeInteger(parsed)) ||
                  (min !== undefined && parsed < min) ||
                  (max !== undefined && parsed > max) ||
                  (exclusiveMin !== undefined && parsed <= exclusiveMin)
                ? NaN
                : parsed,
          )
        }}
      />
      {help && <span className="field-hint">{help}</span>}
      {error && (
        <span className="form-error" role="alert">
          {error}
        </span>
      )}
    </label>
  )
}

export function ChoiceField({
  label,
  value,
  options,
  onChange,
  help,
}: {
  label: string
  value: string
  options: Array<{ value: string; label: string }>
  onChange: (value: string) => void
  help?: string
}) {
  const invalid = !options.some((option) => option.value === value)
  return (
    <label className="field-label">
      {label}
      <select
        className="text-input"
        value={value}
        aria-invalid={invalid}
        onChange={(event) => onChange(event.target.value)}
      >
        {invalid && <option value={value}>{value || '请选择…'}</option>}
        {options.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </select>
      {help && <span className="field-hint">{help}</span>}
      {invalid && <span className="form-error">请选择有效选项；现有值已保留。</span>}
    </label>
  )
}

export function TextField({
  label,
  value,
  onChange,
  required = false,
  help,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  required?: boolean
  help?: string
}) {
  return (
    <label className="field-label">
      {label}
      <input
        className="text-input"
        value={value}
        aria-invalid={required && !value.trim()}
        onChange={(event) => onChange(event.target.value)}
      />
      {help && <span className="field-hint">{help}</span>}
      {required && !value.trim() && <span className="form-error">此项不能为空</span>}
    </label>
  )
}
