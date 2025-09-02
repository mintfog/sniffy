import { ReactNode } from 'react'
import clsx from 'clsx'

interface ResponsiveTableProps {
  children: ReactNode
  className?: string
  maxHeight?: string
}

interface TableHeaderProps {
  children: ReactNode
  className?: string
}

interface TableBodyProps {
  children: ReactNode
  className?: string
}

interface TableRowProps {
  children: ReactNode
  className?: string
  onClick?: () => void
}

interface TableCellProps {
  children: ReactNode
  className?: string
  width?: string
}

export function ResponsiveTable({ 
  children, 
  className = '', 
  maxHeight = 'calc(100vh-20rem)'
}: ResponsiveTableProps) {
  return (
    <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
      <div 
        className="overflow-auto"
        style={{ maxHeight }}
      >
        <table className={clsx(
          'min-w-full divide-y divide-gray-200 table-fixed',
          className
        )}>
          {children}
        </table>
      </div>
    </div>
  )
}

export function TableHeader({ children, className = '' }: TableHeaderProps) {
  return (
    <thead className={clsx(
      'bg-gray-50 sticky top-0 z-10',
      className
    )}>
      {children}
    </thead>
  )
}

export function TableBody({ children, className = '' }: TableBodyProps) {
  return (
    <tbody className={clsx(
      'bg-white divide-y divide-gray-200',
      className
    )}>
      {children}
    </tbody>
  )
}

export function TableRow({ children, className = '', onClick }: TableRowProps) {
  return (
    <tr 
      className={clsx(
        'hover:bg-gray-50 transition-colors',
        onClick && 'cursor-pointer',
        className
      )}
      onClick={onClick}
    >
      {children}
    </tr>
  )
}

export function TableCell({ children, className = '', width }: TableCellProps) {
  return (
    <td 
      className={clsx(
        'px-4 py-3 text-sm',
        className
      )}
      style={width ? { width } : undefined}
    >
      {children}
    </td>
  )
}

export function TableHeaderCell({ children, className = '', width }: TableCellProps) {
  return (
    <th 
      className={clsx(
        'px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider',
        className
      )}
      style={width ? { width } : undefined}
    >
      {children}
    </th>
  )
}

// 空状态组件
interface EmptyStateProps {
  icon: ReactNode
  title: string
  description?: string
  action?: ReactNode
}

export function TableEmptyState({ icon, title, description, action }: EmptyStateProps) {
  return (
    <div className="text-center py-12">
      <div className="mx-auto mb-4 text-gray-300">
        {icon}
      </div>
      <p className="text-gray-500 mb-2">{title}</p>
      {description && (
        <p className="text-sm text-gray-400 mb-4">{description}</p>
      )}
      {action}
    </div>
  )
}
