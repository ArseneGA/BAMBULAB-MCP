// @vitest-environment jsdom
import { act } from 'react';
import { afterEach, describe, expect, it } from 'vitest';
import { CompareCard, normalizeCompare, recommended } from './compare';
import { COMPARE } from './fixtures';
import { mockApp, mount, ok } from './testkit';
import type { CompareOutput } from '../../src/api/goalTypes';

let m: ReturnType<typeof mount>;
afterEach(() => m?.unmount());
const data = () => normalizeCompare(COMPARE)!;

describe('carte Comparaison', () => {
  it('reconnaît compare_variants seulement', () => {
    expect(data().experiment_id).toBe('exp_1');
    expect(normalizeCompare({ models: [], total: 0 })).toBeUndefined();
    expect(normalizeCompare({ experiment_id: 'e', candidates: [] })).toBeUndefined();
  });

  it('recommande la variante première sur le plus de critères, hors violation et échec', () => {
    expect(recommended(data())).toBe('cand_a');
    const viol = structuredClone(data()) as CompareOutput;
    viol.candidates.find((c) => c.id === 'cand_a')!.violates_non_negotiable = true;
    expect(recommended(viol)).not.toBe('cand_a');
    const tied = structuredClone(data()) as CompareOutput;
    tied.rankings = tied.rankings.slice(0, 2); // ref et b : 1 chacun -> égalité, pas de recommandation
    expect(recommended(tied)).toBeUndefined();
  });

  it('affiche temps, grammes, surplomb, jauges, avertissements, ligne recommandée', async () => {
    m = mount();
    await m.render(<CompareCard app={mockApp()} data={data()} onOpen={() => {}} />);
    const t = m.text();
    expect(t).toContain('2 h'); expect(t).toContain('2 h 30'); expect(t).toContain('3 h 30');
    expect(t).toContain('34,2 g'); expect(t).toContain('41,5 g');
    expect(t).toContain('120 mm²'); expect(t).toContain('aucun');
    expect(t).toContain('Translucidité'); expect(t).toContain('Rigidité');
    expect(m.host.querySelectorAll('[role=meter]').length).toBe(4); // 2 variantes × (translucidité + rigidité)
    expect(t).toContain('Surplomb de 60 mm² sans support'); expect(t).toContain('Découpe impossible');
    const hl = m.host.querySelectorAll('tr.lc-hl');
    expect(hl).toHaveLength(1);
    expect(hl[0].textContent).toContain('Parois épaisses'); expect(hl[0].textContent).toContain('Recommandée');
    expect(t).not.toContain('Coût'); // pas de mesure de coût fournie : pas de colonne inventée
  });

  it('« Choisir cette variante » appelle apply_variant (révision figée) et n\'imprime rien', async () => {
    const app = mockApp({ apply_variant: () => ok({ profile_revision: 'rev_9', model_id: 'mod_1', model_version_id: 'ver_7', experiment_id: 'exp_1', candidate_id: 'cand_a', label: 'Parois épaisses', overrides: [], notes: [], applied_at: '2026-11-09T10:00:00Z' }) });
    m = mount();
    await m.render(<CompareCard app={app} data={data()} onOpen={() => {}} />);
    const row = [...m.host.querySelectorAll('tr.lc-hl button')][0] as HTMLButtonElement;
    expect(row.textContent).toBe('Choisir cette variante');
    await act(async () => { row.dispatchEvent(new MouseEvent('click', { bubbles: true })); });
    expect(app.callServerTool).toHaveBeenCalledTimes(1);
    expect(app.callServerTool).toHaveBeenCalledWith({ name: 'apply_variant', arguments: { experiment_id: 'exp_1', candidate_id: 'cand_a', expected_revision: 'ver_7' } });
    expect(m.text()).toContain('rev_9'); expect(m.text()).toContain('Aucune impression lancée'); expect(m.text()).toContain('Choisie');
    expect(app.callServerTool.mock.calls.some((c) => /start_print|queue_print|request_print/.test(c[0].name))).toBe(false);
  });

  it('une variante dont la découpe a échoué ne peut pas être choisie ; erreur de l\'outil affichée', async () => {
    const app = mockApp({ apply_variant: () => ({ isError: true, content: [{ type: 'text', text: 'révision périmée : la courante est ver_8' }] }) });
    m = mount();
    await m.render(<CompareCard app={app} data={data()} onOpen={() => {}} />);
    const btns = [...m.host.querySelectorAll('tbody tr')].map((r) => r.querySelector('button') as HTMLButtonElement);
    expect(btns[3].disabled).toBe(true);
    await act(async () => { btns[0].dispatchEvent(new MouseEvent('click', { bubbles: true })); });
    expect(m.host.querySelector('[role=alert]')?.textContent).toContain('révision périmée');
  });

  it('découpes en attente : badge et actualisation par compare_variants', async () => {
    const pending = { ...COMPARE, complete: false, pending: ['cand_c'] };
    const app = mockApp({ compare_variants: () => ok({ ...COMPARE, complete: true }) });
    m = mount();
    await m.render(<CompareCard app={app} data={normalizeCompare(pending)!} onOpen={() => {}} />);
    expect(m.text()).toContain('1 découpe(s) en attente');
    await m.click(/Actualiser/);
    expect(app.callServerTool).toHaveBeenCalledWith({ name: 'compare_variants', arguments: { experiment_id: 'exp_1' } });
    expect(m.text()).not.toContain('en attente');
  });

  it('état vide', async () => {
    m = mount();
    await m.render(<CompareCard app={mockApp()} data={{ ...data(), candidates: [] }} onOpen={() => {}} />);
    expect(m.text()).toContain('Aucune variante à comparer');
  });
});
