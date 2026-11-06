import { useState } from 'react';
import { Download, PackageOpen, Ruler } from 'lucide-react';
import { Button, Field, Panel, Select, Tag, useToast } from '../components';
import { api, describeError } from '../api/client';
import type { ModelDetails } from '../api/types';
import type { AnalyzeOutput } from '../api/extraTypes';
import { Notice, fmtNum } from './common';

const UNITS = [{ value: 'mm', label: 'millimètres (mm)' }, { value: 'cm', label: 'centimètres (cm)' }, { value: 'm', label: 'mètres (m)' }, { value: 'inch', label: 'pouces (inch)' }];
const MACHINES = [{ value: 'p1s', label: 'Bambu Lab P1S' }, { value: 'p1p', label: 'Bambu Lab P1P' }, { value: 'x1c', label: 'Bambu Lab X1 Carbon' }, { value: 'a1', label: 'Bambu Lab A1' }, { value: 'a1mini', label: 'Bambu Lab A1 mini' }];

/**
 * Unité confirmée (recalcule l'analyse, mémorisée sur le fichier) et fabricabilité par machine
 * (POST /api/models/{id}/analyze). Les mesures sont géométriques ; la fabricabilité est heuristique.
 */
export function AnalysisTools({ m, onChanged }: { m: ModelDetails; onChanged: () => void }) {
  const toast = useToast();
  const cur = m.files.find((f) => f.id === m.current_file_id) ?? m.files[m.files.length - 1];
  const isMesh = !!cur && ['stl', 'obj'].includes(cur.format);
  const usable = !!cur && cur.availability === 'linux' && !m.model.deleted_at && m.analysis.state === 'ok';
  const [unit, setUnit] = useState(cur?.unit || 'mm');
  const [machine, setMachine] = useState('p1s');
  const [busy, setBusy] = useState<'unit' | 'fab' | null>(null);
  const [out, setOut] = useState<AnalyzeOutput>();
  const [err, setErr] = useState<string>();

  const run = async (kind: 'unit' | 'fab') => {
    setBusy(kind); setErr(undefined);
    try {
      const r = await api.analyze(m.model.id, kind === 'unit' ? { unit, machine } : { machine });
      setOut(r);
      if (kind === 'unit') { toast('ok', `Unité ${unit} confirmée : analyse recalculée.`); onChanged(); }
    } catch (e) { setErr(describeError(e)); } finally { setBusy(null); }
  };

  const fab = out?.fabricability;
  return (
    <Panel title="Unité et fabricabilité">
      {!usable ? <p className="hint" style={{ margin: 0 }}>Disponible une fois le fichier copié sur le serveur et analysé.</p> : (
        <div className="mdl-tools">
          {isMesh ? (
            <div className="mdl-tool-row">
              <Field label="Unité des coordonnées du fichier"><Select value={unit} onChange={(e) => setUnit(e.target.value)} options={UNITS} /></Field>
              <Button icon={<Ruler size={14} />} disabled={busy !== null} onClick={() => run('unit')}>{busy === 'unit' ? 'Recalcul…' : "Confirmer l'unité et recalculer"}</Button>
            </div>
          ) : <p className="hint" style={{ margin: 0 }}>Ce format porte son unité : aucune confirmation à faire.</p>}
          {isMesh && <p className="hint" style={{ margin: 0 }}>{m.analysis.unit_assumed ? "L'unité est supposée en millimètres : confirmez-la, sinon les dimensions peuvent être fausses d'un facteur 10 à 1000." : `Unité actuelle : ${cur?.unit || m.analysis.unit || 'mm'}. La corriger recalcule les dimensions et l'aperçu.`}</p>}
          <div className="mdl-tool-row">
            <Field label="Machine cible"><Select value={machine} onChange={(e) => setMachine(e.target.value)} options={MACHINES} /></Field>
            <Button disabled={busy !== null} onClick={() => run('fab')}>{busy === 'fab' ? 'Calcul…' : 'Évaluer la fabricabilité'}</Button>
          </div>
          {err && <Notice tone="err" title="Analyse refusée">{err}</Notice>}
          {fab && (
            <div className="mdl-fab" role="status">
              <p style={{ margin: 0 }}><Tag tone="warn">heuristique</Tag> pour {fab.target.name} ({fmtNum(fab.target.bed_x_mm, 0)} × {fmtNum(fab.target.bed_y_mm, 0)} × {fmtNum(fab.target.bed_z_mm, 0)} mm)</p>
              <dl className="dl">
                <dt>Tient sur le plateau</dt><dd>{fab.fits_bed ? <Tag tone="ok">oui</Tag> : fab.fits_bed_rotated_90_about_z ? <Tag tone="warn">après rotation de 90°</Tag> : <Tag tone="err">non</Tag>}</dd>
                <dt>Surplombs (&gt; {fab.overhang.threshold_deg}°)</dt><dd className="mono">{fmtNum(fab.overhang.fraction_of_surface * 100, 1)} % de la surface · {fab.overhang.region_count} zone(s)</dd>
                <dt>Parois fines (&lt; {fmtNum(fab.thin_walls.threshold_mm, 1)} mm)</dt><dd className="mono">{fmtNum(fab.thin_walls.thin_fraction * 100, 1)} % des échantillons · min {fmtNum(fab.thin_walls.min_mm, 2)} mm · confiance {fab.thin_walls.confidence}</dd>
                {fab.orientation && fab.orientation.candidates[fab.orientation.best_index] && <><dt>Orientation suggérée</dt><dd>{fab.orientation.candidates[fab.orientation.best_index].label}</dd></>}
              </dl>
            </div>
          )}
          {out && out.caveats.length > 0 && <ul className="hint" style={{ paddingLeft: 'var(--sp-4)', margin: 0 }}>{out.caveats.map((c) => <li key={c}>{c}</li>)}</ul>}
        </div>
      )}
    </Panel>
  );
}

/** Téléchargement des originaux (par empreinte) et export groupé en archive. */
export function ExportTools({ m }: { m: ModelDetails }) {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const copied = m.files.filter((f) => f.availability === 'linux');
  const exportZip = async () => {
    setBusy(true);
    try {
      const r = await api.exportZip([m.model.id]);
      const a = document.createElement('a');
      const url = URL.createObjectURL(r.blob);
      a.href = url; a.download = `${m.model.name.replace(/[^\p{L}\p{N}._-]+/gu, '_')}.zip`;
      document.body.appendChild(a); a.click(); a.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 10_000);
      toast('ok', `Archive prête : ${r.files} fichier(s)${r.skipped ? `, ${r.skipped} non copié(s) laissé(s) de côté` : ''}.`);
    } catch (e) { toast('err', describeError(e)); } finally { setBusy(false); }
  };
  if (copied.length === 0) return null;
  return (
    <Button size="sm" icon={<PackageOpen size={14} />} disabled={busy} onClick={exportZip} title="Archive zip des originaux de ce modèle, avec un manifeste d'empreintes">{busy ? 'Archive…' : 'Exporter (zip)'}</Button>
  );
}

/** Export groupé de la sélection de la bibliothèque (POST /api/export). */
export function ExportSelected({ ids }: { ids: string[] }) {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const go = async () => {
    setBusy(true);
    try {
      const r = await api.exportZip(ids);
      const a = document.createElement('a');
      const url = URL.createObjectURL(r.blob);
      a.href = url; a.download = 'atelier-export.zip';
      document.body.appendChild(a); a.click(); a.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 10_000);
      toast('ok', `Archive prête : ${r.files} fichier(s)${r.skipped ? `, ${r.skipped} non copié(s) laissé(s) de côté` : ''}.`);
    } catch (e) { toast('err', describeError(e)); } finally { setBusy(false); }
  };
  return <Button size="sm" icon={<PackageOpen size={14} />} disabled={busy} onClick={go} title="Archive zip des originaux copiés sur le serveur, avec un manifeste d'empreintes">{busy ? 'Archive…' : 'Exporter (zip)'}</Button>;
}

export function DownloadLink({ fileId, name }: { fileId: string; name: string }) {
  return <a className="btn btn-ghost btn-sm" href={api.downloadUrl(fileId)} download={name} aria-label={`Télécharger l'original ${name}`}><Download size={13} aria-hidden /> Télécharger</a>;
}
