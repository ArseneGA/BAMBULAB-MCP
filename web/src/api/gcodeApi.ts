import { ApiError, BASE, CSRF_HEADER } from './client';
import { isGcodeIndex, isGcodeSlice } from '../viewer/gcode/gcodeData';
import type { GcodeSource } from '../viewer/gcode/gcodeData';

/**
 * Source de données de la visionneuse couche par couche pour l'interface web : routes /api/gcode/layers (résumé par
 * couche) et /api/gcode/slice (tranches de ≤ 12 000 points). Mêmes données que les outils réservés de la vue MCP App.
 */
export interface GcodeRef { printJobId?: string; artifactId?: string; plate?: number }

async function getJson(path: string, signal?: AbortSignal): Promise<unknown> {
  let res: Response;
  try { res = await fetch(BASE + path, { signal, credentials: 'same-origin', headers: { ...CSRF_HEADER } }); } catch (e) {
    if ((e as Error).name === 'AbortError') throw e;
    throw new ApiError('Printalk est injoignable', 0, 'offline', true);
  }
  const text = await res.text();
  let data: unknown;
  try { data = text ? JSON.parse(text) : undefined; } catch { /* corps non JSON */ }
  if (!res.ok) throw new ApiError((data as { error?: string } | undefined)?.error ?? `Erreur HTTP ${res.status}`, res.status, (data as { code?: string } | undefined)?.code ?? 'http');
  return data;
}

export function httpGcodeSource(ref: GcodeRef): GcodeSource {
  const q = (extra: Record<string, string | number>) => {
    const u = new URLSearchParams();
    if (ref.printJobId) u.set('print_job_id', ref.printJobId);
    else if (ref.artifactId) u.set('artifact_id', ref.artifactId);
    if (ref.plate) u.set('plate', String(ref.plate));
    for (const [k, v] of Object.entries(extra)) u.set(k, String(v));
    return u.toString();
  };
  return {
    async index(signal) {
      const d = await getJson(`/api/gcode/layers?${q({})}`, signal);
      if (!isGcodeIndex(d)) throw new Error('Réponse /api/gcode/layers invalide');
      return d;
    },
    async slice(from, withTravel, signal) {
      const d = await getJson(`/api/gcode/slice?${q({ from_layer: from, ...(withTravel ? { with_travel: 1 } : {}) })}`, signal);
      if (!isGcodeSlice(d)) throw new Error('Réponse /api/gcode/slice invalide');
      return d;
    },
  };
}
