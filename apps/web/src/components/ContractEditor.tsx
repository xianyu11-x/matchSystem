import type {
  AttributeSpec,
  ContractLimits,
  FactScope,
  FactSpec,
  IndexSpec,
  LogicalNodeContract,
} from '../types'

const valueTypes: AttributeSpec['type'][] = ['strings', 'uint64s', 'int64']
const factScopes: FactScope[] = ['tick', 'object', 'match']
const limitFields: Array<{ key: keyof ContractLimits; label: string }> = [
  { key: 'maxBytes', label: 'JSON bytes' },
  { key: 'maxDepth', label: 'AST depth' },
  { key: 'maxChildren', label: 'Node children' },
  { key: 'maxStringBytes', label: 'String bytes' },
  { key: 'maxIndexes', label: 'Indexes' },
  { key: 'maxAttributes', label: 'Attributes' },
  { key: 'maxFacts', label: 'Facts' },
  { key: 'maxValues', label: 'Values / field' },
  { key: 'maxDocumentValues', label: 'Index document values' },
  { key: 'maxQueryValues', label: 'Index query values' },
]

const uniqueName = (prefix: string, used: Set<string>): string => {
  let index = 1
  while (used.has(`${prefix}${index}`)) index += 1
  return `${prefix}${index}`
}

const indexForAttribute = (attribute: AttributeSpec): IndexSpec =>
  attribute.type === 'int64'
    ? { type: 'int64_range', name: attribute.name }
    : {
        type: 'multi_value',
        name: attribute.name,
        keyType: attribute.type === 'uint64s' ? 'uint64' : 'string',
        maxDocumentValues: Math.max(1, Math.min(attribute.maxValues ?? 1, 256)),
        maxQueryValues: Math.max(1, Math.min(attribute.maxValues ?? 1, 256)),
      }

function EmptyRow({ columns, label }: { columns: number; label: string }) {
  return (
    <tr>
      <td className="contract-empty" colSpan={columns}>
        {label}
      </td>
    </tr>
  )
}

export function ContractEditor({
  contract,
  onChange,
}: {
  contract: LogicalNodeContract
  onChange: (contract: LogicalNodeContract) => void
}) {
  const commit = (patch: Partial<LogicalNodeContract>) => onChange({ ...contract, ...patch })
  const updateAttribute = (index: number, patch: Partial<AttributeSpec>) => {
    const previous = contract.attributes[index]
    const attributes = contract.attributes.map((item, itemIndex) => {
      if (itemIndex !== index) return item
      const next = { ...item, ...patch }
      if (next.type === 'int64') delete next.maxValues
      else if (!next.maxValues) next.maxValues = 1
      return next
    })
    const nextAttribute = attributes[index]
    const indexes = contract.indexes.map((item) =>
      item.name === previous.name ? indexForAttribute(nextAttribute) : item,
    )
    commit({ attributes, indexes })
  }
  const addAttribute = () => {
    const used = new Set([
      ...contract.attributes.map((item) => item.name),
      ...contract.facts.map((item) => item.name),
    ])
    commit({
      attributes: [
        ...contract.attributes,
        { name: uniqueName('attribute', used), type: 'strings', maxValues: 1 },
      ],
    })
  }
  const removeAttribute = (index: number) => {
    const name = contract.attributes[index].name
    commit({
      attributes: contract.attributes.filter((_, itemIndex) => itemIndex !== index),
      indexes: contract.indexes.filter((item) => item.name !== name),
    })
  }

  const updateFact = (index: number, patch: Partial<FactSpec>) => {
    const facts = contract.facts.map((item, itemIndex) => {
      if (itemIndex !== index) return item
      const next = { ...item, ...patch }
      if (next.type === 'int64') delete next.maxValues
      else if (!next.maxValues) next.maxValues = 1
      return next
    })
    commit({ facts })
  }
  const addFact = () => {
    const used = new Set([
      ...contract.attributes.map((item) => item.name),
      ...contract.facts.map((item) => item.name),
    ])
    commit({
      facts: [
        ...contract.facts,
        { name: uniqueName('fact', used), type: 'strings', scope: 'object', maxValues: 1 },
      ],
    })
  }

  const unindexedAttributes = contract.attributes.filter(
    (attribute) => !contract.indexes.some((index) => index.name === attribute.name),
  )
  const addIndex = () => {
    const attribute = unindexedAttributes[0]
    if (attribute) commit({ indexes: [...contract.indexes, indexForAttribute(attribute)] })
  }
  const updateIndexAttribute = (index: number, name: string) => {
    const attribute = contract.attributes.find((item) => item.name === name)
    if (!attribute) return
    commit({
      indexes: contract.indexes.map((item, itemIndex) =>
        itemIndex === index ? indexForAttribute(attribute) : item,
      ),
    })
  }
  const updateIndexLimit = (
    index: number,
    key: 'maxDocumentValues' | 'maxQueryValues',
    value: number,
  ) =>
    commit({
      indexes: contract.indexes.map((item, itemIndex) =>
        itemIndex === index && item.type === 'multi_value'
          ? { ...item, [key]: Math.max(1, Math.min(value || 1, 256)) }
          : item,
      ),
    })

  const updateLimit = (key: keyof ContractLimits, raw: string) => {
    const limits = { ...(contract.limits ?? {}) }
    if (raw === '') delete limits[key]
    else limits[key] = Math.max(0, Number(raw) || 0)
    commit({ limits: Object.keys(limits).length > 0 ? limits : undefined })
  }

  return (
    <div className="detail-panel-stack contract-editor">
      <div className="schema-callout">
        <span className="schema-badge">v3</span>
        <div>
          <strong>logical-node-contract/v3</strong>
          <p>字段类型、Fact scope 和索引类型均使用封闭选项；名称和引用由实时校验检查。</p>
        </div>
      </div>

      <div className="contract-section-heading">
        <div>
          <strong>Attributes</strong>
          <span>Ticket 的类型化属性</span>
        </div>
        <button className="button button-ghost" type="button" onClick={addAttribute}>
          + 增加 Attribute
        </button>
      </div>
      <div className="detail-table-wrap">
        <table className="detail-table contract-table">
          <thead>
            <tr>
              <th>名称</th>
              <th>类型</th>
              <th>最大值数</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {contract.attributes.length === 0 ? (
              <EmptyRow columns={4} label="尚未声明 Attribute" />
            ) : (
              contract.attributes.map((item, index) => (
                <tr key={`attribute-${index}`}>
                  <td>
                    <input
                      className="table-input"
                      aria-label={`Attribute ${index + 1} 名称`}
                      value={item.name}
                      onChange={(event) => updateAttribute(index, { name: event.target.value })}
                    />
                  </td>
                  <td>
                    <select
                      className="table-input"
                      aria-label={`Attribute ${index + 1} 类型`}
                      value={item.type}
                      onChange={(event) =>
                        updateAttribute(index, {
                          type: event.target.value as AttributeSpec['type'],
                        })
                      }
                    >
                      {valueTypes.map((type) => (
                        <option key={type} value={type}>
                          {type}
                        </option>
                      ))}
                    </select>
                  </td>
                  <td>
                    {item.type === 'int64' ? (
                      <span className="muted">单值</span>
                    ) : (
                      <input
                        className="table-input table-number"
                        type="number"
                        min={1}
                        max={10000}
                        aria-label={`Attribute ${index + 1} 最大值数`}
                        value={item.maxValues ?? 1}
                        onChange={(event) =>
                          updateAttribute(index, {
                            maxValues: Math.max(
                              1,
                              Math.min(Number(event.target.value) || 1, 10000),
                            ),
                          })
                        }
                      />
                    )}
                  </td>
                  <td>
                    <button
                      className="table-remove"
                      type="button"
                      aria-label={`删除 Attribute ${item.name || index + 1}`}
                      onClick={() => removeAttribute(index)}
                    >
                      删除
                    </button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      <div className="contract-section-heading">
        <div>
          <strong>Facts</strong>
          <span>Tick、Object 和 Match scope 快照</span>
        </div>
        <button className="button button-ghost" type="button" onClick={addFact}>
          + 增加 Fact
        </button>
      </div>
      <div className="detail-table-wrap">
        <table className="detail-table contract-table">
          <thead>
            <tr>
              <th>名称</th>
              <th>类型</th>
              <th>Scope</th>
              <th>最大值数</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {contract.facts.length === 0 ? (
              <EmptyRow columns={5} label="尚未声明 Fact" />
            ) : (
              contract.facts.map((item, index) => (
                <tr key={`fact-${index}`}>
                  <td>
                    <input
                      className="table-input"
                      aria-label={`Fact ${index + 1} 名称`}
                      value={item.name}
                      onChange={(event) => updateFact(index, { name: event.target.value })}
                    />
                  </td>
                  <td>
                    <select
                      className="table-input"
                      aria-label={`Fact ${index + 1} 类型`}
                      value={item.type}
                      onChange={(event) =>
                        updateFact(index, { type: event.target.value as FactSpec['type'] })
                      }
                    >
                      {valueTypes.map((type) => (
                        <option key={type} value={type}>
                          {type}
                        </option>
                      ))}
                    </select>
                  </td>
                  <td>
                    <select
                      className="table-input"
                      aria-label={`Fact ${index + 1} Scope`}
                      value={item.scope}
                      onChange={(event) =>
                        updateFact(index, { scope: event.target.value as FactScope })
                      }
                    >
                      {factScopes.map((scope) => (
                        <option key={scope} value={scope}>
                          {scope}
                        </option>
                      ))}
                    </select>
                  </td>
                  <td>
                    {item.type === 'int64' ? (
                      <span className="muted">单值</span>
                    ) : (
                      <input
                        className="table-input table-number"
                        type="number"
                        min={1}
                        max={10000}
                        aria-label={`Fact ${index + 1} 最大值数`}
                        value={item.maxValues ?? 1}
                        onChange={(event) =>
                          updateFact(index, {
                            maxValues: Math.max(
                              1,
                              Math.min(Number(event.target.value) || 1, 10000),
                            ),
                          })
                        }
                      />
                    )}
                  </td>
                  <td>
                    <button
                      className="table-remove"
                      type="button"
                      aria-label={`删除 Fact ${item.name || index + 1}`}
                      onClick={() =>
                        commit({
                          facts: contract.facts.filter((_, itemIndex) => itemIndex !== index),
                        })
                      }
                    >
                      删除
                    </button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      <div className="contract-section-heading">
        <div>
          <strong>Indexes</strong>
          <span>只能选择类型兼容且已经声明的 Attribute</span>
        </div>
        <button
          className="button button-ghost"
          type="button"
          onClick={addIndex}
          disabled={unindexedAttributes.length === 0}
        >
          + 增加 Index
        </button>
      </div>
      <div className="detail-table-wrap">
        <table className="detail-table contract-table">
          <thead>
            <tr>
              <th>Attribute</th>
              <th>索引类型</th>
              <th>键类型</th>
              <th>文档上限</th>
              <th>查询上限</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {contract.indexes.length === 0 ? (
              <EmptyRow columns={6} label="尚未声明 Index" />
            ) : (
              contract.indexes.map((item, index) => (
                <tr key={`index-${index}`}>
                  <td>
                    <select
                      className="table-input"
                      aria-label={`Index ${index + 1} Attribute`}
                      value={item.name}
                      onChange={(event) => updateIndexAttribute(index, event.target.value)}
                    >
                      {contract.attributes.map((attribute) => (
                        <option
                          value={attribute.name}
                          key={attribute.name}
                          disabled={contract.indexes.some(
                            (other, otherIndex) =>
                              otherIndex !== index && other.name === attribute.name,
                          )}
                        >
                          {attribute.name} · {attribute.type}
                        </option>
                      ))}
                    </select>
                  </td>
                  <td>
                    <span className="type-chip">{item.type}</span>
                  </td>
                  <td>{item.keyType ?? '—'}</td>
                  <td>
                    {item.type === 'multi_value' ? (
                      <input
                        className="table-input table-number"
                        type="number"
                        min={1}
                        max={256}
                        aria-label={`Index ${index + 1} 文档上限`}
                        value={item.maxDocumentValues ?? 1}
                        onChange={(event) =>
                          updateIndexLimit(index, 'maxDocumentValues', Number(event.target.value))
                        }
                      />
                    ) : (
                      '—'
                    )}
                  </td>
                  <td>
                    {item.type === 'multi_value' ? (
                      <input
                        className="table-input table-number"
                        type="number"
                        min={1}
                        max={256}
                        aria-label={`Index ${index + 1} 查询上限`}
                        value={item.maxQueryValues ?? 1}
                        onChange={(event) =>
                          updateIndexLimit(index, 'maxQueryValues', Number(event.target.value))
                        }
                      />
                    ) : (
                      '—'
                    )}
                  </td>
                  <td>
                    <button
                      className="table-remove"
                      type="button"
                      aria-label={`删除 Index ${item.name}`}
                      onClick={() =>
                        commit({
                          indexes: contract.indexes.filter((_, itemIndex) => itemIndex !== index),
                        })
                      }
                    >
                      删除
                    </button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      <details className="contract-limits">
        <summary>运行时限制（Limits）</summary>
        <div className="contract-limit-grid">
          {limitFields.map(({ key, label }) => (
            <label className="field-label" key={key}>
              {label}
              <input
                className="text-input"
                type="number"
                min={0}
                value={contract.limits?.[key] ?? ''}
                placeholder="使用服务端默认值"
                onChange={(event) => updateLimit(key, event.target.value)}
              />
            </label>
          ))}
        </div>
      </details>
    </div>
  )
}
