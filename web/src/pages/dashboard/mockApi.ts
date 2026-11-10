import { vi } from 'vitest';

/** Faux serveur pour les tests de l'Accueil : une table « méthode + chemin » → corps JSON ; les appels sont enregistrés. Données simulées. */
export interface Call { method: string; url: string; body?: string }
export type Routes = Record<string, unknown | ((call: Call) => unknown)>;

const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } });

export function installFetch(routes: Routes): { calls: Call[]; set: (r: Routes) => void } {
  const calls: Call[] = [];
  let table = routes;
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    const method = (init?.method ?? 'GET').toUpperCase();
    const call: Call = { method, url: String(url), body: init?.body as string | undefined };
    calls.push(call);
    const path = String(url).split('?')[0];
    const hit = table[`${method} ${path}`] ?? Object.entries(table).find(([k]) => k.endsWith('*') && `${method} ${path}`.startsWith(k.slice(0, -1)))?.[1];
    if (hit === undefined) return json({ error: 'introuvable', code: 'not_found' }, 404);
    const v = typeof hit === 'function' ? (hit as (c: Call) => unknown)(call) : hit;
    if (v instanceof Response) return v;
    return json(v);
  }));
  return { calls, set: (r) => { table = r; } };
}

export const PRINTER = { id: 'p1', model_id: 'p1s', model_name: 'P1S', name: 'Atelier', host: '192.168.1.5', serial: 'SN1', nozzle_mm: 0.4, ams_units: 1, access_code_set: true, created_at: '2026-01-01T00:00:00Z' };
export const status = (over: Record<string, unknown> = {}) => ({
  printer_id: 'p1', reachable: true, state: 'idle', progress_percent: 0, layer: 0, total_layers: 0, remaining_minutes: 0, temps: { nozzle: 25, nozzle_target: 0, bed: 24, bed_target: 0, chamber: 0 }, print_error: 0, hms_count: 0, ...over,
});
export const job = (over: Record<string, unknown> = {}) => ({ id: 'j1', state: 'COMPLETED', printer_id: 'p1', name: 'Équerre', use_ams: false, created_at: '2026-11-09T08:00:00Z', updated_at: '2026-11-09T10:00:00Z', ...over });
export const jobs = (list: unknown[]) => ({ print_jobs: list, view: 'all', counts: {} });
export const entry = (over: Record<string, unknown> = {}) => ({
  id: 'l1', print_job_id: 'j1', name: 'Équerre', state: 'COMPLETED', outcome: 'success', finished_at: new Date().toISOString(), settings: {}, estimated: { seconds: 3600, grams: 20 }, actual: { seconds: 3600, grams: 20 },
  reached_100: true, created_at: '', updated_at: '', ...over,
});
export const log = (entries: unknown[]) => ({ entries, total: entries.length, created_from_finished_prints: 0, defect_catalog: [] });
export const month = (t: Record<string, unknown> = {}) => ({
  label: 'novembre', from: '', to: '', period: 'day', currency: 'EUR', totals: { prints: 0, succeeded: 0, partial: 0, failed: 0, cancelled: 0, machine_hours: 0, filament_grams: 0, cost: 0, rated_count: 0, ...t },
  by_material: [], by_printer: [], by_period: [], by_project: [], notes: [],
});
export const progress = (over: Record<string, unknown> = {}) => ({ parts_done: 1, parts_total: 2, units_printed: 4, units_total: 6, units_judged_success: 3, units_judged_failed: 0, units_judged_redo: 0, units_unjudged: 1, complete: false, text: '4/6 pièces imprimées', ...over });
export const BASE_ROUTES: Routes = {
  'GET /health': { ok: true, version: 'test' },
  'GET /api/printers': { printers: [PRINTER] },
  'GET /api/print-jobs': jobs([]),
  'GET /api/print-log': log([]),
  'GET /api/usage/dashboard': month(),
  'GET /api/spools': { spools: [], alert_margin: { margin_percent: 10, margin_grams: 20 } },
  'GET /api/print-queue': { queue: [] },
  'GET /api/models': { models: [{ id: 'm1', name: 'Équerre', formats: ['stl'], size_bytes: 10, updated_at: '2026-11-09T10:00:00Z' }], total: 1 },
  'GET /api/printers/p1/status': status(),
};
