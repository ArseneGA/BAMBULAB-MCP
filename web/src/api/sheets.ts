import { ApiError, BASE, CSRF_HEADER } from './client';

/*
 * Fiche pièce (PJ5) et fiche d'impression (PJ10) du suivi de projet (G9) : routes GET /api/models/{id}/sheet et
 * GET /api/prints/{id}/sheet (internal/httpapi/sheets.go). Elles renvoient EXACTEMENT les structures des outils MCP
 * get_part_sheet et get_print_sheet (internal/sheets/types.go : CONTRAT, ne pas en changer les noms de champs).
 *
 * Conventions de lecture :
 *  - une valeur numérique inconnue est ABSENTE (`undefined`) et la raison est dans `unavailable[<champ>]` : afficher
 *    « non disponible » avec cette raison, jamais 0 ;
 *  - `levels[<valeur>]` dit « measured » (mesuré), « computed » (calculé) ou « declared » (déclaré) ;
 *  - « imprimée » = l'impression a atteint 100 %, verdict ou pas ; le verdict est un jugement à part (déclaré) ;
 *  - les images passent par `url` (/api/camera/photos/{sha256}, même cookie de session) : aucune URL signée.
 * Lecture seule : aucune route ne lance, n'arrête ni ne reprend d'impression.
 */

export type Level = 'measured' | 'computed' | 'declared';
export const LEVEL_LABEL: Record<Level, string> = { measured: 'mesuré', computed: 'calculé', declared: 'déclaré' };
export type SheetVerdict = 'success' | 'failed' | 'redo';
export type StepStatus = 'ok' | 'unavailable' | 'absent';
export type AlertsStatus = 'none' | 'listed' | 'unavailable';
export type ApprovalStatus = 'approved' | 'requested_only' | 'none' | 'not_applicable';
export const APPROVAL_LABEL: Record<ApprovalStatus, string> = {
  approved: 'Lancement approuvé par l’humain', requested_only: 'Confirmation demandée, accord non relevé', none: 'Aucune trace d’accord', not_applicable: 'Impression déclarée hors Printalk',
};

/** Un palier (10, 20, … 100 %) du journal photo. `unavailable` : « photo non disponible » + `reason` ; `absent` : aucun relevé. */
export interface PhotoStep { percent: number; status: StepStatus; reason?: string; sha256?: string; url?: string }
export interface PhotoStrip { steps: PhotoStep[]; thumbnail?: PhotoStep; available: number; note?: string }

export interface EffectiveSettings {
  machine?: string; process?: string; filaments?: string[]; nozzle_mm?: number; overrides?: Array<{ key: string; value: unknown; target?: string }>;
  orientation?: string; bed_type?: string; plate?: number; layers?: number; supports?: boolean; deviations?: unknown[];
}

// ---- fiche pièce ----
export interface PartCounts {
  model_id: string; attempts: number; printed: number; printed_units: number; judged_success: number; judged_success_units: number;
  judged_failed: number; judged_failed_units: number; judged_redo: number; judged_redo_units: number; unjudged: number; unjudged_units: number; external: number;
  top_fail_reason?: string; top_fail_reason_n?: number; actual_cost: number; currency?: string; actual_seconds: number; actual_grams: number;
  in_progress: number; planned_cost?: number; never_printed: boolean; text: string;
}
export interface StrengthRef { test_id: string; created_at: string; material?: string; verdict?: string; safety_factor?: number; assumed: boolean }
export interface PartVersion {
  id: string; label: string; note?: string; parent_id?: string; depth: number; is_latest: boolean; created_at: string;
  strength?: StrengthRef; strength_status: 'tested' | 'never_tested';
}
export interface PartProfile {
  signature: string; label: string; settings: EffectiveSettings; attempts: number; printed: number; judged_success: number; judged_failed: number;
  judged_redo: number; unjudged: number; external: number; median_seconds?: number; median_grams?: number; median_cost?: number; currency?: string;
  last_print_at?: string; last_success_at?: string; level: Level;
}
export interface PartPrint {
  print_job_id: string; log_id: string; name?: string; started_at?: string; finished_at: string; state: string; outcome: string; reached_100: boolean;
  part_count: number; origin: 'printalk' | 'external'; declared: boolean; level: 'measured' | 'declared'; profile_signature: string; profile_label: string;
  verdict?: SheetVerdict; verdict_text: string; fail_reason?: string; actual_seconds?: number; actual_grams?: number; actual_cost?: number; currency?: string; photos: PhotoStrip;
}
export interface PartTotals {
  available: boolean; reason?: string; actual_cost: number; currency?: string; actual_seconds: number; actual_grams: number; cost_per_judged_success?: number;
  planned_cost?: number; declared_prints: number; declared_seconds?: number; declared_grams?: number;
}
export interface PartSheet {
  model: { id: string; name: string; tags?: string[]; collection_id?: string; trashed?: boolean; dimensions_mm?: { x: number; y: number; z: number } };
  counts: PartCounts; versions: PartVersion[]; profiles: PartProfile[]; prints: PartPrint[]; prints_total: number; prints_truncated?: boolean; totals: PartTotals;
  last_success?: { print_job_id: string; finished_at: string; profile_signature: string; profile_label: string };
  projects: Array<{ id: string; name: string; status?: string }>; strength_unassigned?: StrengthRef[];
  levels: Record<string, Level>; unavailable?: Record<string, string>; include: string[]; note: string;
}

// ---- fiche d'impression ----
export interface PrintSheet {
  print_job_id: string; log_id: string;
  header: {
    name?: string; model_id?: string; model_name?: string; printer_id?: string; printer_model?: string; state: string; outcome: string; origin: 'printalk' | 'external';
    declared: boolean; started_at?: string; finished_at: string; reached_100: boolean; part_count: number;
  };
  verdict: { value?: SheetVerdict; text: string; fail_reason?: string; at?: string; source?: string; note?: string; level: 'declared'; fail_reasons: Array<{ id: string; label: string }> };
  profile: { signature: string; label: string; settings: EffectiveSettings; frozen_note: string; level: Level };
  material: {
    family?: string; spools: Array<{ spool_id: string; filament_slot: number; label?: string; grams: number; estimated: boolean; price_per_kg?: number; cost?: number; note?: string; level: Level }>;
    actual_grams?: number; estimated_grams?: number; estimated: boolean; basis?: string; level: Level;
  };
  duration: { planned_seconds?: number; actual_seconds?: number; delta_pct?: number; text: string; level: Level };
  cost: { planned?: number; actual?: number; currency?: string; level: Level };
  photos: PhotoStrip;
  camera: { available: boolean; reason?: string; alerts_status: AlertsStatus; alerts: Array<{ at: string; score: number; level: string; photo_url?: string }>; text: string; level: Level };
  approval: { status: ApprovalStatus; requested_at?: string; approved_at?: string; via?: string; text: string; level: Level };
  comment?: string; rating?: number; defects?: string[];
  levels: Record<string, Level>; unavailable?: Record<string, string>; text: string;
}

async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  let res: Response;
  try {
    res = await fetch(BASE + path, { method: 'GET', signal, credentials: 'same-origin', headers: { ...CSRF_HEADER } });
  } catch (e) {
    if ((e as Error).name === 'AbortError') throw e;
    throw new ApiError('Printalk est injoignable', 0, 'offline', true);
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

export const SHEET_SECTIONS = ['versions', 'profiles', 'prints', 'strength', 'projects'] as const;
export type SheetSection = (typeof SHEET_SECTIONS)[number];

export const sheetsApi = {
  /** Fiche pièce : `include` restreint les sections servies (défaut : toutes), `limit` borne la liste des impressions (défaut 20, max 100). */
  part: (modelId: string, p: { include?: SheetSection[]; limit?: number } = {}, signal?: AbortSignal) => {
    const u = new URLSearchParams();
    if (p.include && p.include.length > 0) u.set('include', p.include.join(','));
    if (p.limit) u.set('limit', String(p.limit));
    const s = u.toString();
    return get<PartSheet>(`/api/models/${encodeURIComponent(modelId)}/sheet${s ? `?${s}` : ''}`, signal);
  },
  /** Fiche d'impression (profil figé, bobines, durée prévue/réelle, photos, alertes, accord humain, verdict). */
  print: (printJobId: string, signal?: AbortSignal) => get<PrintSheet>(`/api/prints/${encodeURIComponent(printJobId)}/sheet`, signal),
};

/** « non disponible (raison) » pour un champ absent ; undefined si le champ est renseigné. */
export function unavailableText(sheet: { unavailable?: Record<string, string> }, field: string): string | undefined {
  const r = sheet.unavailable?.[field];
  return r === undefined ? undefined : r ? `non disponible (${r})` : 'non disponible';
}
