import type {
  CandidateScoringConfig,
  RuleDocument,
  RuleRuntimeConfig,
  SeedSelectionConfig,
} from '../types'
import { ChoiceField, NumberField } from './RuleFormControls'

const runtimeFields: Array<{ key: keyof RuleRuntimeConfig; label: string; help: string }> = [
  {
    key: 'maxPlayers',
    label: '每场最大成员数',
    help: '单场容量上限；是否可以成局仍由 canComplete（成局条件）决定。',
  },
  {
    key: 'candidateScoringLimitPerSeed',
    label: '每个种子的评分候选上限',
    help: '最多对多少个预筛选候选计算分数，默认 500。',
  },
  {
    key: 'candidateLimitPerSeed',
    label: '每个种子的入选候选上限',
    help: '评分后保留的候选数，默认 50；实际候选数量同时受评分上限约束。',
  },
  {
    key: 'attemptLimitPerProduceMatch',
    label: '单次成局的种子尝试上限',
    help: '一次 ProduceMatch（产出匹配）最多尝试多少个种子。',
  },
  {
    key: 'attemptLimitPerMatchRound',
    label: '整轮的种子尝试上限',
    help: '整轮 MatchRound（匹配轮次）累计尝试预算。',
  },
]
const directions = [
  { value: 'ascending', label: '升序：较小值优先' },
  { value: 'descending', label: '降序：较大值优先' },
]

export function RuleSettingsEditor({
  document,
  onChange,
}: {
  document: RuleDocument
  onChange: (key: 'scoring' | 'seedSelection' | 'runtime', value: unknown) => void
}) {
  const { scoring, seedSelection, runtime } = document
  const fields = document.contract.attributes
    .filter((field) => field.type === 'int64')
    .map((field) => ({ value: field.name, label: field.name }))
  const setScoringParam = (key: string, value: string | number | undefined) => {
    const params = { ...scoring.params } as Record<string, string | number>
    if (value === undefined) delete params[key]
    else params[key] = value
    onChange('scoring', { ...scoring, params })
  }
  const setSeedParam = (key: string, value: string | number) =>
    onChange('seedSelection', {
      ...seedSelection,
      params: { ...seedSelection.params, [key]: value },
    })
  return (
    <div className="rule-form-panel">
      <section>
        <h3>常用：种子顺序（SeedOrder）</h3>
        <p className="field-hint">
          决定先从哪个等待 Ticket（匹配票据）开始组局；每个 LogicalNode（逻辑节点）独立维护顺序。
        </p>
        <ChoiceField
          label="种子选择算法"
          value={seedSelection.type}
          options={[
            { value: 'arrival', label: '到达顺序 (arrival)' },
            { value: 'oldest', label: '最早创建 (oldest)' },
            { value: 'int64_priority', label: '整数属性优先级 (int64_priority)' },
            { value: 'random', label: '随机顺序 (random)' },
          ]}
          onChange={(type) => {
            const next: SeedSelectionConfig =
              type === 'int64_priority'
                ? { type, params: { field: fields[0]?.value ?? '', direction: 'descending' } }
                : type === 'random'
                  ? { type, params: { randomSeed: 1 } }
                  : { type: type as 'arrival' | 'oldest', params: {} }
            onChange('seedSelection', next)
          }}
        />
        {seedSelection.type === 'int64_priority' && (
          <div className="rule-form-grid">
            <ChoiceField
              label="种子优先级属性"
              value={seedSelection.params.field}
              options={fields}
              onChange={(value) => setSeedParam('field', value)}
            />
            <ChoiceField
              label="种子排序方向"
              value={seedSelection.params.direction}
              options={directions}
              onChange={(value) => setSeedParam('direction', value)}
            />
          </div>
        )}
        {seedSelection.type === 'random' && (
          <NumberField
            label="随机种子"
            value={seedSelection.params.randomSeed}
            onChange={(value) => setSeedParam('randomSeed', value!)}
            help="相同随机种子与相同输入次序用于复现随机选择。"
          />
        )}
      </section>
      <section>
        <h3>常用：候选评分（CandidateScorer）</h3>
        <p className="field-hint">
          核心按最终分数从高到低选候选；排序方向控制原始值如何换算成分数。
        </p>
        <ChoiceField
          label="候选评分算法"
          value={scoring.type}
          options={[
            { value: 'constant', label: '常量评分 (constant)' },
            { value: 'created_at', label: '创建时间评分 (created_at)' },
            { value: 'int64_field', label: '整数属性评分 (int64_field)' },
          ]}
          onChange={(type) => {
            const next: CandidateScoringConfig =
              type === 'constant'
                ? { type, params: { value: 0 } }
                : type === 'created_at'
                  ? { type, params: { direction: 'ascending' } }
                  : {
                      type: 'int64_field',
                      params: { field: fields[0]?.value ?? '', direction: 'descending' },
                    }
            onChange('scoring', next)
          }}
        />
        {scoring.type === 'constant' ? (
          <NumberField
            label="固定分数"
            integer={false}
            value={scoring.params.value}
            onChange={(value) => setScoringParam('value', value)}
          />
        ) : (
          <div className="rule-form-grid">
            {scoring.type === 'int64_field' && (
              <ChoiceField
                label="评分属性"
                value={scoring.params.field}
                options={fields}
                onChange={(value) => setScoringParam('field', value)}
              />
            )}
            <ChoiceField
              label="评分排序方向"
              value={scoring.params.direction}
              options={directions}
              onChange={(value) => setScoringParam('direction', value)}
            />
            <NumberField
              label="评分权重（可选）"
              optional
              integer={false}
              exclusiveMin={0}
              max={1.94906280228e289}
              value={scoring.params.weight}
              onChange={(value) => setScoringParam('weight', value)}
              help="留空使用 1；必须大于 0。"
            />
            {scoring.type === 'int64_field' && (
              <NumberField
                label="缺失属性的最终分数（可选）"
                optional
                integer={false}
                value={scoring.params.missingScore}
                onChange={(value) => setScoringParam('missingScore', value)}
                help="留空时缺失值排在最后；该分数不再乘权重或反转方向。"
              />
            )}
          </div>
        )}
      </section>
      <section>
        <h3>常用：成局容量</h3>
        <NumberField
          label={runtimeFields[0].label}
          help={runtimeFields[0].help}
          value={runtime.maxPlayers}
          min={1}
          onChange={(value) => onChange('runtime', { ...runtime, maxPlayers: value })}
        />
      </section>
      <details open>
        <summary>高级：候选及尝试预算（Runtime）</summary>
        <div className="rule-form-grid">
          {runtimeFields.slice(1).map(({ key, label, help }) => (
            <NumberField
              key={key}
              label={label}
              help={help}
              value={runtime[key]}
              min={1}
              onChange={(value) => onChange('runtime', { ...runtime, [key]: value })}
            />
          ))}
        </div>
      </details>
    </div>
  )
}
