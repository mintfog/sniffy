import { forwardRef } from 'react'
import clsx from 'clsx'

interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: 'primary' | 'secondary' | 'ghost' | 'danger'
  size?: 'sm' | 'md' | 'lg'
  loading?: boolean
  icon?: React.ReactNode
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant = 'primary', size = 'md', loading, icon, children, disabled, ...props }, ref) => {
    return (
      <button
        className={clsx(
          'inline-flex items-center justify-center font-medium transition-colors focus:outline-none focus:ring-2 focus:ring-offset-2 disabled:opacity-50 disabled:pointer-events-none',
          {
            // Variants
            'bg-primary-600 hover:bg-primary-700 text-white focus:ring-primary-500': variant === 'primary',
            'bg-gray-200 hover:bg-gray-300 text-gray-900 focus:ring-gray-500': variant === 'secondary',
            'bg-transparent hover:bg-gray-100 text-gray-700 focus:ring-gray-500': variant === 'ghost',
            'bg-red-600 hover:bg-red-700 text-white focus:ring-red-500': variant === 'danger',
            
            // Sizes
            'px-3 py-1.5 text-sm h-8 rounded': size === 'sm',
            'px-4 py-2 text-sm h-10 rounded-md': size === 'md',
            'px-6 py-3 text-base h-12 rounded-lg': size === 'lg',
          },
          className
        )}
        disabled={disabled || loading}
        ref={ref}
        {...props}
      >
        {loading && (
          <div className="mr-2 h-4 w-4 animate-spin rounded-full border-2 border-transparent border-t-current" />
        )}
        {icon && !loading && <span className="mr-2">{icon}</span>}
        {children}
      </button>
    )
  }
)

Button.displayName = 'Button'
