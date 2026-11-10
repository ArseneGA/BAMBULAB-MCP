// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SearchPanel, useSearchShortcut, pageHits } from '../components/SearchPanel';
import { ToastProvider } from '../components';
import { PrintSheetPage } from './impressions/PrintSheet';
import { ModelImpressionsTab, ModelProfilesTab } from './model/ModelTabs';
import { SuiviPage, capError, dormantError, retentionError } from './settings/Suivi';
import { ImpressionsPage } from './Impressions';
import { PART_SHEET, PART_SHEET_EMPTY, PRINT_SHEET, PRINT_SHEET_NO_CAMERA } from '../api/sheets.fixtures';
import { BASE_ROUTES, entry, installFetch, job, log } from './dashboard/mockApi';
import type { Routes as ApiRoutes } from './dashboard/mockApi';
import { DORMANT_KEY } from './dashboard/logic';
import { tabsOf } from '../nav';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.scrollTo = (() => undefined) as typeof window.scrollTo;
// jsdom ne fournit pas toujours <dialog>.showModal()
const proto = HTMLDialogElement.prototype as unknown as { showModal?: () => void; close?: () => void };
proto.showModal = function (this: HTMLDialogElement) { this.setAttribute('open', ''); };
proto.close = function (this: HTMLDialogElement) { this.removeAttribute('open'); };

let host: HTMLElement; let root: Root;
beforeEach(() => {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', { getItem: (k: string) => mem.get(k) ?? null, setItem: (k: string, v: string) => { mem.set(k, v); }, removeItem: (k: string) => { mem.delete(k); }, clear: () => mem.clear() });
});
afterEach(() => { act(() => root.unmount()); host.remove(); vi.unstubAllGlobals(); });
const flush = async (n = 4) => { for (let i = 0; i < n; i++) await act(async () => { await Promise.resolve(); await new Promise((r) => setTimeout(r, 0)); }); };
const text = () => host.textContent ?? '';

/* ---------------- recherche globale (CA-R1, CA-R2) ---------------- */
function SearchHarness() {
  const [open, setOpen] = ((React) => React.useState(false))(useReact());
  useSearchShortcut(() => setOpen(true));
  const l = useLocation();
  return <><button id="before">avant</button><output id="loc">{l.pathname}</output><SearchPanel open={open} onClose={() => setOpen(false)} /></>;
}
import * as React from 'react';
const useReact = () => React;

const searchRoutes = (): ApiRoutes => ({
  ...BASE_ROUTES,
  'GET /api/models': (c: { url: string }) => (c.url.includes('q=') && !decodeURIComponent(c.url).toLowerCase().includes('quer') ? { models: [], total: 0 } : c.url.includes('q=') ? { models: [{ id: 'm1', name: 'Équerre 40 mm', formats: ['stl'], size_bytes: 1, updated_at: '', collection_path: 'Atelier' }], total: 1 } : { models: [{ id: 'm9', name: 'Récent', formats: ['stl'], size_bytes: 1, updated_at: '' }], total: 1 }),
  'GET /api/projects': { projects: [{ project: { id: 'pr1', name: 'Étagère équerre', status: 'design', rev: 1 }, depth: 0, progress: { text: '1/2 pièces imprimées' }, parts_count: 1 }], total: 1, counts_by_status: {}, note: '' },
  'GET /api/print-log': log([entry({ id: 'l1', print_job_id: 'j1', name: 'Équerre test' })]),
});
const mountSearch = async () => {
  installFetch(searchRoutes());
  await act(async () => { root.render(<MemoryRouter><SearchHarness /></MemoryRouter>); });
  await flush();
};
const key = (k: string, mods: KeyboardEventInit = {}) => act(async () => { window.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, ...mods })); });
const typeInto = async (v: string) => {
  const input = host.querySelector<HTMLInputElement>('.search-input input')!;
  const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
  await act(async () => { set.call(input, v); input.dispatchEvent(new Event('input', { bubbles: true })); });
  await act(async () => { await new Promise((r) => setTimeout(r, 260)); });
  await flush();
};

describe('recherche globale', () => {
  it('Ctrl+K et « / » ouvrent le panneau ; résultats groupés ; Échap ferme et rend le focus', async () => {
    await mountSearch();
    const before = host.querySelector<HTMLButtonElement>('#before')!;
    before.focus();
    await key('k', { ctrlKey: true });
    expect(host.querySelector('dialog.search-dlg')?.hasAttribute('open')).toBe(true);
    await typeInto('équerre');
    const groups = [...host.querySelectorAll('[role="group"] > .search-h')].map((h) => h.textContent);
    expect(groups).toEqual(['Modèles', 'Projets', 'Impressions']);
    // Échap (événement « cancel » du dialogue natif) ferme et rend le focus
    const dlg = host.querySelector('dialog')!;
    await act(async () => { dlg.dispatchEvent(new Event('cancel', { cancelable: true })); });
    expect(dlg.hasAttribute('open')).toBe(false);
    expect(document.activeElement).toBe(before);
    // « / » hors champ de saisie
    await key('/');
    expect(host.querySelector('dialog')?.hasAttribute('open')).toBe(true);
  });
  it('« / » est ignoré pendant une saisie', async () => {
    await mountSearch();
    const i = document.createElement('input'); host.appendChild(i); i.focus();
    await act(async () => { i.dispatchEvent(new KeyboardEvent('keydown', { key: '/', bubbles: true })); });
    expect(host.querySelector('dialog')?.hasAttribute('open')).toBeFalsy();
  });
  it('saisie piégée (\' ; <script>) : échappée à l\'affichage, paramètre encodé, aucune erreur', async () => {
    await mountSearch();
    const f = installFetch(searchRoutes());
    await key('k', { metaKey: true });
    await typeInto('\'; <script>alert(1)</script>');
    expect(host.querySelector('script')).toBeNull();
    expect(text()).toContain('Aucun résultat pour « \'; <script>alert(1)</script> »');
    const q = f.calls.find((c) => c.url.startsWith('/api/models') && c.url.includes('q='))!;
    expect(new URL(q.url, 'http://x').searchParams.get('q')).toBe('\'; <script>alert(1)</script>');
    expect(q.url).not.toContain('<script>');
  });
  it('sans résultat : message + 2 suggestions ; recherche vide : récents', async () => {
    await mountSearch();
    await key('k', { ctrlKey: true });
    await flush();
    expect(text()).toContain('Récents');
    expect(text()).toContain('Récent');
    await typeInto('zzzz-introuvable');
    const sugg = [...host.querySelectorAll('.search-sugg button')].map((b) => b.textContent);
    expect(sugg).toEqual(['Importer un modèle', 'Voir toutes les impressions']);
  });
  it('pages de l\'interface : titre ou mot-clé, sans les écrans à paramètre', () => {
    expect(pageHits('bobines').map((h) => h.to)).toContain('/matieres');
    expect(pageHits('projet dormant').map((h) => h.to)).toContain('/reglages/suivi');
    expect(pageHits('').length).toBe(0);
    for (const h of pageHits('impression')) expect(h.to).not.toContain(':');
  });
});

/* ---------------- fiche impression (CA-I1) ---------------- */
const mountSheet = async (routes: ApiRoutes, url = '/impressions/pj_5') => {
  const f = installFetch({ ...BASE_ROUTES, ...routes });
  await act(async () => { root.render(<MemoryRouter initialEntries={[url]}><ToastProvider><Routes><Route path="/impressions/:id" element={<PrintSheetPage />} /></Routes></ToastProvider></MemoryRouter>); });
  await flush(6);
  return f;
};
describe('fiche impression /impressions/:id', () => {
  it('adressable : en-tête, verdict, réglages figés, matière, temps prévu/réel, planche des paliers avec « photo non disponible » + raison, alertes', async () => {
    await mountSheet({ 'GET /api/prints/pj_5/sheet': PRINT_SHEET, 'GET /api/print-jobs/pj_5': { job: job({ id: 'pj_5', state: 'COMPLETED', name: 'equerre.gcode.3mf' }) } });
    expect(host.querySelectorAll('h1')).toHaveLength(1);
    expect(host.querySelector('h1')?.textContent).toBe('equerre.gcode.3mf');
    for (const t of ['Verdict', 'Réglages figés', 'Matière', 'Temps et coût', 'Photos de paliers', 'Alertes de la caméra', 'Accord de lancement']) expect(text()).toContain(t);
    expect(host.querySelectorAll('.ps-step')).toHaveLength(10);
    expect(text()).toContain('photo non disponible');
    expect(text()).toContain('caméra injoignable à ce palier');
    expect(text()).toContain('9 photos disponibles sur 10 paliers');
    expect(text()).toContain('Réussie');
    expect(text()).toContain('2 h 00 min');
    expect(text()).toContain('1 alerte de la caméra');
    expect(host.querySelector('.crumb, nav[aria-label="Fil d\'Ariane"]')).not.toBeNull();
    // archivage et corbeille restent possibles
    expect([...host.querySelectorAll('button')].map((b) => b.textContent)).toEqual(expect.arrayContaining(['Archiver', 'Mettre à la corbeille']));
  });
  it('non jugée : action principale « Donner le verdict » ; le verdict écrit sans lancer quoi que ce soit ; caméra non disponible ≠ aucune alerte', async () => {
    const f = await mountSheet({
      'GET /api/prints/pj_6/sheet': PRINT_SHEET_NO_CAMERA, 'GET /api/print-jobs/pj_6': { job: job({ id: 'pj_6', state: 'COMPLETED' }) },
      'POST /api/prints/pj_6/verdict': { print_job_id: 'pj_6', verdict: 'success', changed: true, note: 'ok' },
    }, '/impressions/pj_6');
    expect(host.querySelector('.page-head .btn-primary')?.textContent).toBe('Donner le verdict');
    expect(text()).toContain('Caméra non disponible : détection par caméra désactivée');
    expect(text()).toContain('aucune photo de palier relevée');
    const ok = [...host.querySelectorAll<HTMLButtonElement>('#verdict-bar button')].find((b) => b.textContent === 'Réussi')!;
    await act(async () => { ok.click(); });
    await flush();
    expect(f.calls.filter((c) => c.method !== 'GET').map((c) => `${c.method} ${c.url}`)).toEqual(['POST /api/prints/pj_6/verdict']);
  });
  it('impression inconnue : état vide (ce que c\'est, pourquoi, une action)', async () => {
    await mountSheet({}, '/impressions/nope');
    expect(text()).toContain('Cette impression n\'existe pas');
    expect(host.querySelector('a[href="/impressions"].btn')).not.toBeNull();
  });
  it('un ancien lien « /impressions?job=ID » mène à la fiche', async () => {
    installFetch({ ...BASE_ROUTES });
    function Where() { return <output id="w">{useLocation().pathname}</output>; }
    await act(async () => { root.render(<MemoryRouter initialEntries={['/impressions?job=abc%2F1']}><Routes><Route path="/impressions" element={<ImpressionsPage />} /><Route path="/impressions/:id" element={<Where />} /></Routes></MemoryRouter>); });
    expect(host.querySelector('#w')?.textContent).toBe('/impressions/abc%2F1');
  });
});

/* ---------------- fiche modèle : onglets Impressions et Profils ---------------- */
const mountTab = async (el: React.ReactNode, routes: ApiRoutes) => {
  installFetch({ ...BASE_ROUTES, ...routes });
  await act(async () => { root.render(<MemoryRouter>{el}</MemoryRouter>); });
  await flush(5);
};
describe('fiche modèle', () => {
  it('au plus 4 onglets, état dans l\'URL (?vue=)', () => {
    expect(tabsOf('/modele/:id').map((t) => t.value)).toEqual(['apercu', 'fichiers', 'impressions', 'profils']);
  });
  it('Impressions : compteurs honnêtes (100 %, jugées, non jugées), coût cumulé, versions, liste liée aux fiches', async () => {
    await mountTab(<ModelImpressionsTab id="mdl_sheet1" />, { 'GET /api/models/mdl_sheet1/sheet': PART_SHEET });
    expect(text()).toContain('100 % atteint, verdict ou pas');
    expect(text()).toContain('toutes jugées');
    expect(text()).toContain('Coût cumulé');
    expect(text()).toContain('résistance jamais testée');
    expect(host.querySelector('a[href="/impressions/pj_5"]')).not.toBeNull();
  });
  it('pièce jamais imprimée : état vide avec une action', async () => {
    await mountTab(<ModelImpressionsTab id="mdl_empty" />, { 'GET /api/models/mdl_empty/sheet': PART_SHEET_EMPTY });
    expect(text()).toContain('pas encore été imprimé');
    expect(host.querySelector('a[href="/modele/mdl_empty/preparer"]')).not.toBeNull();
  });
  it('Profils : effectif toujours indiqué ; erreur lisible en trois parties', async () => {
    await mountTab(<ModelProfilesTab id="mdl_sheet1" />, { 'GET /api/models/mdl_sheet1/sheet': PART_SHEET });
    expect(text()).toContain('4 jugées réussies sur 5');
    await act(async () => { root.unmount(); }); root = createRoot(host);
    await mountTab(<ModelProfilesTab id="x" />, { 'GET /api/models/x/sheet': new Response(JSON.stringify({ error: 'boom' }), { status: 500 }) });
    expect(host.querySelector('.state-err h3')?.textContent).toContain('n\'ont pas pu être lues');
    expect(host.querySelector('.state-err .state-impact')).not.toBeNull();
    expect(host.querySelector('.state-err details summary')?.textContent).toBe('Détails techniques');
  });
});

/* ---------------- Réglages › Suivi (CA-P2) ---------------- */
describe('Réglages › Suivi', () => {
  it('bornes : projet dormant > 0 ; conservation 0-3650 ; plafond ≥ 0', () => {
    expect(dormantError(0)).toBeTruthy(); expect(dormantError(-3)).toBeTruthy(); expect(dormantError(1.5)).toBeTruthy(); expect(dormantError(14)).toBeUndefined();
    expect(retentionError(0)).toBeUndefined(); expect(retentionError(3651)).toBeTruthy(); expect(retentionError(undefined)).toBeTruthy();
    expect(capError(0)).toBeUndefined(); expect(capError(-1)).toBeTruthy();
  });
  it('enregistre le seuil dans ce navigateur, affiche la valeur appliquée, et envoie les réglages des photos de paliers', async () => {
    const f = installFetch({
      ...BASE_ROUTES,
      'GET /api/camera/settings': { enabled: true, interval_seconds: 60, progress_photos: true, progress_photo_width: 1280, progress_photo_quality: 80, progress_retention_days: 90, progress_cap_mb: 2048, alert_photo_retention_days: 90 },
      'PUT /api/camera/settings': { enabled: true, interval_seconds: 60, progress_photos: false, progress_photo_width: 1280, progress_photo_quality: 80, progress_retention_days: 30, progress_cap_mb: 2048, alert_photo_retention_days: 90 },
    });
    await act(async () => { root.render(<MemoryRouter><ToastProvider><SuiviPage /></ToastProvider></MemoryRouter>); });
    await flush(5);
    expect(text()).toContain('Valeur appliquée : 14 jours');
    const inputs = [...host.querySelectorAll<HTMLInputElement>('input[inputmode], input[type="text"], input:not([role="switch"])')];
    const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
    const change = async (i: HTMLInputElement, v: string) => { await act(async () => { set.call(i, v); i.dispatchEvent(new Event('input', { bubbles: true })); }); };
    await change(inputs[0], '0');
    const save = () => [...host.querySelectorAll<HTMLButtonElement>('button')].filter((b) => b.textContent === 'Enregistrer');
    expect(save()[0].disabled).toBe(true);
    expect(text()).toContain('Entre 1 et 3650 jours.');
    await change(inputs[0], '30');
    await act(async () => { save()[0].click(); });
    expect(localStorage.getItem(DORMANT_KEY)).toBe('30');
    expect(text()).toContain('Valeur appliquée : 30 jours');
    await act(async () => { save()[1].click(); });
    await flush();
    const put = f.calls.find((c) => c.method === 'PUT')!;
    expect(JSON.parse(put.body!)).toMatchObject({ progress_photos: true, progress_retention_days: 90, progress_cap_mb: 2048 });
  });
});
