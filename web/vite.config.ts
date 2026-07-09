import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

// Wails v3 桌面前端构建配置。
// 产物嵌入 Go 二进制(web/dist),由 Wails 资源服务器在 webview 内提供;
// 不再有网页/headless 形态,故无需 /api 代理(改用 @wailsio/runtime 原生绑定)。
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    // `wails3 dev` 通过 WAILS_VITE_PORT 指定开发端口，并让桌面壳经 FRONTEND_DEVSERVER_URL 代理过来；
    // 独立 `npm run dev` 时无此环境变量，回退 3000。
    port: Number(process.env.WAILS_VITE_PORT) || 3000,
    strictPort: Boolean(process.env.WAILS_VITE_PORT),
    host: true,
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
    rollupOptions: {
      output: {
        manualChunks: {
          vendor: ['react', 'react-dom'],
          router: ['react-router-dom'],
          editor: [
            '@codemirror/state',
            '@codemirror/view',
            '@codemirror/commands',
            '@codemirror/language',
            '@codemirror/autocomplete',
            '@codemirror/search',
            '@codemirror/lang-javascript',
            '@lezer/highlight',
          ],
        },
      },
    },
  },
})
