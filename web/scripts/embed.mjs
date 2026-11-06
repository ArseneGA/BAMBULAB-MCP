// Construit les deux bundles (web app + vue MCP App) puis les copie là où le binaire Go les embarque :
//   web/dist                  -> internal/webui/assets/            (interface servie sur /)
//   web/mcp-app/dist/*.html   -> internal/mcpserver/ui/views/      (ressources ui://bambu/*)
// Usage : node scripts/embed.mjs [--skip-build]
import { spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, readdirSync, rmSync, statSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const web = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const root = resolve(web, '..');
const targets = {
  app: join(root, 'internal', 'webui', 'assets'),
  views: join(root, 'internal', 'mcpserver', 'ui', 'views'),
};

function run(args) {
  const r = spawnSync(process.execPath, [join(web, 'node_modules', 'vite', 'bin', 'vite.js'), ...args], { cwd: web, stdio: 'inherit' });
  if (r.status !== 0) { console.error('Échec :', 'vite', args.join(' ')); process.exit(r.status ?? 1); }
}

/** Vide un dossier d'embarquement en gardant .gitkeep (le dossier doit toujours exister pour go:embed). */
function clean(dir) {
  mkdirSync(dir, { recursive: true });
  for (const f of readdirSync(dir)) if (f !== '.gitkeep') rmSync(join(dir, f), { recursive: true, force: true });
}

if (!process.argv.includes('--skip-build')) {
  run(['build']);
  run(['build', '--config', 'mcp-app/vite.config.ts']);
}

clean(targets.app);
cpSync(join(web, 'dist'), targets.app, { recursive: true });
clean(targets.views);
const viewHtml = join(web, 'mcp-app', 'dist', 'mcp-app.html');
if (existsSync(viewHtml)) cpSync(viewHtml, join(targets.views, 'mcp-app.html'));
else console.warn('mcp-app.html absent : la vue MCP App ne sera pas embarquée');

const size = (p) => (statSync(p).size / 1024).toFixed(0);
console.log(`Interface embarquée : ${targets.app}`);
if (existsSync(join(targets.views, 'mcp-app.html'))) console.log(`Vue MCP App embarquée : ${size(join(targets.views, 'mcp-app.html'))} Kio`);
