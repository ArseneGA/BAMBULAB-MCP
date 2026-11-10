import { ApiError, BASE, CSRF_HEADER } from './client';

/*
 * Suivi de projet (G9) : routes /api/projects, /api/verdicts/pending et POST /api/prints/{id}/verdict (internal/httpapi/projects.go).
 * Aucune route ne lance, n'arrête ni ne reprend d'impression ; un verdict n'a aucun effet sur l'imprimante ni sur la file.
 * « Imprimée » = l'impression a atteint 100 %, verdict ou pas ; le verdict (réussie, ratée, à refaire) est affiché à part.
 */

export type ProjectStatus = 'idea' | 'design' | 'printing' | 'done' | 'in_use' | 'abandoned';
export type Verdict = 'success' | 'failed' | 'redo';

export const STATUS_LABEL: Record<ProjectStatus, string> = { idea: 'Idée', design: 'Conception', printing: 'Impression', done: 'Terminé', in_use: 'En usage', abandoned: 'Abandonné' };
export const VERDICT_LABEL: Record<Verdict, string> = { success: 'Réussie', failed: 'Ratée', redo: 'À refaire' };
export const REASON_LABEL: Record<string, string> = {
  stringing: 'Fils et toiles', warping: 'Gauchissement', adhesion: 'Adhérence', spaghetti: 'Spaghetti', layer_shift: 'Décalage de couches',
  under_extrusion: 'Sous-extrusion', nozzle_clog: 'Buse bouchée', other: 'Autre',
};

export interface Progress {
  parts_done: number; parts_total: number; units_printed: number; units_total: number; units_judged_success: number; units_judged_failed: number;
  units_judged_redo: number; units_unjudged: number; complete: boolean; text: string;
}
export interface Project {
  id: string; name: string; description?: string; status: ProjectStatus; collection_id?: string; parent_id?: string; due_date?: string; archived?: boolean; rev: number;
}
export interface PartLine {
  model_id: string; model_name?: string; quantity: number; role?: string; implicit?: boolean; trashed?: boolean; printed: number; judged_success: number;
  judged_failed: number; judged_redo: number; unjudged: number; attempts: number; in_progress: number; satisfied: boolean; text: string;
}
export interface SubProject { id: string; name: string; status: ProjectStatus; archived?: boolean; own: Progress; progress: Progress; parts_count: number; parent_id: string; depth: number }
export interface Totals { available: boolean; reason?: string; actual_cost: number; currency?: string; actual_seconds: number; actual_grams: number; planned_cost?: number; in_progress: number; attempts: number }
export interface ProjectDetail {
  project: Project; level: number; path?: Array<{ id: string; name: string }>; parts: PartLine[]; own: Progress; progress: Progress; sub_projects: SubProject[];
  totals: Totals; variants: Array<{ id: string; name: string; goal_kind: string }>; suggested_status?: ProjectStatus; suggested_reason?: string; next_action: string; note: string;
}
export interface ListItem { project: Project; depth: number; progress: Progress; parts_count: number; suggested_status?: ProjectStatus }
export interface ProjectList { projects: ListItem[]; total: number; counts_by_status: Record<string, number>; note: string }
export interface PendingVerdict { print_job_id: string; name?: string; model_name?: string; state: string; material_family?: string; finished_at?: string }
export interface PendingList { pending: PendingVerdict[]; total: number; fail_reasons: Array<{ id: string; label: string }>; note: string }
export interface VerdictReply { print_job_id: string; verdict: Verdict; fail_reason?: string; changed: boolean; note: string; warnings?: string[] }

async function req<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  let res: Response;
  try {
    res = await fetch(BASE + path, {
      method, signal, credentials: 'same-origin',
      headers: { ...CSRF_HEADER, ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
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

export const projectsApi = {
  list: (p: { status?: string; includeArchived?: boolean } = {}, signal?: AbortSignal) => {
    const u = new URLSearchParams();
    if (p.status) u.set('status', p.status);
    if (p.includeArchived) u.set('include_archived', 'true');
    const s = u.toString();
    return req<ProjectList>('GET', '/api/projects' + (s ? `?${s}` : ''), undefined, signal);
  },
  get: (id: string, signal?: AbortSignal) => req<ProjectDetail>('GET', `/api/projects/${encodeURIComponent(id)}`, undefined, signal),
  pending: (signal?: AbortSignal) => req<PendingList>('GET', '/api/verdicts/pending', undefined, signal),
  setVerdict: (printJobId: string, verdict: Verdict, reason?: string, note?: string) =>
    req<VerdictReply>('POST', `/api/prints/${encodeURIComponent(printJobId)}/verdict`, { verdict, ...(reason ? { reason } : {}), ...(note ? { note } : {}) }),
};
