import type { PartSheet, PhotoStep, PhotoStrip, PrintSheet } from './sheets';

/*
 * Jeux d'essai des fiches (contrat de internal/sheets/types.go) : servent aux tests des pages et des cartes. Données simulées,
 * aucune donnée réelle. PART_SHEET_EMPTY = pièce jamais imprimée ; PRINT_SHEET_EXTERNAL = impression déclarée hors Printalk ;
 * PRINT_SHEET_NO_CAMERA = caméra non disponible (distinct de « aucune alerte »).
 */

const step = (percent: number, status: PhotoStep['status'] = 'ok', reason?: string): PhotoStep =>
  status === 'ok' ? { percent, status, sha256: `a${percent}`.padEnd(64, '0'), url: `/api/camera/photos/${`a${percent}`.padEnd(64, '0')}` } : { percent, status, ...(reason ? { reason } : {}) };

/** Bandeau complet de 10 paliers ; le palier 40 % est indisponible (caméra en panne à cet instant). */
export const STRIP_FULL: PhotoStrip = (() => {
  const steps = [10, 20, 30, 40, 50, 60, 70, 80, 90, 100].map((p) => (p === 40 ? step(p, 'unavailable', 'caméra injoignable à ce palier') : step(p)));
  return { steps, thumbnail: steps[9], available: 9 };
})();
export const STRIP_NONE: PhotoStrip = { steps: [], available: 0, note: 'aucune photo de palier relevée pour cette impression' };

export const PART_SHEET: PartSheet = {
  model: { id: 'mdl_sheet1', name: 'Équerre 40 mm', tags: ['atelier'], dimensions_mm: { x: 40, y: 40, z: 12 } },
  counts: {
    model_id: 'mdl_sheet1', attempts: 6, printed: 5, printed_units: 5, judged_success: 4, judged_success_units: 4, judged_failed: 1, judged_failed_units: 1, judged_redo: 0,
    judged_redo_units: 0, unjudged: 0, unjudged_units: 0, external: 0, top_fail_reason: 'stringing', top_fail_reason_n: 1, actual_cost: 3.4, currency: 'EUR',
    actual_seconds: 36000, actual_grams: 500, in_progress: 0, never_printed: false,
    text: 'imprimée 5 fois (100 % atteint), 4 jugées réussies, 1 jugée ratée (raison la plus fréquente : stringing, n = 1)',
  },
  versions: [
    { id: 'ver_1', label: 'v1', depth: 0, is_latest: false, created_at: '2026-10-01T08:00:00Z', strength_status: 'never_tested' },
    { id: 'ver_2', label: 'v2', parent_id: 'ver_1', depth: 1, is_latest: true, created_at: '2026-10-10T08:00:00Z', strength_status: 'tested',
      strength: { test_id: 'st_1', created_at: '2026-10-11T09:00:00Z', material: 'PETG', verdict: 'ok', safety_factor: 2.4, assumed: true } },
  ],
  profiles: [
    { signature: 'sig_a1b2c3d4e5f6', label: '0.20mm Standard · Bambu PLA Basic · buse 0,4', settings: { machine: 'Bambu Lab P1S 0.4 nozzle', process: '0.20mm Standard @BBL X1C', filaments: ['Bambu PLA Basic @BBL P1S 0.4 nozzle'], nozzle_mm: 0.4 },
      attempts: 6, printed: 5, judged_success: 4, judged_failed: 1, judged_redo: 0, unjudged: 0, external: 0, median_seconds: 7200, median_grams: 100, median_cost: 0.68, currency: 'EUR',
      last_print_at: '2026-11-08T10:00:00Z', last_success_at: '2026-11-08T10:00:00Z', level: 'computed' },
  ],
  prints: [
    { print_job_id: 'pj_5', log_id: 'log_5', finished_at: '2026-11-08T10:00:00Z', started_at: '2026-11-08T08:00:00Z', state: 'COMPLETED', outcome: 'success', reached_100: true, part_count: 1, origin: 'printalk',
      declared: false, level: 'measured', profile_signature: 'sig_a1b2c3d4e5f6', profile_label: '0.20mm Standard · Bambu PLA Basic · buse 0,4', verdict: 'success', verdict_text: 'réussie',
      actual_seconds: 7200, actual_grams: 100, actual_cost: 0.68, currency: 'EUR', photos: STRIP_FULL },
    { print_job_id: 'pj_4', log_id: 'log_4', finished_at: '2026-11-05T10:00:00Z', state: 'COMPLETED', outcome: 'success', reached_100: true, part_count: 1, origin: 'printalk', declared: false, level: 'measured',
      profile_signature: 'sig_a1b2c3d4e5f6', profile_label: '0.20mm Standard · Bambu PLA Basic · buse 0,4', verdict: 'failed', verdict_text: 'ratée', fail_reason: 'stringing',
      actual_seconds: 7200, actual_grams: 100, actual_cost: 0.68, currency: 'EUR', photos: STRIP_NONE },
  ],
  prints_total: 6, totals: { available: true, actual_cost: 3.4, currency: 'EUR', actual_seconds: 36000, actual_grams: 500, cost_per_judged_success: 0.85, declared_prints: 0 },
  last_success: { print_job_id: 'pj_5', finished_at: '2026-11-08T10:00:00Z', profile_signature: 'sig_a1b2c3d4e5f6', profile_label: '0.20mm Standard · Bambu PLA Basic · buse 0,4' },
  projects: [{ id: 'prj_1', name: 'Étagère', status: 'printing' }],
  levels: { counts: 'computed', cost: 'computed', time: 'measured', verdict: 'declared' },
  include: ['versions', 'profiles', 'prints', 'strength', 'projects'], note: 'imprimée 5 fois (100 % atteint), 4 jugées réussies, 1 jugée ratée (raison la plus fréquente : stringing, n = 1)',
};

/** Pièce jamais imprimée : zéros et « jamais imprimée », sans erreur. */
export const PART_SHEET_EMPTY: PartSheet = {
  model: { id: 'mdl_empty', name: 'Pièce neuve' },
  counts: {
    model_id: 'mdl_empty', attempts: 0, printed: 0, printed_units: 0, judged_success: 0, judged_success_units: 0, judged_failed: 0, judged_failed_units: 0, judged_redo: 0, judged_redo_units: 0,
    unjudged: 0, unjudged_units: 0, external: 0, actual_cost: 0, actual_seconds: 0, actual_grams: 0, in_progress: 0, never_printed: true, text: 'jamais imprimée',
  },
  versions: [], profiles: [], prints: [], prints_total: 0,
  totals: { available: false, reason: 'jamais imprimée : aucun coût ni temps mesuré', actual_cost: 0, actual_seconds: 0, actual_grams: 0, declared_prints: 0 },
  projects: [], levels: { counts: 'computed' }, unavailable: { cost: 'jamais imprimée', last_success: 'jamais imprimée' }, include: ['versions', 'profiles', 'prints', 'strength', 'projects'], note: 'jamais imprimée',
};

const FROZEN = 'Réglages effectifs relevés à la découpe : ils ne changent pas si le profil est modifié ensuite.';

export const PRINT_SHEET: PrintSheet = {
  print_job_id: 'pj_5', log_id: 'log_5',
  header: { name: 'equerre.gcode.3mf', model_id: 'mdl_sheet1', model_name: 'Équerre 40 mm', printer_id: 'pr_1', printer_model: 'Bambu Lab P1S', state: 'COMPLETED', outcome: 'success', origin: 'printalk', declared: false,
    started_at: '2026-11-08T08:00:00Z', finished_at: '2026-11-08T10:00:00Z', reached_100: true, part_count: 1 },
  verdict: { value: 'success', text: 'réussie', at: '2026-11-08T10:05:00Z', source: 'human_card', note: 'Belle première couche.', level: 'declared',
    fail_reasons: [{ id: 'stringing', label: 'Fils et toiles entre les parties' }, { id: 'other', label: 'Autre raison (préciser dans la note)' }] },
  profile: { signature: 'sig_a1b2c3d4e5f6', label: '0.20mm Standard · Bambu PLA Basic · buse 0,4', frozen_note: FROZEN, level: 'measured',
    settings: { machine: 'Bambu Lab P1S 0.4 nozzle', process: '0.20mm Standard @BBL X1C', filaments: ['Bambu PLA Basic @BBL P1S 0.4 nozzle'], nozzle_mm: 0.4, plate: 1, layers: 100, supports: false } },
  material: { family: 'PLA', spools: [{ spool_id: 'sp_1', filament_slot: 1, label: 'PLA Bambu noir', grams: 100, estimated: true, price_per_kg: 20, cost: 2, level: 'measured' }],
    actual_grams: 100, estimated_grams: 100, estimated: true, basis: 'estimée : égale à la masse prévue par le slicer (non pesée)', level: 'measured' },
  duration: { planned_seconds: 7200, actual_seconds: 7200, delta_pct: 0, text: '0 % par rapport au prévu', level: 'computed' },
  cost: { planned: 0.68, actual: 0.68, currency: 'EUR', level: 'computed' },
  photos: STRIP_FULL,
  camera: { available: true, alerts_status: 'listed', alerts: [{ at: '2026-11-08T09:10:00Z', score: 0.62, level: 'warning' }], text: '1 alerte de la caméra', level: 'measured' },
  approval: { status: 'approved', requested_at: '2026-11-08T07:55:00Z', approved_at: '2026-11-08T07:58:00Z', via: 'web', text: 'Lancement approuvé par l’humain le 08/11 à 07:58 (web).', level: 'measured' },
  levels: { profile: 'measured', duration: 'measured', cost: 'computed', verdict: 'declared', grams: 'measured', approval: 'measured' },
  text: 'Équerre 40 mm : imprimée, jugée réussie.',
};

export const PRINT_SHEET_NO_CAMERA: PrintSheet = {
  ...PRINT_SHEET, print_job_id: 'pj_6', log_id: 'log_6', photos: STRIP_NONE,
  verdict: { text: 'non jugée', level: 'declared', fail_reasons: PRINT_SHEET.verdict.fail_reasons },
  duration: { planned_seconds: 7200, text: 'non disponible (durée réelle non relevée)', level: 'computed' },
  camera: { available: false, reason: 'détection par caméra désactivée', alerts_status: 'unavailable', alerts: [], text: 'caméra non disponible (détection par caméra désactivée)', level: 'measured' },
  unavailable: { actual_seconds: 'durée réelle non relevée', duration_delta: 'durée réelle non relevée' },
};

export const PRINT_SHEET_EXTERNAL: PrintSheet = {
  ...PRINT_SHEET, print_job_id: 'ext_1', log_id: 'log_e1', photos: STRIP_NONE,
  header: { ...PRINT_SHEET.header, origin: 'external', declared: true },
  approval: { status: 'not_applicable', text: 'Impression déclarée hors Printalk : aucun accord de lancement à relever.', level: 'measured' },
  camera: { available: false, reason: 'impression déclarée hors Printalk', alerts_status: 'unavailable', alerts: [], text: 'caméra non disponible (impression déclarée hors Printalk)', level: 'measured' },
  cost: { currency: 'EUR', level: 'computed' }, levels: { ...PRINT_SHEET.levels, duration: 'declared', grams: 'declared', profile: 'declared' },
};
