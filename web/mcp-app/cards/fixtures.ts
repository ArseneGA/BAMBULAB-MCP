/** Données d'exemple au format réel des outils (structuredContent), partagées par les tests des cartes. */

const PREVIEW = { state: 'ready', sha256: 'ab12' };
export const MODEL = (id: string, name: string, over: Record<string, unknown> = {}) => ({
  id, name, collection_id: 'col_1', collection_path: 'Maison/Cuisine', tags: ['cuisine'], dimensions: { x: 80, y: 40, z: 25 }, formats: ['stl'], file_count: 1, size_bytes: 120000,
  availability: 'linux', versions: 1, preview: PREVIEW, created_at: '2026-11-01T10:00:00Z', updated_at: '2026-11-02T10:00:00Z', ...over,
});

export const SEARCH = {
  models: [MODEL('mod_1', 'Support de câbles'), MODEL('mod_2', 'Boîte à épices', { formats: ['3mf', 'step'], dimensions: { x: 120, y: 120, z: 60 }, tags: ['cuisine', 'rangement'] }), MODEL('mod_3', 'Engrenage', { formats: ['step'], availability: 'windows_only', preview: { state: 'windows_only' } })],
  total: 7, limit: 3, offset: 0, has_more: true, next_offset: 3,
};
export const COLLECTION = {
  collection: { id: 'col_1', name: 'Cuisine', path: 'Maison/Cuisine', models: 3, total_models: 5, created_at: '2026-10-01T10:00:00Z' },
  subcollections: [{ id: 'col_2', name: 'Épices', path: 'Maison/Cuisine/Épices', models: 2, total_models: 2, created_at: '2026-10-01T10:00:00Z' }],
  models: { models: [MODEL('mod_1', 'Support de câbles')], total: 1, limit: 20, offset: 0, has_more: false },
};

const M = (value: number, unit: string, label = 'estimation slicer') => ({ value, unit, label });
export const COMPARE = {
  experiment_id: 'exp_1', model_version_id: 'ver_7', goal_id: 'goal_1', complete: true,
  candidates: [
    { id: 'cand_ref', label: 'Référence', kind: 'reference', state: 'succeeded', rationale: 'profil de base', changes: [], expected_tradeoffs: [], metrics: { print_time_s: M(7200, 's'), filament_g: M(34.2, 'g'), unsupported_overhang_mm2: M(120, 'mm²', 'géométrie') } },
    { id: 'cand_a', label: 'Parois épaisses', kind: 'variant', state: 'succeeded', rationale: 'plus rigide', changes: ['wall_loops : 2 → 4'], expected_tradeoffs: ['plus lent'], metrics: { print_time_s: M(9000, 's'), filament_g: M(41.5, 'g'), unsupported_overhang_mm2: M(0, 'mm²', 'géométrie'), rigidity: M(0.8, ''), translucency: M(35, '%') } },
    { id: 'cand_b', label: 'Couches fines', kind: 'variant', state: 'succeeded', rationale: 'surface lisse', changes: ['layer_height : 0.2 → 0.12'], expected_tradeoffs: [], metrics: { print_time_s: M(12600, 's'), filament_g: M(34.0, 'g'), unsupported_overhang_mm2: M(60, 'mm²', 'géométrie'), rigidity: M(0.5, ''), translucency: M(60, '%') }, warnings: ['Surplomb de 60 mm² sans support : risque de défaut.'] },
    { id: 'cand_c', label: 'Remplissage gyroïde', kind: 'variant', state: 'failed', rationale: 'léger', changes: [], expected_tradeoffs: [], error: 'Découpe impossible : objet hors plateau.' },
  ],
  rankings: [
    { criterion: 'speed', label: 'Temps', metric: 'print_time_s', better: 'lower', order: [{ candidate_id: 'cand_ref', value: 7200 }, { candidate_id: 'cand_a', value: 9000 }], basis: 'slicer' },
    { criterion: 'mass', label: 'Matière', metric: 'filament_g', better: 'lower', order: [{ candidate_id: 'cand_b', value: 34.0 }, { candidate_id: 'cand_ref', value: 34.2 }], basis: 'slicer' },
    { criterion: 'overhang', label: 'Surplomb', metric: 'unsupported_overhang_mm2', better: 'lower', order: [{ candidate_id: 'cand_a', value: 0 }, { candidate_id: 'cand_b', value: 60 }], basis: 'géométrie' },
    { criterion: 'rigid', label: 'Rigidité', metric: 'rigidity', better: 'higher', order: [{ candidate_id: 'cand_a', value: 0.8 }, { candidate_id: 'cand_b', value: 0.5 }], basis: 'ingénierie' },
  ],
  not_evaluable: [], notice: 'Aucun gagnant désigné.',
};

const FIL = (g: number, price: number) => ({ slot: 1, material: 'PETG', family: 'PETG', grams: g, price_per_kg: price, price_source: 'saisi', amount: +(g * price / 1000).toFixed(2) });
const EST = (kind: string, total: number) => ({
  kind, label: 'Support de câbles · plateau 1', currency: 'EUR', filament: [FIL(34.2, 22)], filament_total: 0.75,
  electricity: { model: 'P1S', power_source: 'défaut', preheat_minutes: 8, preheat_w: 250, print_minutes: 120, print_w: 95, kwh: 0.23, price_per_kwh: 0.2, amount: 0.05 },
  wear: { machine_hours: 2.1, machine_wear_amount: 0.1, nozzle_wear_amount: 0.02, amount: 0.12 },
  electricity_total: 0.05, wear_total: 0.12, total, machine_seconds: 7680, grams_total: 34.2, assumptions: ['Puissance moyenne par défaut du modèle P1S.', 'Prix du kWh : 0,20 €.'],
});
export const ESTIMATE = { print_job_id: 'pj_1', estimate: EST('estimate', 0.92), actual: EST('actual', 0.98), note: 'Estimation : prix par défaut indicatifs.' };

export const PROJECTS = {
  label: '1 oct. – 9 nov. 2026', currency: 'EUR',
  projects: [
    { key: 'col_1', name: 'Cuisine', prints: 6, succeeded: 5, failed: 1, hours: 14.5, failed_hours: 1.2, grams: 310, cost: 7.8, pending_jobs: 1, pending_estimate: 1.1 },
    { key: 'col_2', name: 'Atelier', prints: 3, succeeded: 3, failed: 0, hours: 6, failed_hours: 0, grams: 120, cost: 2.9 },
  ],
};
const ROW = (key: string, name: string, cost: number, hours: number, grams: number) => ({ key, name, prints: 3, succeeded: 3, failed: 0, hours, failed_hours: 0, grams, cost });
export const DASHBOARD = {
  label: 'Du 1 sept. au 9 nov. 2026', from: '2026-09-01', to: '2026-11-09', period: 'month', currency: 'EUR',
  totals: { prints: 14, succeeded: 12, partial: 0, failed: 2, cancelled: 0, success_rate: 0.857, machine_hours: 48.5, filament_grams: 820, cost: 19.4, rated_count: 0 },
  by_material: [ROW('PLA', 'PLA', 11.2, 30, 540), ROW('PETG', 'PETG', 8.2, 18.5, 280)],
  by_printer: [ROW('pr_1', 'P1S atelier', 19.4, 48.5, 820)],
  by_period: [ROW('2026-09', 'sept. 2026', 4.1, 10, 150), ROW('2026-10', 'oct. 2026', 9.3, 22.5, 400), ROW('2026-11', 'nov. 2026', 6.0, 16, 270)],
  by_project: [], notes: ['Coûts estimés.'],
};

export const SPOOLS = {
  spools: [
    { id: 'sp_1', material: 'PLA', brand: 'Bambu Lab', name: 'Vert jade', color_hex: '#00ae42', diameter_mm: 1.75, net_weight_g: 1000, remaining_g: 720, remaining_source: 'ams', archived: false, slot: 'ams1:1', remaining_percent: 72, status: 'ok' },
    { id: 'sp_2', material: 'PETG', brand: 'Sunlu', name: 'Transparent', color_hex: '#d9e8f0', translucent: true, diameter_mm: 1.75, net_weight_g: 1000, remaining_g: 60, remaining_source: 'human', archived: false, remaining_percent: 6, status: 'low' },
    { id: 'sp_3', material: 'TPU', color_hex: '', diameter_mm: 1.75, net_weight_g: 500, remaining_g: 0, remaining_source: 'consumption', archived: false, remaining_percent: 0, status: 'empty' },
  ],
  alert_margin: { margin_percent: 10, margin_grams: 50 }, note: '3 bobine(s).',
};
export const AMS = {
  printer_id: 'pr_1', reachable: true, stale: false, ams_present: true, active_slot: 'ams1:1', humidity_levels: { '1': 2 }, inventory_loaded: [], notes: [],
  trays: [
    { slot: 'ams1:1', unit: 1, index: 1, empty: false, type: 'PLA', sub_brand: 'PLA Basic', color: '00AE42FF', remain_percent: 72, active: true, spool: SPOOLS.spools[0], match: 'assigned' },
    { slot: 'ams1:2', unit: 1, index: 2, empty: false, type: 'PETG', color: 'D9E8F0FF', remain_percent: 6, active: false, spool: SPOOLS.spools[1], match: 'assigned', warnings: ['Reste faible.'] },
    { slot: 'ams1:3', unit: 1, index: 3, empty: true, remain_percent: 0, active: false, match: 'empty' },
    { slot: 'ams1:4', unit: 1, index: 4, empty: false, type: 'ABS', color: 'FF0000FF', remain_percent: 50, active: false, match: 'unregistered' },
  ],
};

const EV = (at: string, to: string) => ({ at, event: 'x', from: 'PREPARED', to });
const JOB = (id: string, name: string, state: string, over: Record<string, unknown> = {}) => ({ id, name, state, printer_id: 'pr_1', use_ams: true, summary: { printer_model: 'P1S', filament_presets: ['Bambu PLA Basic'] }, created_at: '2026-11-08T08:00:00Z', updated_at: '2026-11-08T12:00:00Z', ...over });
export const HISTORY = {
  view: 'all', counts: { all: 3, active: 1, finished: 1, failed: 1 },
  print_jobs: [
    JOB('pj_1', 'Support de câbles', 'PRINTING', { print: { percent: 42, layer: 80, total_layers: 190, remaining_minutes: 75, printer_state: 'printing', observed_at: '2026-11-09T09:00:00Z' } }),
    JOB('pj_2', 'Boîte à épices', 'COMPLETED', { events: [EV('2026-11-08T08:10:00Z', 'PRINTING'), EV('2026-11-08T10:40:00Z', 'COMPLETED')] }),
    JOB('pj_3', 'Engrenage', 'FAILED', { events: [EV('2026-11-07T08:10:00Z', 'PRINTING'), EV('2026-11-07T08:55:00Z', 'FAILED')] }),
  ],
  progress: { pj_1: { job_id: 'pj_1', state: 'running', percent: 42 } },
};
export const QUEUE = {
  queue: [
    { id: 'q1', position: 1, print_job_id: 'pj_5', printer_id: 'pr_1', file_name: 'plateau-1.3mf', state: 'ready', message: 'Prête : attend l\'accord humain.', estimated_minutes: 95, estimated_grams: 28.4, added_at: '2026-11-09T08:00:00Z' },
    { id: 'q2', position: 2, print_job_id: 'pj_6', printer_id: 'pr_1', file_name: 'couvercle.3mf', state: 'needs_assignments', message: 'Affecter les bobines.', added_at: '2026-11-09T08:05:00Z' },
  ],
  note: '2 élément(s).',
};
const LOG = (id: string, name: string, outcome: string, over: Record<string, unknown> = {}) => ({
  id: `log_${id}`, print_job_id: id, name, printer_model: 'P1S', state: 'COMPLETED', outcome, material_family: 'PETG', finished_at: '2026-11-08T10:40:00Z',
  settings: { process: '0.20mm Standard', filaments: ['Bambu PETG HF'], nozzle_mm: 0.4, layers: 120, supports: false }, estimated: { seconds: 9000, grams: 33 }, actual: { seconds: 9300, grams: 34.1 },
  duration_delta_pct: 3.3, mass_delta_pct: 3.3, actual_cost: { ...EST('actual', 0.98) }, rating: 4, comment: 'Très propre, léger stringing.', defects: ['stringing'],
  created_at: '2026-11-08T10:40:00Z', updated_at: '2026-11-08T10:40:00Z', ...over,
});
export const PRINT_LOG = LOG('pj_2', 'Boîte à épices', 'success');
export const PRINT_LOGS = { entries: [PRINT_LOG, LOG('pj_3', 'Engrenage', 'failed', { rating: undefined, comment: undefined, defects: [] })], total: 9, created_from_finished_prints: 0, defect_catalog: [] };

export const PRINTERS = {
  printers: [
    { id: 'pr_1', model_id: 'p1s', model_name: 'Bambu Lab P1S', name: 'P1S atelier', host: '192.168.1.50', serial: '01P00A000000000', firmware: '01.08.02.00', nozzle_mm: 0.4, ams_units: 1, access_code_set: true, created_at: '2026-10-01T10:00:00Z' },
    { id: 'pr_2', model_id: 'a1mini', model_name: 'Bambu Lab A1 mini', name: 'A1 mini bureau', host: '192.168.1.51', serial: '0300A0000000000', nozzle_mm: 0.4, ams_units: 0, access_code_set: false, setup_url: 'http://127.0.0.1:7878/setup/pr_2', created_at: '2026-10-02T10:00:00Z' },
  ],
  note: 'Le code d\'accès LAN n\'apparaît jamais ici.',
};
export const PRINTER_STATUS = {
  printer_id: 'pr_1', reachable: true, state: 'printing', progress_percent: 42, layer: 80, total_layers: 190, remaining_minutes: 75, temps: { nozzle: 220, nozzle_target: 220, bed: 60, bed_target: 60, chamber: 30 },
  print_error: 0, hms_count: 0, current_file: 'support-cables.3mf', firmware: '01.08.02.00', last_job: { print_job_id: 'pj_2', name: 'Boîte à épices', state: 'COMPLETED', at: '2026-11-08T10:40:00Z' },
};

// ---- Suivi de projet (G9) : formes réelles de get_project, list_projects, get_pending_verdicts, set_print_verdict ----
const PROG = (done: number, total: number, printed: number, units: number, text: string, over: Record<string, unknown> = {}) => ({
  parts_done: done, parts_total: total, units_printed: printed, units_total: units, units_judged_success: 0, units_judged_failed: 0, units_judged_redo: 0, units_unjudged: 0,
  complete: total > 0 && done === total, text, ...over,
});
export const PROJECT_DETAIL = {
  project: { id: 'prj_aaaa', name: 'Étagère', description: 'salon', status: 'idea', due_date: '2026-12-24', rev: 3, created_at: 'x', updated_at: 'x' },
  level: 1, path: [],
  parts: [
    { model_id: 'mod_1', model_name: 'Équerre', quantity: 2, role: 'support', printed: 2, judged_success: 1, judged_failed: 1, judged_redo: 0, unjudged: 0, attempts: 2, in_progress: 0, satisfied: true, text: '2/2 imprimées (1 jugée réussie, 1 jugée ratée)' },
    { model_id: 'mod_2', model_name: 'Capot', quantity: 1, printed: 0, judged_success: 0, judged_failed: 0, judged_redo: 0, unjudged: 0, attempts: 0, in_progress: 0, satisfied: false, text: '0/1 imprimée : jamais imprimée' },
    { model_id: 'mod_3', model_name: 'Vis', quantity: 1, implicit: true, printed: 0, judged_success: 0, judged_failed: 0, judged_redo: 0, unjudged: 0, attempts: 0, in_progress: 0, satisfied: false, text: '0/1 imprimée : jamais imprimée' },
  ],
  own: PROG(1, 3, 2, 4, '1/3 pièces imprimées, 2/4 unités'),
  progress: PROG(2, 4, 4, 6, '2/4 pièces imprimées, 4/6 unités (dont 1 jugée réussie, 1 jugée ratée ou à refaire)', { units_judged_success: 1, units_judged_failed: 1 }),
  sub_projects: [
    { id: 'prj_bbbb', name: 'Pieds', status: 'design', own: PROG(1, 1, 1, 1, '1/1 pièce imprimée, 1/1 unité'), progress: PROG(1, 1, 1, 1, '1/1 pièce imprimée, 1/1 unité'), parts_count: 1, parent_id: 'prj_aaaa', depth: 1 },
    { id: 'prj_cccc', name: 'Embouts', status: 'idea', own: PROG(0, 1, 0, 2, '0/1 pièce imprimée, 0/2 unités'), progress: PROG(0, 1, 0, 2, '0/1 pièce imprimée, 0/2 unités'), parts_count: 1, parent_id: 'prj_bbbb', depth: 2 },
  ],
  totals: { available: true, actual_cost: 1.84, currency: 'EUR', actual_seconds: 28800, actual_grams: 400, planned_cost: 0.5, in_progress: 1, attempts: 4, basis: 'x' },
  variants: [], suggested_status: 'printing', suggested_reason: 'au moins une impression est liée aux pièces du projet',
  next_action: 'Imprimer « Capot » : 0/1.', levels: { avancement: 'calculé', verdict: 'déclaré' }, note: 'Projet « Étagère »',
};
export const PROJECT_LIST = {
  projects: [
    { project: { id: 'prj_aaaa', name: 'Étagère', status: 'printing', rev: 1 }, depth: 0, progress: PROG(2, 4, 4, 6, '2/4 pièces imprimées, 4/6 unités'), parts_count: 2, suggested_status: '' },
    { project: { id: 'prj_bbbb', name: 'Pieds', status: 'design', parent_id: 'prj_aaaa', rev: 1 }, depth: 1, progress: PROG(1, 1, 1, 1, '1/1 pièce imprimée, 1/1 unité'), parts_count: 1 },
    { project: { id: 'prj_dddd', name: 'Lampe', status: 'idea', archived: true, rev: 1 }, depth: 0, progress: PROG(0, 0, 0, 0, 'aucune pièce'), parts_count: 0 },
  ],
  total: 2, applied_filters: { include_archived: 'true' }, counts_by_status: { idea: 1, printing: 1, design: 1, done: 0 }, note: '2 projet(s).',
};
export const FAIL_REASONS = [{ id: 'stringing', label: 'Fils' }, { id: 'warping', label: 'Gauchissement' }, { id: 'other', label: 'Autre' }];
export const PENDING = {
  pending: [
    { print_job_id: 'pj_1', log_id: 'log_1', name: 'equerre.gcode.3mf', model_name: 'Équerre', state: 'COMPLETED', reached_100: true, material_family: 'PLA', finished_at: '2026-11-09T21:05:00Z' },
    { print_job_id: 'pj_2', log_id: 'log_2', name: 'capot.gcode.3mf', state: 'FAILED', reached_100: false, material_family: 'PETG', finished_at: '2026-11-08T10:00:00Z' },
  ],
  total: 2, fail_reasons: FAIL_REASONS, note: '2 impression(s) terminée(s) sans verdict.',
};
// Fiches pièce et impression (G9, PJ5 et PJ10) : jeux d'essai du contrat, partagés avec les pages web.
export { PART_SHEET, PART_SHEET_EMPTY, PRINT_SHEET, PRINT_SHEET_EXTERNAL, PRINT_SHEET_NO_CAMERA } from '../../src/api/sheets.fixtures';
export const VERDICT_RESULT = {
  print_job_id: 'pj_1', log_id: 'log_1', verdict: 'failed', fail_reason: 'warping', verdict_at: '2026-11-10T08:00:00Z', verdict_source: 'human_card', previous_verdict: '',
  changed: true, note: 'Verdict enregistré : ratée (warping). Il n\'a aucun effet sur l\'imprimante ni sur la file.', fail_reasons: FAIL_REASONS,
};