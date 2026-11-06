import { useEffect, useState } from 'react';
import { Check } from 'lucide-react';
import { Button, Field, Input, Panel, Select, Tag } from '../components';
import { api, describeError } from '../api/client';
import type { AsyncState } from '../api/hooks';
import type { CompareCandidate, CompareOutput, Experiment } from '../api/goalTypes';
import type { Measurement, MemoryResponse, PrintResult } from '../api/extraTypes';
import { Async, Dialog, Notice, fmtDate } from './common';

export const OUTCOME: Record<string, string> = { success: 'réussi', partial: 'partiel', failed: 'raté' };

/** Mémoire des essais comparables (GET /api/print-memory) et essais enregistrés pendant cette session. */
export function MemoryPanel({ state, saved }: { state: AsyncState<MemoryResponse>; saved: PrintResult[] }) {
  return (
    <Panel title="Mémoire des essais comparables">
      {saved.length > 0 && (
        <Notice tone="info" icon={<Check size={16} aria-hidden />} title={saved.length === 1 ? 'Essai enregistré' : `${saved.length} essais enregistrés`}>
          {saved[0].id} : {OUTCOME[saved[0].outcome] ?? saved[0].outcome}. {saved[0].deviations && saved[0].deviations.length > 0 ? `Écarts de réglage consignés : ${saved[0].deviations.map((d) => d.key).join(', ')}. ` : 'Aucun écart de réglage. '}{saved[0].reuse_conditions}
        </Notice>
      )}
      <Async state={state} label="Recherche d'essais comparables">
        {(m) => m.hits.length === 0
          ? <p className="hint" style={{ margin: 0 }}>Aucun essai réel enregistré pour la même famille de matériau ({m.query.material_family || 'inconnue'}), la même machine et une géométrie similaire. La mémoire se remplit avec « Enregistrer l'essai réel ».</p>
          : (
            <>
              <ul className="cmp-mem" aria-label="Essais antérieurs">{m.hits.map((h) => (
                <li key={h.result_id}><Tag tone={h.outcome === 'success' ? 'ok' : h.outcome === 'failed' ? 'err' : 'warn'}>{OUTCOME[h.outcome] ?? h.outcome}</Tag> <span>{h.comparability}</span>{h.observations && <span className="hint"> — {h.observations}</span>}<span className="mono dim"> {fmtDate(h.created_at)} · {h.filament}</span></li>
              ))}</ul>
              <p className="hint" style={{ margin: 'var(--sp-2) 0 0' }}>{m.note}</p>
            </>
          )}
      </Async>
    </Panel>
  );
}

interface Row { name: string; value: string; unit: string }
const emptyRow = (): Row => ({ name: '', value: '', unit: '' });

/** Saisie du retour d'expérience : POST /api/print-results (record_print_result). Pas de photos ici (références de blobs). */
export function RecordDialog({ c, compare, exp, onClose, onDone }: { c: CompareCandidate | null; compare: CompareOutput; exp?: Experiment; onClose: () => void; onDone: (r: PrintResult) => void }) {
  const [outcome, setOutcome] = useState<'success' | 'partial' | 'failed'>('success');
  const [obs, setObs] = useState('');
  const [rows, setRows] = useState<Row[]>([emptyRow()]);
  const [settings, setSettings] = useState('');
  const [filament, setFilament] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string>();
  useEffect(() => { setErr(undefined); setOutcome('success'); setObs(''); setRows([emptyRow()]); setSettings(''); setFilament(''); }, [c]);
  const setRow = (i: number, p: Partial<Row>) => setRows((l) => l.map((r, k) => (k === i ? { ...r, ...p } : r)));
  const go = async () => {
    if (!c) return;
    setBusy(true); setErr(undefined);
    try {
      const measurements: Measurement[] = [];
      for (const r of rows) {
        if (!r.name.trim() && !r.value.trim()) continue;
        const v = Number(r.value.replace(',', '.'));
        if (!r.name.trim() || !Number.isFinite(v)) throw new Error('Chaque mesure demande un nom et une valeur numérique.');
        measurements.push({ name: r.name.trim(), value: v, unit: r.unit.trim() || undefined });
      }
      const actual: Record<string, unknown> = {};
      for (const line of settings.split('\n').map((l) => l.trim()).filter(Boolean)) {
        const i = line.indexOf('=');
        if (i < 1) throw new Error(`Réglage « ${line} » : écrire clé=valeur (ex. process.layer_height=0.16).`);
        actual[line.slice(0, i).trim()] = line.slice(i + 1).trim();
      }
      onDone(await api.recordResult({
        experiment_id: compare.experiment_id, candidate_id: c.id, outcome, observations: obs.trim() || undefined,
        measurements: measurements.length ? measurements : undefined, actual_settings: Object.keys(actual).length ? actual : undefined, filament: filament.trim() || undefined,
      }));
    } catch (e) { setErr(describeError(e)); } finally { setBusy(false); }
  };
  return (
    <Dialog open={c !== null} onClose={onClose} title="Enregistrer l'essai réel" footer={<><Button onClick={onClose}>Annuler</Button><Button variant="primary" disabled={busy || (!obs.trim() && rows.every((r) => !r.name.trim()))} onClick={go}>{busy ? 'Enregistrement…' : 'Enregistrer'}</Button></>}>
      {c && (
        <>
          <p style={{ margin: 0 }}><strong>{c.label}</strong>{exp ? <span className="hint"> · {exp.details.base.machine}</span> : null}</p>
          <Field label="Résultat"><Select value={outcome} onChange={(e) => setOutcome(e.target.value as typeof outcome)} options={[{ value: 'success', label: 'Réussi' }, { value: 'partial', label: 'Partiel (utilisable avec défauts)' }, { value: 'failed', label: 'Raté' }]} /></Field>
          <Field label="Observations (ce que vous avez réellement constaté)"><textarea className="input prep-text" rows={3} value={obs} onChange={(e) => setObs(e.target.value)} placeholder="Ex. : couches bien liées, léger stringing sur la face avant" /></Field>
          <fieldset className="rec-meas"><legend className="eyebrow">Mesures sur la pièce (facultatif)</legend>
            {rows.map((r, i) => (
              <div key={i} className="rec-row">
                <Input aria-label={`Nom de la mesure ${i + 1}`} placeholder="cote_x" value={r.name} onChange={(e) => setRow(i, { name: e.target.value })} />
                <Input aria-label={`Valeur de la mesure ${i + 1}`} placeholder="10,02" inputMode="decimal" value={r.value} onChange={(e) => setRow(i, { value: e.target.value })} />
                <Input aria-label={`Unité de la mesure ${i + 1}`} placeholder="mm" value={r.unit} onChange={(e) => setRow(i, { unit: e.target.value })} />
              </div>
            ))}
            <div><Button size="sm" onClick={() => setRows((l) => [...l, emptyRow()])}>Ajouter une mesure</Button></div>
          </fieldset>
          <Field label="Réglages réellement utilisés s'ils diffèrent du plan (une ligne par réglage : clé=valeur)"><textarea className="input prep-text" rows={2} value={settings} onChange={(e) => setSettings(e.target.value)} placeholder="process.layer_height=0.16" spellCheck={false} /></Field>
          <Field label="Bobine réellement utilisée si différente du profil (facultatif)"><Input value={filament} onChange={(e) => setFilament(e.target.value)} /></Field>
          <p className="hint" style={{ margin: 0 }}>Le résultat est lié à l'expérience, au candidat et aux réglages relevés à la découpe ; les écarts sont consignés. Les photos ne se joignent pas depuis cette interface (références de blobs, via l'assistant).</p>
          {err && <Notice tone="err" title="Enregistrement refusé">{err}</Notice>}
        </>
      )}
    </Dialog>
  );
}
