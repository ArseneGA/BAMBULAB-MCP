import type { AccessoryCaps, AccessoryState, PrinterStatus } from '../api/printTypes';

/**
 * Logique d'affichage du panneau « Accessoires » (N4) : tout est dit en TEXTE (jamais par la seule couleur), et rien
 * ici ne lance, n'arrête ni ne reprend une impression.
 */

export type LightKey = 'chamber' | 'work';
export type FanKey = 'part' | 'aux' | 'chamber';
export type SpeedKey = 'silent' | 'normal' | 'sport' | 'ludicrous';

export const SPEEDS: Array<{ key: SpeedKey; level: number; label: string }> = [
  { key: 'silent', level: 1, label: 'Silencieux' },
  { key: 'normal', level: 2, label: 'Normal' },
  { key: 'sport', level: 3, label: 'Sport' },
  { key: 'ludicrous', level: 4, label: 'Ludicrous' },
];

export const FAN_PRESETS = [0, 25, 50, 75, 100];

export const lightLabel = (k: LightKey) => (k === 'chamber' ? 'Lumière de la chambre' : 'Lumière de la tête');
export const fanLabel = (k: FanKey) => (k === 'part' ? 'Ventilateur de la pièce' : k === 'aux' ? 'Ventilateur auxiliaire' : 'Ventilateur de la chambre');

/** « allumée » / « éteinte » / « clignotante » ; « état inconnu » tant que l'imprimante ne l'a pas rapporté. */
export function lightText(mode?: string): string {
  switch ((mode ?? '').toLowerCase()) {
    case 'on': return 'allumée';
    case 'off': return 'éteinte';
    case 'flashing': return 'clignotante';
    default: return 'état inconnu';
  }
}

export function fanText(pct?: number): string {
  return pct === undefined ? 'état inconnu' : pct === 0 ? 'arrêté (0 %)' : `${pct} %`;
}

export function speedText(a?: AccessoryState): string {
  const s = SPEEDS.find((x) => x.level === a?.speed_level);
  return s ? `${s.label} (niveau ${s.level})` : 'état inconnu';
}

/** Lumières proposées pour ce modèle, dans l'ordre d'affichage. */
export function availableLights(c?: AccessoryCaps): LightKey[] {
  const out: LightKey[] = [];
  if (c?.chamber_light) out.push('chamber');
  if (c?.work_light) out.push('work');
  return out;
}

export function availableFans(c?: AccessoryCaps): FanKey[] {
  const out: FanKey[] = [];
  if (c?.part_fan) out.push('part');
  if (c?.aux_fan) out.push('aux');
  if (c?.chamber_fan) out.push('chamber');
  return out;
}

export const lightMode = (a: AccessoryState | undefined, k: LightKey) => (k === 'chamber' ? a?.chamber_light : a?.work_light);
export const fanPercent = (a: AccessoryState | undefined, k: FanKey) => (k === 'part' ? a?.part_fan_percent : k === 'aux' ? a?.aux_fan_percent : a?.chamber_fan_percent);

/** La vitesse ne se règle que pendant une impression (préparation, impression ou pause). */
export function speedAvailable(d?: PrinterStatus): boolean {
  if (!d || !d.reachable) return false;
  const gs = (d.gcode_state ?? '').toUpperCase();
  return ['printing', 'paused', 'preparing'].includes(d.state) || ['RUNNING', 'PAUSE', 'PREPARE'].includes(gs);
}

/** Les ventilateurs sont refusés pendant la préparation (mise à niveau, calibration). */
export function fanAvailable(d?: PrinterStatus): boolean {
  return !!d && d.reachable && d.state !== 'preparing';
}

/** Le panneau n'a de sens que si le modèle a au moins un accessoire connu. */
export function hasAnyAccessory(c?: AccessoryCaps): boolean {
  return !!c && (c.chamber_light || c.work_light || c.part_fan || c.aux_fan || c.chamber_fan || c.speed_levels);
}
