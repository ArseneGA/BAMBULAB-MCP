import { ApiError, BASE, CSRF_HEADER } from './client';

/*
 * Lectures et réglages utilisés par l'Accueil, la fiche impression et Réglages › Suivi (G10 U2) :
 * photos de paliers et alertes d'une impression (lecture), réglages de la caméra (journal photo D8).
 * Aucune de ces routes ne lance, n'arrête ni ne reprend d'impression.
 */

export interface JobPhoto { id: string; kind: string; sha256: string; taken_at: string; percent?: number; url: string }
export interface CameraAlert { id: string; print_job_id: string; at: string; score: number; level: string; photo_url?: string; report?: { summary?: string; reasons?: string[] } }
export interface CameraSettings {
  enabled: boolean; interval_seconds: number; progress_photos: boolean; progress_photo_width: number; progress_photo_quality: number;
  progress_retention_days: number; progress_cap_mb: number; alert_photo_retention_days: number;
}
export type CameraSettingsPatch = Partial<Pick<CameraSettings, 'progress_photos' | 'progress_retention_days' | 'progress_cap_mb' | 'alert_photo_retention_days'>>;

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

export const dashboardApi = {
  /** Photos rangées d'une impression (paliers à 10 %, fin, alertes) : lecture seule. */
  jobPhotos: (jobId: string, signal?: AbortSignal) => req<{ photos: JobPhoto[] }>('GET', `/api/print-jobs/${encodeURIComponent(jobId)}/photos`, undefined, signal),
  /** Alertes de la caméra pour une impression (détection déterministe : référence plateau vide + comparaison d'images). */
  jobAlerts: (jobId: string, signal?: AbortSignal) => req<{ alerts: CameraAlert[] }>('GET', `/api/print-jobs/${encodeURIComponent(jobId)}/analysis`, undefined, signal),
  cameraSettings: (signal?: AbortSignal) => req<CameraSettings>('GET', '/api/camera/settings', undefined, signal),
  saveCameraSettings: (p: CameraSettingsPatch) => req<CameraSettings>('PUT', '/api/camera/settings', p),
};

/** Dernière photo de palier (plus grand pourcentage) d'une liste de photos ; undefined si aucune. */
export function lastMilestone(photos: JobPhoto[]): JobPhoto | undefined {
  return photos.filter((p) => (p.percent ?? 0) > 0 && p.url).sort((a, b) => (a.percent ?? 0) - (b.percent ?? 0)).at(-1);
}
