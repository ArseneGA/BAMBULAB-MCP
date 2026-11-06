import { useState } from 'react';
import { Plus, Trash2 } from 'lucide-react';
import { Button, Input, Panel, Select, Tag } from '../components';
import { api, describeError } from '../api/client';
import { useAsync } from '../api/hooks';
import type { GoalCriteriaCatalog } from '../api/goalTypes';
import type { ModifyOutput, Part } from '../api/extraTypes';
import { Async, Notice, fmtDims, fmtNum } from './common';

/** Sous-parties stables du modèle (GET /api/models/{id}/parts) : objets du fichier et corps connexes. */
export function PartsPanel({ modelId, selected, onSelect }: { modelId: string; selected: string | null; onSelect: (p: Part | null) => void }) {
  const st = useAsync((s) => api.parts(modelId, s), [modelId]);
  return (
    <Panel title="Objet ou sous-partie">
      <Async state={st} label="Lecture des sous-parties">
        {(d) => d.parts.length === 0
          ? <p className="hint" style={{ margin: 0 }}>{d.note ?? 'Aucune sous-partie.'}</p>
          : (
            <>
              <div className="partlist" role="group" aria-label="Objets et sous-parties du modèle">
                {d.parts.map((p) => (
                  <button key={p.id} type="button" aria-pressed={selected === p.id} className={p.kind === 'component' ? 'part-sub' : undefined}
                    title={`${fmtDims(p.size_mm)} · ${fmtNum(p.triangles, 0)} triangles · ${fmtNum(p.volume_mm3 / 1000)} cm³`} onClick={() => onSelect(selected === p.id ? null : p)}>{p.label}</button>
                ))}
              </div>
              <p className="hint" style={{ margin: 'var(--sp-2) 0 0' }}>{d.parts.length === 1 ? "Ce fichier ne contient qu'un objet." : 'Identifiants stables : ils ne dépendent que du contenu du fichier.'} {selected ? 'La sélection est consignée dans la note de la révision de profil.' : 'Sans sélection, les réglages visent la pièce entière.'}</p>
            </>
          )}
      </Async>
    </Panel>
  );
}

interface SRow { uid: string; target: 'process' | 'filament' | 'machine'; key: string; value: string }
const uid = () => Math.random().toString(36).slice(2, 8);
const fmtVal = (v: unknown) => (v === undefined || v === null ? '—' : typeof v === 'object' ? JSON.stringify(v) : String(v));

/**
 * Édition manuelle des réglages effectifs : POST /api/profiles/modify (modify_slicing_profile). Chaque réglage est validé contre le profil
 * résolu de Bambu Studio (clé existante, type de la valeur) ; le serveur renvoie l'ancien et le nouveau réglage avec le profil d'origine.
 * Les surcharges valent pour toute la pièce (profil machine / process / filament) : Bambu Studio ne les porte pas par objet dans un profil.
 */
export function SettingsPanel({ machine, revision, onRevision, part, catalog, disabled }: {
  machine: string; revision?: string; onRevision: (r: string | undefined) => void; part?: Part | null; catalog?: GoalCriteriaCatalog; disabled?: boolean;
}) {
  const [rows, setRows] = useState<SRow[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string>();
  const [out, setOut] = useState<ModifyOutput>();
  const keys = [...new Set((catalog?.criteria ?? []).flatMap((c) => (c.levers ?? []).map((l) => l.key).filter((k): k is string => !!k)))].sort();
  const set = (id: string, p: Partial<SRow>) => setRows((l) => l.map((r) => (r.uid === id ? { ...r, ...p } : r)));
  const ready = rows.length > 0 && rows.every((r) => r.key.trim() && r.value.trim());

  const apply = async () => {
    setBusy(true); setErr(undefined);
    try {
      const o = await api.modifyProfile({
        ...(revision ? { base_revision: revision } : { base: { machine: machine.trim() || undefined } }),
        modifications: rows.map((r) => ({ key: r.key.trim(), value: r.value.trim(), target: r.target })),
        note: part ? `Réglages manuels (objet « ${part.label} », identifiant ${part.id}) ; valables pour toute la pièce` : 'Réglages manuels',
      });
      setOut(o); onRevision(o.revision); setRows([]);
    } catch (e) { setErr(describeError(e)); } finally { setBusy(false); }
  };

  return (
    <Panel title="Réglages manuels">
      <div className="prep-form">
        <p className="hint" style={{ margin: 0 }}>Modifiez un réglage de Bambu Studio : il est vérifié contre le profil réel (clé existante, type de valeur). Les variantes comparées partiront de cette révision.</p>
        {part && <p style={{ margin: 0 }}><Tag tone="accent">objet</Tag> {part.label} <span className="hint">— les réglages s'appliquent à la pièce entière ; Bambu Studio ne les porte pas par objet dans un profil.</span></p>}
        <datalist id="setting-keys">{keys.map((k) => <option key={k} value={k} />)}</datalist>
        {rows.map((r) => (
          <div key={r.uid} className="set-row">
            <Select aria-label="Profil visé" value={r.target} onChange={(e) => set(r.uid, { target: e.target.value as SRow['target'] })} options={[{ value: 'process', label: 'process' }, { value: 'filament', label: 'filament' }, { value: 'machine', label: 'machine' }]} />
            <Input aria-label="Clé du réglage" list="setting-keys" placeholder="wall_loops" spellCheck={false} value={r.key} onChange={(e) => set(r.uid, { key: e.target.value })} />
            <Input aria-label="Valeur" placeholder="3" spellCheck={false} value={r.value} onChange={(e) => set(r.uid, { value: e.target.value })} />
            <Button size="sm" variant="ghost" icon={<Trash2 size={13} />} aria-label="Retirer ce réglage" onClick={() => setRows((l) => l.filter((x) => x.uid !== r.uid))} />
          </div>
        ))}
        <div className="row-wrap">
          <Button icon={<Plus size={14} />} disabled={disabled || busy} onClick={() => setRows((l) => [...l, { uid: uid(), target: 'process', key: '', value: '' }])}>Ajouter un réglage</Button>
          <Button variant="primary" disabled={disabled || !ready || busy || (!revision && !machine.trim())} onClick={apply}>{busy ? 'Vérification…' : 'Valider les réglages'}</Button>
          {revision && <Button variant="ghost" onClick={() => { onRevision(undefined); setOut(undefined); }}>Repartir du profil d'origine</Button>}
        </div>
        {err && <Notice tone="err" title="Réglage refusé">{err}</Notice>}
        {revision && (
          <div className="set-result" role="status">
            <p style={{ margin: 0 }}><Tag tone="ok">révision de profil</Tag> <span className="mono">{revision}</span>{out?.parent_revision && <span className="hint"> · issue de <span className="mono">{out.parent_revision}</span></span>}</p>
            {out && (out.diff.length === 0 ? <p className="hint" style={{ margin: 0 }}>Aucun changement par rapport à la base.</p> : (
              <div className="set-scroll"><table className="set-diff" aria-label="Différences avec la base">
                <thead><tr><th scope="col">Réglage</th><th scope="col">Avant</th><th scope="col">Après</th><th scope="col">Hérité de</th></tr></thead>
                <tbody>{out.diff.map((d) => <tr key={d.target + d.key}><th scope="row" className="mono">{d.target}.{d.key}</th><td className="mono">{fmtVal(d.old)}</td><td className="mono"><strong>{fmtVal(d.new)}</strong></td><td className="hint">{d.old_source ?? 'profil d\'origine'}</td></tr>)}</tbody>
              </table></div>
            ))}
            {out?.notes?.map((n) => <p key={n} className="hint" style={{ margin: 0 }}>{n}</p>)}
          </div>
        )}
      </div>
    </Panel>
  );
}
