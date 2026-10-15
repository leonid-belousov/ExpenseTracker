import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

// In development `npm run dev` serves the app on http://127.0.0.1:5173 and
// proxies /api to the Go backend (API_PROXY_TARGET, default the Compose
// loopback port). In production nginx serves the build and proxies /api.
export default defineConfig({
  plugins: [react()],
  server: {
    host: '127.0.0.1',
    port: 5173,
    proxy: {
      '/api': {
        target: process.env.API_PROXY_TARGET ?? 'http://127.0.0.1:8081',
        changeOrigin: false,
      },
    },
  },
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
});
