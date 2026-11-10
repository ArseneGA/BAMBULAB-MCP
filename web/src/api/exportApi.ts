/**
 * Export de projet : archive zip partageable d'une collection (routes /api/exports, internal/httpapi/export.go).
 * Fichier autonome : n'ajoute rien à `api` de client.ts. Rien n'est publié ; le téléchargement passe par un lien signé
 * à durée limitée (POST /api/exports/{id}/link) lisible sans jeton.
 */
import type { StableProgress } from './types';
import { ApiError, BASE, CSRF_HEADER } from './client';

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
    throw new ApiError('Printalk est injoignable', 0, 'offline', true);
  }
  const text = await res.text();
  let data: unknown;
  if (text) { try { data = JSON.parse(text); } catch { /* corps non JSON */ } }
  if (!res.ok) {
    const d = data as { error?: string; code?: string } | undefined;
    throw new ApiError(d?.error ?? `Erreur HTTP ${res.status}`, res.status, d?.code ?? 'http', false, data as Record<string, unknown> | undefined);
  }
  return data as T;
}

export interface ExportOptions {
  skip_subcollections?: boolean;
  title?: string;
  description?: string;
  versions?: Record<string, string>;
  quantities?: Record<string, number>;
  include_gcode_3mf?: boolean;
  include_photos?: boolean;
  license?: string;
  assembly_notes?: string;
  hardware?: string;
  max_bytes?: number;
}
export interface ExportInput { collection_id?: string; model_ids?: string[]; options?: ExportOptions }
export interface ExportSkip { model_id?: string; name?: string; reason: string }
export interface ExportPlan {
  title: string;
  parts: Array<{ model_id: string; name: string; quantity: number; version_id?: string; files: number; bytes: number }>;
  files: number; total_bytes: number; max_bytes: number; warnings?: string[]; skipped?: ExportSkip[];
}
export interface ExportSummary {
  parts: number; files: number; includes_gcode_3mf: boolean; includes_photos: boolean; license: string; warnings?: string[]; skipped?: ExportSkip[];
}
export interface ExportRecord {
  export_id: string; job_id?: string; state: 'queued' | 'ready' | 'failed' | 'expired'; title: string; file_name?: string;
  size_bytes?: number; sha256?: string; summary?: ExportSummary; error?: string; created_at: string; expires_at: string;
}
export interface ExportStarted { export_id: string; job_id: string; plan: ExportPlan }
export interface ExportStatus {
  export: ExportRecord;
  job?: { state: string; progress?: number; message?: string } & StableProgress;
  publish_steps?: string[];
}
export interface ExportLink { url: string; path: string; expires_at: string; file_name: string; size_bytes: number }

export const exportApi = {
  start: (input: ExportInput) => call<ExportStarted>('POST', '/api/exports', input),
  get: (id: string, signal?: AbortSignal) => call<ExportStatus>('GET', `/api/exports/${encodeURIComponent(id)}`, undefined, signal),
  list: (signal?: AbortSignal) => call<{ exports: ExportRecord[] }>('GET', '/api/exports', undefined, signal),
  link: (id: string) => call<ExportLink>('POST', `/api/exports/${encodeURIComponent(id)}/link`),
  /** Adresse de téléchargement relative au service (le lien est signé : aucun jeton n'est nécessaire). */
  downloadUrl: (l: ExportLink) => BASE + l.path,
};
