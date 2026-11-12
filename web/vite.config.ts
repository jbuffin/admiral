import preact from '@preact/preset-vite';
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { defineConfig } from 'vite';

// The Go server the dev server forwards to. The proxy passes the browser's own
// Host and Origin (localhost:<vite port>) through untouched: the server's
// checks accept them, and rewriting them would hide what the checks guard.
const target = process.env.ADMIRAL_SERVER ?? 'http://127.0.0.1:7420';

export default defineConfig({
  plugins: [
    preact(),
    {
      // The new shell's deep links under /app/ fall back to its own index.html,
      // as the Go server does.
      name: 'app-shell-fallback',
      configureServer(server) {
        server.middlewares.use(async (req, res, next) => {
          const url = req.url ?? '';
          const path = url.split('?')[0];
          if (req.method === 'GET' && (path === '/app' || (path.startsWith('/app/') && !/\.[a-z0-9]+$/i.test(path)))) {
            const html = readFileSync(resolve(__dirname, 'app/index.html'), 'utf8');
            res.setHeader('Content-Type', 'text/html');
            res.end(await server.transformIndexHtml('/app/', html));
            return;
          }
          next();
        });
      },
    },
    {
      // emptyOutDir removes the tracked placeholder that keeps go build working.
      name: 'keep-dist-placeholder',
      closeBundle() {
        writeFileSync('dist/.gitkeep', '');
      },
    },
  ],
  build: {
    assetsInlineLimit: 0,
    // Two pages: the old UI at / and the new shell, built to dist/app/index.html.
    rollupOptions: {
      // The worker is built to a fixed /sw.js: a browser finds a new worker by its URL, so it takes no hash.
      input: { main: resolve(__dirname, 'index.html'), app: resolve(__dirname, 'app/index.html'), sw: resolve(__dirname, 'src/app/sw.ts') },
      output: { entryFileNames: (chunk) => (chunk.name === 'sw' ? 'sw.js' : 'assets/[name]-[hash].js') },
    },
  },
  server: {
    proxy: { '/api': { target }, '/ws': { target, ws: true } },
  },
});
