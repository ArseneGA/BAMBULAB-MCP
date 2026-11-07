// Parcours web du plateau multi-pièces contre un vrai bambud (+ Bambu Studio pour la découpe) :
// import de deux pièces -> Préparation -> composer un plateau (quantités, réglages par pièce) -> rapport « Tient » -> aperçu
// du plateau (image) -> découpe -> une impression par plaque, VALIDATED, matière par pièce cohérente avec les réglages.
// Usage : IMPORT_PATH=<dossier sous une racine d'import, accessible en écriture> node scripts/e2e-plates.mjs
//   (web : WEB_URL, défaut http://localhost:7878 ; navigateur : CHANNEL, défaut msedge ; CAPTURE_DIR pour garder les captures)
import { chromium } from 'playwright';
import assert from 'node:assert/strict';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const web = process.env.WEB_URL ?? 'http://localhost:7878';
const importPath = process.env.IMPORT_PATH;
assert(importPath, 'IMPORT_PATH requis');
const step = (m) => console.log('•', m);

// deux pièces : un bloc 90x60x20 (« matrice ») et un cube 30 mm (« poussoir »), STL ASCII
function box(name, x, y, z) {
  const v = [[0, 0, 0], [x, 0, 0], [x, y, 0], [0, y, 0], [0, 0, z], [x, 0, z], [x, y, z], [0, y, z]];
  const f = [[0, 2, 1], [0, 3, 2], [4, 5, 6], [4, 6, 7], [0, 1, 5], [0, 5, 4], [1, 2, 6], [1, 6, 5], [2, 3, 7], [2, 7, 6], [3, 0, 4], [3, 4, 7]];
  let s = `solid ${name}\n`;
  for (const t of f) {
    const [a, b, c] = t.map((i) => v[i]);
    const u = [b[0] - a[0], b[1] - a[1], b[2] - a[2]], w = [c[0] - a[0], c[1] - a[1], c[2] - a[2]];
    const n = [u[1] * w[2] - u[2] * w[1], u[2] * w[0] - u[0] * w[2], u[0] * w[1] - u[1] * w[0]];
    const l = Math.hypot(...n) || 1;
    s += `facet normal ${n.map((q) => q / l).join(' ')}\n outer loop\n${[a, b, c].map((p) => `  vertex ${p.join(' ')}\n`).join('')} endloop\nendfacet\n`;
  }
  return s + `endsolid ${name}\n`;
}
mkdirSync(importPath, { recursive: true });
writeFileSync(join(importPath, 'matrice-e2e.stl'), box('matrice', 90, 60, 20));
writeFileSync(join(importPath, 'poussoir-e2e.stl'), box('poussoir', 30, 30, 30));

const afetch = (u, o = {}) => fetch(u, process.env.API_TOKEN ? { ...o, headers: { ...o.headers, Authorization: `Bearer ${process.env.API_TOKEN}` } } : o);
const browser = await chromium.launch({ channel: process.env.CHANNEL ?? 'msedge' });
const page = await (await browser.newContext({ viewport: { width: 1440, height: 1000 } })).newPage();
const errs = []; page.on('pageerror', (e) => errs.push(e.message));
const snap = async (n) => { if (process.env.CAPTURE_DIR) await page.screenshot({ path: `${process.env.CAPTURE_DIR}/plateau-${n}.png`, fullPage: true }); };

// import via l'interface
await page.goto(`${web}/bibliotheque`);
await page.getByRole('button', { name: 'Importer' }).first().click();
await page.getByLabel(/Dossier ou fichier/).fill(importPath);
await page.getByRole('button', { name: /Inventorier/ }).click();
await page.getByText('Fichiers 3D').waitFor();
await page.getByRole('button', { name: /^Importer \d+ fichier/ }).click();
await page.getByText('Terminé', { exact: true }).waitFor({ timeout: 30000 });
await page.getByRole('button', { name: 'Fermer' }).last().click();
step('deux pièces importées');

// une imprimante P1S (adresse privée : rien n'est contacté pour planifier ou découper)
let printers = (await (await afetch(`${web}/api/printers`)).json()).printers;
if (printers.length === 0) {
  const models = (await (await afetch(`${web}/api/printer-models`)).json()).models;
  const p1s = models.find((m) => m.id === 'p1s');
  assert(p1s, 'modèle P1S absent des profils Bambu Studio');
  const r = await afetch(`${web}/api/printers`, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Atelier-Request': '1' }, body: JSON.stringify({ name: 'P1S atelier', model_id: 'p1s', host: '192.168.1.77', serial: '01P00A000000000', nozzle_mm: 0.4 }) });
  assert(r.ok, `ajout d'imprimante : ${r.status} ${await r.text()}`);
  printers = (await (await afetch(`${web}/api/printers`)).json()).printers;
}
step(`imprimante : ${printers[0].name}`);

// Préparation sans modèle choisi : le panneau « Plateau de plusieurs pièces » est là
await page.goto(`${web}/preparation`);
await page.getByRole('heading', { name: 'Plateau de plusieurs pièces' }).waitFor();
const panel = page.locator('section.panel', { has: page.getByRole('heading', { name: 'Plateau de plusieurs pièces' }) });
assert.equal(await page.getByText('Limite connue').count(), 0, 'plus aucune limite connue sur la Préparation');

const choose = async (item, label) => {
  const sel = item.getByRole('combobox').first();
  const value = await sel.locator('option', { hasText: label }).first().getAttribute('value');
  await sel.selectOption(value);
};
const rows = () => panel.getByRole('listitem');
await choose(rows().nth(0), 'matrice-e2e');
await rows().nth(0).getByLabel('Quantité').fill('4');
await rows().nth(0).getByLabel('Remplissage (%)').fill('100');
await panel.getByRole('button', { name: 'Ajouter une pièce' }).click();
await choose(rows().nth(1), 'poussoir-e2e');
await rows().nth(1).getByLabel('Quantité').fill('2');
await rows().nth(1).getByLabel('Remplissage (%)').fill('15');
await rows().nth(1).getByLabel('Nom affiché').fill('poussoir');

// entrée invalide : refusée avant tout appel
await rows().nth(1).getByLabel('Remplissage (%)').fill('140');
await panel.getByText('Remplissage : 0 à 100 %.').waitFor();
assert(await panel.getByRole('button', { name: 'Calculer le plateau' }).isDisabled());
await rows().nth(1).getByLabel('Remplissage (%)').fill('15');
step('réglage invalide signalé, bouton bloqué');

await panel.getByRole('button', { name: 'Calculer le plateau' }).click();
await panel.getByRole('heading', { name: 'Tient', exact: true }).waitFor({ timeout: 60000 });
const img = panel.locator('img.pl-img');
await img.waitFor({ timeout: 30000 });
await page.waitForFunction(() => { const i = document.querySelector('img.pl-img'); return i && i.complete && i.naturalWidth > 400; });
const dims = await img.evaluate((i) => [i.naturalWidth, i.naturalHeight]);
assert(dims[0] >= 800 && dims[0] <= 1100 && dims[1] < 2000, `taille d'aperçu : ${dims}`);
assert.match(await img.getAttribute('alt'), /Plateau Bambu Lab P1S/);
await snap('1-plan');
step(`rapport « Tient » et aperçu du plateau (${dims[0]}x${dims[1]})`);

await panel.getByRole('button', { name: /^Découper \(\d+ impression/ }).click();
await panel.getByText('Découpe en file').waitFor();
const plans = (await (await afetch(`${web}/api/plate-plans`)).json()).plans;
const plan = plans[0];
assert(plan.fits && plan.jobs?.length === plan.plates.length, 'une impression par plaque');
step(`${plan.jobs.length} impression(s) créée(s) pour ${plan.objects.length} pièces`);

// la découpe réelle aboutit ; la matière par pièce reflète les réglages (100 % contre 15 %)
let dense = 0, light = 0;
for (const j of plan.jobs) {
  let job;
  for (let i = 0; i < 120; i++) {
    job = (await (await afetch(`${web}/api/print-jobs/${j.print_job_id}`)).json()).job;
    if (['VALIDATED', 'FAILED'].includes(job.state)) break;
    await new Promise((r) => setTimeout(r, 2000));
  }
  assert.equal(job.state, 'VALIDATED', `plaque ${j.plate} : ${job.state} ${job.last_error ?? ''}`);
  assert.equal(job.plate, j.plate);
  for (const o of job.summary.objects) { if (o.name.startsWith('matrice')) dense += o.grams; if (o.name.startsWith('poussoir')) light += o.grams; }
}
assert(dense / 4 > 5 * (light / 2), `matière par pièce : ${dense / 4} g contre ${light / 2} g`);
step(`découpe réelle : matrice 100 % ${(dense / 4).toFixed(1)} g, poussoir 15 % ${(light / 2).toFixed(1)} g par pièce`);
assert.deepEqual(errs, [], 'aucune erreur JS dans la page');
await browser.close();
console.log('OK');
