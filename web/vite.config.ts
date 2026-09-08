import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The build output is staged into internal/web/dist by scripts/build.sh and
// embedded into the Go binary, so paths must be relative to the server root.
export default defineConfig({
  plugins: [react()],
  // The version the bundle was built from, so the running app can tell whether
  // it is the build the server is serving. scripts/build.sh exports VERSION;
  // a bare `npm run build` or `npm run dev` has none and gets "dev", which
  // never equals a real server version and so always reports an update.
  define: {
    __BUILD_VERSION__: JSON.stringify(process.env.VERSION ?? 'dev'),
  },
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
