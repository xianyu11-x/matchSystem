import {
  createExpression,
  defaultExpression,
  defaultExpressionField,
  expressionDefinitions,
  expressionFieldType,
  expressionLabels,
} from '../lib/expressionForm'
import { ChoiceField, NumberField, TextField } from './RuleFormControls'
import type { JsonObject, JsonValue, LogicalNodeContract, ValueType } from '../types'

const labels: Record<string, string> = {
  left: '左侧输入',
  right: '右侧输入',
  value: '输入值',
  values: '值集合',
  other: '比较集合',
  input: '映射输入',
  min: '下界（包含）',
  max: '上界（包含）',
  when: '条件',
  then: '条件为真',
  else: '条件为假',
  children: '子条件',
  items: '待合并集合',
  needle: '固定查找值',
  steps: '阶梯项',
  at: '起点',
}
const sourceLabels: Record<string, string> = {
  seed_attributes: '种子属性',
  candidate_attributes: '候选属性',
  seed_facts: '种子事实',
  candidate_facts: '候选事实',
  tick_facts: '本轮事实',
  match_facts: '匹配事实',
}
const object = (value: JsonValue | undefined): JsonObject =>
  value && !Array.isArray(value) && typeof value === 'object' ? value : {}

/** Complete typed expression form shared by tree tabs and graph inspector. */
export function ExpressionEditor({
  value,
  type,
  contract,
  onChange,
  label = '表达式',
  depth = 0,
}: {
  value: JsonObject
  type: ValueType
  contract: LogicalNodeContract
  onChange: (value: JsonObject) => void
  label?: string
  depth?: number
}) {
  const op = typeof value.op === 'string' ? value.op : ''
  const definition = expressionDefinitions.find(
    (item) => item.op === op && item.resultType === type,
  )
  const update = (key: string, next: JsonValue) => onChange({ ...value, [key]: next })
  const source = typeof value.source === 'string' ? value.source : ''
  const fieldType = op.startsWith('int64')
    ? 'int64'
    : op.startsWith('uint64s')
      ? 'uint64s'
      : 'strings'
  const fields = source.endsWith('_attributes')
    ? contract.attributes.filter((field) => field.type === fieldType)
    : contract.facts.filter(
        (field) =>
          field.type === fieldType &&
          field.scope ===
            (source === 'tick_facts' ? 'tick' : source === 'match_facts' ? 'match' : 'object'),
      )
  return (
    <fieldset className="rule-expression">
      <legend>
        {label} · {type}
      </legend>
      <ChoiceField
        label="操作"
        value={op}
        options={expressionDefinitions
          .filter((item) => item.resultType === type)
          .map((item) => ({ value: item.op, label: `${expressionLabels[item.op]} (${item.op})` }))}
        onChange={(next) => onChange(createExpression(next))}
        help="切换操作会重建该表达式的输入；同一操作内修改其他字段会保留全部输入。"
      />
      {definition &&
        Object.entries(definition.fields).map(([key, field]) => {
          const fieldLabel = labels[key] ?? key
          const current = value[key]
          if (key === 'source')
            return (
              <ChoiceField
                key={key}
                label="数据来源"
                value={source}
                options={Object.entries(sourceLabels).map(([name, title]) => ({
                  value: name,
                  label: `${title} (${name})`,
                }))}
                onChange={(next) => update(key, next)}
              />
            )
          if (key === 'name')
            return (
              <ChoiceField
                key={key}
                label="关联字段"
                value={typeof current === 'string' ? current : ''}
                options={fields.map((field) => ({ value: field.name, label: field.name }))}
                onChange={(next) => update(key, next)}
                help="仅列出 Contract（契约）中类型和作用域兼容的字段。"
              />
            )
          if (key === 'index')
            return (
              <ChoiceField
                key={key}
                label="关联索引"
                value={typeof current === 'string' ? current : ''}
                options={contract.indexes
                  .filter((index) =>
                    op === 'lookup_range'
                      ? index.type === 'int64_range'
                      : index.type === 'multi_value' &&
                        index.keyType === (op === 'lookup_uint64' ? 'uint64' : 'string'),
                  )
                  .map((index) => ({ value: index.name, label: index.name }))}
                onChange={(next) => update(key, next)}
              />
            )
          const expression = expressionFieldType(field, op)
          if (expression) {
            const nested = object(current)
            const expr = expression.envelope ? object(nested.expr) : nested
            return (
              <ExpressionEditor
                key={key}
                value={expr}
                type={expression.type}
                contract={contract}
                label={fieldLabel}
                depth={depth + 1}
                onChange={(next) =>
                  update(
                    key,
                    expression.envelope
                      ? {
                          ...nested,
                          schemaVersion: 'expression-scalar/v3',
                          resultType: expression.type,
                          expr: next,
                        }
                      : next,
                  )
                }
              />
            )
          }
          if (field.type === 'array') {
            const values = Array.isArray(current) ? current : []
            const itemExpr = expressionFieldType(field.items!, op)
            const changeAt = (index: number, next: JsonValue) =>
              update(
                key,
                values.map((item, i) => (i === index ? next : item)),
              )
            return (
              <fieldset className="rule-array" key={key}>
                <legend>{fieldLabel}</legend>
                {values.map((item, index) => (
                  <div className="rule-array-row" key={index}>
                    {itemExpr ? (
                      <ExpressionEditor
                        label={`${fieldLabel} ${index + 1}`}
                        value={object(item)}
                        type={itemExpr.type}
                        contract={contract}
                        depth={depth + 1}
                        onChange={(next) => changeAt(index, next)}
                      />
                    ) : key === 'steps' ? (
                      <div className="rule-form-grid">
                        <NumberField
                          label={`阶梯 ${index + 1} 起点`}
                          value={object(item).at as number}
                          onChange={(next) => changeAt(index, { ...object(item), at: next! })}
                        />
                        <NumberField
                          label={`阶梯 ${index + 1} 输出值`}
                          value={object(item).value as number}
                          onChange={(next) => changeAt(index, { ...object(item), value: next! })}
                        />
                        {index > 0 &&
                          Number(object(item).at) <= Number(object(values[index - 1]).at) && (
                            <p className="form-error">阶梯起点必须严格递增。</p>
                          )}
                      </div>
                    ) : field.items?.type === 'integer' ? (
                      <NumberField
                        label={`${fieldLabel} ${index + 1}`}
                        value={item as number}
                        min={0}
                        onChange={(next) => changeAt(index, next!)}
                      />
                    ) : (
                      <TextField
                        label={`${fieldLabel} ${index + 1}`}
                        value={typeof item === 'string' ? item : ''}
                        onChange={(next) => changeAt(index, next)}
                      />
                    )}
                    <div className="rule-array-actions">
                      <button
                        type="button"
                        className="button button-ghost"
                        disabled={index === 0}
                        onClick={() => {
                          const next = [...values]
                          ;[next[index - 1], next[index]] = [next[index], next[index - 1]]
                          update(key, next)
                        }}
                      >
                        上移
                      </button>
                      <button
                        type="button"
                        className="button button-ghost"
                        onClick={() =>
                          update(
                            key,
                            values.filter((_, i) => i !== index),
                          )
                        }
                      >
                        删除
                      </button>
                    </div>
                  </div>
                ))}
                {values.length < (field.minItems ?? 0) && (
                  <p className="form-error">至少需要 {field.minItems} 项。</p>
                )}
                <button
                  type="button"
                  className="button button-ghost"
                  onClick={() =>
                    update(key, [
                      ...values,
                      itemExpr
                        ? defaultExpression(itemExpr.type)
                        : defaultExpressionField(field.items!, op),
                    ])
                  }
                >
                  + 添加{fieldLabel}
                </button>
                {key === 'steps' && (
                  <p className="field-hint">
                    按起点严格递增排列；区间使用最近的不大于输入值的阶梯输出。
                  </p>
                )}
              </fieldset>
            )
          }
          if (field.type === 'boolean')
            return (
              <ChoiceField
                key={key}
                label="布尔值"
                value={String(current)}
                options={[
                  { value: 'false', label: '否 (false)' },
                  { value: 'true', label: '是 (true)' },
                ]}
                onChange={(next) => update(key, next === 'true')}
              />
            )
          if (field.type === 'integer')
            return (
              <NumberField
                key={key}
                label={fieldLabel}
                value={current as number}
                min={field.minimum}
                onChange={(next) => update(key, next!)}
              />
            )
          return (
            <TextField
              key={key}
              label={fieldLabel}
              value={typeof current === 'string' ? current : ''}
              onChange={(next) => update(key, next)}
            />
          )
        })}
    </fieldset>
  )
}
