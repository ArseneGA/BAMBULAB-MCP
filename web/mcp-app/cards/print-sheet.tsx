import { useState } from 'react';
import { useToolCall, type CardProps } from './call';
import { fmtGrams, fmtMoney, isRec } from './format';
import { LiteBadge, LiteButton, LiteCard, LiteState } from './lite';
import { LevelBadge, PhotoStripView, VERDICT_TXT, durationText, frDate, naText, verdictTone } from './sheetparts';
import { REASON_FR, VerdictForm, type VerdictResult } from './verdict';
import type { PrintSheet } from '../../src/api/sheets';

/**
 * Carte Fiche d'impression (ui://bambu/print-sheet.html), liée à get_print_sheet : en-tête, bandeau de verdict (le formulaire
 * de la carte Verdict : submit_print_verdict), réglages FIGÉS à la découpe, matière, temps prévu contre réel, coût, planche de
 * paliers à 10 %, alertes de la caméra (« caméra non disponible » distinct de « aucune alerte »), accord humain lu dans l'audit.
 * Le niveau de chaque bloc est écrit (mesuré, calculé, déclaré). Aucun bouton de lancement, de réimpression ni d'approbation.
 */
export type PrintSheetData = PrintSheet;

export function normalizePrintSheet(s: unknown): PrintSheetData | undefined {
  if (!isRec(s) || typeof s.print_job_id !== 'string' || typeof s.log_id !== 'string') return undefined;
  if (!isRec(s.header) || !isRec(s.verdict) || !isRec(s.profile) || !isRec(s.camera) || !isRec(s.approval) || !isRec(s.duration)) return undefined;
  return s as unknown as PrintSheetData;
}

function Block({ title, level, children }: { title: string; level?: string; children: React.ReactNode }) {
  return (
    <section aria-label={title} style={{ display: 'grid', gap: 6 }}>
      <h2 className="lc-h2">{title} {level && <LevelBadge level={level} />}</h2>
      {children}
    </section>
  );
}

const yesNo = (v: boolean | undefined) => (v ? 'oui' : 'non');

export function PrintSheetCard({ app, data: d, onOpen }: CardProps<PrintSheetData>) {
  const { error, run } = useToolCall(app, onOpen);
  const [editing, setEditing] = useState(false);
  const h = d.header;
  const v = d.verdict;
  const na = (f: string) => naText(d.unavailable?.[f]);
  const title = h.model_name || h.name || d.print_job_id;
  const s = d.profile.settings;
  const refresh = (_: VerdictResult) => { setEditing(false); void run('refresh', 'get_print_sheet', { print_job_id: d.print_job_id }); };
  return (
    <LiteCard title={title} sub={[h.name && h.name !== title ? h.name : '', h.printer_model, `fin le ${frDate(h.finished_at)}`].filter(Boolean).join(' · ')} label={`Fiche de l'impression ${title}`}
      aside={<>
        <LiteBadge tone={h.reached_100 ? 'ok' : 'warn'}>{h.reached_100 ? 'Imprimée (100 %)' : `Arrêtée avant 100 % (${h.state})`}</LiteBadge>
        {h.declared && <LiteBadge tone="warn">Déclarée hors Printalk</LiteBadge>}
      </>}>
      <Block title="Verdict" level="declared">
        <p role="status" style={{ margin: 0 }}>
          <LiteBadge tone={verdictTone(v.value)}>{v.value ? `Jugée ${VERDICT_TXT[v.value] ?? v.value}` : 'Non jugée'}</LiteBadge>{' '}
          {v.fail_reason ? `${REASON_FR[v.fail_reason] ?? v.fail_reason} · ` : ''}{v.at ? `le ${frDate(v.at)}` : ''}
          {v.note ? <> · « {v.note} »</> : null}
        </p>
        {(!v.value || editing)
          ? <VerdictForm app={app} jobId={d.print_job_id} reasons={v.fail_reasons} current={v.value} onDone={refresh} />
          : <div><LiteButton onClick={() => setEditing(true)}>Modifier le verdict</LiteButton></div>}
        <p className="lc-note">Le verdict est un jugement humain (déclaré) ; il n'a aucun effet sur l'imprimante, la file ni la confirmation.</p>
      </Block>

      <Block title="Réglages figés à la découpe" level={d.profile.level}>
        <p style={{ margin: 0 }}>{d.profile.label}</p>
        <ul className="lc-list" aria-label="Réglages effectifs">
          {s.machine && <li className="lc-row-meta">Machine : {s.machine}</li>}
          {s.process && <li className="lc-row-meta">Procédé : {s.process}</li>}
          {(s.filaments ?? []).length > 0 && <li className="lc-row-meta">Filaments : {(s.filaments ?? []).join(', ')}</li>}
          {s.nozzle_mm ? <li className="lc-row-meta">Buse : {String(s.nozzle_mm).replace('.', ',')} mm</li> : null}
          {s.layers ? <li className="lc-row-meta">Couches : {s.layers} · supports : {yesNo(s.supports)}</li> : null}
          {s.orientation && <li className="lc-row-meta">Orientation : {s.orientation}</li>}
          {(s.overrides ?? []).map((o) => <li key={`${o.target ?? ''}${o.key}`} className="lc-row-meta">Réglage modifié : {o.key} = {String(o.value)}</li>)}
        </ul>
        {d.unavailable?.profile && <p className="lc-note">{na('profile')}</p>}
        <p className="lc-note">{d.profile.frozen_note}</p>
      </Block>

      <Block title="Matière" level={d.material.level}>
        <p style={{ margin: 0 }}>
          {d.material.actual_grams === undefined ? `Masse : ${na('actual_grams')}` : `${fmtGrams(d.material.actual_grams)}${d.material.estimated ? ' (estimée, non pesée)' : ' (pesée)'}`}
          {d.material.estimated_grams !== undefined ? ` · annoncée par le slicer : ${fmtGrams(d.material.estimated_grams)}` : ''}{d.material.family ? ` · ${d.material.family}` : ''}
        </p>
        {d.material.spools.length === 0
          ? <p className="lc-note">Aucune bobine décomptée pour cette impression.</p>
          : (
            <ul className="lc-list" aria-label="Bobines consommées">
              {d.material.spools.map((u) => (
                <li key={`${u.spool_id}-${u.filament_slot}`} className="lc-row-meta">
                  Emplacement {u.filament_slot} · {u.label || u.spool_id} : {fmtGrams(u.grams)}{u.estimated ? ' (estimé)' : ''}{u.cost !== undefined ? ` · ${fmtMoney(u.cost, d.cost.currency || 'EUR')}` : ''} <LevelBadge level={u.level} />
                </li>
              ))}
            </ul>
          )}
      </Block>

      <Block title="Durée" level={d.duration.level}>
        <p style={{ margin: 0 }}>
          Prévue : {d.duration.planned_seconds === undefined ? na('planned_seconds') : durationText(d.duration.planned_seconds)} · réelle : {d.duration.actual_seconds === undefined ? na('actual_seconds') : durationText(d.duration.actual_seconds)}
        </p>
        <p className="lc-note">Écart : {d.duration.delta_pct === undefined ? na('duration_delta') : d.duration.text} <LevelBadge level="computed" /></p>
      </Block>

      <Block title="Coût" level={d.cost.level}>
        <p style={{ margin: 0 }}>
          Prévu : {d.cost.planned === undefined ? 'non disponible' : fmtMoney(d.cost.planned, d.cost.currency || 'EUR')} · réel : {d.cost.actual === undefined ? na('actual_cost') : fmtMoney(d.cost.actual, d.cost.currency || 'EUR')}
        </p>
      </Block>

      <Block title="Photos du journal (tous les 10 %)" level="measured">
        <PhotoStripView app={app} strip={d.photos} label="Paliers de photos de l'impression" />
      </Block>

      <Block title="Caméra" level={d.camera.level}>
        <p style={{ margin: 0 }} role="status">
          <LiteBadge tone={d.camera.alerts_status === 'listed' ? 'warn' : d.camera.alerts_status === 'none' ? 'ok' : 'off'}>
            {d.camera.alerts_status === 'listed' ? 'Alertes' : d.camera.alerts_status === 'none' ? 'Aucune alerte' : 'Caméra non disponible'}
          </LiteBadge>{' '}{d.camera.text}
        </p>
        {d.camera.alerts.length > 0 && (
          <ul className="lc-list" aria-label="Alertes de la caméra">
            {d.camera.alerts.map((a) => <li key={a.at} className="lc-row-meta">{frDate(a.at)} · niveau {a.level} · score {String(a.score).replace('.', ',')}</li>)}
          </ul>
        )}
      </Block>

      <Block title="Accord humain de lancement" level={d.approval.level}>
        <p style={{ margin: 0 }}>{d.approval.text}</p>
      </Block>

      {(d.comment || (d.defects ?? []).length > 0 || d.rating) && (
        <Block title="Note">
          <p style={{ margin: 0 }}>{d.comment}{(d.defects ?? []).length > 0 ? ` · défauts : ${(d.defects ?? []).join(', ')}` : ''}{d.rating ? ` · ${d.rating}/5` : ''}</p>
        </Block>
      )}
      {error && <LiteState kind="error">{error}</LiteState>}
      <p className="lc-note">Cette fiche ne lance, n'arrête ni ne reprend aucune impression.</p>
    </LiteCard>
  );
}
