import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Développement : l'interface (port 5174) relaie l'API et le MCP vers bambud (127.0.0.1:7878).
const target = process.env.BAMBU_URL ?? 'http://127.0.0.1:7878';
export default defineConfig({
  plugins: [react()],
  build: { outDir: 'dist', emptyOutDir: true, chunkSizeWarningLimit: 900 },
  server: { port: 5174, strictPort: true, proxy: { '/api': target, '/health': target, '/mcp': target } },
});
