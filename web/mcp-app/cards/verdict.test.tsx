// @vitest-environment jsdom
import { act } from 'react';
import { afterEach, describe, expect, it } from 'vitest';
import { VerdictBanner, VerdictCard, normalizeVerdict } from './verdict';
import { classifyCard } from './index';
import { FAIL_REASONS, PENDING, PRINT_LOG, VERDICT_RESULT } from './fixtures';
import { mockApp, mount, ok } from './testkit';

let m: ReturnType<typeof mount>;
afterEach(() => m?.unmount());

const select = async (host: HTMLElement, value: string) => {
  const s = host.querySelector('select') as HTMLSelectElement;
  await act(async () => { s.value = value; s.dispatchEvent(new Event('change', { bubbles: true })); });
};

describe('carte Verdict', () => {
  it('reconnaît get_pending_verdicts et set_print_verdict, pas la fiche de journal', () => {
    expect(normalizeVerdict(PENDING)?.kind).toBe('pending');
    expect(normalizeVerdict(VERDICT_RESULT)?.kind).toBe('result');
    expect(classifyCard(PENDING)?.card).toBe('verdict');
    expect(classifyCard(VERDICT_RESULT)?.card).toBe('verdict');
    expect(normalizeVerdict(PRINT_LOG)).toBeUndefined();
    expect(classifyCard(PRINT_LOG)?.card).toBe('prints');
    expect(normalizeVerdict({ pending: [], fail_reasons: [], total: 0 })?.kind).toBe('pending');
  });

  it('liste : trois gros boutons par impression ; Réussi envoie le verdict de la carte', async () => {
    m = mount();
    const calls: Array<Record<string, unknown>> = [];
    const app = mockApp({ submit_print_verdict: (a) => { calls.push(a); return ok({ ...VERDICT_RESULT, print_job_id: String(a.print_job_id), verdict: a.verdict, fail_reason: undefined, note: 'Verdict enregistré : réussie.' }); } });
    await m.render(<VerdictCard app={app} data={normalizeVerdict(PENDING)!} onOpen={() => {}} />);
    expect(m.text()).toContain('Équerre'); expect(m.text()).toContain('Terminée'); expect(m.text()).toContain('Échec');
    expect(m.host.querySelectorAll('button')).toHaveLength(6); // 3 gestes x 2 impressions : aucun bouton de lancement
    expect(m.text()).not.toMatch(/lancer|démarrer|approuver|reprendre/i);
    await m.click(/^Réussi$/);
    expect(calls).toEqual([{ print_job_id: 'pj_1', verdict: 'success' }]);
    expect(m.text()).toContain('Verdict enregistré : réussie'); expect(m.text()).toContain('Modifier');
  });

  it('Raté : choix de la raison puis enregistrement ; À refaire : raison facultative', async () => {
    m = mount();
    const calls: Array<Record<string, unknown>> = [];
    const app = mockApp({ submit_print_verdict: (a) => { calls.push(a); return ok({ ...VERDICT_RESULT, print_job_id: String(a.print_job_id), verdict: a.verdict, fail_reason: a.reason }); } });
    await m.render(<VerdictCard app={app} data={normalizeVerdict(PENDING)!} onOpen={() => {}} />);
    await m.click(/^Raté$/);
    expect(m.text()).toContain('Pourquoi a-t-elle raté'); expect(m.text()).toContain('Gauchissement');
    await select(m.host, 'warping');
    await m.click(/Enregistrer le verdict « ratée »/);
    expect(calls).toEqual([{ print_job_id: 'pj_1', verdict: 'failed', reason: 'warping' }]);
    expect(m.text()).toContain('Ratée'); expect(m.text()).toContain('Gauchissement');
    // la seconde impression : « à refaire » sans raison
    const redo = [...m.host.querySelectorAll('button')].filter((b) => b.textContent === 'À refaire');
    await act(async () => { redo[redo.length - 1].dispatchEvent(new MouseEvent('click', { bubbles: true })); });
    await m.click(/Enregistrer le verdict « à refaire »/);
    expect(calls[1]).toEqual({ print_job_id: 'pj_2', verdict: 'redo' });
  });

  it('résultat modifiable ; erreur du serveur affichée ; état vide', async () => {
    m = mount();
    const app = mockApp({ submit_print_verdict: () => ({ isError: true, content: [{ type: 'text', text: "l'impression n'est pas terminée" }] }) });
    await m.render(<VerdictCard app={app} data={normalizeVerdict(VERDICT_RESULT)!} onOpen={() => {}} />);
    expect(m.text()).toContain('Ratée'); expect(m.text()).toContain('aucun effet sur l\'imprimante');
    await m.click(/^Réussi$/);
    expect(m.text()).toContain("l'impression n'est pas terminée");
    m.unmount();
    m = mount();
    await m.render(<VerdictCard app={mockApp()} data={normalizeVerdict({ pending: [], total: 0, fail_reasons: FAIL_REASONS, note: 'Aucune impression terminée n\'attend de verdict.' })!} onOpen={() => {}} />);
    expect(m.text()).toContain("Aucune impression terminée n'attend de verdict");
  });

  it('bandeau de fin d\'impression : un geste, puis résumé modifiable', async () => {
    m = mount();
    const app = mockApp({ submit_print_verdict: (a) => ok({ ...VERDICT_RESULT, print_job_id: String(a.print_job_id), verdict: 'success', fail_reason: undefined, note: 'Verdict enregistré : réussie.' }) });
    await m.render(<VerdictBanner app={app} jobId="pj_9" />);
    expect(m.text()).toContain("Comment s'est passée l'impression");
    await m.click(/^Réussi$/);
    expect(m.text()).toContain('Verdict enregistré : réussie'); expect(m.text()).toContain('Modifier le verdict');
    expect(app.callServerTool).toHaveBeenCalledTimes(1);
  });
});
