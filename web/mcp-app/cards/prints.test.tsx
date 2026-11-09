// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { PrintsCard, jobDurationMinutes, normalizePrints } from './prints';
import { HISTORY, PRINT_LOG, PRINT_LOGS, QUEUE } from './fixtures';
import { mockApp, mount, ok } from './testkit';
import type { PrintJob } from '../../src/api/printTypes';

let m: ReturnType<typeof mount>;
afterEach(() => m?.unmount());

describe('carte Impressions', () => {
  it('reconnaît historique, file, fiche et journal', () => {
    expect(normalizePrints(HISTORY)?.kind).toBe('history');
    expect(normalizePrints(QUEUE)?.kind).toBe('queue');
    expect(normalizePrints(PRINT_LOG)?.kind).toBe('log');
    expect(normalizePrints(PRINT_LOGS)?.kind).toBe('logs');
    expect(normalizePrints({ job: {} })).toBeUndefined();
  });

  it('durée mesurée d\'après les événements PRINTING -> terminal', () => {
    expect(jobDurationMinutes(HISTORY.print_jobs[1] as unknown as PrintJob)).toBe(150);
    expect(jobDurationMinutes(HISTORY.print_jobs[0] as unknown as PrintJob)).toBeUndefined();
  });

  it('historique : statuts colorés, progression, durée, miniature ; clic -> get_print_status', async () => {
    const status = ok({ job: { id: 'pj_2' } });
    const app = mockApp({ get_print_status: () => status });
    const opened: unknown[] = [];
    m = mount();
    await m.render(<PrintsCard app={app} data={normalizePrints(HISTORY)!} onOpen={(r) => opened.push(r)} />);
    const t = m.text();
    expect(t).toContain('En impression'); expect(t).toContain('42 %'); expect(t).toContain('reste 1 h 15'); expect(t).toContain('Terminée'); expect(t).toContain('2 h 30'); expect(t).toContain('Échec'); expect(t).toContain('45 min');
    const badges = [...m.host.querySelectorAll('.lc-badge')].map((b) => b.className);
    expect(badges.some((c) => c.includes('lc-t-ok'))).toBe(true); expect(badges.some((c) => c.includes('lc-t-fail'))).toBe(true); expect(badges.some((c) => c.includes('lc-t-run'))).toBe(true);
    expect(m.host.querySelectorAll('svg.lc-box')).toHaveLength(3);
    await m.click(/Suivi de Boîte à épices/);
    expect(app.callServerTool).toHaveBeenCalledWith({ name: 'get_print_status', arguments: { print_job_id: 'pj_2' } });
    expect(opened).toEqual([status]);
  });

  it('file : rang, état, estimations ; clic -> get_print_status', async () => {
    const app = mockApp({ get_print_status: () => ok({}) });
    m = mount();
    await m.render(<PrintsCard app={app} data={normalizePrints(QUEUE)!} onOpen={() => {}} />);
    expect(m.text()).toContain('plateau-1.3mf'); expect(m.text()).toContain('attend l\'accord humain'); expect(m.text()).toContain('1 h 35'); expect(m.text()).toContain('28,4 g'); expect(m.text()).toContain('Affectation manquante');
    await m.click(/Suivi de couvercle/);
    expect(app.callServerTool).toHaveBeenCalledWith({ name: 'get_print_status', arguments: { print_job_id: 'pj_6' } });
  });

  it('fiche (get_print_log) : durée réelle vs estimée, coût, note, défauts, bouton de suivi', async () => {
    const app = mockApp({ get_print_status: () => ok({}) });
    m = mount();
    await m.render(<PrintsCard app={app} data={normalizePrints(PRINT_LOG)!} onOpen={() => {}} />);
    const t = m.text();
    expect(t).toContain('Boîte à épices'); expect(t).toContain('Réussie'); expect(t).toContain('2 h 35'); expect(t).toContain('estimée 2 h 30'); expect(t).toContain('34,1 g'); expect(t).toContain('0,98 €');
    expect(t).toContain('Très propre'); expect(t).toContain('stringing'); expect(m.host.querySelector('[aria-label="Note 4 sur 5"]')).not.toBeNull();
    await m.click(/Ouvrir le suivi/);
    expect(app.callServerTool).toHaveBeenCalledWith({ name: 'get_print_status', arguments: { print_job_id: 'pj_2' } });
  });

  it('journal : coût, note et durée par ligne', async () => {
    m = mount();
    await m.render(<PrintsCard app={mockApp()} data={normalizePrints(PRINT_LOGS)!} onOpen={() => {}} />);
    expect(m.text()).toContain('2 sur 9'); expect(m.text()).toContain('0,98 €'); expect(m.text()).toContain('Ratée'); expect(m.text()).toContain('« Très propre');
  });

  it('états vide et erreur', async () => {
    m = mount();
    await m.render(<PrintsCard app={mockApp()} data={normalizePrints({ ...HISTORY, print_jobs: [] })!} onOpen={() => {}} />);
    expect(m.text()).toContain('Aucune impression dans cette vue');
    m.unmount(); m = mount();
    await m.render(<PrintsCard app={mockApp()} data={normalizePrints({ queue: [], note: '' })!} onOpen={() => {}} />);
    expect(m.text()).toContain('La file est vide');
    m.unmount(); m = mount();
    const app = mockApp({ get_print_status: () => { throw new Error('bambud injoignable'); } });
    await m.render(<PrintsCard app={app} data={normalizePrints(HISTORY)!} onOpen={() => {}} />);
    await m.click(/Suivi de Engrenage/);
    expect(m.host.querySelector('[role=alert]')?.textContent).toContain('bambud injoignable');
  });
});
