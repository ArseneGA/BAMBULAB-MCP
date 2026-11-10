import { Link } from 'react-router-dom';
import { Button, EmptyState, ErrorState, Panel, Skeleton } from '../../components';
import { useAsync, isOffline } from '../../api/hooks';
import { LEVEL_LABEL, sheetsApi, unavailableText } from '../../api/sheets';
import type { Level, PartSheet } from '../../api/sheets';
import { VERDICT_LABEL, REASON_LABEL } from '../../api/projectsTypes';
import { describeError } from '../../api/client';
import { fmtDate, fmtNum } from '../common';
import './model-tabs.css';

/* Onglets « Impressions » et « Profils » de la fiche modèle (G9 PJ5, G10 UX7). Lecture : GET /api/models/{id}/sheet.
   Honnêteté des chiffres : « imprimée » = 100 % atteint (verdict ou pas), les non jugées sont dites à part, un effectif accompagne chaque taux. */

const lvl = (l?: Level) => (l ? <span className="lvl" title={`Niveau de confiance : ${LEVEL_LABEL[l]}`}>{LEVEL_LABEL[l]}</span> : null);
const hms = (s?: number) => (s === undefined ? 'non disponible' : `${Math.floor(s / 3600)} h ${String(Math.round((s % 3600) / 60)).padStart(2, '0')} min`);
const money = (v?: number, cur = 'EUR') => (v === undefined ? 'non disponible' : `${fmtNum(v, 2)} ${cur === 'EUR' ? '€' : cur}`);

function useSheet(id: string, limit?: number) {
  return useAsync((s) => sheetsApi.part(id, { limit }, s), [id, limit]);
}

function SheetState({ st, children }: { st: ReturnType<typeof useSheet>; children: (s: PartSheet) => React.ReactNode }) {
  if (st.data) return <>{children(st.data)}</>;
  if (st.error) {
    return isOffline(st.error)
      ? <ErrorState title="Printalk est injoignable" text="Le service ne répond pas pour l'instant." impact="Les impressions de ce modèle ne peuvent pas être lues ; rien n'est perdu." action={<Button size="sm" onClick={st.reload}>Réessayer</Button>} />
      : <ErrorState title="Les impressions de ce modèle n'ont pas pu être lues" text="La lecture a échoué." impact="Le reste de la fiche reste utilisable ; rien n'a été modifié." action={<Button size="sm" onClick={st.reload}>Réessayer</Button>} details={describeError(st.error)} />;
  }
  return <Skeleton lines={4} label="Chargement des impressions de ce modèle" />;
}

/** Impressions : compteurs, coût cumulé, profils en bref, versions, liste des impressions avec verdict. */
export function ModelImpressionsTab({ id }: { id: string }) {
  const st = useSheet(id);
  return (
    <SheetState st={st}>
      {(s) => {
        const c = s.counts;
        if (c.never_printed && s.prints.length === 0) {
          return <EmptyState title="Ce modèle n'a pas encore été imprimé" text="Ses impressions, leurs verdicts et leur coût cumulé apparaîtront ici dès la première impression terminée." action={<Link className="btn btn-primary btn-sm" to={`/modele/${encodeURIComponent(id)}/preparer`}>Préparer une impression</Link>} />;
        }
        return (
          <div className="stack">
            <Panel title="Compteurs" actions={lvl(s.levels['counts'] ?? 'computed')}>
              <div className="stats mt-stats">
                <div className="stat"><div className="overline">Imprimée</div><div className="v">{c.printed}<small>fois</small></div><div className="d">100 % atteint, verdict ou pas</div></div>
                <div className="stat"><div className="overline">Jugées réussies</div><div className="v">{c.judged_success}<small>/ {c.judged_success + c.judged_failed + c.judged_redo}</small></div><div className="d">{c.unjudged > 0 ? `${c.unjudged} non jugée${c.unjudged > 1 ? 's' : ''}` : 'toutes jugées'}</div></div>
                <div className="stat"><div className="overline">Coût cumulé</div><div className="v">{s.totals.available ? money(s.totals.actual_cost, s.totals.currency) : 'non disponible'}</div><div className="d">{s.totals.available ? `${hms(s.totals.actual_seconds)} · ${fmtNum(s.totals.actual_grams, 0)} g` : (s.totals.reason ?? unavailableText(s, 'totals') ?? '')}</div></div>
                <div className="stat"><div className="overline">En cours</div><div className="v">{c.in_progress}</div><div className="d">{c.attempts} tentative{c.attempts > 1 ? 's' : ''} au total</div></div>
              </div>
              <p className="sub">{c.text}</p>
              {c.top_fail_reason && <p className="sub">Raison d'échec la plus fréquente : {REASON_LABEL[c.top_fail_reason] ?? c.top_fail_reason} (n = {c.top_fail_reason_n}).</p>}
              {s.totals.cost_per_judged_success !== undefined && <p className="sub">Coût par impression jugée réussie : {money(s.totals.cost_per_judged_success, s.totals.currency)} (calculé).</p>}
            </Panel>
            <Panel title="Impressions" flush>
              <ul className="jobs" aria-label="Impressions de ce modèle">
                {s.prints.map((p) => (
                  <li key={p.print_job_id} className="job job-log">
                    <div className="job-main"><Link to={`/impressions/${encodeURIComponent(p.print_job_id)}`}><b>{p.name ?? s.model.name}</b></Link><span className="job-note">{fmtDate(p.finished_at)} · {p.profile_label}</span></div>
                    <span className="job-meta mono">{p.actual_seconds !== undefined && <span>{hms(p.actual_seconds)}</span>}{p.actual_grams !== undefined && <span>{fmtNum(p.actual_grams, 0)} g</span>}{p.actual_cost !== undefined && <span>{money(p.actual_cost, p.currency)}</span>}</span>
                    {p.declared && <span className="pill"><i />déclarée</span>}
                    <span className={`pill ${p.verdict === 'success' ? 'ok' : p.verdict === 'failed' ? 'fail' : p.verdict ? 'wait' : ''}`}><i />{p.verdict ? VERDICT_LABEL[p.verdict] : p.reached_100 ? 'Non jugée' : 'Interrompue'}</span>
                  </li>
                ))}
              </ul>
              {s.prints_truncated && <p className="sub mt-foot">Liste limitée : {s.prints.length} impressions sur {s.prints_total}.</p>}
            </Panel>
            {s.versions.length > 0 && (
              <Panel title="Versions et résistance">
                <ul className="rowlist">{s.versions.map((v) => <li key={v.id}><div className="grow"><b>{v.label}{v.is_latest ? ' (dernière)' : ''}</b><small>{fmtDate(v.created_at)} · {v.strength_status === 'tested' && v.strength ? `résistance testée${v.strength.assumed ? ' (hypothèses supposées)' : ''}${v.strength.verdict ? ` : ${v.strength.verdict}` : ''}` : 'résistance jamais testée'}</small></div></li>)}</ul>
              </Panel>
            )}
            {s.projects.length > 0 && <Panel title="Projets"><p className="sub">Cette pièce sert dans : {s.projects.map((p) => p.name).join(', ')}.</p></Panel>}
          </div>
        );
      }}
    </SheetState>
  );
}

/** Profils : les réglages avec lesquels le modèle a été imprimé, avec leurs résultats (effectif toujours indiqué). */
export function ModelProfilesTab({ id }: { id: string }) {
  const st = useSheet(id);
  return (
    <SheetState st={st}>
      {(s) => (
        <div className="stack">
          {s.profiles.length === 0
            ? <EmptyState title="Aucun profil pour l'instant" text="Un profil regroupe les impressions faites avec les mêmes réglages. Il apparaît après la première impression de ce modèle." action={<Link className="btn btn-secondary btn-sm" to={`/modele/${encodeURIComponent(id)}/essais`}>Essais de réglages</Link>} />
            : (
              <ul className="mt-profiles" aria-label="Profils d'impression">
                {s.profiles.map((p) => (
                  <li key={p.signature} className="panel mt-profile">
                    <div className="mt-profile-h"><b>{p.label}</b>{lvl(p.level)}</div>
                    <p className="sub">{p.printed} imprimée{p.printed > 1 ? 's' : ''} sur {p.attempts} tentative{p.attempts > 1 ? 's' : ''} · {p.judged_success} jugée{p.judged_success > 1 ? 's' : ''} réussie{p.judged_success > 1 ? 's' : ''} sur {p.judged_success + p.judged_failed + p.judged_redo}{p.unjudged > 0 ? ` · ${p.unjudged} non jugée${p.unjudged > 1 ? 's' : ''}` : ''}</p>
                    <p className="sub">Médiane : {hms(p.median_seconds)} · {p.median_grams !== undefined ? `${fmtNum(p.median_grams, 0)} g` : 'masse non disponible'} · {money(p.median_cost, p.currency)}</p>
                    {p.last_success_at && <p className="sub">Dernière réussite : {fmtDate(p.last_success_at)}</p>}
                  </li>
                ))}
              </ul>
            )}
          <p className="sub">Les profils comparent des impressions réelles. Pour tester d'autres réglages avant d'imprimer : <Link to={`/modele/${encodeURIComponent(id)}/essais`}>essais de réglages</Link>.</p>
        </div>
      )}
    </SheetState>
  );
}
