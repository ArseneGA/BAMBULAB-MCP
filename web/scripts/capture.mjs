// Captures de contrôle de la web app branchée sur un vrai bambud (jalon 3).
// Prérequis : bambud lancé avec le catalogue de démonstration (bambud seed-demo) sur BAMBU_URL (défaut http://127.0.0.1:7878),
// et pour la MCP App : `npx vite --config mcp-app/vite.config.ts` (http://localhost:5175).
// Navigateur : Chrome ou Edge installé (aucun téléchargement) ; CHANNEL=chrome|msedge.
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const out = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'docs', 'J3', 'captures');
mkdirSync(out, { recursive: true });
const base = process.env.BAMBU_URL ?? 'http://127.0.0.1:7878';
const host = process.env.HOST_URL ?? 'http://localhost:5175';
const browser = await chromium.launch({ channel: process.env.CHANNEL ?? 'msedge', args: ['--use-gl=angle', '--use-angle=swiftshader', '--enable-unsafe-swiftshader', '--ignore-gpu-blocklist'] });
const errors = [];

async function shot(page, name) { await page.screenshot({ path: join(out, `${name}.png`) }); console.log('capture', name); }
async function theme(page, t) { await page.evaluate((x) => { document.documentElement.dataset.theme = x; try { localStorage.setItem('atelier-theme', x); } catch { /* ignore */ } }, t); await page.waitForTimeout(250); }

const list = await (await fetch(`${base}/api/models?limit=50`)).json();
const pick = list.models.find((m) => m.name.includes('pince')) ?? list.models[0];

for (const [w, h] of [[1440, 900], [800, 700]]) {
  const ctx = await browser.newContext({ viewport: { width: w, height: h } });
  const page = await ctx.newPage();
  page.on('console', (m) => { if (m.type() === 'error') errors.push(`${w}: ${m.text()}`); });
  page.on('pageerror', (e) => errors.push(`${w}: ${e.message}`));
  for (const t of ['light', 'dark']) {
    await page.goto(`${base}/bibliotheque`); await theme(page, t);
    await page.waitForSelector('.rows');
    await shot(page, `bibliotheque-${w}-${t}`);
    await page.goto(`${base}/modele/${pick.id}`); await theme(page, t);
    await page.waitForSelector('.v3-bar', { timeout: 20000 });
    await page.waitForTimeout(600);
    await shot(page, `modele-${w}-${t}`);
  }
  if (w === 1440) {
    await page.goto(`${base}/bibliotheque`); await theme(page, 'light');
    await page.getByRole('button', { name: 'Importer' }).first().click();
    await page.waitForTimeout(300); await shot(page, 'import-dialogue-1440-light');
    await page.keyboard.press('Escape');
    await page.goto(`${base}/corbeille`); await page.waitForTimeout(500); await shot(page, 'corbeille-1440-light');
    await page.goto(`${base}/preparation`); await page.waitForTimeout(800); await shot(page, 'preparation-bientot-1440-light');
  }
  await ctx.close();
}

// État « bambud injoignable » : requêtes API coupées.
{
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await ctx.newPage();
  await page.route('**/api/**', (r) => r.abort());
  await page.route('**/health', (r) => r.abort());
  await page.goto(`${base}/bibliotheque`); await page.waitForTimeout(1500);
  await shot(page, 'hors-ligne-1440-light');
  await ctx.close();
}

// Hôte MCP Apps simulé : résultats puis fiche avec viewer 3D réel.
try {
  const ctx = await browser.newContext({ viewport: { width: 1100, height: 760 } });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => errors.push(`hôte: ${e.message}`));
  await page.goto(`${host}/dev-host.html`);
  await page.waitForFunction(() => window.__host);
  await page.evaluate(() => window.__host.run('search_models', { limit: 5 }));
  const fr = page.frameLocator('iframe');
  await fr.locator('.results').waitFor({ timeout: 15000 });
  await shot(page, 'mcp-app-resultats-light');
  await fr.locator('.result').first().click();
  await fr.locator('.v3-bar').waitFor({ timeout: 20000 });
  await page.waitForTimeout(600);
  await shot(page, 'mcp-app-fiche-light');
  await fr.locator('.parts button').first().click();
  await page.click('#theme'); await page.waitForTimeout(700);
  await shot(page, 'mcp-app-selection-dark');
  await ctx.close();
} catch (e) { console.warn('MCP App non capturée :', e.message); }

await browser.close();
console.log(errors.length ? `Erreurs console :\n${errors.join('\n')}` : 'Aucune erreur console.');
