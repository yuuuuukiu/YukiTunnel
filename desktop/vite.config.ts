import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';
import path from 'node:path';

const apiProxy = {
  '/api': {
    target: process.env.YUKI_DEV_SERVER ?? 'https://127.0.0.1:18443',
    changeOrigin: true,
    secure: false,
  },
};

// The desktop app reuses the WebUI's API client, components and pages through
// the @web alias so that both surfaces stay visually and behaviourally aligned.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@web': path.resolve(import.meta.dirname, '../web/src'),
    },
    // web/src is imported through the @web alias; without dedupe Vite would
    // resolve its `react` imports from web/node_modules and create a second
    // React instance ("invalid hook call" / null useState).
    dedupe: ['react', 'react-dom', 'react-router-dom', '@tanstack/react-query'],
  },
  server: {
    port: 1420,
    strictPort: true,
    proxy: apiProxy,
  },
  // `pnpm preview` is used for design review in a normal browser; it needs the
  // same API proxy as the dev server.
  preview: { port: 4173, proxy: apiProxy },
  clearScreen: false,
  build: { outDir: 'dist', target: 'safari15' },
});
