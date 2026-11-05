import type { FactSnapshot, FactSpec } from '../types'
import { NumberField, TextField } from './RuleFormControls'

export function TickFactsEditor({
  value,
  fields,
  onChange,
}: {
  value: FactSnapshot
  fields: FactSpec[]
  onChange: (value: FactSnapshot) => void
}) {
  const declared = fields.filter((field) => field.scope === 'tick')
  const names = [...new Set([...declared.map((field) => field.name), ...Object.keys(value)])]
  const remove = (name: string) => {
    const next = { ...value }
    delete next[name]
    onChange(next)
  }

  return (
    <div className="rule-form-panel">
      {!names.length && (
        <p className="field-hint">
          请先在 Contract（契约）中声明 Tick Fact，再配置本轮值。声明与实际值相互独立。
        </p>
      )}
      {names.map((name) => {
        const spec = declared.find((field) => field.name === name)
        const present = Object.prototype.hasOwnProperty.call(value, name)
        const current = value[name]
        const type =
          spec?.type ??
          (Array.isArray(current)
            ? current.every((item) => typeof item === 'string')
              ? 'strings'
              : 'uint64s'
            : 'int64')

        return (
          <fieldset className="rule-expression" key={name}>
            <legend>
              {name} · {type}
            </legend>
            {spec?.description && <p className="field-hint">{spec.description}</p>}
            {type === 'int64' && ['waitingCount', 'queueDepth', 'waiting-count'].includes(name) && (
              <p className="field-hint">内置动态字段：运行时会以当前队列数量覆盖静态值。</p>
            )}
            {!spec && (
              <p className="form-error">
                Contract 未声明该 Tick 字段；值已保留，可删除或补充契约。
              </p>
            )}
            <label className="field-label">
              <span>
                <input
                  type="checkbox"
                  checked={present}
                  onChange={(event) =>
                    event.target.checked
                      ? onChange({ ...value, [name]: type === 'int64' ? 0 : [] })
                      : remove(name)
                  }
                />{' '}
                提供本轮值
              </span>
            </label>
            {present &&
              (type === 'int64' ? (
                <NumberField
                  label={`${name} 数值`}
                  value={typeof current === 'number' ? current : NaN}
                  onChange={(next) => onChange({ ...value, [name]: next! })}
                />
              ) : (
                <>
                  {(Array.isArray(current) ? current : []).map((item, index) => (
                    <div className="rule-array-row" key={index}>
                      {type === 'strings' ? (
                        <TextField
                          label={`${name} 值 ${index + 1}`}
                          value={String(item)}
                          onChange={(next) =>
                            onChange({
                              ...value,
                              [name]: (current as string[]).map((v, i) =>
                                i === index ? next : v,
                              ),
                            })
                          }
                        />
                      ) : (
                        <NumberField
                          label={`${name} 值 ${index + 1}`}
                          min={0}
                          value={item as number}
                          onChange={(next) =>
                            onChange({
                              ...value,
                              [name]: (current as number[]).map((v, i) =>
                                i === index ? next! : v,
                              ),
                            })
                          }
                        />
                      )}
                      <button
                        type="button"
                        className="button button-ghost"
                        onClick={() =>
                          onChange({
                            ...value,
                            [name]:
                              type === 'strings'
                                ? (current as string[]).filter((_, i) => i !== index)
                                : (current as number[]).filter((_, i) => i !== index),
                          })
                        }
                      >
                        删除值
                      </button>
                    </div>
                  ))}
                  <button
                    type="button"
                    className="button button-ghost"
                    onClick={() =>
                      onChange({
                        ...value,
                        [name]:
                          type === 'strings'
                            ? [...((current as string[]) ?? []), '']
                            : [...((current as number[]) ?? []), 0],
                      })
                    }
                  >
                    + 添加值
                  </button>
                  {spec?.maxValues !== undefined &&
                    Array.isArray(current) &&
                    current.length > spec.maxValues && (
                      <p className="form-error">值数量超过声明上限 {spec.maxValues}。</p>
                    )}
                </>
              ))}
          </fieldset>
        )
      })}
    </div>
  )
}
