import preact from '@preact/preset-vite';
import { writeFileSync } from 'node:fs';
import { defineConfig } from 'vite';

// The Go server the dev server forwards to. Its Origin and Host checks stay on,
// so the proxy rewrites both to the server's.
const target = process.env.ADMIRAL_SERVER ?? 'http://127.0.0.1:7420';

const forward = {
  target,
  changeOrigin: true,
  configure: (proxy: { on: (event: string, fn: (req: { setHeader: (k: string, v: string) => void }) => void) => void }) => {
    proxy.on('proxyReq', (req) => req.setHeader('Origin', target));
    proxy.on('proxyReqWs', (req) => req.setHeader('Origin', target));
  },
};

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
    proxy: { '/api': forward, '/ws': { ...forward, ws: true } },
  },
});
