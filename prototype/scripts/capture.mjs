// Captures de docs/D0/captures. Prérequis : `npm run dev` (port 5173) ; `npx playwright install chromium` une fois.
// Usage : node scripts/capture.mjs [baseUrl]
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const base = process.argv[2] ?? 'http://localhost:5173';
const out = join(dirname(fileURLToPath(import.meta.url)), '..', '..', 'docs', 'D0', 'captures');
mkdirSync(out, { recursive: true });
const browser = await chromium.launch({ args: ['--use-gl=angle', '--use-angle=swiftshader', '--enable-unsafe-swiftshader', '--ignore-gpu-blocklist'] });

async function settle(page, hasCanvas) {
  await page.waitForLoadState('load');
  if (hasCanvas) await page.waitForSelector('canvas', { timeout: 15000 });
  await page.waitForTimeout(hasCanvas ? 1800 : 600);
}
const results = [];
async function shot(page, theme, name, opts = {}) {
  const file = `${name}-${theme}.png`;
  /* L'application défile dans <main> : on agrandit la fenêtre à la hauteur du contenu pour tout capturer. */
  const need = await page.evaluate(() => { const m = document.querySelector('main'); return m ? Math.ceil(m.getBoundingClientRect().top + m.scrollHeight + 16) : 0; });
  const h = Math.min(Math.max(900, need), 2600);
  if (h !== page.viewportSize().height) { await page.setViewportSize({ width: 1440, height: h }); await page.waitForTimeout(900); }
  await page.screenshot({ path: join(out, file) });
  if (h !== 900) await page.setViewportSize({ width: 1440, height: 900 });
  const n = await page.locator('canvas').count();
  results.push(`${file}${n ? ` (canvas 3D : ${n})` : ''}`);
}

for (const theme of ['clair', 'sombre']) {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, reducedMotion: 'reduce' });
  await ctx.addInitScript((t) => localStorage.setItem('atelier-theme', t), theme === 'clair' ? 'light' : 'dark');
  const page = await ctx.newPage();
  const go = async (path, hasCanvas) => { await page.goto(base + path); await settle(page, hasCanvas); };

  await go('/'); await shot(page, theme, '01-accueil');
  await go('/bibliotheque'); await shot(page, theme, '02-bibliotheque');
  await go('/bibliotheque'); await page.getByRole('searchbox').fill('pince'); await page.waitForTimeout(300); await shot(page, theme, '02b-bibliotheque-recherche');
  await go('/modele/m01', true); await shot(page, theme, '03-modele');
  await go('/preparation', true); await shot(page, theme, '04-preparation');
  await go('/comparaison', true); await shot(page, theme, '05-comparaison');
  await go('/materiaux'); await shot(page, theme, '06-materiaux');
  await go('/design'); await shot(page, theme, '08-design');

  /* Impression : parcours simulé jusqu'à la confirmation puis au suivi. */
  await go('/impression'); await shot(page, theme, '07a-impression-brouillon');
  if (theme === 'clair') {
    await page.getByRole('button', { name: 'Préparer le fichier' }).click();
    await page.getByLabel('Affectation matière').selectOption({ index: 1 });
    await page.getByRole('button', { name: /Lancer le slicing/ }).click();
    await page.getByRole('button', { name: /Envoyer sur la P1S/ }).click({ timeout: 30000 });
    await page.getByRole('button', { name: /Demander la confirmation/ }).click({ timeout: 30000 });
    await page.waitForTimeout(500); await shot(page, theme, '07b-impression-confirmation');
    await page.getByLabel(/plateau est vide/).check();
    await page.getByRole('button', { name: /Oui, lancer/ }).click();
    await page.waitForTimeout(1500); await shot(page, theme, '07c-impression-suivi');
  }

  /* Vue compacte Claude : 4 tours. */
  await go('/claude', false);
  const tabs = ['1 · Recherche', '2 · Fiche', '3 · Comparaison', '4 · Envoi'];
  for (let i = 0; i < 4; i++) {
    await page.getByRole('tab', { name: tabs[i] }).click();
    if (i === 1) await page.waitForSelector('canvas'); await page.waitForTimeout(i === 1 || i === 2 ? 2000 : 700);
    await shot(page, theme, `09-claude-tour${i + 1}`);
  }
  await ctx.close();
}
await browser.close();
console.log(results.join('\n'));
