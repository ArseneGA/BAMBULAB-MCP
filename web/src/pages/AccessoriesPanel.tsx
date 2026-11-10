import { useState } from 'react';
import { Button } from '../components';
import { api } from '../api/client';
import type { PrinterStatus } from '../api/printTypes';
import {
  FAN_PRESETS, SPEEDS, availableFans, availableLights, fanAvailable, fanLabel, fanPercent, fanText, hasAnyAccessory, lightLabel, lightMode, lightText,
  speedAvailable, speedText,
  type FanKey, type LightKey, type SpeedKey,
} from './accessoriesView';
import './accessories.css';

/**
 * Panneau « Accessoires » de la page Imprimantes (N4) : lumières, ventilateurs, vitesse de l'impression EN COURS.
 * Aucun bouton Lancer, Arrêter, Pause ou Reprendre : ce panneau ne peut rien démarrer ni interrompre. Les états sont écrits
 * en toutes lettres (« allumée », « 50 % », « Sport (niveau 3) ») ; le bouton de la valeur courante porte aria-pressed.
 */
export function AccessoriesPanel({ printerId, status, onChanged }: { printerId: string; status?: PrinterStatus; onChanged?: () => void }) {
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const caps = status?.available_accessories;
  if (!status || !status.reachable || !hasAnyAccessory(caps)) return null;
  const acc = status.accessories;
  const lights = availableLights(caps);
  const fans = availableFans(caps);

  async function run(label: string, f: () => Promise<{ applied: boolean; message: string }>) {
    setBusy(true); setMsg(null);
    try {
      const r = await f();
      setMsg({ ok: r.applied, text: r.applied ? `${label} : fait.` : r.message });
      onChanged?.();
    } catch (e) {
      setMsg({ ok: false, text: `${label} : refusé. ${(e as Error).message}` });
    } finally { setBusy(false); }
  }
  const setLight = (k: LightKey, on: boolean) => run(`${lightLabel(k)} ${on ? 'allumée' : 'éteinte'}`, () => api.setPrinterLight(printerId, k, on));
  const setFan = (k: FanKey, p: number) => run(`${fanLabel(k)} à ${p} %`, () => api.setPrinterFan(printerId, k, p));
  const setSpeed = (k: SpeedKey, label: string) => run(`Vitesse ${label}`, () => api.setPrintSpeedLevel(printerId, k));
  const canSpeed = speedAvailable(status);
  const canFan = fanAvailable(status);

  return (
    <section className="acc" aria-label="Accessoires de l'imprimante">
      <h4>Accessoires</h4>
      <p className="hint">Éclairage, ventilateurs et vitesse de l'impression en cours. Rien ici ne lance, n'arrête ni ne reprend une impression.</p>
      {lights.map((k) => {
        const mode = (lightMode(acc, k) ?? '').toLowerCase();
        return (
          <div className="acc-row" key={k} role="group" aria-label={lightLabel(k)}>
            <span className="acc-name">{lightLabel(k)} : <strong>{lightText(mode)}</strong></span>
            <span className="acc-btns">
              <Button size="sm" disabled={busy} aria-pressed={mode === 'on'} onClick={() => setLight(k, true)}>Allumer</Button>
              <Button size="sm" disabled={busy} aria-pressed={mode === 'off'} onClick={() => setLight(k, false)}>Éteindre</Button>
            </span>
          </div>
        );
      })}
      {fans.map((k) => {
        const cur = fanPercent(acc, k);
        return (
          <div className="acc-row" key={k} role="group" aria-label={fanLabel(k)}>
            <span className="acc-name">{fanLabel(k)} : <strong>{fanText(cur)}</strong></span>
            <span className="acc-btns">
              {FAN_PRESETS.map((p) => (
                <Button size="sm" key={p} disabled={busy || !canFan} aria-pressed={cur === p} onClick={() => setFan(k, p)}>{p === 0 ? 'Arrêt' : `${p} %`}</Button>
              ))}
            </span>
          </div>
        );
      })}
      {fans.length > 0 && !canFan && <p className="hint">Réglage des ventilateurs indisponible pendant la préparation (mise à niveau, calibration).</p>}
      {caps?.speed_levels && (
        <div className="acc-row" role="group" aria-label="Vitesse d'impression">
          <span className="acc-name">Vitesse d'impression : <strong>{speedText(acc)}</strong></span>
          <span className="acc-btns">
            {SPEEDS.map((s) => (
              <Button size="sm" key={s.key} disabled={busy || !canSpeed} aria-pressed={acc?.speed_level === s.level} onClick={() => setSpeed(s.key, s.label)}>{s.label}</Button>
            ))}
          </span>
        </div>
      )}
      {caps?.speed_levels && !canSpeed && <p className="hint">La vitesse ne se règle que pendant une impression.</p>}
      <p className="hint" role="status" aria-live="polite">{msg ? `${msg.ok ? 'Fait' : 'Échec'} · ${msg.text}` : ''}</p>
    </section>
  );
}
