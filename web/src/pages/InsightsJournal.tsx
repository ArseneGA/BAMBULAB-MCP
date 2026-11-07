import { useEffect, useRef, useState } from 'react';
import { Camera } from 'lucide-react';
import { Button, EmptyState, Field, Input, List, Panel, Select, Tag, useToast } from '../components';
import { describeError } from '../api/client';
import { useAsync } from '../api/hooks';
import { insightsApi } from '../api/insights';
import type { Adjustments, LogEntry, LogList } from '../api/insights';
import { Async, Notice, fmtDate, fmtNum } from './common';

const OUTCOME: Record<string, { label: string; tone?: 'ok' | 'warn' | 'err' }> = {
  success: { label: 'réussie', tone: 'ok' }, partial: { label: 'partielle', tone: 'warn' }, failed: { label: 'ratée', tone: 'err' }, cancelled: { label: 'annulée' },
};

function Delta({ v }: { v?: number }) {
  return v === undefined ? <span className="unknown">n/a</span> : <span className="mono">{v > 0 ? '+' : ''}{fmtNum(v, 1)} %</span>;
}

function Adjust({ logId }: { logId: string }) {
  const [res, setRes] = useState<Adjustments>();
  const [err, setErr] = useState<string>();
  const [busy, setBusy] = useState(false);
  useEffect(() => { setRes(undefined); setErr(undefined); }, [logId]);
  const go = async () => {
    setBusy(true); setErr(undefined);
    try { setRes(await insightsApi.adjustments(logId)); } catch (e) { setErr(describeError(e)); } finally { setBusy(false); }
  };
  return (
    <Panel title="Ajustements proposés pour la prochaine impression comparable" actions={<Button size="sm" onClick={go} disabled={busy}>{busy ? 'Calcul…' : 'Proposer'}</Button>}>
      {err && <Notice tone="err" title="Impossible">{err}</Notice>}
      {!res && !err && <p className="hint" style={{ margin: 0 }}>D'après les défauts cochés sur cette fiche et ceux qui reviennent dans les fiches de même matière et de même machine. Rien n'est appliqué automatiquement.</p>}
      {res && (
        <div className="stack">
          <p className="hint" style={{ margin: 0 }}>{res.comparable_logs} fiche(s) comparable(s) · niveau {res.level} · {res.how_to_apply}</p>
          {res.proposals.length === 0 && <p style={{ margin: 0 }}>Aucun défaut à corriger d'après le journal.</p>}
          {res.proposals.map((p) => (
            <div key={p.defect} className="ins-prop">
              <h4>{p.label} <Tag>{p.basis}</Tag></h4>
              <p className="hint" style={{ margin: 0 }}>{p.summary}</p>
              <ul>{p.adjustments.map((a, i) => <li key={i}><span className="mono">{a.key}</span> ({a.target}) : {a.direction === 'increase' ? 'augmenter' : a.direction === 'decrease' ? 'diminuer' : 'régler'} de {a.amount}{a.unit ? ` ${a.unit}` : ''}. <span className="hint">{a.why}</span></li>)}</ul>
              {p.other_actions && p.other_actions.length > 0 && <p className="hint" style={{ margin: 0 }}>Aussi : {p.other_actions.join(' ')}</p>}
              <p className="hint" style={{ margin: 0 }}>Compromis : {p.tradeoffs}</p>
            </div>
          ))}
          {res.best_reference && <Notice tone="info" title={`Meilleure fiche comparable (${res.best_reference.rating}/5)`}>{res.best_reference.note} Fiche {res.best_reference.log_id}{res.best_reference.process ? ` · ${res.best_reference.process}` : ''}.</Notice>}
        </div>
      )}
    </Panel>
  );
}

function Sheet({ e, defects, onChanged }: { e: LogEntry; defects: Array<{ id: string; label: string }>; onChanged: (e: LogEntry) => void }) {
  const toast = useToast();
  const [rating, setRating] = useState(e.rating ?? 0);
  const [outcome, setOutcome] = useState<string>(e.outcome);
  const [comment, setComment] = useState(e.comment ?? '');
  const [picked, setPicked] = useState<string[]>(e.defects ?? []);
  const [grams, setGrams] = useState('');
  const [mins, setMins] = useState('');
  const [busy, setBusy] = useState(false);
  const file = useRef<HTMLInputElement>(null);
  useEffect(() => { setRating(e.rating ?? 0); setOutcome(e.outcome); setComment(e.comment ?? ''); setPicked(e.defects ?? []); setGrams(''); setMins(''); }, [e.id]); // eslint-disable-line react-hooks/exhaustive-deps
  const toggle = (id: string) => setPicked((l) => (l.includes(id) ? l.filter((x) => x !== id) : [...l, id]));
  const save = async () => {
    setBusy(true);
    try {
      const num = (s: string) => (s.trim() === '' ? undefined : Number(s.replace(',', '.')));
      const g = num(grams), m = num(mins);
      if ((g !== undefined && !Number.isFinite(g)) || (m !== undefined && !Number.isFinite(m))) throw new Error('Masse et durée : nombres attendus.');
      const out = await insightsApi.updateLog(e.id, { outcome, rating: rating || undefined, comment, defects: picked, actual_mass_g: g, actual_duration_minutes: m });
      onChanged(out); toast('ok', 'Fiche enregistrée');
    } catch (x) { toast('err', describeError(x)); } finally { setBusy(false); }
  };
  const upload = async (f?: File) => {
    if (!f) return;
    setBusy(true);
    try { const r = await insightsApi.addPhoto(e.id, f); onChanged(r.entry); toast('ok', 'Photo ajoutée'); } catch (x) { toast('err', describeError(x)); } finally { setBusy(false); if (file.current) file.current.value = ''; }
  };
  const oc = OUTCOME[e.outcome];
  const cost = e.actual_cost;
  return (
    <div className="stack">
      <Panel title={e.name || e.print_job_id} actions={<Tag tone={oc?.tone}>{oc?.label ?? e.outcome}</Tag>}>
        <dl className="kvl">
          <dt>Terminée</dt><dd className="mono">{fmtDate(e.finished_at)}</dd>
          <dt>Imprimante</dt><dd>{e.printer_model ?? <span className="unknown">inconnue</span>}</dd>
          <dt>Modèle / projet</dt><dd>{e.model_name ?? '—'}{e.collection_name ? ` · ${e.collection_name}` : ''}</dd>
          <dt>Matière</dt><dd>{e.material_family ?? <span className="unknown">inconnue</span>}{e.settings.filaments?.length ? <span className="mono"> {e.settings.filaments.join(', ')}</span> : null}</dd>
          <dt>Réglages effectifs</dt><dd className="mono">{[e.settings.process, e.settings.nozzle_mm ? `buse ${e.settings.nozzle_mm} mm` : '', e.settings.layers ? `${e.settings.layers} couches` : '', e.settings.supports ? 'supports' : '', e.settings.orientation].filter(Boolean).join(' · ') || '—'}
            {e.settings.overrides?.map((o) => <div key={o.key}>{o.key} = {String(o.value)}</div>)}
            {e.settings.deviations?.map((o) => <div key={o.key}>écart : {o.key} {o.planned !== undefined ? `${String(o.planned)} → ` : ''}{String(o.actual)}</div>)}</dd>
          <dt>Durée</dt><dd className="mono">estimée {e.estimated.seconds ? `${fmtNum(e.estimated.seconds / 60, 0)} min` : '—'} · réelle {e.actual.seconds ? `${fmtNum(e.actual.seconds / 60, 0)} min` : '—'} (<Delta v={e.duration_delta_pct} />)</dd>
          <dt>Masse</dt><dd className="mono">estimée {e.estimated.grams ? `${fmtNum(e.estimated.grams, 1)} g` : '—'} · réelle {e.actual.grams ? `${fmtNum(e.actual.grams, 1)} g` : '—'} (<Delta v={e.mass_delta_pct} />) <span className="hint">{e.actual.basis}</span></dd>
          <dt>Coût</dt><dd className="mono">{e.planned_cost ? `prévu ${fmtNum(e.planned_cost.total, 2)} ${e.planned_cost.currency}` : '—'}{cost ? ` · réel estimé ${fmtNum(cost.total, 2)} ${cost.currency}` : ''} <span className="hint">estimations</span></dd>
        </dl>
      </Panel>
      <Panel title="Mon retour" actions={<Button variant="primary" size="sm" onClick={save} disabled={busy}>Enregistrer</Button>}>
        <div className="stack">
          <div className="row-wrap" role="radiogroup" aria-label="Note de 1 à 5">
            <span className="hint">Note</span>
            {[1, 2, 3, 4, 5].map((n) => <button key={n} type="button" role="radio" aria-checked={rating === n} className={`ins-star${rating >= n ? ' on' : ''}`} onClick={() => setRating(rating === n ? 0 : n)} aria-label={`${n} sur 5`}>★</button>)}
            <Select aria-label="Issue" value={outcome} onChange={(ev) => setOutcome(ev.target.value)} options={[{ value: 'success', label: 'Réussie' }, { value: 'partial', label: 'Partielle' }, { value: 'failed', label: 'Ratée' }, { value: 'cancelled', label: 'Annulée' }]} />
          </div>
          <fieldset className="ins-defects"><legend>Défauts constatés</legend>
            {defects.map((d) => <label key={d.id}><input type="checkbox" checked={picked.includes(d.id)} onChange={() => toggle(d.id)} /> {d.label}</label>)}
          </fieldset>
          <Field label="Commentaire"><textarea className="textarea" value={comment} onChange={(ev) => setComment(ev.target.value)} maxLength={4000} /></Field>
          <div className="row-wrap">
            <Field label="Masse réellement consommée (g)"><Input inputMode="decimal" value={grams} onChange={(ev) => setGrams(ev.target.value)} placeholder="pesée" /></Field>
            <Field label="Durée réelle (min)"><Input inputMode="decimal" value={mins} onChange={(ev) => setMins(ev.target.value)} placeholder="optionnel" /></Field>
          </div>
        </div>
      </Panel>
      <Panel title="Photos" actions={<><input ref={file} type="file" accept="image/jpeg,image/png,image/webp" hidden onChange={(ev) => upload(ev.target.files?.[0])} /><Button size="sm" icon={<Camera size={13} aria-hidden />} onClick={() => file.current?.click()} disabled={busy}>Ajouter une photo</Button></>}>
        {(e.photos?.length ?? 0) + (e.camera_photos?.length ?? 0) === 0 ? <p className="hint" style={{ margin: 0 }}>Aucune photo. JPEG, PNG ou WebP, 12 Mio au plus.</p> : (
          <div className="ins-photos">{[...(e.photos ?? []), ...(e.camera_photos ?? []).map((c) => c.sha256)].map((p) => <a key={p} href={insightsApi.photoUrl(e.id, p)} target="_blank" rel="noreferrer"><img src={insightsApi.photoUrl(e.id, p)} alt={`Photo de ${e.name ?? e.print_job_id}`} loading="lazy" /></a>)}</div>
        )}
      </Panel>
      <Adjust logId={e.id} />
    </div>
  );
}

function Body({ data, reload }: { data: LogList; reload: () => void }) {
  const [sel, setSel] = useState('');
  const [local, setLocal] = useState<Record<string, LogEntry>>({});
  const entries = data.entries.map((e) => local[e.id] ?? e);
  const cur = entries.find((e) => e.id === sel) ?? entries[0];
  return (
    <div className="ins-layout">
      <Panel title={`Impressions (${data.total})`} flush>
        <List label="Impressions terminées" items={entries} getKey={(e) => e.id} selectedKey={cur?.id} onSelect={(e) => setSel(e.id)}
          render={(e) => <span className="ins-item"><strong>{e.name || e.print_job_id}</strong><span><Tag tone={OUTCOME[e.outcome]?.tone}>{OUTCOME[e.outcome]?.label ?? e.outcome}</Tag> {e.rating ? '★'.repeat(e.rating) : ''} <span className="mono dim">{fmtDate(e.finished_at)}</span></span></span>} />
      </Panel>
      {cur && <Sheet key={cur.id} e={cur} defects={data.defect_catalog} onChanged={(n) => { setLocal((l) => ({ ...l, [n.id]: n })); reload(); }} />}
    </div>
  );
}

export function InsightsJournal() {
  const state = useAsync((s) => insightsApi.log({ limit: 100 }, s), []);
  return (
    <Async state={state} label="Lecture du journal" empty={(d) => d.total === 0 ? (
      <EmptyState title="Aucune impression terminée" text="Une fiche est créée pour chaque impression terminée, échouée ou annulée. Lancez une impression depuis l'espace Impression : sa fiche apparaîtra ici." />
    ) : null}>
      {(d) => <Body data={d} reload={state.reload} />}
    </Async>
  );
}
