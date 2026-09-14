import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    // Fail loudly instead of silently falling back to 5174 when the port is
    // taken. A different port is a different origin, so the backend's CORS
    // allow-list stops matching and a perfectly healthy backend looks dead.
    // Better to be told the port is busy than to debug a phantom outage.
    strictPort: true,
  },
  test: {
    // The playback tests are pure functions; no DOM environment is needed.
    environment: 'node',
    include: ['src/**/*.test.js'],
  },
});
