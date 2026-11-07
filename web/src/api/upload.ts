import { ApiError, BASE, CSRF_HEADER } from './client';

/**
 * Envoi de fichiers 3D vers bambud, par morceaux binaires (PUT /api/uploads/{id}?offset=…), avec reprise
 * à l'offset donné par le serveur. Cookie de navigateur enregistré + en-tête CSRF, comme le reste de l'API.
 * Le serveur recalcule l'empreinte, contrôle la signature du format et range le fichier dans son magasin ;
 * le client ne fournit jamais de chemin, seulement le nom du fichier.
 */

export const CHUNK_BYTES = 1 << 20; // 1 Mio : progression régulière, bien sous la limite serveur (8 Mio)
export const ACCEPT = '.stl,.obj,.3mf,.step,.stp';
const EXT = /\.(stl|obj|3mf|step|stp)$/i;

export interface UploadMeta { collection?: string; tags?: string[]; license?: string; source?: string; unit?: string; on_duplicate?: 'skip' | 'keep' }

export interface UploadedFile {
  status: string; format?: string; size_bytes?: number; sha256?: string; model_id?: string; source_file_id?: string;
  duplicate_of_model_id?: string; note?: string;
}
export interface UploadResult { file: UploadedFile; note?: string }

export interface UploadLimits {
  import_roots: string[]; max_upload_bytes: number; max_upload_mb_by_format?: Record<string, number>; max_chunk_chars: number;
  formats: string[]; upload_ttl_seconds: number;
  url_import: { enabled: boolean; allowed_domains: string[]; max_bytes: number; timeout_seconds: number; note?: string };
}

export const isSupported = (name: string) => EXT.test(name);

async function call<T>(method: string, path: string, body?: BodyInit, json?: unknown, signal?: AbortSignal): Promise<T> {
  let res: Response;
  try {
    res = await fetch(BASE + path, {
      method, signal, credentials: 'same-origin',
      headers: { ...CSRF_HEADER, ...(json !== undefined ? { 'Content-Type': 'application/json' } : body ? { 'Content-Type': 'application/octet-stream' } : {}) },
      body: json !== undefined ? JSON.stringify(json) : body,
    });
  } catch (e) {
    if ((e as Error).name === 'AbortError') throw e;
    throw new ApiError('bambud est injoignable', 0, 'offline', true);
  }
  const text = await res.text();
  let data: Record<string, unknown> | undefined;
  if (text) { try { data = JSON.parse(text) as Record<string, unknown>; } catch { /* non JSON */ } }
  if (!res.ok) throw new ApiError((data?.error as string | undefined) ?? `Erreur HTTP ${res.status}`, res.status, (data?.code as string | undefined) ?? 'http', false, data);
  return data as T;
}

export const uploadLimits = (signal?: AbortSignal) => call<{ upload?: UploadLimits }>('GET', '/api/capabilities', undefined, undefined, signal).then((c) => c.upload);

interface Status { upload_id: string; next_offset: number; complete: boolean }

/** Envoie un fichier ; `onProgress(octetsEnvoyés, total)` à chaque morceau accepté. */
export async function uploadFile(file: File, meta: UploadMeta, onProgress: (sent: number, total: number) => void, signal?: AbortSignal): Promise<UploadResult> {
  if (!isSupported(file.name)) throw new ApiError('Format non pris en charge (STL, OBJ, 3MF, STEP).', 400, 'invalid');
  if (file.size === 0) throw new ApiError('Fichier vide.', 400, 'invalid');
  const st = await call<Status>('POST', '/api/uploads', undefined, { filename: file.name, total_size: file.size, ...meta }, signal);
  const id = encodeURIComponent(st.upload_id);
  let offset = 0;
  let failures = 0;
  try {
    while (offset < file.size) {
      const end = Math.min(offset + CHUNK_BYTES, file.size);
      try {
        const s = await call<Status>('PUT', `/api/uploads/${id}?offset=${offset}`, file.slice(offset, end), undefined, signal);
        offset = s.next_offset;
        failures = 0;
        onProgress(offset, file.size);
      } catch (e) {
        if ((e as Error).name === 'AbortError') throw e;
        if (e instanceof ApiError && e.code === 'offset_mismatch') { // reprise : le serveur indique où repartir
          const next = (e.body?.status as { next_offset?: number } | undefined)?.next_offset;
          if (typeof next === 'number' && next !== offset && ++failures <= 3) { offset = next; continue; }
        } else if (e instanceof ApiError && e.offline && ++failures <= 3) {
          await new Promise((r) => setTimeout(r, 500 * failures));
          try { offset = (await call<Status>('GET', `/api/uploads/${id}`, undefined, undefined, signal)).next_offset; } catch { /* nouvelle tentative */ }
          continue;
        }
        throw e;
      }
    }
    return await call<UploadResult>('POST', `/api/uploads/${id}/finish`, undefined, undefined, signal);
  } catch (e) {
    // Session abandonnée côté serveur (meilleur effort) : pas de morceaux orphelins en attendant l'expiration.
    void call('DELETE', `/api/uploads/${id}`).catch(() => undefined);
    throw e;
  }
}

export interface UrlImportResult { url: string; source: string; license?: string; author?: string; files: UploadedFile[]; note?: string }

/** Import par lien (domaines autorisés par le serveur seulement). */
export const importFromUrl = (url: string, meta: UploadMeta, signal?: AbortSignal) =>
  call<UrlImportResult>('POST', '/api/import-url', undefined, { url, ...meta }, signal);
