import preact from '@preact/preset-vite';
import { writeFileSync } from 'node:fs';
import { defineConfig } from 'vite';

// The Go server the dev server forwards to. The proxy passes the browser's own
// Host and Origin (localhost:<vite port>) through untouched: the server's
// checks accept them, and rewriting them would hide what the checks guard.
const target = process.env.ADMIRAL_SERVER ?? 'http://127.0.0.1:7420';

export default defineConfig({
  plugins: [
    preact(),
    {
      // emptyOutDir removes the tracked placeholder that keeps go build working.
      name: 'keep-dist-placeholder',
      closeBundle() {
        writeFileSync('dist/.gitkeep', '');
      },
    },
  ],
  build: { assetsInlineLimit: 0 },
  server: {
    proxy: { '/api': { target }, '/ws': { target, ws: true } },
  },
});
