import { resolveTicketTarget, type TicketTarget, type TicketTargetMode } from '../lib/ticketTargets'

export function TicketTargetPicker({
  mode,
  targets,
  selectedKey,
  disabled,
  onModeChange,
  onSelectionChange,
}: {
  mode: TicketTargetMode
  targets: TicketTarget[]
  selectedKey: string | undefined
  disabled: boolean
  onModeChange: (mode: TicketTargetMode) => void
  onSelectionChange: (key: string) => void
}) {
  const activeTarget = resolveTicketTarget(targets, selectedKey)
  const missing = selectedKey !== undefined && !activeTarget
  const label = mode === 'rule' ? '目标规则' : '目标节点与部署'

  return (
    <>
      <div className="composer-heading">
        <div className="mode-switch" role="group" aria-label="投递目标方式">
          <button
            type="button"
            className={mode === 'rule' ? 'active' : ''}
            aria-pressed={mode === 'rule'}
            disabled={disabled}
            onClick={() => onModeChange('rule')}
          >
            指定规则
          </button>
          <button
            type="button"
            className={mode === 'node' ? 'active' : ''}
            aria-pressed={mode === 'node'}
            disabled={disabled}
            onClick={() => onModeChange('node')}
          >
            指定节点
          </button>
        </div>
        <label className="field-label">
          {label}
          <select
            className="text-input"
            value={selectedKey ?? activeTarget?.key ?? ''}
            disabled={disabled}
            onChange={(event) => onSelectionChange(event.target.value)}
          >
            {!activeTarget && !missing && (
              <option value="" disabled>
                暂无可用{mode === 'rule' ? '规则' : '节点部署'}
              </option>
            )}
            {missing && (
              <option value={selectedKey} disabled>
                原目标已不存在，请重新选择
              </option>
            )}
            {targets.map((target) => (
              <option key={target.key} value={target.key} disabled={!target.enabled}>
                {target.label}
              </option>
            ))}
          </select>
        </label>
      </div>
      <p className="muted">
        {mode === 'rule'
          ? '由 Router 根据规则、亲和键和部署权重选择可用部署。'
          : '投递到所选物理节点上的规则部署。'}
      </p>
      {(!activeTarget || !activeTarget.enabled) && (
        <p className="form-error" role="alert">
          {missing
            ? '所选目标已不存在，请重新选择或切换投递方式。'
            : activeTarget
              ? '所选目标已停用，请重新选择或切换投递方式。'
              : '暂无可用目标，请检查规则和节点部署。'}
        </p>
      )}
    </>
  )
}
