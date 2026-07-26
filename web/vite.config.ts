import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The console is served by the acquire binary itself (go:embed), mounted at the
// deployment's base path — '/acquire/' on the reference host. Relative asset
// URLs keep it working wherever it is mounted.
export default defineConfig({
  plugins: [react()],
  base: './',
  build: {
    outDir: '../internal/httpapi/web',
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    // `npm run dev` against a locally running acquire.
    proxy: { '/api': 'http://localhost:8080' },
  },
})
