import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:7681',
      '/ws': { target: 'ws://localhost:7681', ws: true },
    },
  },
  build: {
    outDir: 'dist',
    // emptyOutDir would wipe the dir on every build, including the
    // .gitkeep placeholder we track so go:embed has something to embed
    // in a fresh clone. Vite reuses content-hashed filenames so leaving
    // stale chunks here is fine.
    emptyOutDir: false,
  },
});
