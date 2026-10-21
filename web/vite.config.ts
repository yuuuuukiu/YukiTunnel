import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// The dev server proxies the API to a locally running yuki-server so that
// `pnpm dev` can talk to https://127.0.0.1:8443 without CORS.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: process.env.YUKI_DEV_SERVER ?? 'https://127.0.0.1:18443',
        changeOrigin: true,
        secure: false,
        ws: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
    chunkSizeWarningLimit: 900,
  },
});
