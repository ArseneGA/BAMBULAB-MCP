import { useRef } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Camera, CheckCircle2, ImageOff, RotateCcw } from 'lucide-react';
import { Button, EmptyState, ErrorState, PageHeader, Panel, Skeleton, Tag, useToast } from '../../components';
import { VerdictBar, useVerdictFlow } from '../../components/VerdictBar';
import { api, ApiError, assetUrl, describeError } from '../../api/client';
import { useAsync } from '../../api/hooks';
import { LEVEL_LABEL, sheetsApi, unavailableText } from '../../api/sheets';
import type { Level, PhotoStep, PhotoStrip, PrintSheet } from '../../api/sheets';
import { REASON_LABEL, VERDICT_LABEL } from '../../api/projectsTypes';
import type { PrintStatus } from '../../api/printTypes';
import { logOutcomeLabel, printStateLabel } from '@shared/labels';
import { fmtDate, fmtNum } from '../common';
import { JobDetail } from '../Print';
import './print-sheet.css';

/* Fiche impression (G9 PJ10, G10 UX7) : /impressions/:id, adressable. Lecture : GET /api/prints/{id}/sheet (contrat internal/sheets).
   Le verdict est un jugement de l'humain, sans effet sur l'imprimante. Le lancement et l'arrêt restent sur les pages du serveur
   (voir JobDetail dans Print.tsx, seul composant qui enregistre un lancement après l'accord humain). */

const lvl = (l?: Level) => (l ? <span className="lvl" title={`Niveau de confiance : ${LEVEL_LABEL[l]}`}>{LEVEL_LABEL[l]}</span> : null);
const hms = (s?: number) => (s === undefined ? undefined : `${Math.floor(s / 3600)} h ${String(Math.round((s % 3600) / 60)).padStart(2, '0')} min`);
const money = (v?: number, cur = 'EUR') => (v === undefined ? undefined : `${fmtNum(v, 2)} ${cur === 'EUR' ? '€' : cur}`);
const na = (sheet: PrintSheet, field: string) => unavailableText(sheet, field) ?? 'non disponible';

/** Planche des paliers à 10 % : une vignette par palier ; « photo non disponible » + raison quand le palier manque, jamais un trou muet. */
export function PhotoBoard({ strip, name }: { strip: PhotoStrip; name: string }) {
  if (strip.steps.length === 0) return <EmptyState title="Aucune photo de palier" text={strip.note ?? 'Aucune photo n\'a été relevée pour cette impression.'} />;
  const step = (s: PhotoStep) => (
    <li key={s.percent} className={`ps-step ps-${s.status}`}>
      {s.status === 'ok' && s.url
        ? <a href={assetUrl(s.url)} target="_blank" rel="noreferrer"><img src={assetUrl(s.url)} alt={`${name} à ${s.percent} %`} loading="lazy" /></a>
        : <div className="ps-none" role="note"><ImageOff size={18} aria-hidden /><span>photo non disponible</span>{s.reason && <small>{s.reason}</small>}{s.status === 'absent' && !s.reason && <small>aucun relevé à ce palier</small>}</div>}
      <span className="ps-pct">{s.percent} %</span>
    </li>
  );
  return (
    <>
      <ol className="ps-board" aria-label="Photos de paliers à 10 %">{strip.steps.map(step)}</ol>
      <p className="sub">{strip.available} photo{strip.available > 1 ? 's' : ''} disponible{strip.available > 1 ? 's' : ''} sur {strip.steps.length} paliers.{strip.note ? ` ${strip.note}` : ''}</p>
    </>
  );
}

function SettingsList({ s }: { s: PrintSheet['profile']['settings'] }) {
  const rows: Array<[string, string | undefined]> = [
    ['Imprimante', s.machine], ['Réglage de qualité', s.process], ['Matières', s.filaments?.join(', ')], ['Buse', s.nozzle_mm ? `${s.nozzle_mm} mm` : undefined],
    ['Plateau', s.bed_type], ['Plaque', s.plate ? String(s.plate) : undefined], ['Couches', s.layers ? String(s.layers) : undefined],
    ['Supports', s.supports === undefined ? undefined : s.supports ? 'oui' : 'non'], ['Orientation', s.orientation],
  ];
  const shown = rows.filter(([, v]) => v);
  return (
    <>
      <dl className="dl">{shown.map(([k, v]) => <div key={k} className="ps-row"><dt>{k}</dt><dd>{v}</dd></div>)}</dl>
      {(s.overrides?.length ?? 0) > 0 && <details><summary>Réglages modifiés ({s.overrides!.length})</summary><ul className="audit">{s.overrides!.map((o, i) => <li key={i}><span className="mono">{o.key}</span><span>{String(o.value)}</span></li>)}</ul></details>}
    </>
  );
}

function VerdictPanel({ sheet, flow }: { sheet: PrintSheet; flow: ReturnType<typeof useVerdictFlow> }) {
  const v = sheet.verdict;
  const title = sheet.header.name ?? sheet.header.model_name ?? sheet.print_job_id;
  return (
    <Panel title="Verdict" actions={lvl(v.level)}>
      {v.value
        ? <p><span className={`pill ${v.value === 'success' ? 'ok' : v.value === 'failed' ? 'fail' : 'wait'}`}><i />{VERDICT_LABEL[v.value]}</span> {v.fail_reason && <>raison : {REASON_LABEL[v.fail_reason] ?? v.fail_reason}. </>}{v.at && <span className="sub">Donné le {fmtDate(v.at)}.</span>}</p>
        : <p className="sub">{sheet.header.reached_100 ? 'Cette impression n\'est pas encore jugée.' : 'Le verdict se donne quand l\'impression a atteint 100 %.'}</p>}
      {v.note && <p className="sub">Note : {v.note}</p>}
      {sheet.header.reached_100 && <div id="verdict-bar"><VerdictBar label={title} reasons={v.fail_reasons.map((r) => r.id)} busy={flow.busy} onGive={(val, r) => flow.give(sheet.print_job_id, title, val, r)} /></div>}
      <p className="sub">Un verdict n'a aucun effet sur l'imprimante ni sur la file.</p>
    </Panel>
  );
}

function SheetBody({ sheet, reload }: { sheet: PrintSheet; reload: () => void }) {
  const flow = useVerdictFlow(reload);
  const h = sheet.header;
  const title = h.name ?? h.model_name ?? sheet.print_job_id;
  return (
    <>
      <div className="ps-grid">
        <VerdictPanel sheet={sheet} flow={flow} />
        <Panel title="Réglages figés" actions={lvl(sheet.profile.level)}>
          <p className="sub">{sheet.profile.label}</p>
          <SettingsList s={sheet.profile.settings} />
          <p className="sub">{sheet.profile.frozen_note}</p>
        </Panel>
        <Panel title="Matière" actions={lvl(sheet.material.level)}>
          <p>{sheet.material.family ?? 'Matière non renseignée'} · {sheet.material.actual_grams !== undefined ? `${fmtNum(sheet.material.actual_grams, 0)} g` : sheet.material.estimated_grams !== undefined ? `~${fmtNum(sheet.material.estimated_grams, 0)} g (estimé)` : na(sheet, 'material.grams')}</p>
          {sheet.material.spools.length > 0 ? (
            <ul className="rowlist">{sheet.material.spools.map((s) => <li key={`${s.spool_id}-${s.filament_slot}`}><div className="grow"><b>{s.label ?? s.spool_id}</b><small>Filament {s.filament_slot} · {fmtNum(s.grams, 0)} g{s.estimated ? ' (estimé)' : ''}{s.cost !== undefined ? ` · ${money(s.cost, sheet.cost.currency)}` : ''}</small></div></li>)}</ul>
          ) : <p className="sub">Aucune bobine rattachée à cette impression.</p>}
        </Panel>
        <Panel title="Temps et coût" actions={lvl(sheet.duration.level)}>
          <dl className="dl">
            <div className="ps-row"><dt>Temps prévu</dt><dd>{hms(sheet.duration.planned_seconds) ?? na(sheet, 'duration.planned_seconds')}</dd></div>
            <div className="ps-row"><dt>Temps réel</dt><dd>{hms(sheet.duration.actual_seconds) ?? na(sheet, 'duration.actual_seconds')}</dd></div>
            <div className="ps-row"><dt>Coût prévu</dt><dd>{money(sheet.cost.planned, sheet.cost.currency) ?? 'non disponible'}</dd></div>
            <div className="ps-row"><dt>Coût réel</dt><dd>{money(sheet.cost.actual, sheet.cost.currency) ?? 'non disponible'} {lvl(sheet.cost.level)}</dd></div>
          </dl>
          <p className="sub">{sheet.duration.text}</p>
        </Panel>
      </div>

      <Panel title="Photos de paliers" actions={<span className="sub">à chaque 10 %</span>}>
        <PhotoBoard strip={sheet.photos} name={title} />
      </Panel>

      <div className="ps-grid">
        <Panel title="Alertes de la caméra" actions={lvl(sheet.camera.level)}>
          <p className="sub">{sheet.camera.text}</p>
          {sheet.camera.alerts.length > 0 && <ul className="rowlist">{sheet.camera.alerts.map((a, i) => <li key={i}><Camera size={16} aria-hidden /><div className="grow"><b>Alerte {a.level}</b><small>{fmtDate(a.at)} · score {fmtNum(a.score, 2)}</small></div>{a.photo_url && <a className="btn btn-ghost btn-sm" href={assetUrl(a.photo_url)} target="_blank" rel="noreferrer">Voir la photo</a>}</li>)}</ul>}
          {!sheet.camera.available && sheet.camera.reason && <p className="sub">Caméra non disponible : {sheet.camera.reason}</p>}
        </Panel>
        <Panel title="Accord de lancement" actions={lvl(sheet.approval.level)}>
          <p><CheckCircle2 size={14} aria-hidden /> {sheet.approval.text}</p>
          {sheet.approval.approved_at && <p className="sub">Approuvé le {fmtDate(sheet.approval.approved_at)}{sheet.approval.via ? ` (${sheet.approval.via})` : ''}.</p>}
        </Panel>
      </div>
      {(sheet.comment || (sheet.defects?.length ?? 0) > 0) && <Panel title="Remarques">{sheet.comment && <p>{sheet.comment}</p>}{sheet.defects?.map((d) => <Tag key={d}>{d}</Tag>)}</Panel>}
    </>
  );
}

/** Fiche d'une impression : en-tête, verdict, réglages figés, matière, temps, photos de paliers, alertes ; puis le suivi du travail (confirmation sur la page du serveur). */
export function PrintSheetPage() {
  const { id = '' } = useParams();
  return <PrintSheetView key={id} id={id} />;
}

function PrintSheetView({ id }: { id: string }) {
  const toast = useToast();
  const sheet = useAsync((s) => sheetsApi.print(id, s).then((r) => ({ ok: r }), (e) => { if (e instanceof ApiError && e.status === 404) return { ok: undefined as PrintSheet | undefined }; throw e; }), [id]);
  const job = useAsync((s) => api.printStatus(id, s).then((r) => ({ ok: r }), (e) => { if (e instanceof ApiError && e.status === 404) return { ok: undefined as PrintStatus | undefined }; throw e; }), [id]);
  const printers = useAsync((s) => api.printers(s), []);
  const reload = useRef(() => undefined as void); reload.current = () => { sheet.reload(); job.reload(); };
  const history = async (action: 'archive' | 'trash' | 'restore') => {
    try { const r = await api.printHistory(action, [id]); toast('ok', r.note); reload.current(); } catch (e) { toast('err', describeError(e)); }
  };

  const s = sheet.data?.ok; const j = job.data?.ok?.job;
  const loading = (sheet.data === undefined && !sheet.error) || (job.data === undefined && !job.error);
  const name = s?.header.name ?? s?.header.model_name ?? j?.name ?? id;
  const missing = !loading && !s && !j && !sheet.error && !job.error;
  const err = !s && !j ? (sheet.error ?? job.error) : undefined;
  const crumbs = [{ label: 'Impressions', to: '/impressions' }, { label: name }];
  const closed = j && ['COMPLETED', 'FAILED', 'CANCELLED'].includes(j.state);
  const state = s ? logOutcomeLabel(s.header.outcome) : j ? printStateLabel(j.state) : undefined;
  const action = s && !s.verdict.value && s.header.reached_100
    ? <a className="btn btn-primary" href="#verdict-bar" onClick={(e) => { e.preventDefault(); document.querySelector<HTMLElement>('#verdict-bar button')?.focus(); }}>Donner le verdict</a>
    : s?.header.model_id ? <Link className="btn btn-primary" to={`/modele/${encodeURIComponent(s.header.model_id)}/preparer`}><RotateCcw size={15} aria-hidden />Refaire</Link> : undefined;

  if (missing) return (
    <>
      <PageHeader title="Impression introuvable" crumbs={[{ label: 'Impressions', to: '/impressions' }, { label: 'Introuvable' }]} />
      <EmptyState title="Cette impression n'existe pas" text="Elle a peut-être été supprimée, ou le lien est incorrect." action={<Link className="btn btn-secondary btn-sm" to="/impressions">Retour aux impressions</Link>} />
    </>
  );
  return (
    <>
      <PageHeader title={name} crumbs={crumbs} actions={action}
        help={s ? `${state?.label ?? ''}${s.header.finished_at ? ` · ${fmtDate(s.header.finished_at)}` : ''}${s.header.printer_model ? ` · ${s.header.printer_model}` : ''}` : state?.label} />
      <div className="stack">
        {loading && <Skeleton lines={4} label="Chargement de la fiche" />}
        {err && <ErrorState title="Cette fiche n'a pas pu se charger" text="La lecture a échoué." impact="Rien n'a été modifié ; le reste de Printalk reste utilisable." action={<Button size="sm" onClick={reload.current}>Réessayer</Button>} details={describeError(err)} />}
        {s && <SheetBody sheet={s} reload={reload.current} />}
        {j && (
          <JobDetail key={j.id + j.state} job={j} printer={printers.data?.printers.find((p) => p.id === j.printer_id)} progress={undefined} onChange={reload.current} />
        )}
        {closed && j && (
          <Panel title="Classement">
            <p className="sub">Archiver ou mettre à la corbeille ne supprime rien définitivement : l'impression reste récupérable.</p>
            <div className="ps-acts">
              {j.archived || j.trashed
                ? <Button size="sm" onClick={() => history('restore')}>Restaurer</Button>
                : <><Button size="sm" onClick={() => history('archive')}>Archiver</Button><Button size="sm" variant="danger" onClick={() => history('trash')}>Mettre à la corbeille</Button></>}
            </div>
          </Panel>
        )}
      </div>
    </>
  );
}
