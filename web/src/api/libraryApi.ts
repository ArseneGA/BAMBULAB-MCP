import { ApiError, BASE, CSRF_HEADER } from './client';
import type {
  CalibrationLookup, CalibrationKey, CreateInput, CreateOutput, GenerateOutput, Measures, ParamSpec, RecordOutput, SaveOutput, TemplateInfo, TemplateList,
} from './libraryTypes';

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
    throw new ApiError('bambud est injoignable', 0, 'offline', true);
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

export const libraryApi = {
  templates: (signal?: AbortSignal) => req<TemplateList>('GET', '/api/library/templates', undefined, signal),
  create: (i: CreateInput) => req<CreateOutput>('POST', '/api/library/create', i),
  saveVariant: (model_id: string, name: string, description?: string) => req<SaveOutput>('POST', '/api/library/variants', { model_id, name, description }),
  generateCalibration: (k: CalibrationKey & { kinds?: string[] }) => req<GenerateOutput>('POST', '/api/calibration/sets', k),
  recordCalibration: (k: CalibrationKey, measures: Measures, set_id?: string) => req<RecordOutput>('POST', '/api/calibration/records', { ...k, measures, set_id }),
  calibration: (k: CalibrationKey, signal?: AbortSignal) => {
    const u = new URLSearchParams({ nozzle: String(k.nozzle_mm), material: k.material });
    if (k.printer) u.set('printer', k.printer);
    if (k.filament) u.set('filament', k.filament);
    return req<CalibrationLookup>('GET', '/api/calibration?' + u.toString(), undefined, signal);
  },
};

/**
 * Convertit les valeurs saisies (texte) en paramètres typés d'après le schéma du gabarit.
 * Seuls les champs renseignés partent : un champ vide garde le défaut, ou la valeur calculée par l'ingénierie.
 * Retourne les erreurs de saisie par nom de paramètre.
 */
export function paramsFromForm(specs: ParamSpec[], values: Record<string, string>): { params: Record<string, unknown>; errors: Record<string, string> } {
  const params: Record<string, unknown> = {};
  const errors: Record<string, string> = {};
  for (const sp of specs) {
    const raw = values[sp.name];
    if (raw === undefined || raw.trim() === '') continue;
    if (sp.type === 'boolean') { params[sp.name] = raw === 'true'; continue; }
    if (sp.type === 'enum') {
      if (!sp.options?.includes(raw)) errors[sp.name] = `valeur parmi ${sp.options?.join(', ')}`;
      else params[sp.name] = raw;
      continue;
    }
    const n = Number(raw.replace(',', '.'));
    if (!Number.isFinite(n)) { errors[sp.name] = 'nombre attendu'; continue; }
    if (sp.type === 'integer' && !Number.isInteger(n)) { errors[sp.name] = 'entier attendu'; continue; }
    if (sp.min !== undefined && n < sp.min) { errors[sp.name] = `minimum ${sp.min}${sp.unit ? ' ' + sp.unit : ''}`; continue; }
    if (sp.max !== undefined && n > sp.max) { errors[sp.name] = `maximum ${sp.max}${sp.unit ? ' ' + sp.unit : ''}`; continue; }
    params[sp.name] = n;
  }
  return { params, errors };
}

/** Variante : les paramètres figés deviennent les valeurs initiales du formulaire. */
export const initialForm = (t: TemplateInfo): Record<string, string> =>
  Object.fromEntries(Object.entries(t.fixed_params ?? {}).map(([k, v]) => [k, String(v)]));

/** Convertit une saisie décimale (virgule acceptée) ; undefined si vide ou invalide. */
export function num(s: string): number | undefined {
  if (s.trim() === '') return undefined;
  const n = Number(s.replace(',', '.'));
  return Number.isFinite(n) ? n : undefined;
}
