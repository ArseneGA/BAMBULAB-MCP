import { defineConfig } from 'vite';
import type { Plugin } from 'vite';
import react from '@vitejs/plugin-react';
import { fileURLToPath } from 'node:url';
import { PRODUCT_NAME, TAGLINE } from './src/brand';

// Développement : l'interface (port 5174) relaie l'API et le MCP vers bambud (127.0.0.1:7878).
const target = process.env.BAMBU_URL ?? 'http://127.0.0.1:7878';

/** Le nom du produit n'est écrit qu'une fois (src/brand.ts) : index.html en reçoit les jetons %PRODUCT_NAME% et %TAGLINE%. */
const brand = (): Plugin => ({
  name: 'brand-html',
  transformIndexHtml: (html) => html.replaceAll('%PRODUCT_NAME%', PRODUCT_NAME).replaceAll('%TAGLINE%', TAGLINE),
});

export default defineConfig({
  plugins: [react(), brand()],
  resolve: { alias: { '@shared': fileURLToPath(new URL('./shared', import.meta.url)) } },
  build: { outDir: 'dist', emptyOutDir: true, chunkSizeWarningLimit: 900 },
  server: { port: 5174, strictPort: true, proxy: { '/api': target, '/health': target, '/mcp': target } },
});
