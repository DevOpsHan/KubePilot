import path from 'node:path'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: 5174,
    host: true, // 同时监听 IPv4/IPv6,localhost 与 127.0.0.1 均可访问
    // 本地开发时将 API 请求代理到运行中的 Go 后端。
    proxy: {
      // ws: true 让 Pod 终端 / 日志跟随的 WebSocket 也能经 dev 代理转发到后端。
      '/api': { target: 'http://127.0.0.1:8080', changeOrigin: true, ws: true },
      '/health': { target: 'http://127.0.0.1:8080', changeOrigin: true },
    },
  },
})
