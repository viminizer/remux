import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The build output is staged into internal/web/dist by scripts/build.sh and
// embedded into the Go binary, so paths must be relative to the server root.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // One chunk keeps the embedded asset count low and the app shell simple
    // for the service worker to cache.
    chunkSizeWarningLimit: 900,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:7399',
      '/ws': { target: 'ws://127.0.0.1:7399', ws: true },
    },
  },
})
