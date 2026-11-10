import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import type { AccessoryCaps, PrinterStatus } from '../api/printTypes';
import { AccessoriesPanel } from './AccessoriesPanel';
import { availableFans, availableLights, fanAvailable, fanText, lightText, speedAvailable, speedText } from './accessoriesView';

const base: PrinterStatus = {
  printer_id: 'prn_1', reachable: true, state: 'idle', progress_percent: 0, layer: 0, total_layers: 0, remaining_minutes: 0,
  temps: { nozzle: 25, nozzle_target: 0, bed: 25, bed_target: 0, chamber: 25 }, print_error: 0, hms_count: 0,
};
const p1s: AccessoryCaps = { chamber_light: true, work_light: false, part_fan: true, aux_fan: true, chamber_fan: true, speed_levels: true };
const p1p: AccessoryCaps = { chamber_light: false, work_light: false, part_fan: true, aux_fan: true, chamber_fan: false, speed_levels: true };
const html = (s?: PrinterStatus) => renderToStaticMarkup(<AccessoriesPanel printerId="prn_1" status={s} />);

describe('accessoriesView', () => {
  it('dit les états en toutes lettres', () => {
    expect(lightText('on')).toBe('allumée');
    expect(lightText('off')).toBe('éteinte');
    expect(lightText(undefined)).toBe('état inconnu');
    expect(fanText(0)).toBe('arrêté (0 %)');
    expect(fanText(60)).toBe('60 %');
    expect(fanText(undefined)).toBe('état inconnu');
    expect(speedText({ speed_level: 3 })).toBe('Sport (niveau 3)');
    expect(speedText(undefined)).toBe('état inconnu');
  });
  it('ne propose que les accessoires du modèle', () => {
    expect(availableLights(p1p)).toEqual([]);
    expect(availableLights(p1s)).toEqual(['chamber']);
    expect(availableFans(p1p)).toEqual(['part', 'aux']);
    expect(availableFans(p1s)).toEqual(['part', 'aux', 'chamber']);
  });
  it('la vitesse exige une impression ; les ventilateurs refusent la préparation', () => {
    expect(speedAvailable({ ...base, state: 'idle' })).toBe(false);
    expect(speedAvailable({ ...base, state: 'printing' })).toBe(true);
    expect(speedAvailable({ ...base, state: 'paused' })).toBe(true);
    expect(speedAvailable({ ...base, reachable: false, state: 'printing' })).toBe(false);
    expect(fanAvailable({ ...base, state: 'preparing' })).toBe(false);
    expect(fanAvailable({ ...base, state: 'idle' })).toBe(true);
  });
});

describe('AccessoriesPanel', () => {
  it('affiche les états en texte, avec aria-pressed sur la valeur courante', () => {
    const out = html({ ...base, state: 'printing', available_accessories: p1s, accessories: { chamber_light: 'on', part_fan_percent: 50, speed_level: 3 } });
    expect(out).toContain('Accessoires');
    expect(out).toContain('Lumière de la chambre : <strong>allumée</strong>');
    expect(out).toContain('Ventilateur de la pièce : <strong>50 %</strong>');
    expect(out).toContain('Sport (niveau 3)');
    expect(out).toContain('aria-pressed="true"');
    expect(out).toContain('Silencieux');
    expect(out).toContain('Ludicrous');
  });
  it('ne contient aucun bouton Lancer, Arrêter, Pause ni Reprendre', () => {
    const out = html({ ...base, state: 'printing', available_accessories: p1s, accessories: { speed_level: 2 } });
    expect(out).not.toMatch(/Lancer|Démarrer|Arrêter|Annuler|Reprendre|Pause/);
  });
  it('un modèle sans lumière de chambre n\'en propose pas', () => {
    const out = html({ ...base, available_accessories: p1p, accessories: { speed_level: 2 } });
    expect(out).not.toContain('Lumière');
    expect(out).not.toContain('Ventilateur de la chambre');
    expect(out).toContain('Ventilateur auxiliaire');
  });
  it('au repos, la vitesse est désactivée et expliquée', () => {
    const out = html({ ...base, state: 'idle', available_accessories: p1s, accessories: { speed_level: 2 } });
    expect(out).toContain('La vitesse ne se règle que pendant une impression.');
    expect(out).toMatch(/<button[^>]*disabled[^>]*>Sport<\/button>/);
  });
  it('n\'affiche rien sans imprimante joignable ni accessoires connus', () => {
    expect(html(undefined)).toBe('');
    expect(html({ ...base, reachable: false, available_accessories: p1s })).toBe('');
    expect(html({ ...base })).toBe('');
  });
});
