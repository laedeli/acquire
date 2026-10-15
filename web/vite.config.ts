import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import federation from '@originjs/vite-plugin-federation'

// The console is built as a federated module and nothing else: acquire has no
// page of its own. The portal shell renders the console inside its own React
// tree, as acquire's app — one DOM, one React instance, no iframe, and the
// shell's sign-in. React and react-dom are shared: the shell provides them at
// runtime, so the shell and every app it hosts agree on a single copy (hooks
// and context only work if they do).
//
// The output is served by the acquire binary and reached through the portal's
// app proxy at /api/portal/apps/acquire/embed/assets/remoteEntry.js.
export default defineConfig({
  plugins: [
    react(),
    federation({
      name: 'acquire',
      filename: 'remoteEntry.js',
      exposes: { './Console': './src/Console.tsx' },
      shared: {
        react: { requiredVersion: '^18.3.0' },
        'react-dom': { requiredVersion: '^18.3.0' },
      },
    }),
  ],
  build: {
    outDir: '../internal/httpapi/web/embed',
    emptyOutDir: true,
    // Federation emits ES modules with top-level await.
    target: 'esnext',
    minify: true,
    cssCodeSplit: false,
    rollupOptions: {
      // No HTML page to start from: the federation plugin adds the remote
      // entry, the exposed console and the shared modules as the build's
      // chunks itself.
      input: {},
      output: {
        format: 'es',
        // A predictable stylesheet name: the shell links it when it mounts the
        // app, and it cannot do that if the filename carries a build hash.
        assetFileNames: (info) =>
          info.name && info.name.endsWith('.css') ? 'assets/console.css' : 'assets/[name]-[hash][extname]',
      },
    },
  },
})
