import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { viteSingleFile } from 'vite-plugin-singlefile';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

// Bundle HTML unique (JS, CSS et Three.js en ligne) : aucune ressource externe, CSP de la vue vide.
const target = process.env.BAMBU_URL ?? 'http://127.0.0.1:7878';
const here = dirname(fileURLToPath(import.meta.url));
export default defineConfig({
  root: here,
  plugins: [react(), viteSingleFile()],
  server: { port: 5175, strictPort: true, proxy: { '/mcp': { target, changeOrigin: true } } },
  build: {
    outDir: resolve(here, 'dist'), emptyOutDir: true, chunkSizeWarningLimit: 4000,
    rollupOptions: { input: resolve(here, 'mcp-app.html') },
  },
});
