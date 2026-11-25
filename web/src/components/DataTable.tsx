import { Table } from 'antd'
import type { TableProps } from 'antd'
import { useCallback, useMemo, useState } from 'react'

/**
 * 通用表格：所有列都能排序、拖表头右边界调列宽、拖表头调列顺序。
 * 调整结果按 tableId 记到浏览器本地，刷新后还在。
 * 列要带 key（或 dataIndex）；自定义渲染的列可给 sortValue 指定排序依据。
 */
type AnyRec = Record<string, unknown>
export type DataColumn<T> = NonNullable<TableProps<T>['columns']>[number] & {
  sortValue?: (r: T) => string | number
  resizable?: boolean
  reorderable?: boolean
}

const load = (id?: string): { order?: string[]; widths?: Record<string, number> } => {
  if (!id) return {}
  try { return JSON.parse(localStorage.getItem('pp-table-' + id) || '{}') } catch { return {} }
}
const persist = (id: string | undefined, order: string[], widths: Record<string, number>) => {
  if (!id) return
  try { localStorage.setItem('pp-table-' + id, JSON.stringify({ order, widths })) } catch { /* 隐私模式等存不了，忽略 */ }
}

export function DataTable<T extends object>({ tableId, columns = [], ...rest }: TableProps<T> & { tableId?: string; columns?: DataColumn<T>[] }) {
  const keyOf = (c: DataColumn<T>, i: number) => String((c as { key?: React.Key }).key ?? (c as { dataIndex?: React.Key }).dataIndex ?? i)
  const saved = useMemo(() => load(tableId), [tableId])
  const [order, setOrder] = useState<string[]>(() => saved.order ?? columns.map(keyOf))
  const [widths, setWidths] = useState<Record<string, number>>(() => saved.widths ?? {})
  const [dragKey, setDragKey] = useState<string | null>(null)

  const startResize = useCallback((key: string, startX: number, startW: number) => {
    const move = (e: MouseEvent) => {
      const w = Math.max(56, Math.round(startW + (e.clientX - startX)))
      setWidths((prev) => ({ ...prev, [key]: w }))
    }
    const up = () => {
      document.removeEventListener('mousemove', move)
      document.removeEventListener('mouseup', up)
      setWidths((prev) => { persist(tableId, order, prev); return prev })
    }
    document.addEventListener('mousemove', move)
    document.addEventListener('mouseup', up)
  }, [order, tableId])

  const drop = (target: string) => {
    if (!dragKey || dragKey === target) { setDragKey(null); return }
    setOrder((prev) => {
      const base = prev.length ? prev : columns.map(keyOf)
      const next = base.filter((k) => k !== dragKey)
      const idx = next.indexOf(target)
      next.splice(idx < 0 ? next.length : idx, 0, dragKey)
      persist(tableId, next, widths)
      return next
    })
    setDragKey(null)
  }

  const ordered = useMemo(() => {
    const byKey = new Map(columns.map((c, i) => [keyOf(c, i), c]))
    const keys = [...order.filter((k) => byKey.has(k)), ...columns.map(keyOf).filter((k) => !order.includes(k))]
    return keys.map((key) => {
      const c = byKey.get(key)!
      const di = (c as { dataIndex?: string }).dataIndex
      const sortValue = (c as DataColumn<T>).sortValue
      const canSort = (c as { sorter?: unknown }).sorter || sortValue || di
      const sorter = (c as { sorter?: unknown }).sorter ?? (canSort ? (a: T, b: T) => {
        const va = sortValue ? sortValue(a) : (a as AnyRec)[di as string]
        const vb = sortValue ? sortValue(b) : (b as AnyRec)[di as string]
        if (va == null && vb == null) return 0
        if (va == null) return -1
        if (vb == null) return 1
        return typeof va === 'number' && typeof vb === 'number' ? va - vb : String(va).localeCompare(String(vb))
      } : undefined)
      const width = widths[key] ?? (c as { width?: number }).width
      return {
        ...c, key, width, sorter,
        onHeaderCell: () => ({
          ppColKey: key,
          ppWidth: width,
          ppDraggable: (c as DataColumn<T>).reorderable !== false,
          ppOnDragStart: () => setDragKey(key),
          ppOnDrop: () => drop(key),
          ppOnResizeStart: (c as DataColumn<T>).resizable !== false ? startResize : undefined,
        }),
      } as DataColumn<T>
    })
  }, [columns, order, widths, dragKey, startResize]) // eslint-disable-line react-hooks/exhaustive-deps

  return <Table<T> {...rest} columns={ordered} components={{ header: { cell: HeaderCell } }} />
}

interface HeaderCellProps extends React.ThHTMLAttributes<HTMLTableCellElement> {
  ppColKey?: string
  ppWidth?: number
  ppDraggable?: boolean
  ppOnDragStart?: () => void
  ppOnDrop?: () => void
  ppOnResizeStart?: (key: string, startX: number, startW: number) => void
}

function HeaderCell({ ppColKey, ppWidth, ppDraggable, ppOnDragStart, ppOnDrop, ppOnResizeStart, style, children, ...rest }: HeaderCellProps) {
  return (
    <th
      {...rest}
      style={{ ...style, width: ppWidth, position: 'relative' }}
      draggable={ppDraggable}
      onDragStart={ppOnDragStart}
      onDragOver={(e) => e.preventDefault()}
      onDrop={ppOnDrop}
    >
      {children}
      {ppOnResizeStart && ppColKey && (
        <span
          className="pp-col-resize"
          onMouseDown={(e) => { e.preventDefault(); e.stopPropagation(); const w = (e.currentTarget.parentElement as HTMLElement)?.offsetWidth ?? ppWidth ?? 120; ppOnResizeStart(ppColKey, e.clientX, w) }}
          onClick={(e) => e.stopPropagation()}
          draggable={false}
        />
      )}
    </th>
  )
}
