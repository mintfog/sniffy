const controlSizes = {
  'control-xs': 'var(--wb-control-xs)',
  'control-sm': 'var(--wb-control-sm)',
  'control-md': 'var(--wb-control-md)',
  'control-lg': 'var(--wb-control-lg)',
  row: 'var(--wb-row-h)',
}

/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        // ── 工作台语义 token（CSS 变量驱动，支持深/亮切换 + 透明度） ──
        base: 'rgb(var(--c-base) / <alpha-value>)',
        surface: 'rgb(var(--c-surface) / <alpha-value>)',
        popover: 'rgb(var(--c-popover) / <alpha-value>)',
        elevated: 'rgb(var(--c-elevated) / <alpha-value>)',
        hover: 'rgb(var(--c-hover) / <alpha-value>)',
        inset: 'rgb(var(--c-inset) / <alpha-value>)',
        ring: 'rgb(var(--c-ring) / <alpha-value>)',
        'row-selected': 'rgb(var(--c-row-selected) / <alpha-value>)',
        'row-focused': 'rgb(var(--c-row-focused) / <alpha-value>)',
        'row-indicator': 'rgb(var(--c-row-indicator) / <alpha-value>)',
        // 遮罩自带透明度（亮暗不同），不接 <alpha-value>
        overlay: 'rgb(var(--wb-overlay))',
        type: {
          number: 'rgb(var(--c-type-number) / <alpha-value>)',
          string: 'rgb(var(--c-type-string) / <alpha-value>)',
          boolean: 'rgb(var(--c-type-boolean) / <alpha-value>)',
          null: 'rgb(var(--c-type-null) / <alpha-value>)',
          key: 'rgb(var(--c-type-key) / <alpha-value>)',
          binary: 'rgb(var(--c-type-binary) / <alpha-value>)',
        },
        line: {
          DEFAULT: 'rgb(var(--c-line) / <alpha-value>)',
          strong: 'rgb(var(--c-line-strong) / <alpha-value>)',
        },
        fg: {
          DEFAULT: 'rgb(var(--c-fg) / <alpha-value>)',
          muted: 'rgb(var(--c-fg-muted) / <alpha-value>)',
          faint: 'rgb(var(--c-fg-faint) / <alpha-value>)',
        },
        accent: {
          DEFAULT: 'rgb(var(--c-accent) / <alpha-value>)',
          hover: 'rgb(var(--c-accent-hover) / <alpha-value>)',
          fg: 'rgb(var(--c-accent-fg) / <alpha-value>)',
        },
        // 主操作实底：主按钮、激活分段、激活标签胶囊
        sel: {
          DEFAULT: 'rgb(var(--c-sel) / <alpha-value>)',
          hover: 'rgb(var(--c-sel-hover) / <alpha-value>)',
          fg: 'rgb(var(--c-sel-fg) / <alpha-value>)',
        },
        ok: {
          DEFAULT: 'rgb(var(--c-ok) / <alpha-value>)',
          fg: 'rgb(var(--c-ok-fg) / <alpha-value>)',
        },
        warn: {
          DEFAULT: 'rgb(var(--c-warn) / <alpha-value>)',
          fg: 'rgb(var(--c-warn-fg) / <alpha-value>)',
        },
        danger: {
          DEFAULT: 'rgb(var(--c-danger) / <alpha-value>)',
          fg: 'rgb(var(--c-danger-fg) / <alpha-value>)',
        },
        info: {
          DEFAULT: 'rgb(var(--c-info) / <alpha-value>)',
          fg: 'rgb(var(--c-info-fg) / <alpha-value>)',
        },
        iris: 'rgb(var(--c-violet) / <alpha-value>)',
        // 高亮标记配色（按主题分值）
        mark: {
          red: 'rgb(var(--c-mark-red) / <alpha-value>)',
          yellow: 'rgb(var(--c-mark-yellow) / <alpha-value>)',
          green: 'rgb(var(--c-mark-green) / <alpha-value>)',
          blue: 'rgb(var(--c-mark-blue) / <alpha-value>)',
          cyan: 'rgb(var(--c-mark-cyan) / <alpha-value>)',
        },
        // method 配色
        method: {
          get: 'rgb(var(--c-method-get) / <alpha-value>)',
          post: 'rgb(var(--c-method-post) / <alpha-value>)',
          put: 'rgb(var(--c-method-put) / <alpha-value>)',
          delete: 'rgb(var(--c-method-delete) / <alpha-value>)',
          patch: 'rgb(var(--c-method-patch) / <alpha-value>)',
          other: 'rgb(var(--c-method-other) / <alpha-value>)',
        },
      },
      fontFamily: {
        sans: ['var(--font-sans)'],
        mono: ['var(--font-mono)'],
      },
      borderRadius: {
        wb: 'var(--wb-radius)',
        'wb-sm': 'var(--wb-radius-sm)',
        control: 'var(--wb-radius-control)',
        lg: 'var(--wb-radius-lg)',
      },
      // 紧凑模式改 --wb-control-* / --wb-row-h 即整体生效
      height: controlSizes,
      width: controlSizes,
      minHeight: controlSizes,
      minWidth: controlSizes,
      boxShadow: {
        wb: 'var(--wb-shadow)',
        dialog: 'var(--wb-shadow-dialog)',
        focus: 'var(--wb-focus-ring)',
      },
      fontSize: {
        '2xs': ['11px', '15px'],
      },
      animation: {
        'fade-in': 'fadeIn 0.2s ease-in-out',
        'slide-in': 'slideIn 0.3s ease-out',
        'spin-slow': 'spin 3s linear infinite',
      },
      keyframes: {
        fadeIn: {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' },
        },
        slideIn: {
          '0%': { transform: 'translateY(-10px)', opacity: '0' },
          '100%': { transform: 'translateY(0)', opacity: '1' },
        }
      }
    },
  },
  plugins: [],
}
