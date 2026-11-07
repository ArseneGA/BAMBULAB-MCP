/**
 * Plateau multi-pièces (chantier B) : plan_plate, execute_plate_slice et aperçu du plateau.
 * Routes : /api/plate-plans (internal/httpapi/plates.go) et POST /api/previews (mode « plate »).
 * Fichier autonome : n'ajoute rien à `api` de client.ts.
 */
import { ApiError, BASE, CSRF_HEADER, assetUrl } from './client';
import type { PrintJob } from './printTypes';

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
    throw new ApiError(d?.error ?? `Erreur HTTP ${res.status}`, res.status, d?.code ?? 'http', false, (data as Record<string, unknown> | undefined));
  }
  return data as T;
}

/** Réglages propres à une pièce (clés acceptées par le serveur : GET /api/plate-plans/settings). */
export interface PieceSettings {
  sparse_infill_density?: number;
  sparse_infill_pattern?: string;
  wall_loops?: number;
  layer_height?: number;
  top_shell_layers?: number;
  bottom_shell_layers?: number;
  brim_type?: string;
  brim_width?: number;
  enable_support?: boolean;
}
export interface PlateItemInput { model_id: string; version_id?: string; quantity?: number; label?: string; settings?: PieceSettings }
export interface PlanPlateInput {
  printer_id: string; items: PlateItemInput[]; name?: string; arrangement?: 'studio' | 'estimate'; group_by_settings?: boolean; spacing_mm?: number; max_plates?: number;
}
export interface PlanObject {
  index: number; name: string; model_id: string; copy: number; plate: number; x_mm: number; y_mm: number; width_mm: number; depth_mm: number; height_mm: number;
  rotated_90?: boolean; settings?: Record<string, string>; fits: boolean; problem?: string;
}
export interface PlanPlateInfo { index: number; objects: string[]; footprint_mm2: number; usage_percent: number; max_height_mm: number }
export interface PlatePlan {
  id: string; name: string; printer_id: string; printer_model: string; arrangement: 'studio' | 'estimate'; fits: boolean; report: string[]; warnings?: string[];
  bed: { x_mm: number; y_mm: number; max_height_mm: number; exclusions?: number[][] };
  objects: PlanObject[]; plates: PlanPlateInfo[]; jobs?: Array<{ plate: number; print_job_id: string }>; created_at: string;
}
export interface PlateSliceOutput { plan: PlatePlan; jobs: PrintJob[]; note: string }
export interface PreviewOutput { kind: string; width: number; height: number; png_sha256: string; legend: string[]; represented: string[]; not_represented: string[] }

export const plateApi = {
  settingKeys: (signal?: AbortSignal) => call<{ object_setting_keys: string[] }>('GET', '/api/plate-plans/settings', undefined, signal),
  plans: (signal?: AbortSignal) => call<{ plans: PlatePlan[] }>('GET', '/api/plate-plans', undefined, signal),
  plan: (id: string, signal?: AbortSignal) => call<PlatePlan>('GET', `/api/plate-plans/${encodeURIComponent(id)}`, undefined, signal),
  planPlate: (p: PlanPlateInput) => call<PlatePlan>('POST', '/api/plate-plans', p),
  slice: (id: string, plates?: number[]) => call<PlateSliceOutput>('POST', `/api/plate-plans/${encodeURIComponent(id)}/slice`, { plates }),
  /** Aperçu d'une plaque du plan (avant découpe) : URL de l'image. */
  previewPlan: async (planId: string, plate: number, signal?: AbortSignal) => {
    const o = await call<PreviewOutput>('POST', '/api/previews', { mode: 'plate', plan_id: planId, plate, width: 900 }, signal);
    return { ...o, url: assetUrl(`/api/previews/${o.png_sha256}`) };
  },
  /** Aperçu d'une plaque découpée : pièces lues dans le 3MF. */
  previewJob: async (printJobId: string, signal?: AbortSignal) => {
    const o = await call<PreviewOutput>('POST', '/api/previews', { mode: 'plate', print_job_id: printJobId, width: 900 }, signal);
    return { ...o, url: assetUrl(`/api/previews/${o.png_sha256}`) };
  },
};

/** Convertit un champ texte vide en `undefined`, un nombre sinon (NaN refusé : renvoie `null`). */
export function optNum(s: string): number | undefined | null {
  const t = s.trim().replace(',', '.');
  if (t === '') return undefined;
  const n = Number(t);
  return Number.isFinite(n) ? n : null;
}
