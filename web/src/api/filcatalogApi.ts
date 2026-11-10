import { ApiError, BASE, CSRF_HEADER } from './client';

/** Catalogue officiel des filaments Bambu (internal/httpapi/filcatalog.go) : données embarquées, lecture seule. */

export type CatalogConfidence = 'official_profile' | 'official_site' | 'estimated';

export interface CatalogSource { kind: CatalogConfidence; ref: string; profile_version?: string; retrieved: string; note?: string }

export interface CatalogFamily {
  id: string; name: string; preset_line?: string; type?: string; class: string; vendor?: string; finish: string; finish_fr: string;
  translucent?: boolean; support_material?: boolean; soluble?: boolean;
  nozzle_temp_c?: number; nozzle_temp_first_layer_c?: number; bed_temp_c?: Record<string, number>; fan_min_pct?: number; fan_max_pct?: number;
  density_g_cm3?: number; diameter_mm?: number; max_volumetric_speed_mm3_s?: number; drying_bed_temp_c?: number; drying_note?: string;
  required_nozzle_hrc?: number; hardened_nozzle_required: boolean; incompatible_nozzles?: Record<string, string[]>;
  confidence: CatalogConfidence; source: CatalogSource; color_count: number;
}

export interface CatalogColor {
  id: string; family_id: string; family_name?: string; color_code: string; tray_color_code?: string; name: Record<string, string>;
  hex: string[]; kind: 'single' | 'gradient' | 'multicolor'; finish?: string; confidence: CatalogConfidence;
}

export interface CatalogMatch { color: CatalogColor; delta_e?: number; quality?: 'exact' | 'proche' | 'approximatif' | 'aucune' }
export interface CatalogCompat { ok: boolean; reasons?: string[] }

export interface CatalogSearch {
  colors: CatalogMatch[]; families: CatalogFamily[]; nozzle_compat?: Record<string, CatalogCompat>; total_colors: number;
  catalog: { families: number; colors: number; colors_by_class: Record<string, number>; by_confidence: Record<string, number> };
  sources: CatalogSource[]; note: string;
}

export interface CatalogEntry { family: CatalogFamily; color?: CatalogColor; colors?: CatalogColor[]; nozzle_compat?: CatalogCompat; sources: CatalogSource[]; not_available?: string[]; note: string }

export interface CatalogQuery { family?: string; finish?: string; text?: string; hex?: string; max_delta_e?: number; nozzle?: string; hardened?: boolean; limit?: number }

/** Nom lisible : français, à défaut anglais. */
export const colorName = (c: CatalogColor) => c.name.fr || c.name.en || c.id;

/** Fond CSS d'une pastille : couleur unie ou dégradé. */
export const swatchBackground = (c: CatalogColor) => (c.hex.length > 1 ? `linear-gradient(135deg, ${c.hex.join(', ')})` : c.hex[0] ?? 'transparent');

export const FINISHES: { value: string; label: string }[] = [
  { value: '', label: 'Toutes finitions' }, { value: 'standard', label: 'Standard' }, { value: 'matte', label: 'Mat' }, { value: 'silk', label: 'Soie' },
  { value: 'translucent', label: 'Translucide' }, { value: 'metal', label: 'Métal' }, { value: 'sparkle', label: 'Paillettes' }, { value: 'marble', label: 'Marbre' },
  { value: 'glow', label: 'Phosphorescent' }, { value: 'galaxy', label: 'Galaxie' }, { value: 'wood', label: 'Bois' }, { value: 'carbon', label: 'Fibre de carbone' },
  { value: 'glass', label: 'Fibre de verre' },
];

const qs = (q: Record<string, string | number | boolean | undefined>) => {
  const u = new URLSearchParams();
  for (const [k, v] of Object.entries(q)) if (v !== undefined && v !== '' && v !== false && v !== 0) u.set(k, v === true ? '1' : String(v));
  const s = u.toString();
  return s ? `?${s}` : '';
};

async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  let res: Response;
  try {
    res = await fetch(BASE + path, { method: 'GET', signal, credentials: 'same-origin', headers: { ...CSRF_HEADER } });
  } catch (e) {
    if ((e as Error).name === 'AbortError') throw e;
    throw new ApiError('bambud est injoignable', 0, 'offline', true);
  }
  const text = await res.text();
  let data: unknown;
  if (text) { try { data = JSON.parse(text); } catch { /* corps non JSON */ } }
  if (!res.ok) {
    const d = data as { error?: string; code?: string } | undefined;
    throw new ApiError(d?.error ?? `HTTP ${res.status}`, res.status, d?.code ?? 'http');
  }
  return data as T;
}

export const filcatalogApi = {
  overview: (signal?: AbortSignal) => get<CatalogSearch>('/api/filament-catalog', signal),
  colors: (q: CatalogQuery, signal?: AbortSignal) =>
    get<CatalogSearch>('/api/filament-catalog/colors' + qs({ family: q.family, finish: q.finish, text: q.text, hex: q.hex, max_delta_e: q.max_delta_e, nozzle: q.nozzle, hardened: q.hardened, limit: q.limit }), signal),
  entry: (id: string, signal?: AbortSignal) => get<CatalogEntry>('/api/filament-catalog/entry' + qs({ id }), signal),
};
