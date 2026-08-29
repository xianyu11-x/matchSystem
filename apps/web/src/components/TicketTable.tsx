import { useMemo, useRef } from 'react'
import { flexRender, getCoreRowModel, useReactTable, type ColumnDef } from '@tanstack/react-table'
import { useVirtualizer } from '@tanstack/react-virtual'
import { formatDate, flattenFacts, flattenTypedAttributes } from '../lib/format'
import type { Ticket } from '../types'
import { StatusPill } from './States'

export function TicketTable({
  tickets,
  onDelete,
}: {
  tickets: Ticket[]
  onDelete?: (ticket: Ticket) => void
}) {
  const columns = useMemo<ColumnDef<Ticket>[]>(
    () => [
      {
        accessorKey: 'ticketId',
        header: 'Ticket',
        cell: ({ row }) => (
          <div className="cell-primary">
            <strong>{row.original.ticketId}</strong>
            <span>{formatDate(row.original.createdAt)}</span>
          </div>
        ),
      },
      {
        accessorKey: 'status',
        header: '状态',
        cell: ({ row }) => <StatusPill status={row.original.status} />,
      },
      {
        id: 'owner',
        header: 'OwnerRef',
        cell: ({ row }) => (
          <div className="cell-primary">
            <strong>{row.original.routeDecision?.owner?.physicalNodeId ?? '—'}</strong>
            <span>{row.original.routeDecision?.owner?.logicalNodeKey ?? '尚未路由'}</span>
          </div>
        ),
      },
      {
        id: 'attributes',
        header: 'Attributes',
        cell: ({ row }) => (
          <span className="cell-truncate" title={flattenTypedAttributes(row.original.attributes)}>
            {flattenTypedAttributes(row.original.attributes)}
          </span>
        ),
      },
      {
        id: 'facts',
        header: 'Object Facts',
        cell: ({ row }) => (
          <span className="cell-truncate" title={flattenFacts(row.original.facts)}>
            {flattenFacts(row.original.facts)}
          </span>
        ),
      },
      {
        id: 'action',
        header: '',
        cell: ({ row }) =>
          onDelete ? (
            <button
              className="icon-button danger"
              type="button"
              aria-label={`删除 ${row.original.ticketId}`}
              onClick={() => onDelete(row.original)}
            >
              ×
            </button>
          ) : null,
      },
    ],
    [onDelete],
  )
  const table = useReactTable({
    data: tickets,
    columns,
    getCoreRowModel: getCoreRowModel(),
    getRowId: (row) => row.ticketId,
  })
  const rows = table.getRowModel().rows
  const scrollRef = useRef<HTMLDivElement>(null)
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 66,
    overscan: 10,
  })

  return (
    <div className="virtual-table" role="table" aria-label="Tickets">
      <div className="virtual-header" role="row">
        {table.getHeaderGroups()[0].headers.map((header) => (
          <div role="columnheader" key={header.id}>
            {flexRender(header.column.columnDef.header, header.getContext())}
          </div>
        ))}
      </div>
      <div className="virtual-scroll" ref={scrollRef}>
        <div style={{ height: `${virtualizer.getTotalSize()}px`, position: 'relative' }}>
          {virtualizer.getVirtualItems().map((virtualRow) => {
            const row = rows[virtualRow.index]
            return (
              <div
                className="virtual-row"
                data-index={virtualRow.index}
                key={row.id}
                role="row"
                ref={virtualizer.measureElement}
                style={{ transform: `translateY(${virtualRow.start}px)` }}
              >
                {row.getVisibleCells().map((cell) => (
                  <div role="cell" key={cell.id}>
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </div>
                ))}
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}
