import type { FactScope, FactSnapshot, FactSpec, ProviderDescriptorSet } from '../types'
import { ChoiceField, NumberField, TextField } from './RuleFormControls'

const scopes: Array<{ scope: FactScope; label: string }> = [
  { scope: 'tick', label: '本轮 (Tick)' },
  { scope: 'object', label: '对象 (Object)' },
  { scope: 'match', label: '匹配 (Match)' },
]
export function ProviderDescriptorsEditor({
  value,
  onChange,
}: {
  value: ProviderDescriptorSet
  onChange: (value: ProviderDescriptorSet) => void
}) {
  return (
    <div className="rule-form-panel">
      {scopes.map(({ scope, label }) => {
        const descriptor = value[scope]
        const update = (patch: Partial<NonNullable<typeof descriptor>>) =>
          onChange({ ...value, [scope]: { ...descriptor, ...patch } })
        const updateFact = (index: number, patch: Partial<FactSpec>) =>
          update({
            facts: descriptor!.facts.map((item, i) => {
              if (i !== index) return item
              const next = { ...item, ...patch }
              if (next.type === 'int64') delete next.maxValues
              else next.maxValues ??= 1
              return next
            }),
          })
        return (
          <fieldset className="rule-expression" key={scope}>
            <legend>{label} Provider（事实提供器）</legend>
            <label className="field-label">
              <span>
                <input
                  type="checkbox"
                  checked={!!descriptor}
                  onChange={(event) => {
                    const next = { ...value }
                    if (event.target.checked)
                      next[scope] = { id: `simulator-${scope}`, version: '1', facts: [] }
                    else delete next[scope]
                    onChange(next)
                  }}
                />{' '}
                显式配置握手声明
              </span>
            </label>
            {descriptor && (
              <>
                <div className="rule-form-grid">
                  <TextField
                    label="提供器标识 (ID)"
                    value={descriptor.id}
                    required
                    onChange={(id) => update({ id })}
                  />
                  <TextField
                    label="提供器版本"
                    value={descriptor.version}
                    required
                    onChange={(version) => update({ version })}
                  />
                </div>
                {descriptor.facts.map((fact, index) => (
                  <fieldset className="rule-array" key={index}>
                    <legend>声明字段 {index + 1}</legend>
                    <div className="rule-form-grid">
                      <TextField
                        label="字段名称"
                        value={fact.name}
                        required
                        onChange={(name) => updateFact(index, { name })}
                      />
                      <ChoiceField
                        label="字段类型"
                        value={fact.type}
                        options={[
                          { value: 'strings', label: '字符串集合 (strings)' },
                          { value: 'uint64s', label: '无符号整数集合 (uint64s)' },
                          { value: 'int64', label: '有符号整数 (int64)' },
                        ]}
                        onChange={(type) => updateFact(index, { type: type as FactSpec['type'] })}
                      />
                      <ChoiceField
                        label="字段作用域"
                        value={fact.scope}
                        options={[{ value: scope, label }]}
                        onChange={() => updateFact(index, { scope })}
                      />
                      {fact.type !== 'int64' && (
                        <NumberField
                          label="最大值数"
                          min={1}
                          value={fact.maxValues}
                          onChange={(maxValues) => updateFact(index, { maxValues })}
                        />
                      )}
                      <TextField
                        label="字段说明（可选）"
                        value={fact.description ?? ''}
                        onChange={(description) => updateFact(index, { description })}
                      />
                    </div>
                    {descriptor.facts.some(
                      (other, i) => i !== index && other.name === fact.name,
                    ) && <p className="form-error">字段名称重复。</p>}
                    <button
                      type="button"
                      className="button button-ghost"
                      onClick={() =>
                        update({ facts: descriptor.facts.filter((_, i) => i !== index) })
                      }
                    >
                      删除声明字段
                    </button>
                  </fieldset>
                ))}
                <button
                  type="button"
                  className="button button-ghost"
                  onClick={() =>
                    update({ facts: [...descriptor.facts, { name: '', type: 'int64', scope }] })
                  }
                >
                  + 添加声明字段
                </button>
              </>
            )}
          </fieldset>
        )
      })}
    </div>
  )
}

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
          先在 Contract（契约）中声明 Tick Fact，再配置本轮值。声明与实际值独立。
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
                              [name]: (current as string[]).map((v, i) => (i === index ? next : v)),
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
                            [name]: (current as number[]).filter((_, i) => i !== index),
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
