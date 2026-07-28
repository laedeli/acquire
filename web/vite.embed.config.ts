import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import federation from '@originjs/vite-plugin-federation'

// The REMOTE build.
//
// The console is exposed as a federated module so the portal shell renders it
// inside its own React tree — one DOM, one React instance, no iframe. React and
// react-dom are shared: the shell provides them at runtime, so the shell and
// every app it hosts agree on a single copy (hooks and context only work if
// they do).
//
// The output is served by the acquire binary and reached through the portal's
// app proxy at /api/portal/apps/acquire/embed/remoteEntry.js.
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
