import { defineConfig, mergeConfig } from 'vitest/config'
import viteConfig from './vite.config'

export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: 'jsdom',
      include: ['src/**/*.test.tsx'],
      setupFiles: ['src/test/setup.ts'],
      coverage: {
        provider: 'v8',
        include: [
          'src/workbench/lib/update.ts',
          'src/workbench/lib/updateSync.ts',
          'src/workbench/lib/links.ts',
          'src/workbench/views/AboutView.tsx',
          'src/workbench/shell/StatusBar.tsx',
        ],
        reporter: ['text', 'json', 'lcov', 'html'],
        thresholds: {
          perFile: true,
          statements: 95,
          lines: 95,
          functions: 95,
          branches: 95,
        },
      },
    },
  })
)
