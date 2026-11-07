import { ApiError, BASE, CSRF_HEADER } from './client';

/* Coût, journal des impressions, tableau de bord, post-traitement (routes /api/cost, /api/print-log, /api/usage, /api/post-processing). */

export interface FilamentCost { slot?: number; material: string; family?: string; grams: number; price_per_kg: number; price_source: string; amount: number }
export interface CostEstimate {
  kind: string; label: string; currency: string; filament: FilamentCost[];
  electricity: { model: string; power_source: string; preheat_minutes: number; preheat_w: number; print_minutes: number; print_w: number; kwh: number; price_per_kwh: number; amount: number };
  wear: { machine_hours: number; machine_wear_amount: number; nozzle_wear_amount: number; amount: number };
  filament_total: number; electricity_total: number; wear_total: number; total: number; machine_seconds: number; grams_total: number; assumptions: string[];
}
export interface EstimateOutput { print_job_id?: string; estimate: CostEstimate; actual?: CostEstimate; note: string }
export interface EstimateInput { print_job_id?: string; printer_model?: string; duration_minutes?: number; filaments?: Array<{ material: string; grams: number; price_per_kg?: number; label?: string }> }

export interface PowerOverride { preheat_w?: number; print_w?: number }
export interface CostSettings {
  currency: string; electricity_per_kwh: number; filament_per_kg: Record<string, number>; spool_per_kg?: Record<string, number>;
  machine_wear_per_hour: number; nozzle_wear_per_hour: number; power_overrides?: Record<string, PowerOverride>;
  using_defaults: boolean; defaulted_prices?: string[]; note: string;
}
export type SettingsPatch = Partial<Pick<CostSettings, 'currency' | 'electricity_per_kwh' | 'filament_per_kg' | 'spool_per_kg' | 'machine_wear_per_hour' | 'nozzle_wear_per_hour' | 'power_overrides'>> & { apply_to_history?: boolean };

export interface LogEntry {
  id: string; print_job_id: string; name?: string; printer_id?: string; printer_model?: string; model_id?: string; model_name?: string; collection_name?: string;
  state: string; outcome: 'success' | 'partial' | 'failed' | 'cancelled'; material_family?: string; started_at?: string; finished_at: string;
  settings: { machine?: string; process?: string; filaments?: string[]; nozzle_mm?: number; overrides?: Array<{ key: string; value: unknown; target?: string }>; orientation?: string; layers?: number; supports?: boolean; deviations?: Array<{ key: string; planned?: unknown; actual: unknown }> };
  estimated: { seconds?: number; grams?: number }; actual: { seconds?: number; grams?: number; basis?: string };
  duration_delta_pct?: number; mass_delta_pct?: number; planned_cost?: CostEstimate; actual_cost?: CostEstimate;
  photos?: string[]; camera_photos?: Array<{ id: string; kind: 'final' | 'alert'; sha256: string; taken_at: string }>; rating?: number; comment?: string; defects?: string[]; experiment_id?: string; candidate_id?: string; result_ids?: string[];
  created_at: string; updated_at: string;
}
export interface LogList { entries: LogEntry[]; total: number; created_from_finished_prints: number; defect_catalog: Array<{ id: string; label: string }> }
export interface LogUpdate { outcome?: string; rating?: number; comment?: string; defects?: string[]; actual_mass_g?: number; actual_duration_minutes?: number }

export interface AdjustmentItem { key: string; target: string; direction: string; amount: string; unit?: string; why: string }
export interface Adjustments {
  material_family: string; printer_model?: string; comparable_logs: number; automatic: boolean; how_to_apply: string; level: string; notes: string[];
  proposals: Array<{ defect: string; label: string; basis: string; occurrences_in_comparable_logs: number; summary: string; adjustments: AdjustmentItem[]; other_actions?: string[]; tradeoffs: string; sources: Array<{ title: string; url: string; verified: boolean }> }>;
  best_reference?: { log_id: string; rating: number; same_model: boolean; process?: string; filaments?: string[]; overrides?: Array<{ key: string; value: unknown }>; note: string };
}

export interface DashRow { key: string; name: string; prints: number; succeeded: number; failed: number; success_rate?: number; hours: number; failed_hours: number; grams: number; cost: number; pending_jobs?: number; pending_estimate?: number }
export interface Dashboard {
  label: string; from: string; to: string; period: string; currency: string;
  totals: { prints: number; succeeded: number; partial: number; failed: number; cancelled: number; success_rate?: number; machine_hours: number; filament_grams: number; cost: number; avg_rating?: number; rated_count: number };
  by_material: DashRow[]; by_printer: DashRow[]; by_period: DashRow[]; by_project: DashRow[]; notes: string[];
}

export interface PostAdvice {
  material: string; family: string; goals_recognized: string[]; level: string; data_version: string;
  steps: Array<{ order: number; technique: string; title: string; applies: string; steps: string[]; parameters?: Record<string, string>; hazards?: string[]; notes?: string[] }>;
  not_recommended?: Array<{ technique: string; title: string; reason: string }>;
  inserts?: Array<{ size: string; hole_diameter_mm: number; hole_depth_mm: number; typical_insert_length_mm: number; boss_outer_diameter_mm: number; min_wall_around_hole_mm: number; soldering_iron_start_temperature?: string }>;
  warnings: string[]; honesty?: string[]; sources: Array<{ title: string; url: string; verified: boolean }>;
}

async function req<T>(method: string, path: string, body?: unknown, signal?: AbortSignal, raw?: { data: Blob; type: string }): Promise<T> {
  let res: Response;
  try {
    res = await fetch(BASE + path, {
      method, signal, credentials: 'same-origin',
      headers: { ...CSRF_HEADER, ...(raw ? { 'Content-Type': raw.type } : body === undefined ? {} : { 'Content-Type': 'application/json' }) },
      body: raw ? raw.data : body === undefined ? undefined : JSON.stringify(body),
    });
  } catch (e) {
    if ((e as Error).name === 'AbortError') throw e;
    throw new ApiError('bambud est injoignable', 0, 'offline', true);
  }
  const text = await res.text();
  let data: unknown;
  if (text) { try { data = JSON.parse(text); } catch { /* corps non JSON */ } }
  if (!res.ok) {
    const d = data as { error?: string; code?: string } | undefined;
    const gateway = d?.error === undefined && [502, 503, 504].includes(res.status);
    throw new ApiError(d?.error ?? `Erreur HTTP ${res.status}`, res.status, d?.code ?? (gateway ? 'offline' : 'http'), gateway);
  }
  return data as T;
}

const qs = (p: Record<string, string | number | undefined>) => {
  const u = new URLSearchParams();
  for (const [k, v] of Object.entries(p)) if (v !== undefined && v !== '') u.set(k, String(v));
  const s = u.toString();
  return s ? `?${s}` : '';
};

export const insightsApi = {
  settings: (signal?: AbortSignal) => req<CostSettings>('GET', '/api/cost/settings', undefined, signal),
  saveSettings: (p: SettingsPatch) => req<CostSettings>('PUT', '/api/cost/settings', p),
  estimate: (i: EstimateInput) => req<EstimateOutput>('POST', '/api/cost/estimate', i),
  dashboard: (p: { from?: string; to?: string; period?: string; printer_id?: string }, signal?: AbortSignal) => req<Dashboard>('GET', '/api/usage/dashboard' + qs(p), undefined, signal),
  log: (p: { outcome?: string; material_family?: string; limit?: number }, signal?: AbortSignal) => req<LogList>('GET', '/api/print-log' + qs(p), undefined, signal),
  updateLog: (id: string, u: LogUpdate) => req<LogEntry>('PATCH', `/api/print-log/${encodeURIComponent(id)}`, u),
  addPhoto: (id: string, file: File) => req<{ photo: string; entry: LogEntry }>('POST', `/api/print-log/${encodeURIComponent(id)}/photo`, undefined, undefined, { data: file, type: file.type || 'application/octet-stream' }),
  photoUrl: (id: string, sha: string) => `${BASE}/api/print-log/${encodeURIComponent(id)}/photos/${encodeURIComponent(sha)}`,
  adjustments: (log_id: string) => req<Adjustments>('POST', '/api/print-log/adjustments', { log_id }),
  postProcessing: (i: { material: string; finish?: string; geometry?: string; usage?: string; insert_sizes?: string[] }) => req<PostAdvice>('POST', '/api/post-processing', i),
};
