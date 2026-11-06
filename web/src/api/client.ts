import type {
  ApplyResult, Capabilities, CollectionInfo, DuplicatesResult, Health, ImportInput, ImportStarted, Job, ModelDetails,
  OpPreview, Operation, RestoreResult, ScanReport, SearchParams, SearchResult,
} from './types';
import type { ApplyOutput, CompareOutput, CreateExperimentInput, Experiment, Goal, GoalCriteriaCatalog, GoalInput } from './goalTypes';

/**
 * Client de l'API HTTP de bambud. Les erreurs sont normalisées :
 *  - `offline`  : bambud est injoignable (réseau, service arrêté) ;
 *  - `status`/`code` : réponse d'erreur du serveur (not_allowed, not_found, invalid, conflict, backend_missing...).
 */
export class ApiError extends Error {
  constructor(message: string, readonly status: number, readonly code: string, readonly offline = false, readonly body?: Record<string, unknown>) {
    super(message);
    this.name = 'ApiError';
  }
}

export const BASE = (import.meta.env?.VITE_API_BASE as string | undefined) ?? '';

async function request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  let res: Response;
  try {
    res = await fetch(BASE + path, {
      method, signal,
      headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch (e) {
    if ((e as Error).name === 'AbortError') throw e;
    throw new ApiError('bambud est injoignable', 0, 'offline', true);
  }
  const text = await res.text();
  let data: unknown = undefined;
  if (text) { try { data = JSON.parse(text); } catch { /* corps non JSON */ } }
  if (!res.ok) {
    const d = data as { error?: string; code?: string } | undefined;
    // Un proxy ou un serveur arrêté répond parfois 502/503/504 sans corps JSON : c'est de l'indisponibilité.
    const gateway = d?.error === undefined && [502, 503, 504].includes(res.status);
    throw new ApiError(d?.error ?? `Erreur HTTP ${res.status}`, res.status, d?.code ?? (gateway ? 'offline' : 'http'), gateway, (data as Record<string, unknown> | undefined) ?? undefined);
  }
  return data as T;
}

const qs = (p: Record<string, string | number | boolean | string[] | undefined>) => {
  const u = new URLSearchParams();
  for (const [k, v] of Object.entries(p)) {
    if (v === undefined || v === '' || v === false) continue;
    if (Array.isArray(v)) v.forEach((x) => u.append(k, x));
    else u.set(k, String(v));
  }
  const s = u.toString();
  return s ? `?${s}` : '';
};

/** Rend l'URL d'aperçu signée relative à l'origine courante (le serveur peut être joint sous un autre nom). */
export function assetUrl(signed: string): string {
  try {
    const u = new URL(signed, window.location.href);
    return BASE + u.pathname + u.search;
  } catch { return signed; }
}

export const api = {
  health: (signal?: AbortSignal) => request<Health>('GET', '/health', undefined, signal),
  capabilities: (signal?: AbortSignal) => request<Capabilities>('GET', '/api/capabilities', undefined, signal),
  models: (p: SearchParams, signal?: AbortSignal) =>
    request<SearchResult>('GET', '/api/models' + qs({ q: p.q, tag: p.tag, collection: p.collection, format: p.format, availability: p.availability, trashed: p.trashed, sort: p.sort, desc: p.desc, limit: p.limit, offset: p.offset }), undefined, signal),
  model: (id: string, signal?: AbortSignal) => request<ModelDetails>('GET', `/api/models/${encodeURIComponent(id)}`, undefined, signal),
  collections: (signal?: AbortSignal) => request<{ collections: CollectionInfo[] }>('GET', '/api/collections', undefined, signal),
  trash: (limit = 100, offset = 0, signal?: AbortSignal) => request<SearchResult>('GET', '/api/trash' + qs({ limit, offset }), undefined, signal),
  restore: (ids: string[]) => request<RestoreResult>('POST', '/api/trash/restore', { model_ids: ids }),
  scan: (path: string, recursive = true) => request<ScanReport>('POST', '/api/scan', { path, recursive }),
  import: (input: ImportInput) => request<ImportStarted>('POST', '/api/import', input),
  job: (id: string, signal?: AbortSignal) => request<Job>('GET', `/api/jobs/${encodeURIComponent(id)}`, undefined, signal),
  cancelJob: (id: string) => request<{ job_id: string; outcome: string; state: string }>('POST', `/api/jobs/${encodeURIComponent(id)}/cancel`),
  previewOps: (operations: Operation[]) => request<OpPreview>('POST', '/api/operations/preview', { operations }),
  applyOps: (token: string) => request<ApplyResult>('POST', '/api/operations/apply', { token }),
  tag: (model_ids: string[], add: string[], remove: string[]) => request<unknown>('POST', '/api/tags', { model_ids, add, remove }),
  duplicates: (limit = 50, signal?: AbortSignal) => request<DuplicatesResult>('GET', '/api/duplicates' + qs({ limit }), undefined, signal),
  goalCriteria: (signal?: AbortSignal) => request<GoalCriteriaCatalog>('GET', '/api/goal-criteria', undefined, signal),
  setGoal: (g: GoalInput) => request<Goal>('POST', '/api/goals', g),
  createExperiment: (x: CreateExperimentInput) => request<Experiment>('POST', '/api/experiments', x),
  experiment: (id: string, signal?: AbortSignal) => request<Experiment>('GET', `/api/experiments/${encodeURIComponent(id)}`, undefined, signal),
  compare: (id: string, signal?: AbortSignal) => request<CompareOutput>('GET', `/api/experiments/${encodeURIComponent(id)}/compare`, undefined, signal),
  applyVariant: (experiment_id: string, candidate_id: string, expected_revision: string) => request<ApplyOutput>('POST', '/api/variants/apply', { experiment_id, candidate_id, expected_revision }),
  /** Télécharge le GLB d'aperçu (URL signée de la fiche). */
  async glb(signedUrl: string, signal?: AbortSignal): Promise<ArrayBuffer> {
    let res: Response;
    try { res = await fetch(assetUrl(signedUrl), { signal }); } catch (e) {
      if ((e as Error).name === 'AbortError') throw e;
      throw new ApiError('bambud est injoignable', 0, 'offline', true);
    }
    if (res.status === 403) throw new ApiError("L'URL d'aperçu a expiré : rechargez la fiche.", 403, 'expired');
    if (!res.ok) throw new ApiError(`Aperçu indisponible (HTTP ${res.status})`, res.status, 'http');
    return res.arrayBuffer();
  },
};

/** Suit un travail jusqu'à son état final. Appelle onUpdate à chaque lecture. */
export async function waitForJob(id: string, onUpdate: (j: Job) => void, signal?: AbortSignal, intervalMs = 700): Promise<Job> {
  for (;;) {
    const j = await api.job(id, signal);
    onUpdate(j);
    if (j.state !== 'queued' && j.state !== 'running') return j;
    await new Promise<void>((resolve, reject) => {
      const t = window.setTimeout(resolve, intervalMs);
      signal?.addEventListener('abort', () => { window.clearTimeout(t); reject(new DOMException('abort', 'AbortError')); }, { once: true });
    });
  }
}

/** Message utilisateur pour une erreur d'API. */
export function describeError(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.offline) return 'bambud est injoignable. Vérifiez que le service tourne puis réessayez.';
    switch (e.code) {
      case 'not_allowed': return `Refusé : ${e.message}`;
      case 'slicer_missing': return 'Bambu Studio est introuvable sur le serveur : la découpe des variantes est indisponible.';
      case 'stale_revision': return `Révision périmée : ${e.message}`;
      case 'backend_missing': return `Backend indisponible : ${e.message}`;
      default: return e.message;
    }
  }
  return e instanceof Error ? e.message : String(e);
}
