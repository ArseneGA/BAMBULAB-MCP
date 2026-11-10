// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Home } from '../Home';
import { ToastProvider } from '../../components';
import { BASE_ROUTES, entry, installFetch, job, jobs, log, month, progress, status } from './mockApi';
import type { Routes } from './mockApi';
import { buildTodo, dormantProjects, judgedText, lowSpools, monthStats, printingTitle, useVisiblePolling } from './logic';
import type { LogEntry } from '../../api/insights';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.scrollTo = (() => undefined) as typeof window.scrollTo;

let host: HTMLElement; let root: Root;
beforeEach(() => {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', { getItem: (k: string) => mem.get(k) ?? null, setItem: (k: string, v: string) => { mem.set(k, v); }, removeItem: (k: string) => { mem.delete(k); }, clear: () => mem.clear() });
});
afterEach(() => { act(() => root.unmount()); host.remove(); vi.unstubAllGlobals(); vi.useRealTimers(); });
const flush = async (n = 4) => { for (let i = 0; i < n; i++) await act(async () => { await Promise.resolve(); await new Promise((r) => setTimeout(r, 0)); }); };
const mount = async (routes: Routes) => {
  const f = installFetch({ ...BASE_ROUTES, ...routes });
  await act(async () => { root.render(<MemoryRouter><ToastProvider><Home /></ToastProvider></MemoryRouter>); });
  await flush();
  return f;
};
const text = () => host.textContent ?? '';

const e = (over: Partial<LogEntry> & Record<string, unknown> = {}) => entry(over) as unknown as LogEntry;

describe('chiffres du mois (CA-T3)', () => {
  it('10 impressions : 7 jugées réussies, 1 ratée, 2 non jugées → « 7/8 jugées · 2 non jugées »', () => {
    const list = [...Array(7)].map((_, i) => e({ id: `s${i}`, verdict: 'success' })).concat([e({ id: 'f', verdict: 'failed' }), e({ id: 'u1' }), e({ id: 'u2' })]);
    const s = monthStats(list);
    expect(s).toEqual({ printed: 10, judged: 8, success: 7, unjudged: 2 });
    expect(judgedText(s)).toBe('7/8 jugées · 2 non jugées');
  });
  it('mois vide ou sans verdict : aucun NaN, aucune division par zéro ; une impression arrêtée avant 100 % n\'est pas « imprimée »', () => {
    expect(judgedText(monthStats([]))).toBe('aucune impression terminée ce mois-ci');
    expect(judgedText(monthStats([e()]))).toBe('aucune jugée · 1 non jugée');
    expect(monthStats([e({ reached_100: false, state: 'CANCELLED', outcome: 'cancelled' })]).printed).toBe(0);
    expect(monthStats([e({ finished_at: '2020-01-01T00:00:00Z' })]).printed).toBe(0);
  });
});

describe('logique de « À faire »', () => {
  it('ordre : confirmer, alerte, verdict, bobine, projet dormant ; un lien serveur seulement si la confirmation existe', () => {
    const todo = buildTodo({
      jobs: [job({ id: 'a', state: 'UPLOADED', confirmation_id: 'c1' }) as never, job({ id: 'b', state: 'AWAITING_PRINT_CONFIRMATION' }) as never],
      pending: [{ print_job_id: 'v', state: 'COMPLETED', model_name: 'Boîtier' }],
      alerts: [{ job: job({ id: 'z', state: 'PRINTING' }) as never, summary: 'Alerte' }],
      spoolsNeeded: [{ name: 'Équerre', spool: { id: 's', material: 'PLA', remaining_g: 40 } as never }], dormant: [{ project: { id: 'p', name: 'Étagère', status: 'design', updated_at: '2026-01-01T00:00:00Z' } } as never],
    });
    expect(todo.map((t) => t.kind)).toEqual(['confirm', 'confirm', 'alert', 'verdict', 'spool', 'dormant']);
    expect(todo[0]).toMatchObject({ href: '/confirm/c1', server: true });
    expect(todo[1]).toMatchObject({ href: '/impressions/b', server: false });
  });
  it('projet dormant : racine, actif, sans activité au-delà du seuil ; bobines basses ; titre d\'onglet', () => {
    const now = new Date('2026-11-10T00:00:00Z').getTime();
    const items = [
      { project: { id: 'a', name: 'A', status: 'design', updated_at: '2026-10-01T00:00:00Z' } },
      { project: { id: 'b', name: 'B', status: 'design', updated_at: '2026-11-05T00:00:00Z' } },
      { project: { id: 'c', name: 'C', status: 'done', updated_at: '2026-01-01T00:00:00Z' } },
      { project: { id: 'd', name: 'D', status: 'design', parent_id: 'a', updated_at: '2026-01-01T00:00:00Z' } },
    ] as never[];
    expect(dormantProjects(items, 14, now).map((i) => i.project.id)).toEqual(['a']);
    expect(dormantProjects(items, 60, now)).toEqual([]);
    expect(lowSpools([{ id: '1', status: 'low' }, { id: '2', status: 'ok' }, { id: '3', status: 'empty', archived: true }] as never)).toHaveLength(1);
    expect(printingTitle(41.6, 'Équerre', 'Printalk')).toBe('42 % · Équerre · Printalk');
  });
});

describe('sondage et visibilité de l\'onglet (CA-T4)', () => {
  function Poller({ run }: { run: () => void }) { useVisiblePolling(run, 5000); return null; }
  const setVisibility = (v: 'visible' | 'hidden') => { Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => v }); document.dispatchEvent(new Event('visibilitychange')); };
  it('onglet caché : plus aucune lecture ; au retour : une lecture immédiate puis reprise', async () => {
    vi.useFakeTimers();
    const run = vi.fn();
    await act(async () => { root.render(<Poller run={run} />); });
    await act(async () => { vi.advanceTimersByTime(5000); });
    expect(run).toHaveBeenCalledTimes(1);
    await act(async () => { setVisibility('hidden'); vi.advanceTimersByTime(30000); });
    expect(run).toHaveBeenCalledTimes(1);
    await act(async () => { setVisibility('visible'); });
    expect(run).toHaveBeenCalledTimes(2);
    await act(async () => { vi.advanceTimersByTime(5000); });
    expect(run).toHaveBeenCalledTimes(3);
  });
});

describe('Accueil : « À faire » (CA-T1, CA-T2)', () => {
  it('rien n\'attend : le bloc « À faire » est absent ; l\'ordre des blocs suit la spec', async () => {
    await mount({});
    expect(host.querySelector('[aria-label="À faire"]')).toBeNull();
    const order = [...host.querySelectorAll('h1, h2')].map((h) => h.textContent);
    expect(order[0]).toBe('Bonjour');
    expect(order.indexOf('En direct')).toBeLessThan(order.indexOf('Dernières impressions'));
    expect(order.indexOf('Dernières impressions')).toBeLessThan(order.indexOf('Chiffres du mois'));
    expect(text()).toContain('Tout est calme.');
    expect(host.querySelector('a[href="/impressions/nouvelle"]')).not.toBeNull();
    // le « Parcours » en six cartes et les listes qui se recoupaient ont disparu
    expect(text()).not.toContain('Prêtes à lancer');
    expect(text()).not.toContain('File d\'attente');
    expect(text()).not.toContain('Parcours');
  });

  const full: Routes = {
    'GET /api/print-jobs': jobs([job({ id: 'j1', state: 'AWAITING_PRINT_CONFIRMATION', confirmation_id: 'conf1', name: 'Équerre' })]),
    'GET /api/verdicts/pending': { pending: [{ print_job_id: 'j2', name: 'Boîtier', state: 'COMPLETED', finished_at: '2026-11-08T10:00:00Z' }], total: 1, fail_reasons: [{ id: 'stringing', label: 'Fils' }, { id: 'warping', label: 'Gauchissement' }], note: '' },
    'GET /api/spools': { spools: [{ id: 's1', material: 'PLA', name: 'PLA noir', remaining_g: 40, status: 'low', archived: false, printer_id: 'p1', slot: 'ams1:1' }], alert_margin: { margin_percent: 10, margin_grams: 20 } },
    'GET /api/print-queue': { queue: [{ id: 'q1', print_job_id: 'j9', printer_id: 'p1', file_name: 'Support', state: 'waiting', assignments: [{ filament_slot: 1, tray: 'ams1:1', material_id: 'PLA' }], position: 1, message: '', added_at: '' }] },
    'GET /api/projects': { projects: [{ project: { id: 'pr1', name: 'Étagère salon', status: 'design', rev: 1, updated_at: '2026-01-01T00:00:00Z' }, depth: 0, progress: progress(), parts_count: 2 }], total: 1, counts_by_status: {}, note: '' },
    'GET /api/projects/pr1': { project: { id: 'pr1', name: 'Étagère salon', status: 'design', rev: 1 }, progress: progress(), parts: [], sub_projects: [], totals: { available: true, actual_cost: 3.2, currency: 'EUR', actual_seconds: 0, actual_grams: 0, in_progress: 0, attempts: 0 }, variants: [], next_action: 'Imprimer 2 × support', note: '' },
  };
  it('présent avec ses lignes : confirmation (LIEN vers /confirm), verdict à donner, bobine basse requise, projet dormant', async () => {
    const f = await mount(full);
    const todo = host.querySelector('[aria-label="À faire"]')!;
    expect(todo).not.toBeNull();
    const kinds = [...todo.querySelectorAll('.dash-kind')].map((k) => k.textContent);
    expect(kinds).toEqual(['À confirmer', 'Verdict', 'Bobine basse', 'Projet dormant']);
    const link = todo.querySelector<HTMLAnchorElement>('a[href^="/confirm/"]')!;
    expect(link.getAttribute('href')).toBe('/confirm/conf1');
    expect(link.tagName).toBe('A');
    expect(todo.textContent).toContain('requise par Support');
    expect(todo.textContent).toContain('Verdict à donner');
    // le projet apparaît aussi en « Projets en cours », avec son avancement et la prochaine action
    expect(text()).toContain('4/6 pièces imprimées');
    expect(text()).toContain('Prochaine action : Imprimer 2 × support');
    // aucune écriture au rendu
    expect(f.calls.filter((c) => c.method !== 'GET')).toEqual([]);
    // l'action principale devient le lien « Vérifier et lancer »
    expect(host.querySelector('.page-head a[href="/confirm/conf1"]')).not.toBeNull();
  });
  it('clics sur l\'Accueil : seule requête non-GET autorisée = le verdict ; jamais de lancement ni d\'arrêt', async () => {
    const f = await mount({ ...full, 'POST /api/prints/j2/verdict': { print_job_id: 'j2', verdict: 'success', changed: true, note: 'ok' } });
    const btn = [...host.querySelectorAll<HTMLButtonElement>('[aria-label="À faire"] button')].find((b) => b.textContent === 'Réussi')!;
    await act(async () => { btn.click(); });
    await flush();
    const writes = f.calls.filter((c) => c.method !== 'GET');
    expect(writes.map((c) => `${c.method} ${c.url}`)).toEqual(['POST /api/prints/j2/verdict']);
    expect(writes.some((c) => /start|cancel|stop|confirmation/.test(c.url))).toBe(false);
  });
});

describe('verdict en un geste (CA-T5)', () => {
  const finished: Routes = {
    'GET /api/print-log': log([entry({ id: 'l1', print_job_id: 'j1', name: 'Équerre' })]),
    'POST /api/prints/j1/verdict': { print_job_id: 'j1', verdict: 'success', changed: true, note: 'ok' },
    'DELETE /api/prints/j1/verdict': { print_job_id: 'j1', changed: true, note: 'retiré' },
    'GET /api/verdicts/pending': { pending: [], total: 0, fail_reasons: [{ id: 'stringing', label: 'Fils' }, { id: 'warping', label: 'Gauchissement' }], note: '' },
  };
  it('un tap sur « Réussi » écrit via le verdict et propose « Annuler » ; Annuler retire le verdict', async () => {
    const f = await mount(finished);
    const ok = [...host.querySelectorAll<HTMLButtonElement>('#dernieres button')].find((b) => b.textContent === 'Réussi')!;
    await act(async () => { ok.click(); });
    await flush();
    expect(f.calls.find((c) => c.method === 'POST')?.body).toContain('"verdict":"success"');
    const undo = [...host.querySelectorAll<HTMLButtonElement>('.undo-bar button')].find((b) => b.textContent === 'Annuler')!;
    expect(undo).toBeTruthy();
    await act(async () => { undo.click(); });
    await flush();
    expect(f.calls.some((c) => c.method === 'DELETE' && c.url === '/api/prints/j1/verdict')).toBe(true);
    expect(host.querySelector('.undo-bar')).toBeNull();
  });
  it('« Raté » demande la raison avant d\'écrire', async () => {
    const f = await mount(finished);
    const bad = [...host.querySelectorAll<HTMLButtonElement>('#dernieres button')].find((b) => b.textContent === 'Raté')!;
    await act(async () => { bad.click(); });
    expect(f.calls.filter((c) => c.method === 'POST')).toHaveLength(0);
    const chip = [...host.querySelectorAll<HTMLButtonElement>('.chip')].find((b) => b.textContent === 'Gauchissement')!;
    await act(async () => { chip.click(); });
    await flush();
    expect(f.calls.find((c) => c.method === 'POST')?.body).toContain('"reason":"warping"');
  });
});

describe('impression en cours (CA-T4)', () => {
  it('carte agrandie : progression, couche, fin estimée, titre d\'onglet « NN % · … » ; photo à la demande', async () => {
    const f = await mount({
      'GET /api/print-jobs': jobs([job({ id: 'j1', state: 'PRINTING', name: 'Équerre', printer_id: 'p1' })]),
      'GET /api/printers/p1/status': status({ state: 'printing', progress_percent: 42, layer: 40, total_layers: 100, remaining_minutes: 75, current_file: 'Équerre' }),
      'GET /api/print-jobs/j1/photos': { photos: [{ id: 'a', kind: 'progress', sha256: 'x', taken_at: '', percent: 40, url: '/api/camera/photos/x' }] },
      'GET /api/print-jobs/j1/analysis': { alerts: [] },
    });
    expect(document.title).toBe('42 % · Équerre · Printalk');
    expect(host.querySelector('.dash-live-big')).not.toBeNull();
    expect(text()).toContain('40 / 100');
    expect(text()).toContain('Fin estimée');
    expect(text()).toContain('Dernier palier : 40 %');
    expect(text()).toContain('1 impression en cours');
    // la photo n'est lue qu'une fois, à l'affichage ; aucun rafraîchissement automatique par défaut
    expect(f.calls.filter((c) => c.url.includes('/camera/snapshot')).length).toBeLessThanOrEqual(1);
    expect(host.querySelector('input[role="switch"]')).not.toBeNull();
    expect((host.querySelector('input[role="switch"]') as HTMLInputElement).checked).toBe(false);
    expect(host.querySelector('a[href="/impressions/j1"]')).not.toBeNull();
    // aucun bouton d'arrêt, de reprise ou de lancement
    expect([...host.querySelectorAll('button, a')].some((b) => /arrêter|reprendre|lancer l'impression/i.test(b.textContent ?? ''))).toBe(false);
  });
  it('palier sans photo : « photo non disponible » (jamais un trou muet)', async () => {
    await mount({
      'GET /api/print-jobs': jobs([job({ id: 'j1', state: 'PRINTING', printer_id: 'p1' })]),
      'GET /api/printers/p1/status': status({ state: 'printing', progress_percent: 5, remaining_minutes: 10 }),
      'GET /api/print-jobs/j1/photos': { photos: [] },
    });
    expect(text()).toContain('photo non disponible');
  });
});

describe('dégradation (CA-T6, CA-T7)', () => {
  it('sans G9 (routes de projets en 404) : l\'Accueil s\'affiche sans erreur, sans bloc Projets ni verdict', async () => {
    await mount({ 'GET /api/projects': new Response('{}', { status: 404 }), 'GET /api/verdicts/pending': new Response('{}', { status: 404 }) });
    expect(host.querySelector('#projets')).toBeNull();
    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(text()).toContain('Dernières impressions');
  });
  it('service injoignable : chaque bloc le dit en une ligne (le bandeau d\'erreur unique est celui de la coque), sans effacer les autres', async () => {
    const down = (): never => { throw new TypeError('fetch failed'); };
    vi.stubGlobal('fetch', vi.fn(async () => down()));
    await act(async () => { root.render(<MemoryRouter><ToastProvider><Home /></ToastProvider></MemoryRouter>); });
    await flush();
    expect(text()).toContain('Indisponible pour l\'instant');
    expect(host.querySelectorAll('.state-err').length).toBe(0);
    expect(text()).toContain('Chiffres du mois');
  });
  it('premier lancement (ni imprimante ni modèle) : trois étapes', async () => {
    await mount({ 'GET /api/printers': { printers: [] }, 'GET /api/models': { models: [], total: 0 } });
    expect(text()).toContain('Ajouter une imprimante');
    expect([...host.querySelectorAll('.dash-steps li')]).toHaveLength(3);
  });
  it('mois vide : tuiles à zéro, aucune division par zéro', async () => {
    await mount({ 'GET /api/usage/dashboard': month(), 'GET /api/print-log': log([]) });
    expect(text()).not.toMatch(/NaN|Infinity/);
    expect(host.querySelector('#chiffres')?.textContent).toContain('aucune impression terminée ce mois-ci');
  });
});
