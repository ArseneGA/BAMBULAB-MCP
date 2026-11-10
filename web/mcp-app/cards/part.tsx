import { useState } from 'react';
import { useToolCall, type CardProps } from './call';
import { fmtGrams, fmtMoney, fmtDims, isRec } from './format';
import { LiteBadge, LiteButton, LiteCard, LiteState, LiteStat, LiteStats, LiteTable } from './lite';
import { LevelBadge, PhotoStripView, VERDICT_TXT, durationText, frDate, naText, verdictTone } from './sheetparts';
import { VerdictForm } from './verdict';
import type { PartPrint, PartSheet, PartVersion, StrengthRef } from '../../src/api/sheets';

/**
 * Carte Fiche pièce (ui://bambu/part.html), liée à get_part_sheet : compteurs (« imprimée » = 100 % atteint, verdict ou pas),
 * onglets Versions / Profils / Impressions / Résistance, bandeau de paliers à 10 % par impression, coût et temps cumulés
 * (mesuré ; les impressions déclarées hors Printalk sont listées « déclarée » et exclues). Le viewer 3D existant s'ouvre
 * avec « Voir en 3D » (get_model_details). Gestes : le verdict (submit_print_verdict, réservé à la carte). Aucun bouton de
 * lancement, de réimpression ni d'approbation : cette carte ne démarre, n'arrête ni ne reprend aucune impression.
 */
export type PartData = PartSheet;

export function normalizePart(s: unknown): PartData | undefined {
  if (!isRec(s) || !isRec(s.model) || typeof s.model.id !== 'string' || !isRec(s.counts) || typeof s.counts.printed !== 'number') return undefined;
  if (!Array.isArray(s.versions) || !Array.isArray(s.profiles) || !Array.isArray(s.prints) || !isRec(s.totals)) return undefined;
  return s as unknown as PartData;
}

type Tab = 'versions' | 'profiles' | 'prints' | 'strength';
const TABS: Array<[Tab, string]> = [['versions', 'Versions'], ['profiles', 'Profils'], ['prints', 'Impressions'], ['strength', 'Résistance']];

function Strength({ t }: { t: StrengthRef }) {
  return (
    <span className="lc-row-meta">
      test du {frDate(t.created_at)} · {t.material || 'matière non précisée'} · verdict {t.verdict || 'non disponible'}
      {t.safety_factor ? ` · coefficient ${String(t.safety_factor).replace('.', ',')}` : ''}
      {t.assumed ? ' · hypothèses (assumed) : une valeur d\'entrée n\'est pas une donnée de l\'humain' : ''}
    </span>
  );
}

function VersionsTab({ versions }: { versions: PartVersion[] }) {
  if (versions.length === 0) return <LiteState kind="empty">Aucune version enregistrée pour cette pièce.</LiteState>;
  return (
    <ul className="lc-list" aria-label="Versions de la pièce">
      {versions.map((v) => (
        <li key={v.id} className="lc-row" style={{ marginLeft: v.depth * 14 }} aria-label={`Version ${v.label}`}>
          <span className="lc-row-main">
            <span className="lc-row-name">{v.label}{v.is_latest ? ' (courante)' : ''}</span>
            <span className="lc-row-meta">{v.parent_id ? `issue de ${v.parent_id} · ` : 'version d\'origine · '}{frDate(v.created_at)}{v.note ? ` · ${v.note}` : ''}</span>
          </span>
          <span className="lc-row-end">{v.strength ? <LiteBadge tone="run">Testée en résistance</LiteBadge> : <LiteBadge>Jamais testée</LiteBadge>}</span>
        </li>
      ))}
    </ul>
  );
}

function ProfilesTab({ d }: { d: PartData }) {
  if (d.profiles.length === 0) return <LiteState kind="empty">Aucun profil testé : la pièce n'a pas encore été imprimée.</LiteState>;
  return (
    <div style={{ display: 'grid', gap: 6 }}>
      <LiteTable label="Profils testés">
        <thead><tr><th>Profil</th><th className="lc-num">Impressions</th><th className="lc-num">Jugées réussies</th><th className="lc-num">Ratées / à refaire</th><th className="lc-num">Temps</th><th className="lc-num">Matière</th><th className="lc-num">Coût</th></tr></thead>
        <tbody>
          {d.profiles.map((p) => (
            <tr key={p.signature} className={d.last_success?.profile_signature === p.signature ? 'lc-hl' : undefined}>
              <td>{p.label}{d.last_success?.profile_signature === p.signature ? <> <LiteBadge tone="ok">dernier réussi</LiteBadge></> : null}</td>
              <td className="lc-num">{p.attempts} (dont {p.printed} à 100 %)</td>
              <td className="lc-num">{p.judged_success}</td>
              <td className="lc-num">{p.judged_failed + p.judged_redo}</td>
              <td className="lc-num">{p.median_seconds === undefined ? 'non disponible' : durationText(p.median_seconds)}</td>
              <td className="lc-num">{p.median_grams === undefined ? 'non disponible' : fmtGrams(p.median_grams)}</td>
              <td className="lc-num">{p.median_cost === undefined ? 'non disponible' : fmtMoney(p.median_cost, p.currency || 'EUR')}</td>
            </tr>
          ))}
        </tbody>
      </LiteTable>
      <p className="lc-note">Un profil = les réglages effectifs relevés à la découpe ; temps, matière et coût sont des médianes calculées sur les impressions mesurées (<LevelBadge level="computed" />). Les non jugées n'entrent pas dans les réussites.</p>
    </div>
  );
}

function PrintRow({ p, app, run, dis, refresh }: { p: PartPrint; app: CardProps<unknown>['app']; run: (k: string, n: string, a: Record<string, unknown>) => Promise<unknown>; dis: boolean; refresh: () => void }) {
  const [judge, setJudge] = useState(false);
  const title = p.name || p.print_job_id;
  return (
    <li className="lc-row" style={{ display: 'grid', gap: 8 }} aria-label={`Impression ${title}`}>
      <span className="lc-row-main">
        <span className="lc-row-name">{frDate(p.finished_at)} · {p.profile_label}</span>
        <span className="lc-row-meta">
          {p.reached_100 ? 'imprimée (100 % atteint)' : `arrêtée avant 100 % (${p.state})`} · {p.actual_seconds === undefined ? 'durée non disponible' : durationText(p.actual_seconds)}
          {p.actual_grams !== undefined ? ` · ${fmtGrams(p.actual_grams)}` : ''}
          {p.declared ? ' · coût hors mesure (déclarée)' : p.actual_cost !== undefined ? ` · ${fmtMoney(p.actual_cost, p.currency || 'EUR')}` : ' · coût non disponible'}
        </span>
      </span>
      <span style={{ display: 'flex', gap: 6, flexWrap: 'wrap', alignItems: 'center' }}>
        <LiteBadge tone={verdictTone(p.verdict)}>{p.verdict ? `Jugée ${VERDICT_TXT[p.verdict] ?? p.verdict}` : 'Non jugée'}</LiteBadge>
        {p.fail_reason && <LiteBadge tone="warn">{p.fail_reason}</LiteBadge>}
        {p.declared && <LiteBadge tone="warn">Déclarée</LiteBadge>}
        <LevelBadge level={p.level} />
        <LiteButton disabled={dis} onClick={() => { void run(`p-${p.print_job_id}`, 'get_print_sheet', { print_job_id: p.print_job_id }); }}>Ouvrir la fiche d'impression</LiteButton>
        {!p.verdict && <LiteButton disabled={dis} pressed={judge} onClick={() => setJudge((j) => !j)}>Juger cette impression</LiteButton>}
      </span>
      {judge && !p.verdict && <VerdictForm app={app} jobId={p.print_job_id} compact onDone={() => { setJudge(false); refresh(); }} />}
      <details>
        <summary>Photos du journal ({p.photos.available} palier(s) avec photo)</summary>
        <PhotoStripView app={app} strip={p.photos} label={`Paliers de photos de l'impression ${title}`} />
      </details>
    </li>
  );
}

function StrengthTab({ d }: { d: PartData }) {
  const un = d.strength_unassigned ?? [];
  return (
    <div style={{ display: 'grid', gap: 8 }}>
      {d.versions.length === 0 && un.length === 0 && <LiteState kind="empty">Aucun test de résistance : plan_strength_test puis assess_strength.</LiteState>}
      <ul className="lc-list" aria-label="Tests de résistance par version">
        {d.versions.map((v) => (
          <li key={v.id} className="lc-row" aria-label={`Résistance de la version ${v.label}`}>
            <span className="lc-row-main">
              <span className="lc-row-name">{v.label}</span>
              {v.strength ? <Strength t={v.strength} /> : <span className="lc-row-meta">jamais testée en résistance</span>}
            </span>
            <span className="lc-row-end">{v.strength ? <LiteBadge tone={v.strength.verdict === 'ok' ? 'ok' : 'warn'}>{v.strength.verdict || 'sans verdict'}</LiteBadge> : <LiteBadge>Jamais testée</LiteBadge>}</span>
          </li>
        ))}
      </ul>
      {un.length > 0 && (
        <>
          <h2 className="lc-h2">Version non déterminée</h2>
          <ul className="lc-list" aria-label="Tests sans version">{un.map((t) => <li key={t.test_id} className="lc-row"><Strength t={t} /></li>)}</ul>
        </>
      )}
      <p className="lc-note">Chaque test est rattaché à sa version ; « assumed » signale une valeur d'entrée qui est une hypothèse.</p>
    </div>
  );
}

export function PartCard({ app, data: d, onOpen }: CardProps<PartData>) {
  const { busy, error, run } = useToolCall(app, onOpen);
  const [tab, setTab] = useState<Tab>('prints');
  const dis = busy !== null;
  const c = d.counts;
  const t = d.totals;
  const refresh = () => { void run('refresh', 'get_part_sheet', { model_id: d.model.id }); };
  const sub = [d.model.tags?.join(', '), fmtDims(d.model.dimensions_mm), d.model.trashed ? 'dans la corbeille' : ''].filter(Boolean).join(' · ');
  return (
    <LiteCard title={d.model.name} sub={sub || undefined} label={`Fiche de la pièce ${d.model.name}`}
      aside={c.never_printed ? <LiteBadge>Jamais imprimée</LiteBadge> : <LiteBadge tone="ok">Imprimée {c.printed} fois</LiteBadge>}>
      <p role="status" style={{ margin: 0 }}>{c.text}</p>
      <LiteStats>
        <LiteStat label="Imprimée (100 %)" value={c.printed} hint="verdict ou pas" />
        <LiteStat label="Jugées réussies" value={c.judged_success} hint={`${c.judged_failed + c.judged_redo} ratée(s) ou à refaire · ${c.unjudged} non jugée(s) (déclaré)`} />
        <LiteStat label="Coût réel" value={t.available ? fmtMoney(t.actual_cost, t.currency || 'EUR') ?? 'non disponible' : 'non disponible'}
          hint={t.available ? (t.cost_per_judged_success !== undefined ? `calculé · ${fmtMoney(t.cost_per_judged_success, t.currency || 'EUR')} par réussite` : 'calculé') : naText(t.reason)} />
        <LiteStat label="Temps machine" value={t.available ? durationText(t.actual_seconds) : 'non disponible'} hint={t.available ? 'mesuré' : naText(t.reason)} />
      </LiteStats>
      {t.declared_prints > 0 && <p className="lc-note">{t.declared_prints} impression(s) déclarée(s) hors Printalk : listées « déclarée », exclues du coût et du temps mesurés.</p>}
      {c.in_progress > 0 && <p className="lc-note">{c.in_progress} tentative(s) en cours, coût estimé à part : {fmtMoney(c.planned_cost ?? 0, c.currency || 'EUR')}.</p>}
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <LiteButton disabled={dis} busy={busy === 'view'} onClick={() => { void run('view', 'get_model_details', { model_id: d.model.id }); }}>Voir en 3D</LiteButton>
        <LiteButton disabled={dis} busy={busy === 'refresh'} onClick={refresh}>Actualiser</LiteButton>
      </div>
      {d.projects.length > 0 && (
        <p className="lc-note">Projets qui citent la pièce : {d.projects.map((p, i) => (
          <span key={p.id}>{i > 0 ? ', ' : ''}<button type="button" disabled={dis} onClick={() => { void run(`prj-${p.id}`, 'get_project', { id: p.id }); }} style={{ all: 'unset', cursor: 'pointer', textDecoration: 'underline' }} aria-label={`Ouvrir le projet ${p.name}`}>{p.name}</button></span>
        ))}.</p>
      )}

      <div role="tablist" aria-label="Sections de la fiche" style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
        {TABS.map(([k, label]) => (
          <button key={k} type="button" role="tab" id={`part-tab-${k}`} aria-selected={tab === k} aria-controls={`part-panel-${k}`} className={`lc-tab${tab === k ? ' is-on' : ''}`}
            onClick={() => setTab(k)} style={{ padding: '6px 10px', borderRadius: 8, border: '1px solid var(--c-line)', background: tab === k ? 'var(--c-run-bg)' : 'transparent', color: 'inherit', font: 'inherit', cursor: 'pointer' }}>
            {label}{k === 'prints' ? ` (${d.prints_total})` : k === 'versions' ? ` (${d.versions.length})` : k === 'profiles' ? ` (${d.profiles.length})` : ''}
          </button>
        ))}
      </div>
      <div role="tabpanel" id={`part-panel-${tab}`} aria-labelledby={`part-tab-${tab}`} style={{ display: 'grid', gap: 8 }}>
        {tab === 'versions' && <VersionsTab versions={d.versions} />}
        {tab === 'profiles' && <ProfilesTab d={d} />}
        {tab === 'strength' && <StrengthTab d={d} />}
        {tab === 'prints' && (d.prints.length === 0
          ? <LiteState kind="empty">{c.never_printed ? 'Jamais imprimée : aucune impression au journal.' : 'Aucune impression listée (section non demandée ou liste vide).'}</LiteState>
          : (
            <ul className="lc-list" aria-label="Impressions de la pièce">
              {d.prints.map((p) => <PrintRow key={p.print_job_id} p={p} app={app} run={run} dis={dis} refresh={refresh} />)}
            </ul>
          ))}
        {tab === 'prints' && d.prints_truncated && <p className="lc-note">Liste bornée : {d.prints.length} sur {d.prints_total} impressions ; les compteurs portent sur toutes.</p>}
      </div>
      {d.last_success && <p className="lc-note">Dernier profil réussi : {d.last_success.profile_label} (impression du {frDate(d.last_success.finished_at)}).</p>}
      {error && <LiteState kind="error">{error}</LiteState>}
      <p className="lc-note">Les verdicts sont déclarés par l'humain. Cette fiche ne lance aucune impression.</p>
    </LiteCard>
  );
}
