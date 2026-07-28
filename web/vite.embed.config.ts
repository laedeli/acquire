import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// A second build producing the EMBED bundle: one self-contained ES module the
// portal shell imports at runtime and mounts into its own page.
//
// It is emitted alongside the standalone SPA (into embed/) and served by the
// same binary, so the shell reaches it through the portal's app proxy at
// /api/portal/apps/acquire/embed/console.js.
export default defineConfig({
  plugins: [react()],
  define: { 'process.env.NODE_ENV': '"production"' },
  build: {
    outDir: '../internal/httpapi/web/embed',
    emptyOutDir: true,
    // The shell loads this with a plain dynamic import(), so the CSS has to
    // come along inside the module rather than as a separate <link>.
    cssCodeSplit: false,
    lib: {
      entry: 'src/mount.tsx',
      formats: ['es'],
      fileName: () => 'console.js',
    },
    rollupOptions: {
      output: { inlineDynamicImports: true, assetFileNames: 'console.[ext]' },
    },
  },
})
