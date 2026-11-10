import { useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { Button, EmptyState, Panel, Select, Tag, useToast } from '../components';
import { describeError } from '../api/client';
import { useAsync } from '../api/hooks';
import { REASON_LABEL, STATUS_LABEL, VERDICT_LABEL, projectsApi } from '../api/projectsTypes';
import type { ListItem, PendingList, PendingVerdict, ProjectDetail, ProjectList, ProjectStatus, Progress, Verdict } from '../api/projectsTypes';
import { Async, fmtDate } from './common';
import './projects.css';

const STATUS_TONE: Record<ProjectStatus, 'ok' | 'warn' | 'accent' | 'err' | undefined> = { idea: undefined, design: 'accent', printing: 'accent', done: 'ok', in_use: 'ok', abandoned: 'err' };

function Bar({ p }: { p: Progress }) {
  const v = p.units_total > 0 ? Math.round((p.units_printed / p.units_total) * 100) : 0;
  return (
    <div className="prj-bar">
      <div className="prj-track" role="meter" aria-valuemin={0} aria-valuemax={100} aria-valuenow={v} aria-label={`Avancement : ${p.text}`}><span style={{ width: `${v}%` }} /></div>
      <span className="hint">{p.text}</span>
    </div>
  );
}

/** Verdict en un geste : trois boutons ; « Raté » et « À refaire » demandent la raison. Aucun effet sur l'imprimante ni sur la file. */
function VerdictRow({ p, reasons, onDone }: { p: PendingVerdict; reasons: Array<{ id: string; label: string }>; onDone: () => void }) {
  const toast = useToast();
  const [ask, setAsk] = useState<Verdict | null>(null);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const send = async (v: Verdict, why?: string) => {
    setBusy(true);
    try {
      const r = await projectsApi.setVerdict(p.print_job_id, v, why);
      toast('ok', r.note);
      setAsk(null); onDone();
    } catch (e) { toast('err', describeError(e)); } finally { setBusy(false); }
  };
  const label = p.model_name || p.name || p.print_job_id;
  return (
    <li className="prj-verdict" aria-label={`Impression ${label}`}>
      <div><strong>{label}</strong><div className="hint">{p.state === 'COMPLETED' ? 'Terminée' : 'Échec'}{p.material_family ? ` · ${p.material_family}` : ''}{p.finished_at ? ` · ${fmtDate(p.finished_at)}` : ''}</div></div>
      <div className="prj-acts">
        <Button size="sm" variant="primary" disabled={busy} onClick={() => send('success')}>Réussi</Button>
        <Button size="sm" disabled={busy} onClick={() => setAsk('failed')}>Raté</Button>
        <Button size="sm" disabled={busy} onClick={() => setAsk('redo')}>À refaire</Button>
      </div>
      {ask && (
        <div className="prj-ask">
          <Select aria-label="Raison" value={reason} onChange={(e) => setReason(e.target.value)}
            options={[{ value: '', label: ask === 'failed' ? 'Raison non précisée (autre)' : 'Aucune raison' }, ...reasons.map((r) => ({ value: r.id, label: REASON_LABEL[r.id] ?? r.label }))]} />
          <Button size="sm" variant="primary" disabled={busy} onClick={() => send(ask, reason || undefined)}>Enregistrer « {VERDICT_LABEL[ask].toLowerCase()} »</Button>
          <Button size="sm" variant="ghost" onClick={() => setAsk(null)}>Annuler</Button>
        </div>
      )}
    </li>
  );
}

function Pending({ data, reload }: { data: PendingList; reload: () => void }) {
  if (data.pending.length === 0) return <EmptyState title="Rien à juger" text="Aucune impression terminée n'attend de verdict." />;
  return (
    <>
      <p className="hint">{data.note} Un verdict n'a aucun effet sur l'imprimante, la file ni la confirmation.</p>
      <ul className="prj-list" aria-label="Impressions sans verdict">{data.pending.map((p) => <VerdictRow key={p.print_job_id} p={p} reasons={data.fail_reasons} onDone={reload} />)}</ul>
    </>
  );
}

function Tree({ data, selected }: { data: ProjectList; selected: string }) {
  if (data.projects.length === 0) return <EmptyState title="Aucun projet" text="Demandez à Claude de créer un projet (create_project) puis d'y ajouter des pièces (add_project_part) : l'avancement apparaîtra ici." />;
  return (
    <ul className="prj-list" aria-label="Projets">
      {data.projects.map((r: ListItem) => (
        <li key={r.project.id} style={{ paddingLeft: r.depth * 16 }} className={r.project.id === selected ? 'prj-sel' : undefined}>
          <Link to={`/projets?p=${encodeURIComponent(r.project.id)}`} aria-current={r.project.id === selected ? 'true' : undefined}>
            <span className="prj-name">{r.project.name}</span>
            <Tag tone={STATUS_TONE[r.project.status]}>{STATUS_LABEL[r.project.status]}</Tag>
            {r.project.archived ? <Tag>Archivé</Tag> : null}
            <span className="hint"> {r.progress.text}</span>
          </Link>
        </li>
      ))}
    </ul>
  );
}

const hms = (s: number) => { const m = Math.round(s / 60); return m < 60 ? `${m} min` : `${Math.floor(m / 60)} h ${String(m % 60).padStart(2, '0')}`; };

function Sheet({ d }: { d: ProjectDetail }) {
  const p = d.project, t = d.totals;
  return (
    <>
      <div className="prj-head">
        <h2>{p.name}</h2>
        <Tag tone={STATUS_TONE[p.status]}>{STATUS_LABEL[p.status]}</Tag>{p.archived ? <Tag>Archivé</Tag> : null}
        {d.path && d.path.length > 0 && <span className="hint">dans {d.path.map((x) => x.name).join(' › ')}</span>}
      </div>
      {p.description && <p>{p.description}</p>}
      <Bar p={d.progress} />
      <dl className="prj-stats">
        <div><dt>Pièces</dt><dd>{d.progress.parts_done}/{d.progress.parts_total}</dd></div>
        <div><dt>Coût réel (calculé)</dt><dd>{t.available ? `${t.actual_cost.toFixed(2).replace('.', ',')} ${t.currency === 'EUR' || !t.currency ? '€' : t.currency}` : 'non disponible'}</dd>{!t.available && t.reason && <small>{t.reason}</small>}</div>
        <div><dt>Temps réel (mesuré)</dt><dd>{t.available ? hms(t.actual_seconds) : 'non disponible'}</dd></div>
      </dl>
      <p className="hint">Jugement (déclaré) : {d.progress.units_judged_success} réussie(s), {d.progress.units_judged_failed + d.progress.units_judged_redo} ratée(s) ou à refaire, {d.progress.units_unjudged} non jugée(s).</p>
      {d.suggested_status && <p role="status" className="hint">Statut suggéré : <b>{STATUS_LABEL[d.suggested_status]}</b> ({d.suggested_reason}). Il ne change que si vous le demandez à Claude (update_project).</p>}
      <h3>Pièces</h3>
      {d.parts.length === 0 ? <EmptyState title="Aucune pièce" text="Ajoutez des pièces avec add_project_part." /> : (
        <ul className="prj-list" aria-label="Pièces du projet">
          {d.parts.map((m) => (
            <li key={m.model_id}>
              <Link to={`/modele/${encodeURIComponent(m.model_id)}`}>{m.model_name || m.model_id}</Link>{m.role ? <span className="hint"> · {m.role}</span> : null}
              {' '}<Tag tone={m.satisfied ? 'ok' : undefined}>{m.satisfied ? 'Imprimée' : m.in_progress > 0 ? 'En cours' : 'À imprimer'}</Tag>
              {m.judged_failed + m.judged_redo > 0 && <Tag tone="warn">Jugée ratée ou à refaire ×{m.judged_failed + m.judged_redo}</Tag>}
              <div className="hint">×{m.quantity} voulue(s) : {m.text}{m.implicit ? ' (adoptée avec la collection)' : ''}</div>
            </li>
          ))}
        </ul>
      )}
      {d.sub_projects.length > 0 && (
        <>
          <h3>Sous-projets</h3>
          <ul className="prj-list" aria-label="Sous-projets">
            {d.sub_projects.map((s) => (
              <li key={s.id} style={{ paddingLeft: (s.depth - 1) * 16 }}>
                <Link to={`/projets?p=${encodeURIComponent(s.id)}`}>{s.name}</Link> <Tag tone={STATUS_TONE[s.status]}>{STATUS_LABEL[s.status]}</Tag>
                <div className="hint">{s.progress.text} (propre : {s.own.text})</div>
              </li>
            ))}
          </ul>
        </>
      )}
      <p className="hint">Prochaine étape : {d.next_action}</p>
    </>
  );
}

/** Projets : arbre des projets, fiche (avancement, pièces, sous-projets, coût et temps) et impressions à juger. Aucun geste d'impression. */
export function ProjectsPage() {
  const [sp] = useSearchParams();
  const sel = sp.get('p') ?? '';
  const [archived, setArchived] = useState(false);
  const list = useAsync((s) => projectsApi.list({ includeArchived: archived }, s), [archived]);
  const sheet = useAsync((s) => (sel ? projectsApi.get(sel, s) : Promise.resolve(undefined)), [sel]);
  const pending = useAsync((s) => projectsApi.pending(s), []);
  return (
    <>
      <div className="page-head">
        <div><h1>Projets</h1><div className="sub">Où en est chaque projet : pièces imprimées, coût, temps, et ce qui attend votre verdict.</div></div>
      </div>
      <div className="prj-grid">
        <Panel title="Projets" actions={<label className="hint"><input type="checkbox" checked={archived} onChange={(e) => setArchived(e.target.checked)} /> Archivés</label>}>
          <Async state={list} label="Lecture des projets">{(d) => <Tree data={d} selected={sel} />}</Async>
        </Panel>
        <Panel title="Fiche du projet">
          {!sel ? <EmptyState title="Choisissez un projet" text="Sa fiche s'affiche ici." /> : <Async state={sheet} label="Lecture du projet">{(d) => (d ? <Sheet d={d} /> : null)}</Async>}
        </Panel>
      </div>
      <Panel title="Impressions à juger">
        <Async state={pending} label="Lecture des impressions">{(d) => <Pending data={d} reload={() => { pending.reload(); list.reload(); sheet.reload(); }} />}</Async>
      </Panel>
    </>
  );
}
