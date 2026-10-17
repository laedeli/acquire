import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The harness: a plain page (no federation) that renders a console view on
// made-up data, for screenshots and design review. Run from web/:
//   npm run harness   ->  http://127.0.0.1:5199/
export default defineConfig({
  root: 'harness',
  plugins: [react()],
  server: { host: '127.0.0.1', port: 5199, strictPort: true },
})
