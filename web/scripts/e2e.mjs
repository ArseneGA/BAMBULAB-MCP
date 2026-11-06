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

// Objectif -> expérience -> comparaison réelle -> application d'une variante (Bambu Studio requis).
const caps = await (await fetch(`${web}/api/capabilities`)).json();
if (caps.features.find((f) => f.id === 'slicing')?.available) {
  const cale = (await (await fetch(`${web}/api/models?q=cale`)).json()).models[0];
  await page.goto(`${web}/modele/${cale.id}`);
  await page.getByRole('button', { name: 'Préparer' }).click();
  await page.waitForURL(/preparation\?model=/);
  await page.getByPlaceholder(/solide mais rapide/).fill('rapide à imprimer');
  await page.getByLabel('Ajouter un critère').selectOption('speed');
  await page.getByRole('button', { name: 'Ajouter', exact: true }).click();
  await page.getByRole('button', { name: "Valider l'objectif" }).click();
  await page.getByText('Objectif validé').waitFor();
  step('objectif validé par le serveur');
  await page.getByLabel(/Nombre de variantes/).selectOption('2');
  await page.getByRole('button', { name: /Créer l'expérience/ }).click();
  await page.waitForURL(/comparaison\?experiment=/);
  await page.getByRole('table', { name: 'Variantes comparées' }).waitFor();
  await page.getByText('terminée').first().waitFor({ timeout: 120000 });
  await page.waitForFunction(() => document.querySelectorAll('.cmp thead .tag-ok').length >= 3, null, { timeout: 120000 });
  step('comparaison réelle : 3 découpes terminées');
  await page.locator('.cmp tbody tr:last-child button').nth(1).click();
  await page.getByRole('button', { name: 'Appliquer', exact: true }).last().click();
  await page.getByText(/appliquée/).waitFor();
  step('variante appliquée');
}

// Impression contre le simulateur (go run ./cmd/printer-sim) : SIM_PIN = empreinte du certificat affichée par le simulateur.
if (process.env.SIM_PIN && caps.features.find((f) => f.id === 'print_chain')?.available) {
  const sim = process.env.SIM_CODE ?? '12345678'; // code d'accès par défaut du simulateur local (valeur de test du dépôt)
  await page.goto(`${web}/impression`);
  if ((await (await fetch(`${web}/api/printers`)).json()).printers.length === 0) {
    await page.getByRole('button', { name: 'Ajouter', exact: true }).first().click();
    await page.getByLabel("Nom d'usage").fill('Simulateur');
    await page.getByRole('dialog').locator('select').first().selectOption('p1s');
    await page.getByLabel(/Adresse sur le réseau/).fill('127.0.0.1');
    await page.getByLabel('Numéro de série').fill('01P00A000000001');
    await page.getByLabel(/Empreinte SHA-256/).fill(process.env.SIM_PIN);
    await page.getByRole('dialog').getByRole('button', { name: 'Ajouter', exact: true }).click();
  }
  const link = page.getByRole('link', { name: /Ouvrir la page de saisie/ });
  if (await link.count()) {
    const setup = await (await browser.newContext()).newPage();
    await setup.goto(new URL(await link.getAttribute('href'), web).toString().replace(web, process.env.BAMBU_URL ?? 'http://127.0.0.1:7878'));
    await setup.getByLabel(/Code d'accès/).fill(sim);
    await setup.getByRole('button', { name: 'Enregistrer' }).click();
    await setup.getByText(/enregistré dans le coffre/).waitFor();
    step("code d'accès saisi sur la page locale");
  }
  await page.reload();
  await page.getByText('Joignable').waitFor({ timeout: 20000 });
  step('imprimante joignable (état lu)');
  const cale = (await (await fetch(`${web}/api/models?q=cale`)).json()).models[0];
  await page.getByLabel(/^Modèle \(dernière/).selectOption(cale.id);
  await page.locator('.prt-new select').nth(1).selectOption({ index: 1 });
  await page.getByRole('button', { name: /Découper pour cette imprimante/ }).click();
  await page.getByText(/Valider pour l'imprimante/).waitFor({ timeout: 180000 });
  await page.getByLabel(/^Matière du filament 1/).fill('PLA Basic gris');
  await page.getByRole('button', { name: "Valider pour l'imprimante" }).click();
  await page.getByText('validé', { exact: true }).waitFor({ timeout: 60000 });
  await page.getByRole('button', { name: "Envoyer sur l'imprimante" }).click();
  await page.getByRole('button', { name: /Demander la confirmation/ }).waitFor({ timeout: 90000 });
  step('fichier déposé sur le simulateur');
  await page.getByRole('button', { name: /Demander la confirmation/ }).click();
  const href = await page.getByRole('link', { name: /Ouvrir la page de confirmation/ }).getAttribute('href');
  assert(!(await page.getByRole('button', { name: /Lancer l'impression/ }).count()), "aucun bouton de lancement avant l'accord humain");
  step('confirmation demandée : pas de bouton de lancement tant que la page serveur n\'a pas été validée');
  const conf = await (await browser.newContext()).newPage();
  await conf.goto(new URL(href, process.env.BAMBU_URL ?? 'http://127.0.0.1:7878').toString());
  await conf.getByLabel(/plateau est vide/).check();
  await conf.getByRole('button', { name: "Lancer l'impression" }).click();
  await conf.getByText(/Lancement autorisé/).waitFor();
  await page.getByRole('button', { name: /Lancer l'impression \(autorisée\)/ }).waitFor({ timeout: 15000 });
  await page.getByRole('button', { name: /Lancer l'impression \(autorisée\)/ }).click();
  await page.getByText(/Lancement :/).waitFor({ timeout: 30000 });
  step('impression lancée après accord sur la page de confirmation');
}

await browser.close();
assert.deepEqual(errs, [], `erreurs de page : ${errs.join(' | ')}`);
console.log('E2E OK');
