/** Formats communs des cartes (français, Europe/Paris). Aucune valeur inventée : l'absence donne undefined. */

export type Rec = Record<string, unknown>;
export const isRec = (v: unknown): v is Rec => typeof v === 'object' && v !== null && !Array.isArray(v);
export const num = (v: unknown): number | undefined => (typeof v === 'number' && Number.isFinite(v) ? v : undefined);
export const str = (v: unknown): string | undefined => (typeof v === 'string' && v !== '' ? v : undefined);

export function fmtSeconds(s: number | undefined): string | undefined {
  if (s === undefined || s < 0) return undefined;
  return fmtMinutes(Math.round(s / 60));
}
export function fmtMinutes(m: number | undefined): string | undefined {
  if (m === undefined || m < 0) return undefined;
  if (m < 1) return '< 1 min';
  if (m < 60) return `${Math.round(m)} min`;
  const h = Math.floor(m / 60); const r = Math.round(m % 60);
  return r === 0 ? `${h} h` : `${h} h ${String(r).padStart(2, '0')}`;
}
export function fmtHours(h: number | undefined): string | undefined {
  if (h === undefined) return undefined;
  return `${h >= 100 ? Math.round(h) : +h.toFixed(1)} h`.replace('.', ',');
}
export function fmtGrams(g: number | undefined): string | undefined {
  if (g === undefined) return undefined;
  if (Math.abs(g) >= 1000) return `${(g / 1000).toFixed(2).replace('.', ',')} kg`;
  return `${+g.toFixed(1)} g`.replace('.', ',');
}
const SYMBOL: Record<string, string> = { EUR: '€', USD: '$', GBP: '£', CHF: 'CHF' };
export function fmtMoney(v: number | undefined, currency = 'EUR'): string | undefined {
  if (v === undefined) return undefined;
  const sym = SYMBOL[currency] ?? currency;
  const t = v.toFixed(2).replace('.', ',');
  return sym === '€' || sym === 'CHF' ? `${t} ${sym}` : `${sym}${t}`;
}
export const fmtDims = (d?: { x: number; y: number; z: number }): string | undefined => (d ? `${+d.x.toFixed(1)} × ${+d.y.toFixed(1)} × ${+d.z.toFixed(1)} mm`.replace(/\./g, ',') : undefined);
export const fmtPct = (v: number | undefined): string | undefined => (v === undefined ? undefined : `${Math.round(v)} %`);

/** Date courte « 9 nov., 21:05 » (fuseau Europe/Paris). */
export function fmtDate(iso: string | undefined): string | undefined {
  if (!iso) return undefined;
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return undefined;
  return new Intl.DateTimeFormat('fr-FR', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', timeZone: 'Europe/Paris' }).format(new Date(t));
}
export function fmtMonth(key: string): string {
  const m = /^(\d{4})-(\d{2})/.exec(key);
  if (!m) return key;
  return new Intl.DateTimeFormat('fr-FR', { month: 'short', timeZone: 'UTC' }).format(new Date(Date.UTC(+m[1], +m[2] - 1, 1))).replace('.', '');
}

/** Couleur #rrggbb sûre : accepte « #RRGGBB », « RRGGBB » et « RRGGBBAA » (AMS), sinon undefined. */
export function safeColor(c: unknown): string | undefined {
  if (typeof c !== 'string') return undefined;
  const m = /^#?([0-9a-fA-F]{6})([0-9a-fA-F]{2})?$/.exec(c.trim());
  return m ? `#${m[1].toLowerCase()}` : undefined;
}
/** Teinte stable à partir d'un texte (vignettes de repli). */
export function hueOf(text: string): number {
  let h = 0;
  for (let i = 0; i < text.length; i++) h = (h * 31 + text.charCodeAt(i)) % 360;
  return h;
}
export const toolText = (r: unknown): string => {
  const c = isRec(r) && Array.isArray(r.content) ? r.content : [];
  return c.filter((x): x is { type: string; text: string } => isRec(x) && x.type === 'text' && typeof x.text === 'string').map((x) => x.text).join('\n').trim();
};
