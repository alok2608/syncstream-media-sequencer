import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
  },
  test: {
    // The playback tests are pure functions; no DOM environment is needed.
    environment: 'node',
    include: ['src/**/*.test.js'],
  },
});
