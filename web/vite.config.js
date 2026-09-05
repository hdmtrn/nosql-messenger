import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

const api = { target: 'http://localhost:8080', changeOrigin: true }

export default defineConfig({
  plugins: [vue()],
  build: { outDir: 'dist' },
  server: {
    proxy: {
      '/auth': api,
      '/channels': api,
      '/messages': api,
      '/health': api,
      '/ws': { ...api, ws: true },
    },
  },
})
