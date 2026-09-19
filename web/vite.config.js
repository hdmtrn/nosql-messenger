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
      // '/invites' does not swallow the page route /invite/{code}: the match is by prefix
      '/invites': api,
      '/messages': api,
      '/users': api,
      '/presence': api,
      '/friends': api,
      '/media': api,
      '/health': api,
      '/ws': { ...api, ws: true },
    },
  },
})
