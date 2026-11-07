import { ApiError, BASE, CSRF_HEADER } from './client';

/** Types et appels de l'API bobines / AMS / multicolore / file d'attente (internal/httpapi/ams.go). */

export interface SpoolObserved { type?: string; color?: string; remain_percent: number; slot?: string; at: string }
export interface Spool {
  id: string; material: string; brand?: string; name?: string; color_hex?: string; diameter_mm: number; net_weight_g: number; remaining_g: number;
  remaining_source: 'human' | 'ams' | 'consumption' | 'default'; price_per_kg?: number; opened_at?: string; last_dried_at?: string; drying_note?: string; notes?: string;
  printer_id?: string; slot?: string; rfid_uuid?: string; archived: boolean; human_fields?: string[]; observed?: SpoolObserved;
  remaining_percent: number; status: 'ok' | 'low' | 'empty' | 'archived';
}
export interface SpoolInput {
  material?: string; brand?: string; name?: string; color_hex?: string; net_weight_g?: number; remaining_g?: number; price_per_kg?: number;
  opened_at?: string; last_dried_at?: string; drying_note?: string; notes?: string; printer_id?: string; slot?: string; unassign?: boolean; replace_existing?: boolean; archived?: boolean;
}
export interface AlertMargin { margin_percent: number; margin_grams: number }

export interface AMSTray {
  slot: string; unit: number; index: number; empty: boolean; type?: string; sub_brand?: string; color?: string; uuid?: string; remain_percent: number; weight_g?: number; active: boolean;
}
export interface AMSTrayView extends AMSTray {
  spool?: Spool; match: string; suggested_spool?: Spool; warnings?: string[]; ams_estimate_g?: number; effective_remaining_g?: number;
}
export interface AMSView {
  printer_id: string; reachable: boolean; stale: boolean; error?: string; ams_present: boolean; active_slot?: string; trays: AMSTrayView[];
  humidity_levels?: Record<string, number>; inventory_loaded: Spool[]; notes?: string[]; observed_at?: string;
}

export interface FilamentAssignment { filament_slot: number; tray: string; material_id: string }
export interface ColorSlotPlan {
  filament_slot: number; parts?: string; wanted_color?: string; wanted_type?: string; tray?: string; spool_id?: string; spool_label?: string; loaded_color?: string;
  delta_e: number; quality: string; pinned?: boolean; needed_g: number; purge_g: number; purge_cost?: number; note?: string;
}
export interface PurgeReport {
  plate: number; source: string; prime_tower_enabled: boolean; color_changes: number; flush_g: number; tower_g: number; waste_g: number; total_g: number; waste_percent: number;
  per_filament: Array<{ filament_slot: number; flush_g: number; tower_g: number; waste_g: number; used_g: number }>; matrix_estimate_g?: number; deviation_percent: number; check?: string;
}
export interface CheckSlot {
  filament_slot: number; tray: string; spool_label?: string; needed_g: number; remaining_g: number; remaining_source: string; required_with_margin_g: number; shortfall_g?: number;
  verdict: 'ok' | 'marginal' | 'insufficient' | 'unknown' | 'no_spool'; detail: string; alternatives?: string[];
}
export interface CheckOutput { print_job_id: string; ok: boolean; verdict: string; slots: CheckSlot[]; summary: string; margin_percent: number; margin_grams: number }
export interface ColorPlan {
  print_job_id: string; printer_id: string; plate: number; slots: ColorSlotPlan[]; assignments: FilamentAssignment[]; ams_mapping?: number[]; use_ams: boolean; unmatched_filaments?: number[];
  complete: boolean; purge?: PurgeReport; purge_cost?: { waste_g: number; cost?: number; basis: string; complete: boolean }; filament_check?: CheckOutput; warnings?: string[]; next: string;
}
export interface ColorWish { filament_slot: number; color_hex?: string; material?: string; part?: string }

export type QueueState = 'slicing' | 'needs_assignments' | 'waiting' | 'uploading' | 'ready' | 'printing' | 'done' | 'failed' | 'removed';
export interface QueueEntry {
  id: string; position: number; print_job_id: string; printer_id: string; file_name?: string; plate?: number; assignments?: FilamentAssignment[]; note?: string;
  state: QueueState; job_state?: string; message: string; confirm_url?: string; estimated_minutes?: number; estimated_grams?: number; added_at: string;
}

async function call<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  let res: Response;
  try {
    res = await fetch(BASE + path, {
      method, signal, credentials: 'same-origin',
      headers: { ...CSRF_HEADER, ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
      body: body === undefined ? undefined : JSON.stringify(body),
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
    throw new ApiError(d?.error ?? `Erreur HTTP ${res.status}`, res.status, d?.code ?? 'http', false, (data as Record<string, unknown> | undefined) ?? undefined);
  }
  return data as T;
}

const qs = (p: Record<string, string | number | boolean | undefined>) => {
  const u = new URLSearchParams();
  for (const [k, v] of Object.entries(p)) if (v !== undefined && v !== '' && v !== false) u.set(k, v === true ? '1' : String(v));
  const s = u.toString();
  return s ? `?${s}` : '';
};

export const filamentApi = {
  spools: (p: { printer_id?: string; material?: string; low?: boolean; archived?: boolean } = {}, signal?: AbortSignal) =>
    call<{ spools: Spool[]; alert_margin: AlertMargin }>('GET', '/api/spools' + qs(p), undefined, signal),
  createSpool: (b: SpoolInput) => call<Spool>('POST', '/api/spools', b),
  updateSpool: (id: string, b: SpoolInput) => call<Spool>('PATCH', `/api/spools/${encodeURIComponent(id)}`, b),
  ams: (printerId: string, signal?: AbortSignal) => call<AMSView>('GET', `/api/printers/${encodeURIComponent(printerId)}/ams`, undefined, signal),
  setMargin: (m: AlertMargin) => call<AlertMargin>('PUT', '/api/filament/settings', m),
  check: (b: { print_job_id: string; printer_id?: string; assignments?: FilamentAssignment[] }) => call<CheckOutput>('POST', '/api/filament/check', b),
  planColors: (b: { print_job_id: string; printer_id?: string; colors?: ColorWish[]; fixed?: FilamentAssignment[] }) => call<ColorPlan>('POST', '/api/filament/plan-colors', b),
  queue: (finished = false, signal?: AbortSignal) => call<{ queue: QueueEntry[] }>('GET', '/api/print-queue' + qs({ finished }), undefined, signal),
  queuePrint: (b: { print_job_id: string; printer_id?: string; assignments?: FilamentAssignment[]; note?: string }) => call<QueueEntry>('POST', '/api/print-queue', b),
  reorder: (ids: string[]) => call<{ queue: QueueEntry[] }>('POST', '/api/print-queue/reorder', { queue_ids: ids }),
  removeFromQueue: (id: string) => call<QueueEntry>('DELETE', `/api/print-queue/${encodeURIComponent(id)}`),
  renewConfirmation: (id: string) => call<QueueEntry>('POST', `/api/print-queue/${encodeURIComponent(id)}/confirmation`),
};
