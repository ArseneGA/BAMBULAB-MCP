// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { PartCard, normalizePart } from './part';
import { PART_SHEET, PART_SHEET_EMPTY, PROJECT_DETAIL } from './fixtures';
import { mockApp, mount, ok } from './testkit';

let m: ReturnType<typeof mount>;
afterEach(() => m?.unmount());

describe('carte Fiche pièce', () => {
  it('reconnaît la fiche pièce, pas la fiche modèle ni un projet', () => {
    expect(normalizePart(PART_SHEET)?.model.id).toBe('mdl_sheet1');
    expect(normalizePart({ model: { id: 'mdl_1' }, files: [], versions: [] })).toBeUndefined();
    expect(normalizePart(PROJECT_DETAIL)).toBeUndefined();
    expect(normalizePart(null)).toBeUndefined();
  });

  it('compteurs D7, coût mesuré, onglet Impressions avec verdict, raison et bandeau de paliers', async () => {
    m = mount();
    await m.render(<PartCard app={mockApp()} data={normalizePart(PART_SHEET)!} onOpen={() => {}} />);
    const t = m.text();
    expect(t).toContain('imprimée 5 fois (100 % atteint), 4 jugées réussies, 1 jugée ratée');
    expect(t).toContain('Imprimée 5 fois');
    expect(t).toContain('3,40 €');
    expect(t).toContain('Jugée réussie'); expect(t).toContain('Jugée ratée'); expect(t).toContain('stringing');
    expect(t).toContain('Photos du journal (9 palier(s) avec photo)');
    expect(t).toContain('photo non disponible (caméra injoignable à ce palier)'); // palier 40 %
    expect(t).toContain('aucune photo de palier relevée'); // 2e impression, sans journal photo
    expect(t).toContain('Dernier profil réussi');
    expect(t).not.toMatch(/Lancer|Approuver|Réimprimer/);
  });

  it('onglets : versions suivant parent_id, profils avec médianes, résistance par version avec assumed', async () => {
    m = mount();
    await m.render(<PartCard app={mockApp()} data={normalizePart(PART_SHEET)!} onOpen={() => {}} />);
    await m.click(/^Versions/);
    expect(m.text()).toContain('v2 (courante)'); expect(m.text()).toContain('issue de ver_1'); expect(m.text()).toContain('Jamais testée');
    await m.click(/^Profils/);
    expect(m.text()).toContain('0.20mm Standard'); expect(m.text()).toContain('dernier réussi'); expect(m.text()).toContain('2 h');
    await m.click(/^Résistance/);
    expect(m.text()).toContain('Résistance de la version v2'.length > 0 ? 'hypothèses (assumed)' : '');
    expect(m.text()).toContain('jamais testée en résistance'); // v1
  });

  it('pièce jamais imprimée : zéros, états vides explicites, sans photo ni 3D ni erreur', async () => {
    m = mount();
    await m.render(<PartCard app={mockApp()} data={normalizePart(PART_SHEET_EMPTY)!} onOpen={() => {}} />);
    expect(m.text()).toContain('jamais imprimée'); expect(m.text()).toContain('Jamais imprimée : aucune impression au journal');
    expect(m.text()).toContain('non disponible');
    await m.click(/^Versions/); expect(m.text()).toContain('Aucune version');
    await m.click(/^Profils/); expect(m.text()).toContain('Aucun profil testé');
    await m.click(/^Résistance/); expect(m.text()).toContain('Aucun test de résistance');
  });

  it('gestes : voir en 3D, ouvrir une impression, juger une impression ; photo à la demande', async () => {
    m = mount();
    const calls: Array<[string, Record<string, unknown>]> = [];
    const app = mockApp({
      get_model_details: (a) => { calls.push(['get_model_details', a]); return ok({}); },
      get_print_sheet: (a) => { calls.push(['get_print_sheet', a]); return ok({}); },
      submit_print_verdict: (a) => { calls.push(['submit_print_verdict', a]); return ok({ print_job_id: 'pj_x', verdict: 'success', changed: true, note: 'ok', fail_reasons: [{ id: 'other', label: 'Autre' }] }); },
      get_part_sheet: (a) => { calls.push(['get_part_sheet', a]); return ok(PART_SHEET); },
      get_sheet_photo: (a) => { calls.push(['get_sheet_photo', a]); return { content: [{ type: 'image', data: 'AAAA', mimeType: 'image/jpeg' }] }; },
    });
    const sheet = { ...PART_SHEET, prints: [{ ...PART_SHEET.prints[0], verdict: undefined, verdict_text: 'non jugée' }, PART_SHEET.prints[1]] };
    await m.render(<PartCard app={app} data={normalizePart(sheet)!} onOpen={() => {}} />);
    await m.click(/^Voir en 3D$/);
    await m.click(/Ouvrir la fiche d'impression/);
    await m.click(/Juger cette impression/);
    await m.click(/^Réussi$/);
    await m.click(/Voir la photo du palier 10 %/);
    expect(calls.map((c) => c[0])).toEqual(['get_model_details', 'get_print_sheet', 'submit_print_verdict', 'get_part_sheet', 'get_sheet_photo']);
    expect(calls[0][1]).toEqual({ model_id: 'mdl_sheet1' });
    expect(calls[1][1]).toEqual({ print_job_id: 'pj_5' });
    expect(calls[2][1]).toEqual({ print_job_id: 'pj_5', verdict: 'success' });
    expect(calls[4][1]).toEqual({ sha256: 'a10'.padEnd(64, '0') });
    expect(m.host.querySelector('img')?.getAttribute('src')).toBe('data:image/jpeg;base64,AAAA');
  });

  it('impression déclarée : listée « déclarée », coût hors mesure ; liste tronquée signalée', async () => {
    m = mount();
    const sheet = { ...PART_SHEET, prints_truncated: true, prints_total: 30,
      prints: [{ ...PART_SHEET.prints[0], declared: true, origin: 'external' as const, level: 'declared' as const, actual_cost: undefined }],
      totals: { ...PART_SHEET.totals, declared_prints: 1 } };
    await m.render(<PartCard app={mockApp()} data={normalizePart(sheet)!} onOpen={() => {}} />);
    expect(m.text()).toContain('Déclarée'); expect(m.text()).toContain('coût hors mesure (déclarée)');
    expect(m.text()).toContain('exclues du coût et du temps mesurés');
    expect(m.text()).toContain('1 sur 30 impressions');
  });

  it('erreur d\'un outil affichée, pas de plantage', async () => {
    m = mount();
    const app = mockApp({ get_model_details: () => ({ isError: true, content: [{ type: 'text', text: 'aperçu indisponible' }] }) });
    await m.render(<PartCard app={app} data={normalizePart(PART_SHEET)!} onOpen={() => {}} />);
    await m.click(/^Voir en 3D$/);
    expect(m.text()).toContain('aperçu indisponible');
  });
});
