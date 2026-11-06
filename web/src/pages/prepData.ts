import { demoModels } from '../data/demo';
import type { DemoModel } from '../data/demo';

/** Réglages de démonstration. Aucune valeur ne provient d'un vrai profil Bambu Studio. */
export type SupportMode = 'none' | 'normal' | 'tree';
export type SeamMode = 'aligned' | 'nearest' | 'back';
export interface Settings { layer: number; walls: number; infill: number; supports: SupportMode; seam: SeamMode; mods: string[] }
export type Overrides = Partial<Settings>;

export const PROFILE = {
  name: '0,20 mm Standard @ P1S 0,4 (démo)',
  chain: ['Profil système Bambu Studio (démo)', '0,20 mm Standard', 'Copie de travail'],
  values: { layer: 0.2, walls: 3, infill: 15, supports: 'none', seam: 'aligned', mods: [] } as Settings,
};
export const MODS = ['Parois renforcées (+1)', 'Remplissage dense local', 'Couture masquée'];
export const labels: Record<keyof Settings, string> = { layer: 'Hauteur de couche', walls: 'Parois', infill: 'Remplissage', supports: 'Supports', seam: 'Coutures', mods: 'Modificateurs' };
export const supportLabel: Record<SupportMode, string> = { none: 'Aucun', normal: 'Normaux', tree: 'Arborescents' };
export const seamLabel: Record<SeamMode, string> = { aligned: 'Alignée', nearest: 'La plus proche', back: 'À l\'arrière' };

const fr = (n: number, d = 0) => n.toFixed(d).replace('.', ',');
export function fmtValue(k: keyof Settings, v: Settings[keyof Settings]): string {
  switch (k) {
    case 'layer': return `${fr(v as number, 2)} mm`;
    case 'walls': return `${v}`;
    case 'infill': return `${v} %`;
    case 'supports': return supportLabel[v as SupportMode];
    case 'seam': return seamLabel[v as SeamMode];
    default: return (v as string[]).length ? (v as string[]).join(', ') : 'Aucun';
  }
}
export const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

export const getModel = (id: string | null): DemoModel => demoModels.find((m) => m.id === id) ?? demoModels[0];
export const workingRev = (m: DemoModel) => `${m.versions[0].label}.1`;

/* Passage Préparation -> Comparaison, mémoire de session uniquement. */
export interface PrepState { objectOverrides: Overrides; goal: string }
export const loadPrep = (id: string): PrepState | null => {
  try { const r = sessionStorage.getItem(`prep:${id}`); return r ? (JSON.parse(r) as PrepState) : null; } catch { return null; }
};
export const savePrep = (id: string, s: PrepState) => { try { sessionStorage.setItem(`prep:${id}`, JSON.stringify(s)); } catch { /* stockage indisponible */ } };

/** Estimations de démonstration, calculées par une formule simple (PAS un slicer). */
export function estimate(m: DemoModel, s: Settings) {
  const { x, y, z } = m.dims;
  const env = x * y * z * 0.6; // mm3, enveloppe approchée
  const shell = Math.min(1, (s.walls * 0.42 * 2) / Math.min(x, y));
  const solid = shell + (s.infill / 100) * (1 - shell);
  const vol = (env * solid) / 1000; // cm3
  const supVol = s.supports === 'none' ? 0 : (env / 1000) * (s.supports === 'normal' ? 0.1 : 0.06);
  const g = (vol + supVol) * 1.24;
  const layers = Math.ceil(z / s.layer);
  const flow = 8 * Math.sqrt(s.layer / 0.2); // mm3/s
  const min = ((vol + supVol) * 1000) / flow / 60 + layers * 0.12;
  return { minutes: Math.round(min), grams: Math.round(g * 10) / 10, metres: Math.round((g / 2.98) * 10) / 10, layers, supportCm3: Math.round(supVol * 10) / 10 };
}
export const fmtTime = (min: number) => `${Math.floor(min / 60)} h ${String(min % 60).padStart(2, '0')} min`;
export const fmtNum = (n: number, d = 1) => fr(n, d);

export interface Variant { id: 'A' | 'B' | 'C'; name: string; intent: string; delta: Overrides }
export const VARIANTS: Variant[] = [
  { id: 'A', name: 'Variante A', intent: 'Proche du profil, pour référence', delta: { layer: 0.2, walls: 3, infill: 15, supports: 'none', seam: 'aligned' } },
  { id: 'B', name: 'Variante B', intent: 'Couches fines, parois épaisses', delta: { layer: 0.12, walls: 4, infill: 20, supports: 'tree', seam: 'back' } },
  { id: 'C', name: 'Variante C', intent: 'Couches épaisses, matière réduite', delta: { layer: 0.28, walls: 2, infill: 10, supports: 'normal', seam: 'nearest' } },
];
