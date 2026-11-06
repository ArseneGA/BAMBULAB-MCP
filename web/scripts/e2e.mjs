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
// CAPTURE_DIR : enregistre des captures du parcours (docs/J3/captures/parcours-*.png).
const snap = async (p, n) => { if (process.env.CAPTURE_DIR) await p.screenshot({ path: `${process.env.CAPTURE_DIR}/parcours-${n}.png` }); };

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
  // Sous-parties du serveur + réglage manuel validé contre le profil réel, avec provenance (modify_slicing_profile).
  const parts = page.getByRole('group', { name: /Objets et sous-parties/ }).getByRole('button');
  await parts.first().waitFor();
  await parts.first().click();
  await page.getByRole('button', { name: 'Ajouter un réglage' }).click();
  await page.getByLabel('Clé du réglage').fill('wall_loops');
  await page.getByLabel('Valeur', { exact: true }).fill('3');
  await page.getByRole('button', { name: 'Valider les réglages' }).click();
  await page.getByRole('table', { name: 'Différences avec la base' }).waitFor();
  assert.match(await page.locator('.set-result').innerText(), /wall_loops/);
  await page.getByRole('button', { name: 'Ajouter un réglage' }).click();
  await page.getByLabel('Clé du réglage').fill('clef_inexistante');
  await page.getByLabel('Valeur', { exact: true }).fill('1');
  await page.getByRole('button', { name: 'Valider les réglages' }).click();
  await page.getByText('Réglage refusé').waitFor();
  await snap(page, '1-preparation-reglages');
  step('sous-parties, réglage manuel avec provenance, réglage invalide refusé');
  await page.getByLabel('Retirer ce réglage').click();
  await page.getByLabel(/Nombre de variantes/).selectOption('2');
  await page.getByRole('button', { name: /Créer l'expérience/ }).click();
  await page.waitForURL(/comparaison\?experiment=/);
  await page.getByRole('table', { name: 'Variantes comparées' }).waitFor();
  await page.getByText('terminée').first().waitFor({ timeout: 120000 });
  await page.waitForFunction(() => document.querySelectorAll('.cmp thead .tag-ok').length >= 3, null, { timeout: 120000 });
  await snap(page, '2-comparaison');
  step('comparaison réelle : 3 découpes terminées');
  await page.getByRole('button', { name: 'Appliquer', exact: true }).nth(1).click();
  await page.getByRole('button', { name: 'Appliquer', exact: true }).last().click();
  await page.getByText(/appliquée/).waitFor();
  step('variante appliquée');
}

// Écrans branchés au jalon « sans endpoint » : matériaux, agent Windows, fiche (unité, téléchargement, export), expériences passées.
{
  await page.goto(`${web}/materiaux`);
  await page.getByRole('tab', { name: 'Matériaux' }).click();
  await page.getByRole('option', { name: /PLA Basic/ }).first().click();
  await page.getByRole('table', { name: /Propriétés de PLA Basic/ }).waitFor();
  const tds = await page.locator('.mat-table').first().innerText();
  assert.match(tds, /Masse volumique/); assert.match(tds, /fiche fabricant/);
  step('matériaux : fiche réelle avec source et niveau de confiance');

  await page.goto(`${web}/agents`);
  await page.getByRole('heading', { name: 'Agent Windows', exact: true }).waitFor();
  const agentsApi = await (await fetch(`${web}/api/agents`)).json();
  if (agentsApi.agents.length === 0) await page.getByText('Aucun agent Windows appairé').waitFor();
  step('écran agent Windows (état réel)');

  const cale = (await (await fetch(`${web}/api/models?q=cale`)).json()).models[0];
  await page.goto(`${web}/modele/${cale.id}`);
  await page.getByRole('button', { name: /Confirmer l'unité/ }).waitFor({ timeout: 20000 });
  await page.getByRole('button', { name: /Confirmer l'unité/ }).click();
  await page.getByText(/confirmée : analyse recalculée/).waitFor();
  await page.getByRole('button', { name: 'Évaluer la fabricabilité' }).click();
  await page.getByText('heuristique').first().waitFor();
  step('unité confirmée (nouvelle analyse) et fabricabilité par machine');
  const link = page.getByRole('link', { name: /Télécharger l'original/ }).first();
  const href = new URL(await link.getAttribute('href'), web).toString();
  const orig = Buffer.from(await (await fetch(href)).arrayBuffer());
  const detail = await (await fetch(`${web}/api/models/${cale.id}`)).json();
  const { createHash } = await import('node:crypto');
  assert.equal(createHash('sha256').update(orig).digest('hex'), detail.files[0].sha256, "l'original téléchargé doit avoir l'empreinte du catalogue");
  const [dl] = await Promise.all([page.waitForEvent('download'), page.getByRole('button', { name: 'Exporter (zip)' }).first().click()]);
  const { readFileSync } = await import('node:fs');
  assert.equal(readFileSync(await dl.path()).subarray(0, 2).toString(), 'PK', 'archive zip attendue');
  step("téléchargement de l'original (empreinte vérifiée) et export zip");

  await page.evaluate(() => localStorage.removeItem('atelier-last-experiment'));
  await page.goto(`${web}/comparaison`);
  await page.getByRole('list', { name: 'Expériences' }).waitFor();
  assert((await page.getByRole('list', { name: 'Expériences' }).locator('a').count()) >= 1, 'les expériences passées doivent être listées');
  step('liste des expériences passées');
}

// Impression contre le simulateur (go run ./cmd/printer-sim) : SIM_PIN = empreinte du certificat affichée par le simulateur.
if (process.env.SIM_PIN && caps.features.find((f) => f.id === 'print_chain')?.available) {
  const BASE = process.env.BAMBU_URL ?? 'http://127.0.0.1:7878';
  assert(process.env.ENROLL_CODE, 'ENROLL_CODE requis (bambud enroll) : les pages /setup, /confirm et /stop exigent un navigateur enregistré');
  // Navigateur « humain » : enregistré par code à usage unique (le cookie sert aux pages serveur, jamais à l'API JSON).
  const human = await (await browser.newContext()).newPage();
  await human.goto(`${BASE}/enroll`);
  await human.locator('input[name=code]').fill(process.env.ENROLL_CODE);
  await human.getByRole('button').first().click();
  await human.getByText(/enregistré/i).first().waitFor();
  step('navigateur enregistré par code à usage unique');
  const sim = process.env.SIM_CODE ?? '12345678'; // code d'accès par défaut du simulateur local (valeur de test du dépôt)
  await page.goto(`${web}/impression`);
  const known = () => fetch(`${web}/api/printers`).then((r) => r.json()).then((d) => d.printers.find((p) => p.serial === (process.env.SIM_SERIAL ?? '01P00A000000001')));
  if (!(await known())) { // empreinte inconnue (simulateur relancé : nouveau certificat) : nouvelle imprimante
    await page.reload();
    await page.getByRole('button', { name: 'Ajouter', exact: true }).first().click();
    await page.getByLabel("Nom d'usage").fill(`Simulateur ${Date.now() % 10000}`);
    await page.getByRole('dialog').locator('select').first().selectOption('p1s');
    await page.getByLabel(/Adresse sur le réseau/).fill('127.0.0.1');
    await page.getByLabel('Numéro de série').fill(process.env.SIM_SERIAL ?? '01P00A000000001');
    await page.getByRole('dialog').getByRole('button', { name: 'Ajouter', exact: true }).click();
  }
  let simPrinter; for (let i = 0; i < 20 && !simPrinter; i++) { simPrinter = await known(); if (!simPrinter) await page.waitForTimeout(500); }
  assert(simPrinter, 'imprimante simulée non enregistrée (série déjà utilisée ?)');
  await page.reload();
  await page.getByRole('listbox', { name: 'Imprimantes' }).getByRole('option', { name: new RegExp(`^${simPrinter.name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')} Bambu`) }).click();
  const link = page.getByRole('link', { name: /Ouvrir la page de saisie/ });
  if (await link.count()) {
    const setup = human;
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
  await page.locator('.prt-new select').nth(1).selectOption(simPrinter.id);
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
  const conf = human;
  await conf.goto(new URL(href, process.env.BAMBU_URL ?? 'http://127.0.0.1:7878').toString());
  await conf.getByLabel(/plateau est vide/).check();
  await snap(conf, '3-page-confirm-serveur');
  await conf.getByRole('button', { name: "Lancer l'impression" }).click();
  await conf.getByText(/Lancement autorisé/).waitFor();
  await page.getByRole('button', { name: /Lancer l'impression \(autorisée\)/ }).waitFor({ timeout: 15000 });
  await page.getByRole('button', { name: /Lancer l'impression \(autorisée\)/ }).click();
  await page.getByText(/Lancement :/).waitFor({ timeout: 30000 });
  step('impression lancée après accord sur la page de confirmation');

  // Suivi jusqu'à la fin (le simulateur imprime pendant quelques dizaines de secondes), puis retour d'expérience.
  await page.getByRole('progressbar', { name: /Avancement/ }).waitFor({ timeout: 30000 });
  await snap(page, '4-suivi-impression');
  step("suivi : barre d'avancement affichée pendant l'impression");
  await page.getByRole('heading', { name: 'Impression terminée' }).waitFor({ timeout: 180000 });
  await snap(page, '5-impression-terminee');
  step("impression suivie jusqu'à sa fin");
  await page.getByRole('link', { name: /Enregistrer le résultat réel/ }).click();
  await page.waitForURL(/comparaison\?experiment=/);
  const expId = new URL(page.url()).searchParams.get('experiment');
  await page.getByRole('table', { name: 'Variantes comparées' }).waitFor();
  await page.getByRole('button', { name: "Enregistrer l'essai réel" }).first().click();
  await page.getByLabel(/Observations/).fill('e2e : pièce correcte, couches bien liées');
  await page.getByLabel('Nom de la mesure 1').fill('cote_x');
  await page.getByLabel('Valeur de la mesure 1').fill('10,02');
  await page.getByLabel('Unité de la mesure 1').fill('mm');
  await page.getByRole('dialog').getByRole('button', { name: 'Enregistrer', exact: true }).click();
  await page.getByText('Essai enregistré').waitFor({ timeout: 15000 });
  const mem = await (await fetch(`${web}/api/print-memory?experiment_id=${expId}`)).json();
  assert(mem.hits.length >= 1 && mem.hits[0].outcome === 'success', "l'essai enregistré doit apparaître dans la mémoire");
  await page.getByText(/Aucun écart de réglage|Écarts de réglage consignés/).first().waitFor();
  await page.getByText('Essai enregistré').scrollIntoViewIfNeeded();
  await snap(page, '6-retour-experience');
  step('résultat réel enregistré, retrouvé dans la mémoire des essais');
}

await browser.close();
assert.deepEqual(errs, [], `erreurs de page : ${errs.join(' | ')}`);
console.log('E2E OK');
