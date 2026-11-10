// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { PrintSheetCard, normalizePrintSheet } from './print-sheet';
import { PART_SHEET, PRINT_SHEET, PRINT_SHEET_EXTERNAL, PRINT_SHEET_NO_CAMERA } from './fixtures';
import { mockApp, mount, ok } from './testkit';

let m: ReturnType<typeof mount>;
afterEach(() => m?.unmount());

describe('carte Fiche d\'impression', () => {
  it('reconnaît la fiche, pas une fiche pièce', () => {
    expect(normalizePrintSheet(PRINT_SHEET)?.print_job_id).toBe('pj_5');
    expect(normalizePrintSheet(PART_SHEET)).toBeUndefined();
    expect(normalizePrintSheet({ print_job_id: 'x' })).toBeUndefined();
  });

  it('blocs : verdict, profil figé, matière estimée, durée, coût, paliers, caméra, accord humain, niveaux écrits', async () => {
    m = mount();
    await m.render(<PrintSheetCard app={mockApp()} data={normalizePrintSheet(PRINT_SHEET)!} onOpen={() => {}} />);
    const t = m.text();
    expect(t).toContain('Jugée réussie'); expect(t).toContain('Belle première couche');
    expect(t).toContain('0.20mm Standard @BBL X1C'); expect(t).toContain('ne changent pas si le profil est modifié');
    expect(t).toContain('100 g (estimée, non pesée)'); expect(t).toContain('PLA Bambu noir');
    expect(t).toContain('Prévue : 2 h'); expect(t).toContain('réelle : 2 h');
    expect(t).toContain('0,68 €');
    expect(t).toContain('photo non disponible (caméra injoignable à ce palier)');
    expect(t).toContain('1 alerte de la caméra'); expect(t).toContain('score 0,62');
    expect(t).toContain('Lancement approuvé par l’humain');
    for (const lvl of ['mesuré', 'calculé', 'déclaré']) expect(t).toContain(lvl);
    expect(t).not.toMatch(/Lancer|Approuver\b|Réimprimer|Comparer avec/);
  });

  it('sans caméra ni durée réelle : « non disponible » avec la raison, distinct de « aucune alerte »', async () => {
    m = mount();
    await m.render(<PrintSheetCard app={mockApp()} data={normalizePrintSheet(PRINT_SHEET_NO_CAMERA)!} onOpen={() => {}} />);
    const t = m.text();
    expect(t).toContain('Caméra non disponible'); expect(t).not.toContain('Aucune alerte');
    expect(t).toContain('réelle : non disponible (durée réelle non relevée)');
    expect(t).toContain('Écart : non disponible (durée réelle non relevée)');
    expect(t).toContain('Non jugée'); expect(t).toContain('photo non disponible');
  });

  it('impression déclarée : badge, coût hors mesure, accord sans objet', async () => {
    m = mount();
    await m.render(<PrintSheetCard app={mockApp()} data={normalizePrintSheet(PRINT_SHEET_EXTERNAL)!} onOpen={() => {}} />);
    expect(m.text()).toContain('Déclarée hors Printalk'); expect(m.text()).toContain('hors coût mesuré');
    expect(m.text()).toContain('aucun accord de lancement à relever');
  });

  it('verdict : le bandeau passe par submit_print_verdict puis recharge la fiche', async () => {
    m = mount();
    const calls: Array<[string, Record<string, unknown>]> = [];
    const app = mockApp({
      submit_print_verdict: (a) => { calls.push(['submit_print_verdict', a]); return ok({ print_job_id: 'pj_6', verdict: 'success', changed: true, note: 'ok', fail_reasons: [{ id: 'other', label: 'Autre' }] }); },
      get_print_sheet: (a) => { calls.push(['get_print_sheet', a]); return ok(PRINT_SHEET); },
    });
    await m.render(<PrintSheetCard app={app} data={normalizePrintSheet(PRINT_SHEET_NO_CAMERA)!} onOpen={() => {}} />);
    await m.click(/^Réussi$/);
    expect(calls).toEqual([['submit_print_verdict', { print_job_id: 'pj_6', verdict: 'success' }], ['get_print_sheet', { print_job_id: 'pj_6' }]]);
  });

  it('verdict déjà donné : modifiable sur demande, erreur affichée', async () => {
    m = mount();
    await m.render(<PrintSheetCard app={mockApp()} data={normalizePrintSheet(PRINT_SHEET)!} onOpen={() => {}} />);
    expect(m.host.querySelector('.verdict-form')).toBeNull();
    await m.click(/Modifier le verdict/);
    expect(m.host.querySelector('.verdict-form')).not.toBeNull();
  });
});
