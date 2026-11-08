import { useEffect, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { Check, ClipboardCheck, SlidersHorizontal } from 'lucide-react';
import { Button, EmptyState, Panel, ProgressBar, Tag } from '../components';
import { api, ApiError, describeError } from '../api/client';
import { useAsync } from '../api/hooks';
import type { ApplyOutput, CompareCandidate, CompareOutput, Experiment, Metric } from '../api/goalTypes';
import type { ExperimentSummary, PrintResult } from '../api/extraTypes';
import { MemoryPanel, RecordDialog } from './RecordResult';
import { PrepareNav } from './PrepareNav';
import { Async, Dialog, Notice, fmtDate, fmtNum } from './common';
import './model.css';
import './compare.css';

const STATE: Record<string, { label: string; tone?: 'ok' | 'warn' | 'err' }> = {
  pending: { label: 'en attente' }, queued: { label: 'en file' }, running: { label: 'découpe en cours', tone: 'warn' },
  succeeded: { label: 'terminée', tone: 'ok' }, failed: { label: 'échec', tone: 'err' }, cancelled: { label: 'annulée', tone: 'warn' },
};
const METRIC_ORDER: Array<[string, string]> = [
  ['print_time_s', 'Durée estimée'], ['filament_g', 'Matière estimée'], ['filament_m', 'Filament (longueur)'], ['layers', 'Couches'],
  ['layer_height_mm', 'Hauteur de couche'], ['support_g', 'Supports'], ['height_mm', 'Hauteur'],
];

/** Avancement global de la découpe : variantes terminées sur total (le serveur ne publie pas de pourcentage par variante). */
function sliceSource(c: CompareOutput) {
  const total = c.candidates.length;
  const done = c.candidates.filter((k) => !['pending', 'queued', 'running'].includes(k.state)).length;
  return { state: 'running', percent: total > 0 ? (done / total) * 100 : undefined, phase_label: `${done} variante${done > 1 ? 's' : ''} découpée${done > 1 ? 's' : ''} sur ${total}` };
}

function fmtMetric(id: string, m: Metric): string {
  if (id === 'print_time_s') { const s = Math.round(m.value); const h = Math.floor(s / 3600), mn = Math.floor((s % 3600) / 60); return h ? `${h} h ${String(mn).padStart(2, '0')} min` : `${mn} min ${String(s % 60).padStart(2, '0')} s`; }
  return `${fmtNum(m.value, 2)} ${m.unit}`;
}

export function ComparePage() {
  const [sp] = useSearchParams();
  let id = sp.get('experiment');
  if (!id) { try { id = (JSON.parse(localStorage.getItem('atelier-last-experiment') ?? 'null') as { experiment_id?: string } | null)?.experiment_id ?? null; } catch { /* ignore */ } }
  return <><PrepareNav />{id ? <CompareView key={id} id={id} /> : <ExperimentIndex />}</>;
}

const EXP_STATE: Record<ExperimentSummary['state'], { label: string; tone?: 'ok' | 'warn' }> = {
  running: { label: 'découpes en cours', tone: 'warn' }, complete: { label: 'terminée', tone: 'ok' }, partial: { label: 'terminée avec échecs', tone: 'warn' },
};

/** Expériences passées (GET /api/experiments), la plus récente d'abord. */
function ExperimentIndex() {
  const list = useAsync((s) => api.experiments(undefined, s), []);
  const models = useAsync((s) => api.models({ limit: 100, sort: 'name' }, s), []);
  const name = (id: string) => models.data?.models.find((m) => m.id === id)?.name ?? id;
  return (
    <>
      <div className="page-head"><span className="eyebrow">Espace</span><h1>Comparaison</h1><p>Expériences de découpe déjà créées : rouvrez-en une pour comparer ses variantes ou enregistrer le résultat réel d'une impression.</p></div>
      <Async state={list} label="Chargement des expériences" empty={(d) => d.experiments.length === 0
        ? <EmptyState title="Aucune expérience" text="Une comparaison part d'un objectif validé : créez une expérience depuis la préparation d'un modèle." action={<Link to="/preparation"><Button size="sm" variant="primary" icon={<SlidersHorizontal size={14} />}>Ouvrir la préparation</Button></Link>} /> : null}>
        {(d) => (
          <Panel title={`Expériences (${d.experiments.length})`} flush>
            <ul className="exp-list" aria-label="Expériences">
              {d.experiments.map((x) => (
                <li key={x.id}>
                  <Link to={`/comparaison?experiment=${encodeURIComponent(x.id)}`} className="exp-item">
                    <strong>{name(x.model_id)}</strong>
                    <span className="exp-meta"><Tag tone={EXP_STATE[x.state].tone}>{EXP_STATE[x.state].label}</Tag> {x.succeeded}/{x.candidates} découpes réussies{x.failed > 0 ? `, ${x.failed} en échec` : ''}</span>
                    <span className="mono dim">{fmtDate(x.created_at)} · {x.machine ?? 'machine inconnue'}{x.material_family ? ` · ${x.material_family}` : ''} · {x.id}</span>
                  </Link>
                </li>
              ))}
            </ul>
          </Panel>
        )}
      </Async>
    </>
  );
}

function CompareView({ id }: { id: string }) {
  const exp = useAsync((s) => api.experiment(id, s), [id]);
  const cmp = useAsync((s) => api.compare(id, s), [id]);
  const reloadRef = useRef(cmp.reload); reloadRef.current = cmp.reload;
  const complete = cmp.data?.complete;
  useEffect(() => { if (complete !== false) return; const t = window.setInterval(() => reloadRef.current(), 2500); return () => window.clearInterval(t); }, [complete]);
  const [apply, setApply] = useState<CompareCandidate | null>(null);
  const [applied, setApplied] = useState<ApplyOutput>();
  const [record, setRecord] = useState<CompareCandidate | null>(null);
  const [saved, setSaved] = useState<PrintResult[]>([]);
  const memory = useAsync((s) => api.printMemory(id, s), [id, saved.length]);

  return (
    <>

      <Async state={cmp} label="Chargement de l'expérience">
        {(c) => (
          <>
            <div className="mdl-head">
              <div>
                <nav className="mdl-crumb" aria-label="Fil d'Ariane"><Link to="/bibliotheque">Bibliothèque</Link><span aria-hidden>/</span>{exp.data && <><Link to={`/modele/${exp.data.model_id}`}>Modèle</Link><span aria-hidden>/</span></>}<span>Comparaison</span></nav>
                <h1>Comparaison des variantes</h1>
                <p className="hint" style={{ margin: 0 }}>Expérience <span className="mono">{c.experiment_id}</span> · révision figée <span className="mono">{c.model_version_id}</span>{exp.data && <> · {exp.data.details.base.machine} · {exp.data.details.base.process}</>}</p>
              </div>
              {exp.data && <Link to={`/preparation?model=${exp.data.model_id}`}><Button icon={<SlidersHorizontal size={15} />}>Retour à la préparation</Button></Link>}
            </div>
            {!c.complete && (
              <Notice tone="info" icon={<span className="spinner" aria-hidden />} title="Découpe des variantes en cours">
                Bambu Studio découpe chaque candidat ; le tableau se complète tout seul ({c.pending?.length ?? 0} en attente).
                <ProgressBar label="Avancement de la découpe des variantes" source={sliceSource(c)} />
              </Notice>
            )}
            <Table c={c} exp={exp.data} onApply={setApply} onRecord={setRecord} />
            <Rankings c={c} />
            {c.not_evaluable.length > 0 && (
              <Panel title="À vérifier par essai">
                {c.not_evaluable.map((n) => <p key={n.criterion} className="hint" style={{ margin: '0 0 var(--sp-2)' }}><Tag tone="warn">non calculé</Tag> {n.label} : {n.reason}{n.test && <> Essai : {n.test}</>}</p>)}
              </Panel>
            )}
            {c.skipped_levers && c.skipped_levers.length > 0 && <p className="hint">Leviers écartés : {c.skipped_levers.map((s) => s.reason).join(' ; ')}.</p>}
            <p className="hint" style={{ maxWidth: '80ch' }}>{c.notice}</p>
            {applied && (
              <Notice tone="info" icon={<Check size={16} aria-hidden />} title={`Variante « ${applied.label} » appliquée`}>
                Révision de profil <span className="mono">{applied.profile_revision}</span> : {applied.overrides.length === 0 ? 'profil de base inchangé' : applied.overrides.map((o) => `${o.key} = ${String(o.value)}`).join(', ')}. {applied.notes.join(' ')}
              </Notice>
            )}
            <MemoryPanel state={memory} saved={saved} />
            <ApplyDialog c={apply} compare={c} onClose={() => setApply(null)} onDone={(o) => { setApplied(o); setApply(null); }} />
            <RecordDialog c={record} compare={c} exp={exp.data} onClose={() => setRecord(null)} onDone={(r) => { setSaved((l) => [r, ...l]); setRecord(null); }} />
          </>
        )}
      </Async>
    </>
  );
}

function Table({ c, exp, onApply, onRecord }: { c: CompareOutput; exp?: Experiment; onApply: (k: CompareCandidate) => void; onRecord: (k: CompareCandidate) => void }) {
  const metricIds = METRIC_ORDER.filter(([m]) => c.candidates.some((k) => k.metrics?.[m]));
  const ref = c.candidates.find((k) => k.kind === 'reference');
  return (
    <div className="cmp-scroll">
      <table className="cmp" aria-label="Variantes comparées">
        <thead>
          <tr><th scope="col"><span className="sr-only">Critère</span></th>
            {c.candidates.map((k) => (
              <th key={k.id} scope="col"><span className="cmp-title">{k.label}</span><span className="cmp-state"><Tag tone={STATE[k.state]?.tone}>{STATE[k.state]?.label ?? k.state}</Tag>{k.kind === 'reference' && <Tag>référence</Tag>}{k.violates_non_negotiable && <Tag tone="err">viole un critère non négociable</Tag>}</span></th>
            ))}</tr>
        </thead>
        <tbody>
          <tr><th scope="row">Changements par rapport à la référence</th>{c.candidates.map((k) => <td key={k.id}>{k.changes.length === 0 ? <span className="hint">aucun</span> : <ul className="cmp-list mono">{k.changes.map((x) => <li key={x}>{x}</li>)}</ul>}</td>)}</tr>
          <tr><th scope="row">Compromis attendus</th>{c.candidates.map((k) => <td key={k.id}>{k.expected_tradeoffs.length === 0 ? <span className="hint">—</span> : <ul className="cmp-list">{k.expected_tradeoffs.map((x) => <li key={x}>{x}</li>)}</ul>}</td>)}</tr>
          {metricIds.map(([m, label]) => {
            const any = c.candidates.find((k) => k.metrics?.[m])?.metrics?.[m];
            return (
              <tr key={m}><th scope="row">{label} {any && <Tag tone="warn">{any.label}</Tag>}</th>
                {c.candidates.map((k) => { const v = k.metrics?.[m]; const rv = ref?.metrics?.[m]; const d = v && rv && k !== ref ? v.value - rv.value : 0;
                  return <td key={k.id} className="mono">{v ? <>{fmtMetric(m, v)}{d !== 0 && <span className="cmp-delta"> ({d > 0 ? '+' : ''}{fmtNum(d, 2)})</span>}</> : <span className="hint">{k.state === 'succeeded' ? 'non mesuré' : '—'}</span>}</td>; })}
              </tr>
            );
          })}
          {c.candidates.some((k) => k.threshold_checks?.length) && <tr><th scope="row">Seuils de l'objectif</th>{c.candidates.map((k) => <td key={k.id}>{(k.threshold_checks ?? []).map((t) => <div key={t.criterion + t.metric}><Tag tone={t.pass ? 'ok' : 'err'}>{t.pass ? 'respecté' : 'non respecté'}</Tag> {t.label} {t.op} {t.limit}</div>)}</td>)}</tr>}
          {c.candidates.some((k) => k.engineering?.length) && <tr><th scope="row">Calcul d'ingénierie</th>{c.candidates.map((k) => <td key={k.id}>{(k.engineering ?? []).map((e) => <div key={e.criterion}>{e.error ? <Tag tone="warn">{e.error}</Tag> : <><span className="mono">SF {fmtNum(e.safety_factor, 2)}</span> <Tag>{e.confidence_level}</Tag> <span className="hint">{e.verdict}</span></>}</div>)}</td>)}</tr>}
          {c.candidates.some((k) => k.error) && <tr><th scope="row">Erreurs</th>{c.candidates.map((k) => <td key={k.id}>{k.error ? <Tag tone="err">{k.error}</Tag> : <span className="hint">—</span>}</td>)}</tr>}
          <tr><th scope="row">Décision</th>{c.candidates.map((k) => <td key={k.id}><div className="cmp-acts"><Button size="sm" variant="primary" disabled={k.state !== 'succeeded'} onClick={() => onApply(k)}>Appliquer</Button><Button size="sm" icon={<ClipboardCheck size={13} />} disabled={k.state !== 'succeeded'} onClick={() => onRecord(k)} title="Consigner ce que l'impression réelle a donné">Enregistrer l'essai réel</Button></div></td>)}</tr>
        </tbody>
      </table>
      {exp && <p className="hint">Mesures : « estimation slicer » = Bambu Studio hors préchauffage ; « lecture du G-code » = relevé sur le fichier découpé. Aucune ne prouve résistance, aspect ou précision réelle.</p>}
    </div>
  );
}

function Rankings({ c }: { c: CompareOutput }) {
  const name = (id: string) => c.candidates.find((k) => k.id === id)?.label ?? id;
  if (c.rankings.length === 0) return null;
  return (
    <Panel title="Classement par critère mesurable (sans gagnant global)">
      {c.rankings.map((r) => (
        <div key={r.criterion + r.metric} className="cmp-rank">
          <strong>{r.label}</strong> <span className="hint">({r.metric}, {r.better === 'lower' ? 'plus bas = mieux' : 'plus haut = mieux'}{r.basis ? ` · ${r.basis}` : ''})</span>
          {r.all_tied ? <p className="hint" style={{ margin: 0 }}>Égalité : {r.note ?? 'valeurs identiques.'}</p> : <ol className="cmp-order">{r.order.map((o) => <li key={o.candidate_id}>{name(o.candidate_id)} <span className="mono">{fmtNum(o.value, 2)}</span></li>)}</ol>}
        </div>
      ))}
    </Panel>
  );
}

function ApplyDialog({ c, compare, onClose, onDone }: { c: CompareCandidate | null; compare: CompareOutput; onClose: () => void; onDone: (o: ApplyOutput) => void }) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string>();
  useEffect(() => { setErr(undefined); }, [c]);
  const go = async () => {
    if (!c) return;
    setBusy(true); setErr(undefined);
    try { onDone(await api.applyVariant(compare.experiment_id, c.id, compare.model_version_id)); }
    catch (e) {
      const cur = e instanceof ApiError && e.code === 'stale_revision' ? ` Révision courante : ${String(e.body?.current ?? 'inconnue')}. Relisez l'état du modèle avant de recommencer.` : '';
      setErr(describeError(e) + cur);
    } finally { setBusy(false); }
  };
  return (
    <Dialog open={c !== null} onClose={onClose} title="Appliquer cette variante ?" footer={<><Button onClick={onClose}>Annuler</Button><Button variant="primary" disabled={busy} onClick={go}>{busy ? 'Application…' : 'Appliquer'}</Button></>}>
      {c && (
        <>
          <p style={{ margin: 0 }}><strong>{c.label}</strong></p>
          {c.changes.length === 0 ? <p className="hint" style={{ margin: 0 }}>Profil de base sans modification.</p> : <ul className="cmp-list mono" style={{ margin: 0 }}>{c.changes.map((x) => <li key={x}>{x}</li>)}</ul>}
          <p className="hint" style={{ margin: 0 }}>Appliquée sur la révision <span className="mono">{compare.model_version_id}</span> ; refusée si le modèle a une version plus récente. L'application fixe un profil de découpe : aucune impression n'est lancée.</p>
          {err && <Notice tone="err" title="Application refusée">{err}</Notice>}
        </>
      )}
    </Dialog>
  );
}
