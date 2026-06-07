import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
      '@/components': path.resolve(__dirname, './src/components'),
      '@/pages': path.resolve(__dirname, './src/pages'),
      '@/hooks': path.resolve(__dirname, './src/hooks'),
      '@/store': path.resolve(__dirname, './src/store'),
      '@/services': path.resolve(__dirname, './src/services'),
      '@/types': path.resolve(__dirname, './src/types'),
      '@/utils': path.resolve(__dirname, './src/utils'),
    },
  },
  server: {
    port: 3000,
    host: true,
    proxy: {
      // 代理到 sniffy 管理 API(headless 默认 8888);路径含 /api 前缀,不要 rewrite。
      // 注意:app 默认直接以绝对地址访问 VITE_API_BASE_URL(默认 http://localhost:8888),
      // 此 dev 代理仅服务于把前端打到同源 /api 的场景。
      '/api': {
        target: 'http://localhost:8888',
        changeOrigin: true,
      },
      '/api/ws': {
        target: 'ws://localhost:8888',
        ws: true,
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    // 关闭生产 sourcemap:打包 monaco-editor 后体量很大,生成 sourcemap 会显著抬高内存占用。
    sourcemap: false,
    rollupOptions: {
      output: {
        manualChunks: {
          vendor: ['react', 'react-dom'],
          router: ['react-router-dom'],
          query: ['@tanstack/react-query'],
          utils: ['axios', 'dayjs', 'clsx'],
          monaco: ['monaco-editor'],
        },
      },
    },
  },
})
