import { describe, expect, it } from 'vitest';
import { finishAt, formatBytesProgress, formatEta, formatMinutes, normalizeProgress } from './progress';

describe('normalizeProgress', () => {
  it('utilise les champs stables du serveur quand ils existent', () => {
    const p = normalizeProgress({ state: 'running', percent: 42.4, done_bytes: 10, total_bytes: 100, eta_seconds: 90, phase: 'uploading', phase_label: "Envoi FTPS vers l'imprimante" });
    expect(p.percent).toBe(42);
    expect(p.doneBytes).toBe(10);
    expect(p.totalBytes).toBe(100);
    expect(p.etaSeconds).toBe(90);
    expect(p.phaseLabel).toBe("Envoi FTPS vers l'imprimante");
    expect(p.terminal).toBe(false);
  });
  it('retombe sur Job.progress (ratio 0..1) et message', () => {
    const p = normalizeProgress({ state: 'running', progress: 0.37, message: 'Copie 12/30' });
    expect(p.percent).toBe(37);
    expect(p.phaseLabel).toBe('Copie 12/30');
    expect(p.etaSeconds).toBeUndefined();
  });
  it('retombe sur les octets quand aucun pourcentage n\'est fourni', () => {
    expect(normalizeProgress({ state: 'running', bytes_done: 25, bytes_total: 100 }).percent).toBe(25);
    expect(normalizeProgress({ state: 'running', done_bytes: 1, total_bytes: 4 }).percent).toBe(25);
  });
  it('lit le JobProgress existant (percent, phase, estimation, file)', () => {
    const p = normalizeProgress({ job_id: 'j', state: 'running', percent: 63, phase: 'découpe par Bambu Studio', eta_seconds: 120, estimated: true, stalled: true });
    expect(p.percent).toBe(63);
    expect(p.phaseLabel).toBe('découpe par Bambu Studio');
    expect(p.estimated).toBe(true);
    expect(p.stalled).toBe(true);
    expect(normalizeProgress({ state: 'queued', percent: 0, queue_position: 2 }).queuePosition).toBe(2);
    expect(normalizeProgress({ state: 'queued' }).phaseLabel).toBe("En file d'attente");
  });
  it('lit l\'état de l\'imprimante (progress_percent, remaining_minutes)', () => {
    const p = normalizeProgress({ progress_percent: 61, remaining_minutes: 578 });
    expect(p.percent).toBe(61);
    expect(p.etaSeconds).toBe(578 * 60);
  });
  it('borne le pourcentage et gère les états finaux', () => {
    expect(normalizeProgress({ percent: 180 }).percent).toBe(100);
    expect(normalizeProgress({ percent: -5 }).percent).toBe(0);
    const ok = normalizeProgress({ state: 'succeeded', progress: 0.9 });
    expect(ok.percent).toBe(100);
    expect(ok.terminal).toBe(true);
    expect(ok.failed).toBe(false);
    const ko = normalizeProgress({ state: 'failed', progress: 0.4 });
    expect(ko.failed).toBe(true);
    expect(ko.percent).toBe(40);
  });
  it('reste indéterminé sans aucune donnée, sans lever d\'erreur', () => {
    expect(normalizeProgress(undefined).percent).toBeUndefined();
    expect(normalizeProgress(null).percent).toBeUndefined();
    expect(normalizeProgress({ percent: 'x', progress: null }).percent).toBeUndefined();
  });
});

describe('formats', () => {
  it('formatEta', () => {
    expect(formatEta(0)).toBe('moins d\'une minute');
    expect(formatEta(30)).toBe('30 s');
    expect(formatEta(600)).toBe('10 min');
    expect(formatEta(9 * 3600 + 38 * 60)).toBe('9 h 38');
    expect(formatEta(undefined)).toBeUndefined();
    expect(formatEta(-3)).toBeUndefined();
  });
  it('formatMinutes', () => { expect(formatMinutes(24 * 60 + 43)).toBe('24 h 43'); });
  it('formatBytesProgress', () => {
    expect(formatBytesProgress(5 << 20, 10 << 20)).toBe('5,0 Mio / 10,0 Mio');
    expect(formatBytesProgress(1, 0)).toBeUndefined();
  });
  it('finishAt rend une heure locale', () => {
    expect(finishAt(3600, new Date(2026, 10, 8, 10, 5))).toMatch(/11 h 05/);
    expect(finishAt(undefined)).toBeUndefined();
  });
});
