// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ProgressCard, fmtBytes, fmtEta, normalizeProgress, refreshCall, type AppLike } from './progress';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLElement; let root: Root;
beforeEach(() => { vi.useFakeTimers(); host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); vi.useRealTimers(); });

const JOB = (extra: Record<string, unknown> = {}, pd: Record<string, unknown> = {}, job: Record<string, unknown> = {}) => ({
  job: { id: 'job_1', kind: 'slice', state: 'running', progress: 0.4, ...job },
  progress_detail: { job_id: 'job_1', state: 'running', percent: 40, terminal: false, elapsed_seconds: 12, ...pd },
  ...extra,
});

describe('normalizeProgress : champs actuels (branche courante)', () => {
  it('lit progress_detail d\'un travail', () => {
    const p = normalizeProgress(JOB({}, { phase: 'slicing', bytes_done: 5_242_880, bytes_total: 10_485_760, eta_seconds: 95 }))!;
    expect(p).toMatchObject({ source: 'job', id: 'job_1', percent: 40, phase: 'slicing', phaseLabel: 'Découpe', doneBytes: 5_242_880, totalBytes: 10_485_760, etaSeconds: 95, terminal: false, failed: false });
    expect(p.title).toBe('Travail slice');
  });
  it('lit un envoi : pourcentage déduit des octets, terminé quand complete', () => {
    const p = normalizeProgress({ upload_id: 'upl_1', filename: 'boite.stl', total_size: 2000, received: 500, next_offset: 500, complete: false })!;
    expect(p).toMatchObject({ source: 'upload', id: 'upl_1', percent: 25, doneBytes: 500, totalBytes: 2000, terminal: false });
    expect(p.title).toContain('boite.stl');
    const d = normalizeProgress({ upload_id: 'upl_1', filename: 'b.stl', total_size: 2000, received: 2000, next_offset: 2000, complete: true })!;
    expect(d).toMatchObject({ percent: 100, terminal: true, failed: false });
  });
});

describe('normalizeProgress : champs stables (autre branche) et défensif', () => {
  it('privilégie percent, done_bytes, total_bytes, eta_seconds, phase, phase_label au premier niveau', () => {
    const p = normalizeProgress(JOB({ percent: 73, done_bytes: 73, total_bytes: 100, eta_seconds: 12, phase: 'uploading', phase_label: 'Envoi vers l\'imprimante' }))!;
    expect(p).toMatchObject({ percent: 73, doneBytes: 73, totalBytes: 100, etaSeconds: 12, phase: 'uploading', phaseLabel: 'Envoi vers l\'imprimante' });
  });
  it('un champ manquant ne casse rien : barre indéterminée, aucune ligne vide', () => {
    const p = normalizeProgress({ job_id: 'job_9', state: 'running' })!;
    expect(p.percent).toBeNull(); expect(p.etaSeconds).toBeUndefined(); expect(p.totalBytes).toBeUndefined(); expect(p.terminal).toBe(false);
  });
  it('ignore les valeurs non numériques et borne 0..100', () => {
    expect(normalizeProgress(JOB({ percent: 'beaucoup' }, { percent: 140 }))!.percent).toBe(100);
    expect(normalizeProgress(JOB({ eta_seconds: 'bientôt' }))!.etaSeconds).toBeUndefined();
  });
  it('état terminal : succès = 100 %, échec garde son message', () => {
    const ok = normalizeProgress(JOB({}, { percent: undefined, terminal: true }, { state: 'succeeded' }))!;
    expect(ok).toMatchObject({ terminal: true, failed: false, percent: 100 });
    const ko = normalizeProgress(JOB({}, { terminal: true }, { state: 'failed', error: { code: 'x', message: 'Bambu Studio absent' } }))!;
    expect(ko).toMatchObject({ terminal: true, failed: true, message: 'Bambu Studio absent' });
  });
  it('ne reconnaît pas un résultat d\'une autre forme', () => {
    expect(normalizeProgress(undefined)).toBeUndefined();
    expect(normalizeProgress({ models: [], total: 0 })).toBeUndefined();
    expect(normalizeProgress({ confirmation_id: 'cf_1', binding: {} })).toBeUndefined();
    expect(normalizeProgress([1, 2])).toBeUndefined();
  });
  it('choisit l\'outil de rafraîchissement', () => {
    expect(refreshCall(normalizeProgress(JOB())!).name).toBe('get_job_status');
    expect(refreshCall(normalizeProgress({ upload_id: 'u', total_size: 1, received: 0 })!)).toEqual({ name: 'get_upload_status', arguments: { upload_id: 'u' } });
  });
  it('formate durées et tailles', () => {
    expect(fmtEta(42)).toBe('42 s'); expect(fmtEta(125)).toBe('2 min 05 s'); expect(fmtEta(3720)).toBe('1 h 02 min'); expect(fmtEta(undefined)).toBeUndefined();
    expect(fmtBytes(512)).toBe('512 o'); expect(fmtBytes(5_242_880)).toBe('5.00 Mo'); expect(fmtBytes(30_000_000)).toBe('28.6 Mo');
  });
});

describe('ProgressCard', () => {
  const mk = (handler: () => unknown) => {
    const app = { callServerTool: vi.fn(async () => handler()) };
    return app as unknown as AppLike & typeof app;
  };
  const render = async (el: JSX.Element) => { await act(async () => { root.render(el); }); };
  const tick = async (ms: number) => { await act(async () => { await vi.advanceTimersByTimeAsync(ms); }); };

  it('affiche barre, pourcentage, phase, octets et durée restante', async () => {
    const first = normalizeProgress(JOB({ phase_label: 'Découpe en cours', done_bytes: 1_048_576, total_bytes: 4_194_304, eta_seconds: 65 }))!;
    await render(<ProgressCard app={mk(() => ({}))} first={first} pollMs={60_000} />);
    const bar = host.querySelector('[role=progressbar]')!;
    expect(bar.getAttribute('aria-valuenow')).toBe('40');
    expect((host.querySelector('.prog-fill') as HTMLElement).style.width).toBe('40%');
    const t = host.textContent ?? '';
    expect(t).toContain('40 %'); expect(t).toContain('Découpe en cours'); expect(t).toContain('1.00 Mo / 4.00 Mo'); expect(t).toContain('1 min 05 s');
  });

  it('se rafraîchit par l\'outil d\'état puis s\'arrête au terminal', async () => {
    const results = [JOB({}, { percent: 70 }), JOB({}, { percent: 100, terminal: true }, { state: 'succeeded' })];
    const app = mk(() => ({ structuredContent: results.shift() ?? results[0] }));
    await render(<ProgressCard app={app} first={normalizeProgress(JOB())!} pollMs={1000} />);
    await tick(1000);
    expect(app.callServerTool).toHaveBeenCalledWith({ name: 'get_job_status', arguments: { job_id: 'job_1', log_tail: 1 } });
    expect(host.textContent).toContain('70 %');
    await tick(1000);
    expect(host.textContent).toContain('100 %'); expect(host.textContent).toContain('Terminé');
    const n = app.callServerTool.mock.calls.length;
    await tick(5000);
    expect(app.callServerTool.mock.calls.length).toBe(n); // plus d'appel une fois terminé
  });

  it('garde le dernier état et le dit quand l\'actualisation échoue', async () => {
    const app = { callServerTool: vi.fn(async () => { throw new Error('hors ligne'); }) } as unknown as AppLike;
    await render(<ProgressCard app={app} first={normalizeProgress(JOB())!} pollMs={500} />);
    await tick(600);
    expect(host.textContent).toContain('40 %');
    expect(host.textContent).toContain('dernier état affiché');
  });

  it('barre indéterminée sans pourcentage, avertissement si bloqué, aucun appel sans état non terminal inutile', async () => {
    const first = normalizeProgress({ job_id: 'job_2', state: 'running', stalled: true })!;
    await render(<ProgressCard app={mk(() => ({}))} first={first} pollMs={60_000} />);
    expect(host.querySelector('.prog-indet')).not.toBeNull();
    expect(host.querySelector('[role=alert]')?.textContent).toContain('bloqué');
  });

  it('un envoi terminé n\'appelle rien', async () => {
    const app = mk(() => ({}));
    await render(<ProgressCard app={app} first={normalizeProgress({ upload_id: 'u', filename: 'a.stl', total_size: 10, received: 10, complete: true })!} pollMs={100} />);
    await tick(1000);
    expect(app.callServerTool).not.toHaveBeenCalled();
    expect(host.textContent).toContain('100 %');
  });
});
