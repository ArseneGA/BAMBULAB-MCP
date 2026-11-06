// Parcours fonctionnel de la web app contre un vrai bambud (import -> opérations avec aperçu -> corbeille -> restauration).
// Usage : IMPORT_PATH=<dossier sous une racine d'import> node scripts/e2e.mjs   (web : WEB_URL, défaut http://localhost:5174)
import { chromium } from 'playwright';
import assert from 'node:assert/strict';

const web = process.env.WEB_URL ?? 'http://localhost:5174';
const importPath = process.env.IMPORT_PATH;
assert(importPath, 'IMPORT_PATH requis');
const browser = await chromium.launch({ channel: process.env.CHANNEL ?? 'msedge' });
const page = await (await browser.newContext({ viewport: { width: 1440, height: 900 } })).newPage();
const errs = []; page.on('pageerror', (e) => errs.push(e.message));
const total = async () => (await (await fetch(`${web}/api/models?limit=1`)).json()).total;
const TAG = `e2e${Date.now()}`;
const step = (m) => console.log('•', m);

await fetch(`${web}/api/trash`); const before = await total();
await page.goto(`${web}/bibliotheque`);
await page.getByRole('button', { name: 'Importer' }).first().click();
await page.getByLabel(/Dossier ou fichier/).fill(importPath);
await page.getByRole('button', { name: /Inventorier/ }).click();
await page.getByText('Fichiers 3D').waitFor();
step('inventaire affiché');
assert.equal(await total(), before, "l'inventaire ne doit rien créer");
await page.getByRole('button', { name: /^Importer \d+ fichier/ }).click();
await page.getByText('Terminé', { exact: true }).waitFor({ timeout: 30000 });
step('import terminé');
assert((await total()) >= before, 'le catalogue ne doit pas diminuer'); step(`catalogue : ${await total()} modèles`);
await page.getByRole('button', { name: 'Fermer' }).last().click();

// Opération groupée avec aperçu : étiquette sur le premier modèle.
await page.goto(`${web}/bibliotheque`);
await page.locator('.row-check').first().check();
await page.getByRole('button', { name: /Ajouter une étiquette/ }).click();
await page.getByRole('textbox', { name: 'Étiquette' }).fill(TAG);
await page.getByRole('button', { name: 'Prévisualiser' }).click();
await page.getByText('Aperçu uniquement').waitFor();
assert.equal((await (await fetch(`${web}/api/models?tag=${TAG}`)).json()).total, 0, "l'aperçu ne doit rien modifier");
await page.getByRole('button', { name: /^Appliquer 1 changement/ }).click();
await page.getByText(/changement.* appliqué/).waitFor();
assert.equal((await (await fetch(`${web}/api/models?tag=${TAG}`)).json()).total, 1);
step('aperçu puis application des étiquettes');
await page.getByRole('button', { name: 'Fermer' }).last().click();

// Corbeille puis restauration.
await page.locator('.row-check').first().check();
await page.getByRole('button', { name: /Corbeille…/ }).click();
await page.getByRole('button', { name: /^Appliquer/ }).click();
await page.getByText(/appliqué/).waitFor();
await page.getByRole('button', { name: 'Fermer' }).last().click();
const trash = await (await fetch(`${web}/api/trash`)).json();
assert.equal(trash.total, 1, 'exactement un modèle doit être en corbeille');
await page.goto(`${web}/corbeille`);
await page.locator('.lib-row').getByRole('button', { name: 'Restaurer' }).first().click();
await page.waitForTimeout(800);
assert.equal((await (await fetch(`${web}/api/trash`)).json()).total, 0);
step('corbeille puis restauration');

await browser.close();
assert.deepEqual(errs, [], `erreurs de page : ${errs.join(' | ')}`);
console.log('E2E OK');
