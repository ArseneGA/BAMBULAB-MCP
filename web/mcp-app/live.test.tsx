// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { LiveCard, endFallback, fmtMin, normalizeLive, type Live } from './live';
import { normalizeProgress } from './progress';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLElement; let root: Root; let vis: 'visible' | 'hidden' = 'visible';
beforeEach(() => {
  vi.useFakeTimers();
  vis = 'visible';
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => vis });
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); vi.useRealTimers(); });

/** Un PrintStatus tel que le rend get_print_status (champs principaux). */
const STATUS = (job: Record<string, unknown> = {}, extra: Record<string, unknown> = {}, printer: Record<string, unknown> | null = {}) => ({
  job: { id: 'pj_1', state: 'PRINTING', printer_id: 'pr_1', name: 'boite.gcode.3mf', print: { printer_state: 'printing', percent: 40, layer: 50, total_layers: 124, remaining_minutes: 90 }, ...job },
  ...(printer === null ? {} : { printer: { printer_id: 'pr_1', reachable: true, stale: false, state: 'printing', progress_percent: 41, layer: 52, total_layers: 124, remaining_minutes: 88, temps: { nozzle: 214.6, nozzle_target: 220, bed: 59.8, bed_target: 60, chamber: 31 }, ...printer } }),
  ...extra,
});
const LIVE = (status = STATUS(), extras: Record<string, unknown> = {}) => ({ status, terminal: false, camera_available: true, ends_at_local: '21:05', height_mm: 10.3, height_total_mm: 24.8, ...extras });

describe('normalizeLive', () => {
  it('lit un PrintStatus : progression, couche, température, étapes, alerte', () => {
    const l = normalizeLive(STATUS({}, {
      alert: "attend une action à l'écran : bobine vide",
      stage_report: { current: 'Chauffe de la buse', stages: [{ label: 'Mise à niveau du plateau : effectuée à 21:05', state: 'done' }, { label: 'Chauffe de la buse : en cours', state: 'in_progress' }] },
    }, { error_details: [{ code: '0300400C', short: '0300_400C', text: 'Bobine vide', action: 'Changer la bobine' }, { code: 'x', short: 'x', text: 'reliquat', benign: true }] }))!;
    expect(l).toMatchObject({ jobId: 'pj_1', printerId: 'pr_1', name: 'boite.gcode.3mf', state: 'PRINTING', percent: 41, layer: 52, totalLayers: 124, remainingMin: 88, terminal: false, paused: true, currentStage: 'Chauffe de la buse' });
    expect(l.temps).toEqual({ nozzle: 214.6, nozzleTarget: 220, bed: 59.8, bedTarget: 60, chamber: 31 });
    expect(l.stages).toEqual([{ label: 'Mise à niveau du plateau : effectuée à 21:05', done: true }, { label: 'Chauffe de la buse : en cours', done: false }]);
    expect(l.alerts.map((a) => a.kind)).toEqual(['attention', 'error']); // l'erreur bénigne est écartée
    expect(l.alerts[1].text).toContain('Bobine vide');
  });
  it('lit l\'enveloppe de get_live_state : heure de fin, hauteur, résumé final', () => {
    const l = normalizeLive({ ...LIVE(STATUS({ state: 'COMPLETED' }), { terminal: true, ends_at_local: undefined, final: { outcome: 'completed', duration: '2 h 05 min', grams: 18.4, grams_basis: 'estimé', photo: { mime: 'image/jpeg', data: 'QUJD' } } }) })!;
    expect(l).toMatchObject({ terminal: true, failed: false, percent: 100, heightMm: 10.3, heightTotalMm: 24.8, cameraAvailable: true });
    expect(l.final).toMatchObject({ outcome: 'completed', duration: '2 h 05 min', grams: 18.4, photoSrc: 'data:image/jpeg;base64,QUJD' });
    expect(normalizeLive(LIVE())!.endsAtLocal).toBe('21:05');
  });
  it('tolère les champs manquants : rien d\'inventé, barre indéterminée', () => {
    const l = normalizeLive({ job: { id: 'pj_9', state: 'PRINTING', printer_id: 'pr_1' } })!;
    expect(l.percent).toBeNull(); expect(l.temps).toBeUndefined(); expect(l.layer).toBeUndefined(); expect(l.alerts).toEqual([]); expect(l.stages).toEqual([]);
    expect(l.remainingMin).toBeUndefined(); expect(l.heightMm).toBeUndefined(); expect(l.paused).toBe(false);
    const j = normalizeLive({ job: { id: 'pj_9', state: 'UPLOADED' }, printer: null })!; // pas de printer_id : état connu de la machine à états
    expect(j.state).toBe('UPLOADED');
  });
  it('imprimante injoignable : valeurs périmées signalées, pourcentage du dernier suivi', () => {
    const l = normalizeLive(STATUS({}, {}, { reachable: false, stale: true }))!;
    expect(l.stale).toBe(true); expect(l.percent).toBe(40); expect(l.remainingMin).toBe(90);
  });
  it('ne reconnaît pas un travail de la file, une progression ni une autre forme', () => {
    expect(normalizeLive({ job: { id: 'job_1', kind: 'slice', state: 'running' }, progress_detail: { percent: 3 } })).toBeUndefined();
    expect(normalizeLive({ job_id: 'job_1', state: 'running', percent: 5 })).toBeUndefined();
    expect(normalizeLive({ models: [], total: 0 })).toBeUndefined();
    expect(normalizeLive(undefined)).toBeUndefined();
    expect(normalizeProgress(STATUS())).toBeDefined(); // l'ancien classement l'aurait pris pour un travail : main.tsx teste le suivi d'abord
  });
  it('formate durée et heure de fin (Europe/Paris)', () => {
    expect(fmtMin(45)).toBe('45 min'); expect(fmtMin(125)).toBe('2 h 05 min'); expect(fmtMin(undefined)).toBeUndefined();
    expect(endFallback(90, Date.UTC(2026, 10, 9, 18, 0, 0))).toBe('20:30'); // 18:00 UTC + 90 min = 19:30 UTC = 20:30 à Paris (CET)
    expect(endFallback(0)).toBeUndefined();
  });
});

describe('LiveCard', () => {
  const FRAME = { structuredContent: { printer_id: 'pr_1', mime: 'image/jpeg', data: 'QUJDRA==', taken_at_local: '21:04:12', next_after_ms: 2500 } };
  type Calls = Record<string, unknown>;
  const mk = (over: Record<string, (args: Calls) => unknown> = {}) => {
    const handlers: Record<string, (args: Calls) => unknown> = {
      get_live_frame: () => FRAME,
      get_live_state: () => ({ structuredContent: LIVE() }),
      ...over,
    };
    const app = {
      callServerTool: vi.fn(async (p: { name: string; arguments: Calls }) => { const h = handlers[p.name]; if (!h) throw new Error(`outil inattendu ${p.name}`); return h(p.arguments); }),
      openLink: vi.fn(async () => ({})),
    };
    return app;
  };
  const count = (app: ReturnType<typeof mk>, name: string) => app.callServerTool.mock.calls.filter((c) => c[0].name === name).length;
  const render = async (el: JSX.Element) => { await act(async () => { root.render(el); }); };
  const tick = async (ms: number) => { await act(async () => { await vi.advanceTimersByTimeAsync(ms); }); };
  const first = (l = STATUS()): Live => normalizeLive(l)!;
  const txt = () => host.textContent ?? '';
  const click = async (label: RegExp) => {
    const b = [...host.querySelectorAll('button')].find((x) => label.test(x.textContent ?? ''));
    if (!b) throw new Error(`bouton ${label} introuvable : ${txt()}`);
    await act(async () => { b.dispatchEvent(new MouseEvent('click', { bubbles: true })); });
    await tick(0);
  };

  it('affiche progression, couche, hauteur, temps restant, heure de fin, températures, étapes et image', async () => {
    const app = mk({ get_live_state: () => ({ structuredContent: LIVE(STATUS({}, { stage_report: { current: 'Chauffe', stages: [{ label: 'Mise à niveau : effectuée à 21:05', state: 'done' }] } })) }) });
    await render(<LiveCard app={app} first={first()} />);
    await tick(10);
    const t = txt();
    expect(t).toContain('boite.gcode.3mf'); expect(t).toContain('41 %'); expect(t).toContain('couche 52 / 124');
    expect(t).toContain('10,3 / 24,8 mm'); expect(t).toContain('Reste 1 h 28 min'); expect(t).toContain('fin à 21:05');
    expect(t).toContain('215 / 220 °C'); expect(t).toContain('60 / 60 °C'); expect(t).toContain('31 °C'); expect(t).toContain('Mise à niveau : effectuée à 21:05');
    expect(host.querySelector('[role=progressbar]')!.getAttribute('aria-valuenow')).toBe('41');
    expect((host.querySelector('.live-cam img') as HTMLImageElement).src).toBe('data:image/jpeg;base64,QUJDRA==');
    expect(t).toContain('Image de 21:04:12');
  });

  it('rafraîchit l\'image toutes les 2,5 s et l\'état toutes les 5 s, sans empiler de requêtes', async () => {
    const app = mk();
    await render(<LiveCard app={app} first={first()} />);
    await tick(10);
    expect(count(app, 'get_live_frame')).toBe(1); expect(count(app, 'get_live_state')).toBe(1);
    expect(app.callServerTool).toHaveBeenCalledWith({ name: 'get_live_frame', arguments: { printer_id: 'pr_1' } });
    await tick(10_000);
    expect(count(app, 'get_live_frame')).toBe(5); // 0, 2,5, 5, 7,5, 10 s
    expect(count(app, 'get_live_state')).toBe(3); // 0, 5, 10 s
  });

  it('n\'attend pas la réponse précédente : une image lente ne déclenche pas une seconde requête', async () => {
    let release: () => void = () => {};
    const app = mk({ get_live_frame: () => new Promise((r) => { release = () => r(FRAME); }) });
    await render(<LiveCard app={app} first={first()} />);
    await tick(20_000);
    expect(count(app, 'get_live_frame')).toBe(1);
    await act(async () => { release(); });
    await tick(2500);
    expect(count(app, 'get_live_frame')).toBe(2);
  });

  it('s\'arrête quand la vue n\'est pas visible et reprend à son retour', async () => {
    const app = mk();
    await render(<LiveCard app={app} first={first()} />);
    await tick(10);
    const f0 = count(app, 'get_live_frame'), s0 = count(app, 'get_live_state');
    vis = 'hidden';
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')); });
    await tick(30_000);
    expect(count(app, 'get_live_frame')).toBe(f0); expect(count(app, 'get_live_state')).toBe(s0);
    vis = 'visible';
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')); });
    await tick(10);
    expect(count(app, 'get_live_frame')).toBeGreaterThan(f0); expect(count(app, 'get_live_state')).toBeGreaterThan(s0);
  });

  it('s\'arrête après N minutes sans interaction, avec un bouton « Reprendre »', async () => {
    const app = mk();
    await render(<LiveCard app={app} first={first()} idleMs={60_000} />);
    await tick(55_000);
    expect(count(app, 'get_live_frame')).toBeGreaterThan(10);
    await tick(6_000); // 61 s sans interaction
    const f = count(app, 'get_live_frame'), s = count(app, 'get_live_state');
    expect(txt()).toContain('Actualisation en pause');
    await tick(30_000);
    expect(count(app, 'get_live_frame')).toBe(f); expect(count(app, 'get_live_state')).toBe(s);
    await click(/Reprendre$/);
    await tick(10);
    expect(txt()).not.toContain('Actualisation en pause');
    expect(count(app, 'get_live_frame')).toBeGreaterThan(f);
  });

  it('une interaction repousse la mise en pause', async () => {
    const app = mk();
    await render(<LiveCard app={app} first={first()} idleMs={60_000} />);
    await tick(50_000);
    await act(async () => { host.querySelector('section')!.dispatchEvent(new Event('pointerdown', { bubbles: true })); });
    await tick(50_000); // 100 s au total, mais 50 s depuis l'interaction
    expect(txt()).not.toContain('Actualisation en pause');
  });

  it('état terminal : lit le résumé final avec la photo, puis ne rappelle plus rien', async () => {
    const done = LIVE(STATUS({ state: 'COMPLETED', print: { printer_state: 'finished', percent: 100, layer: 124, total_layers: 124 } }, {}, { state: 'finished', progress_percent: 100 }), { terminal: true, ends_at_local: undefined });
    const calls: Calls[] = [];
    const app = mk({
      get_live_state: (a) => { calls.push(a); return { structuredContent: a.with_final_photo ? { ...done, final: { outcome: 'completed', duration: '2 h 05 min', grams: 18.4, grams_basis: 'journal des impressions (pesée)', photo: { mime: 'image/jpeg', data: 'QUJD' } } } : { ...LIVE(), status: STATUS({ state: 'COMPLETED' }), terminal: true } }; },
    });
    await render(<LiveCard app={app} first={first()} />);
    await tick(5_100); // la première lecture annonce l'état terminal -> une lecture de plus avec la photo
    expect(calls.map((c) => !!c.with_final_photo)).toEqual([false, true]);
    const t = txt();
    expect(t).toContain('Impression terminée'); expect(t).toContain('2 h 05 min'); expect(t).toContain('18,4 g'); expect(t).toContain('journal des impressions');
    expect((host.querySelector('.live-final img') as HTMLImageElement).src).toBe('data:image/jpeg;base64,QUJD');
    expect(host.querySelector('.live-actions')).toBeNull(); // plus de boutons Arrêter / Reprendre
    const n = app.callServerTool.mock.calls.length;
    await tick(60_000);
    expect(app.callServerTool.mock.calls.length).toBe(n);
  });

  it('un suivi déjà terminal à l\'ouverture ne lance aucune boucle d\'image', async () => {
    const app = mk({ get_live_state: () => ({ structuredContent: { ...LIVE(STATUS({ state: 'FAILED' })), terminal: true, final: { outcome: 'failed' }} }) });
    await render(<LiveCard app={app} first={first(STATUS({ state: 'FAILED' }))} />);
    await tick(30_000);
    expect(count(app, 'get_live_frame')).toBe(0);
    expect(count(app, 'get_live_state')).toBe(1);
    expect(txt()).toContain('Impression échouée');
  });

  it('« Arrêter » appelle cancel_print et n\'affiche que la demande à valider : aucun arrêt direct', async () => {
    const app = mk({
      cancel_print: () => ({ structuredContent: { print_job_id: 'pj_1', outcome: 'stop_requested', stop_url: 'http://127.0.0.1:7878/stop/stp_abc', question: 'Arrêter la boîte ?', note: 'n' } }),
      get_stop_view_state: () => ({ structuredContent: { stop: { stop_id: 'stp_abc', print_job_id: 'pj_1', file: 'boite.gcode.3mf', printer_name: 'P1S', percent: 41, layer: 52, total_layers: 124, expires_at: '' }, stop_url: 'http://127.0.0.1:7878/stop/stp_abc', code_set: true, locked: false, attempts_left: 3, setup_url: '' } }),
    });
    await render(<LiveCard app={app} first={first()} />);
    await tick(10);
    expect(txt()).toContain('crée une demande d\'arrêt');
    await click(/Arrêter$/);
    await tick(10);
    expect(app.callServerTool.mock.calls.filter((c) => c[0].name === 'cancel_print')).toEqual([[{ name: 'cancel_print', arguments: { print_job_id: 'pj_1' } }]]);
    expect(count(app, 'get_stop_view_state')).toBeGreaterThan(0);
    expect(txt()).toContain('Arrêter la boîte ?'); // la question de validation est affichée avec la carte d'arrêt
    expect([...host.querySelectorAll('button')].some((b) => /Arrêter$/.test(b.textContent ?? '') && b.classList.contains('live-btn-stop'))).toBe(false); // pas de second clic possible
    await tick(30_000);
    expect(count(app, 'approve_stop_with_code')).toBe(0); // la carte n'arrête rien d'elle-même
    expect(count(app, 'cancel_print')).toBe(1);
  });

  it('« Arrêter » refusé ou sans demande : le dit, sans rien arrêter', async () => {
    const app = mk({ cancel_print: () => ({ structuredContent: { print_job_id: 'pj_1', outcome: 'refused', note: 'Le lancement est en cours.' } }) });
    await render(<LiveCard app={app} first={first()} />);
    await click(/Arrêter$/);
    expect(txt()).toContain('Le lancement est en cours.');
    expect(count(app, 'approve_stop_with_code')).toBe(0);
  });

  it('pas de bouton Arrêter hors impression en cours', async () => {
    const app = mk({ get_live_state: () => ({ structuredContent: LIVE(STATUS({ state: 'UPLOADED' }, {}, null)) }) });
    await render(<LiveCard app={app} first={first(STATUS({ state: 'UPLOADED' }, {}, null))} />);
    await tick(10);
    expect([...host.querySelectorAll('button')].some((b) => /Arrêter/.test(b.textContent ?? ''))).toBe(false);
    expect(count(app, 'get_live_frame')).toBe(0); // pas de caméra avant le lancement
  });

  it('impression en pause : « Reprendre » crée la demande de reprise, validée par l\'humain', async () => {
    const paused = STATUS({}, { alert: "attend une action à l'écran : bobine externe vide" }, { state: 'paused' });
    const app = mk({
      get_live_state: () => ({ structuredContent: LIVE(paused) }),
      resume_print: () => ({ structuredContent: { action_id: 'act_1', action_url: 'http://127.0.0.1:7878/action/act_1', question: 'Reprendre ?', details: ['Bobine rechargée'] } }),
    });
    await render(<LiveCard app={app} first={first(paused)} />);
    await tick(10);
    expect(txt()).toContain('En pause'); expect(txt()).toContain('bobine externe vide');
    await click(/Reprendre l'impression$/);
    expect(app.callServerTool).toHaveBeenCalledWith({ name: 'resume_print', arguments: { print_job_id: 'pj_1' } });
    expect(txt()).toContain('Reprendre ?'); expect(txt()).toContain('http://127.0.0.1:7878/action/act_1');
    expect(count(app, 'validate_action_with_code')).toBe(0);
  });

  it('caméra indisponible : message, nouvel essai espacé, le reste de la carte continue', async () => {
    let n = 0;
    const app = mk({ get_live_frame: () => { n++; throw new Error('caméra injoignable'); } });
    await render(<LiveCard app={app} first={first()} />);
    await tick(10);
    expect(txt()).toContain('Caméra indisponible'); expect(txt()).toContain('caméra injoignable'); expect(txt()).toContain('41 %');
    await tick(9_000);
    expect(n).toBe(1); // pas de martèlement
    await tick(2_000);
    expect(n).toBe(2);
  });

  it('serveur sans caméra : aucune requête d\'image', async () => {
    const app = mk({ get_live_state: () => ({ structuredContent: LIVE(STATUS(), { camera_available: false }) }) });
    await render(<LiveCard app={app} first={first()} />);
    await tick(6_000);
    expect(count(app, 'get_live_frame')).toBeLessThanOrEqual(1);
  });

  it('champs manquants : la carte s\'affiche sans erreur', async () => {
    const bare = normalizeLive({ job: { id: 'pj_9', state: 'PRINTING', printer_id: 'pr_1' } })!;
    const app = mk({ get_live_state: () => ({ structuredContent: { status: { job: { id: 'pj_9', state: 'PRINTING', printer_id: 'pr_1' } } } }), get_live_frame: () => ({ isError: true, content: [{ type: 'text', text: 'Aucune caméra' }] }) });
    await render(<LiveCard app={app} first={bare} />);
    await tick(10);
    expect(host.querySelector('.live-indet')).not.toBeNull();
    expect(txt()).toContain('Impression'); expect(txt()).toContain('Aucune caméra');
    expect(txt()).not.toContain('NaN'); expect(txt()).not.toContain('undefined');
  });

  it('état illisible ou refus de l\'outil : garde le dernier état et le dit', async () => {
    const app = mk({ get_live_state: () => ({ isError: true, content: [{ type: 'text', text: 'x' }] }) });
    await render(<LiveCard app={app} first={first()} />);
    await tick(10);
    expect(txt()).toContain('41 %'); expect(txt()).toContain('dernier état affiché');
  });
});
