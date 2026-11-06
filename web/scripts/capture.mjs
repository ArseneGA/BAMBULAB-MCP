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
    await page.goto(`${base}/materiaux`); await page.waitForTimeout(800); await shot(page, 'materiaux-bientot-1440-light');
    await page.goto(`${base}/impression`); await page.waitForTimeout(800); await shot(page, 'impression-bientot-1440-light');
  }
  // Préparation et comparaison réelles (objectif -> critères validés -> expérience -> découpes Bambu Studio).
  if ((await (await fetch(`${base}/api/capabilities`)).json()).features.find((f) => f.id === 'slicing')?.available) {
    const cale = list.models.find((m) => m.name.includes('cale')) ?? list.models[0];
    await page.goto(`${base}/preparation?model=${cale.id}`); await theme(page, 'light');
    await page.waitForSelector('.prep-text');
    await page.getByPlaceholder(/solide mais rapide/).fill('solide mais rapide à imprimer');
    for (const c of ['strength', 'speed']) { await page.getByLabel('Ajouter un critère').selectOption(c); await page.getByRole('button', { name: 'Ajouter', exact: true }).click(); }
    await page.locator('.prep-row').first().getByLabel(/^load_direction/).selectOption('xy');
    await page.getByRole('button', { name: "Valider l'objectif" }).click();
    await page.getByText('Objectif validé').waitFor();
    await page.evaluate(() => document.querySelector('.main').scrollTo(0, 0)); await page.waitForTimeout(400); await shot(page, `preparation-${w}-light`);
    await page.getByText('Objectif validé').scrollIntoViewIfNeeded(); await page.waitForTimeout(300); await shot(page, `preparation-objectif-${w}-light`);
    await page.evaluate(() => document.querySelector('.main').scrollTo(0, 0));
    await page.getByLabel(/Nombre de variantes/).selectOption('3');
    await page.getByRole('button', { name: /Créer l'expérience/ }).click();
    await page.waitForURL(/comparaison/);
    await page.waitForFunction(() => document.querySelectorAll('.cmp thead .tag-ok').length >= 4, null, { timeout: 180000 });
    for (const t of ['light', 'dark']) { await theme(page, t); await page.waitForTimeout(300); await shot(page, `comparaison-${w}-${t}`); }
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

// Impression : registre, état, étapes, confirmation (simulateur d'imprimante requis) puis vue MCP App de confirmation.
try {
  const prs = (await (await fetch(`${base}/api/printers`)).json()).printers;
  if (prs.length === 0) throw new Error('aucune imprimante enregistrée (lancer le simulateur puis e2e.mjs avec SIM_PIN)');
  const pr = prs[prs.length - 1];
  const j = async (m, p, b) => { const r = await fetch(base + p, { method: m, headers: { 'Content-Type': 'application/json' }, body: b ? JSON.stringify(b) : undefined }); return r.json(); };
  // Imprimante au repos (le simulateur imprime pendant quelques secondes après un lancement).
  for (let i = 0; i < 40; i++) { const s = await j('GET', `/api/printers/${pr.id}/status`); if (s.reachable && !['RUNNING', 'PREPARE', 'PAUSE'].includes(s.gcode_state ?? '')) break; await new Promise((r) => setTimeout(r, 3000)); }
  const cale = list.models.find((m) => m.name.includes('cale')) ?? list.models[0];
  let job = await j('POST', '/api/print-jobs', { model_id: cale.id, printer_id: pr.id, plate: 1, idempotency_key: `cap-${Date.now()}` });
  for (let i = 0; i < 90 && job.state !== 'VALIDATED'; i++) { await new Promise((r) => setTimeout(r, 2000)); job = (await j('GET', `/api/print-jobs/${job.id}`)).job; }
  const assignments = [{ filament_slot: 1, tray: 'ext', material_id: 'PLA Basic gris' }];
  await j('POST', '/api/print-jobs/validate', { print_job_id: job.id, printer_id: pr.id, assignments });
  const up = await j('POST', '/api/print-jobs/upload', { print_job_id: job.id, printer_id: pr.id, idempotency_key: `cap-up-${Date.now()}`, assignments });
  for (let i = 0; i < 60; i++) { job = (await j('GET', `/api/print-jobs/${job.id}`)).job; if (['UPLOADED', 'AWAITING_PRINT_CONFIRMATION'].includes(job.state)) break; await new Promise((r) => setTimeout(r, 2000)); }
  void up;
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await ctx.newPage();
  await page.goto(`${base}/impression`); await theme(page, 'light');
  await page.getByText('Joignable').waitFor({ timeout: 20000 });
  await page.getByRole('listbox', { name: 'Impressions' }).getByRole('option').first().click();
  await page.getByRole('button', { name: /Demander la confirmation/ }).waitFor();
  await page.waitForTimeout(400); await shot(page, 'impression-1440-light');
  await page.getByRole('button', { name: /Demander la confirmation/ }).click();
  await page.getByRole('link', { name: /Ouvrir la page de confirmation/ }).waitFor();
  await page.waitForTimeout(400); await theme(page, 'dark'); await shot(page, 'impression-confirmation-1440-dark');
  await page.goto(`${base}/materiaux`); await theme(page, 'light'); await page.waitForTimeout(800); await shot(page, 'materiaux-machines-1440-light');
  await ctx.close();
  // Vue MCP App : nouvelle impression déposée, demande de confirmation par l'outil, puis accord depuis la vue.
  const j2 = await j('POST', '/api/print-jobs', { model_id: cale.id, printer_id: pr.id, plate: 1, idempotency_key: `cap2-${Date.now()}` });
  let job2 = j2;
  for (let i = 0; i < 90 && job2.state !== 'VALIDATED'; i++) { await new Promise((r) => setTimeout(r, 2000)); job2 = (await j('GET', `/api/print-jobs/${j2.id}`)).job; }
  await j('POST', '/api/print-jobs/validate', { print_job_id: job2.id, printer_id: pr.id, assignments });
  await j('POST', '/api/print-jobs/upload', { print_job_id: job2.id, printer_id: pr.id, idempotency_key: `cap2-up-${Date.now()}`, assignments });
  for (let i = 0; i < 60; i++) { job2 = (await j('GET', `/api/print-jobs/${j2.id}`)).job; if (['UPLOADED', 'AWAITING_PRINT_CONFIRMATION'].includes(job2.state)) break; await new Promise((r) => setTimeout(r, 2000)); }
  const hctx = await browser.newContext({ viewport: { width: 1100, height: 760 } });
  const hp = await hctx.newPage();
  await hp.goto(`${host}/dev-host.html`);
  await hp.waitForFunction(() => window.__host);
  await hp.evaluate((id) => window.__host.run('request_print_confirmation', { print_job_id: id }), job2.id);
  const fr = hp.frameLocator('iframe');
  await fr.getByRole('heading', { name: 'Confirmation de lancement' }).waitFor({ timeout: 15000 });
  await hp.waitForTimeout(500); await shot(hp, 'mcp-app-confirmation-light');
  await hp.click('#theme'); await hp.waitForTimeout(600); await shot(hp, 'mcp-app-confirmation-lecture-seule-dark');
  await hctx.close();
} catch (e) { console.warn('Captures impression ignorées :', e.message); }

await browser.close();
console.log(errors.length ? `Erreurs console :\n${errors.join('\n')}` : 'Aucune erreur console.');
