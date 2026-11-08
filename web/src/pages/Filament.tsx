import { useEffect, useRef, useState } from 'react';
import { ArrowDown, ArrowUp, ExternalLink, Plus, RefreshCw, Trash2 } from 'lucide-react';
import { Button, EmptyState, Field, Input, Panel, ProgressBar, Select, Tabs, Tag, useToast } from '../components';
import { api, describeError } from '../api/client';
import { useAsync } from '../api/hooks';
import { filamentApi } from '../api/filamentApi';
import type { AMSTrayView, ColorPlan, ColorWish, QueueEntry, QueueState, Spool, SpoolInput } from '../api/filamentApi';
import type { JobProgress, PrintJob } from '../api/printTypes';
import { printSource } from '../api/progress';
import { Async, Dialog, Notice, fmtDate, fmtNum } from './common';
import './filament.css';

type Tab = 'bobines' | 'multicolore' | 'file';

const SOURCE: Record<string, string> = { human: 'saisi', ams: 'lu sur l\'AMS', consumption: 'décompté', default: 'bobine neuve présumée' };
const STATUS: Record<string, { label: string; tone?: 'ok' | 'warn' | 'err' }> = { ok: { label: 'suffisante', tone: 'ok' }, low: { label: 'presque vide', tone: 'warn' }, empty: { label: 'vide', tone: 'err' }, archived: { label: 'archivée' } };
export const QSTATE: Record<QueueState, { label: string; tone?: 'ok' | 'warn' | 'err' }> = {
  slicing: { label: 'découpe', tone: 'warn' }, needs_assignments: { label: 'affectation à choisir', tone: 'warn' }, waiting: { label: 'en attente' }, uploading: { label: 'envoi en cours', tone: 'warn' },
  ready: { label: 'prête : attend votre accord', tone: 'ok' }, printing: { label: 'en cours', tone: 'ok' }, done: { label: 'terminée', tone: 'ok' }, failed: { label: 'échec', tone: 'err' }, removed: { label: 'retirée' },
};

function Swatch({ color }: { color?: string }) {
  return <span className="fil-swatch" style={color ? { background: color } : undefined} aria-label={color ?? 'couleur inconnue'} title={color ?? 'couleur inconnue'} />;
}

export function FilamentPage({ initial = 'bobines' }: { initial?: Tab }) {
  const [tab, setTab] = useState<Tab>(initial);
  const [pid, setPid] = useState('');
  const printers = useAsync((s) => api.printers(s), []);
  const plist = printers.data?.printers ?? [];
  const printer = plist.find((p) => p.id === pid) ?? plist[0];
  return (
    <>
      <div className="page-head">
        <span className="eyebrow">Espace</span><h1>Bobines, couleurs et file d'attente</h1>
        <p>Inventaire des bobines et état de l'AMS, affectation des couleurs aux emplacements, file d'impressions. Aucune impression ne démarre ici : chaque lancement, y compris le suivant d'une file, se confirme sur la page de confirmation du serveur.</p>
      </div>
      <div className="fil-bar">
        <Tabs label="Sections" value={tab} onChange={setTab} tabs={[{ value: 'bobines', label: 'Bobines et AMS' }, { value: 'multicolore', label: 'Multicolore' }, { value: 'file', label: 'File d\'attente' }]} />
        {plist.length > 1 && <Select aria-label="Imprimante" value={printer?.id ?? ''} onChange={(e) => setPid(e.target.value)} options={plist.map((p) => ({ value: p.id, label: p.name }))} />}
      </div>
      {tab === 'bobines' && <SpoolsTab printerId={printer?.id} />}
      {tab === 'multicolore' && <ColorsTab printerId={printer?.id} />}
      {tab === 'file' && <QueueTab />}
    </>
  );
}

/* ---------------------------------------------------------------- Bobines et AMS */

function SpoolsTab({ printerId }: { printerId?: string }) {
  const spools = useAsync((s) => filamentApi.spools({}, s), []);
  const ams = useAsync((s) => (printerId ? filamentApi.ams(printerId, s) : Promise.resolve(undefined)), [printerId]);
  const [edit, setEdit] = useState<Spool | 'new' | undefined>();
  const toast = useToast();
  const reloadAll = () => { spools.reload(); ams.reload(); };
  return (
    <div className="fil-grid">
      <Panel title="AMS" actions={<Button size="sm" icon={<RefreshCw size={13} />} onClick={ams.reload}>Relire</Button>}>
        {!printerId ? <EmptyState title="Aucune imprimante" text="Ajoutez une imprimante dans l'écran Imprimantes." /> : (
          <Async state={ams} label="Lecture de l'AMS">
            {(v) => !v ? null : (
              <>
                {v.error && <Notice tone="err" title="Imprimante injoignable">{v.error}</Notice>}
                {v.notes?.map((n) => <p key={n} className="hint">{n}</p>)}
                <div className="fil-trays">
                  {v.trays.map((t) => <TrayCard key={t.slot} t={t} onAssign={(sp) => { filamentApi.updateSpool(sp.id, { printer_id: v.printer_id, slot: t.slot, replace_existing: true }).then(reloadAll, (e) => toast('err', describeError(e))); }} spools={spools.data?.spools ?? []} />)}
                </div>
                {v.trays.length === 0 && !v.error && <p className="hint">Aucun emplacement lu pour l'instant.</p>}
              </>
            )}
          </Async>
        )}
      </Panel>
      <Panel title="Inventaire" actions={<Button size="sm" variant="primary" icon={<Plus size={13} />} onClick={() => setEdit('new')}>Nouvelle bobine</Button>}>
        <Async state={spools} label="Chargement de l'inventaire" empty={(d) => d.spools.length === 0 ? <EmptyState title="Aucune bobine" text="Inscrivez vos bobines pour suivre leur poids restant, leur prix et être prévenu avant qu'une impression ne manque de matière." action={<Button size="sm" variant="primary" onClick={() => setEdit('new')}>Ajouter une bobine</Button>} /> : null}>
          {(d) => (
            <>
              <div className="spools" role="list" aria-label="Bobines">
                {d.spools.map((sp) => {
                  const pct = sp.remaining_percent >= 0 ? sp.remaining_percent : Math.min(100, Math.round((sp.remaining_g / Math.max(1, sp.net_weight_g)) * 100));
                  const lowish = sp.status === 'low' || sp.status === 'empty';
                  return (
                    <article key={sp.id} role="listitem" className="panel spool-card">
                      <div className="spool">
                        <span className="reel" style={sp.color_hex ? { background: sp.color_hex } : undefined} role="img" aria-label={sp.color_hex ? `Couleur ${sp.color_hex}` : 'couleur inconnue'} />
                        <div><b>{[sp.brand, sp.material, sp.name].filter(Boolean).join(' ')}</b><small>{sp.material} · {sp.diameter_mm} mm{sp.slot ? ` · ${sp.slot}` : ' · rangée'}</small></div>
                      </div>
                      <div className="rest"><span>{fmtNum(sp.remaining_g, 0)} g</span><span className="muted">sur {fmtNum(sp.net_weight_g, 0)} g</span></div>
                      <div className="progress" role="progressbar" aria-label={`Restant de ${sp.name ?? sp.material}`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct}><span style={{ width: `${pct}%`, background: lowish ? 'var(--st-wait)' : undefined }} /></div>
                      <p className="spool-meta"><Tag tone={STATUS[sp.status]?.tone}>{STATUS[sp.status]?.label ?? sp.status}</Tag> <span className="sub">{SOURCE[sp.remaining_source]}</span></p>
                      {lowish && <p className="sub spool-low">Bientôt vide</p>}
                      <dl className="spool-facts">
                        <div><dt>Prix/kg</dt><dd className="mono">{sp.price_per_kg !== undefined ? fmtNum(sp.price_per_kg, 2) : <span className="unknown">inconnu</span>}</dd></div>
                        <div><dt>Séchage</dt><dd>{sp.last_dried_at ? fmtDate(sp.last_dried_at) : <span className="unknown">—</span>}</dd></div>
                      </dl>
                      <div className="spool-act"><span className="mono sub">{sp.id}</span><Button size="sm" onClick={() => setEdit(sp)}>Modifier</Button></div>
                    </article>
                  );
                })}
              </div>
              <MarginEditor margin={d.alert_margin} onSaved={spools.reload} />
            </>
          )}
        </Async>
      </Panel>
      {edit && <SpoolDialog spool={edit === 'new' ? undefined : edit} printerId={printerId} onClose={() => setEdit(undefined)} onSaved={() => { setEdit(undefined); reloadAll(); }} />}
    </div>
  );
}

function TrayCard({ t, spools, onAssign }: { t: AMSTrayView; spools: Spool[]; onAssign: (s: Spool) => void }) {
  const [pick, setPick] = useState('');
  const free = spools.filter((s) => !s.slot && !s.archived);
  return (
    <div className={`fil-tray${t.active ? ' active' : ''}`}>
      <header><strong className="mono">{t.slot}</strong>{t.active && <Tag tone="ok">en cours</Tag>}</header>
      {t.empty ? <p className="hint">Vide</p> : (
        <p className="fil-name"><Swatch color={t.color} />{t.sub_brand || t.type || 'filament'}{t.remain_percent >= 0 && <span className="mono"> · {t.remain_percent} %</span>}</p>
      )}
      {t.spool && <p className="sub">Inventaire : {t.spool.name || t.spool.material}, {fmtNum(t.spool.remaining_g, 0)} g</p>}
      {t.match !== 'assigned' && t.match !== 'empty' && <Tag tone={t.match === 'mismatch' ? 'err' : 'warn'}>{t.match === 'suggested' ? 'bobine proposée' : t.match === 'mismatch' ? 'lecture différente' : t.match === 'rfid_assigned' ? 'reconnue par RFID' : 'non inscrite'}</Tag>}
      {t.warnings?.map((w) => <p key={w} className="hint">{w}</p>)}
      {!t.empty && !t.spool && free.length > 0 && (
        <div className="row-wrap">
          <Select aria-label={`Bobine pour ${t.slot}`} value={pick} onChange={(e) => setPick(e.target.value)} options={[{ value: '', label: 'Inscrire une bobine…' }, ...free.map((s) => ({ value: s.id, label: `${s.material} ${s.name ?? ''} (${fmtNum(s.remaining_g, 0)} g)` }))]} />
          <Button size="sm" disabled={!pick} onClick={() => { const sp = free.find((s) => s.id === pick); if (sp) onAssign(sp); }}>Charger</Button>
        </div>
      )}
    </div>
  );
}

function MarginEditor({ margin, onSaved }: { margin: { margin_percent: number; margin_grams: number }; onSaved: () => void }) {
  const [pct, setPct] = useState(String(margin.margin_percent));
  const [g, setG] = useState(String(margin.margin_grams));
  const toast = useToast();
  return (
    <div className="fil-margin">
      <span className="hint">Alerte de manque : prévenir quand le restant est sous le besoin +</span>
      <Input aria-label="Marge en pourcentage" className="fil-num" inputMode="decimal" value={pct} onChange={(e) => setPct(e.target.value)} /> %
      <Input aria-label="Marge en grammes" className="fil-num" inputMode="decimal" value={g} onChange={(e) => setG(e.target.value)} /> g
      <Button size="sm" onClick={() => filamentApi.setMargin({ margin_percent: Number(pct), margin_grams: Number(g) }).then(() => { toast('ok', 'Marge enregistrée'); onSaved(); }, (e) => toast('err', describeError(e)))}>Enregistrer</Button>
    </div>
  );
}

function SpoolDialog({ spool, printerId, onClose, onSaved }: { spool?: Spool; printerId?: string; onClose: () => void; onSaved: () => void }) {
  const [f, setF] = useState({
    material: spool?.material ?? 'PLA', brand: spool?.brand ?? '', name: spool?.name ?? '', color_hex: spool?.color_hex ?? '', net_weight_g: String(spool?.net_weight_g ?? 1000),
    remaining_g: spool ? String(Math.round(spool.remaining_g)) : '', price_per_kg: spool?.price_per_kg !== undefined ? String(spool.price_per_kg) : '', opened_at: spool?.opened_at ?? '',
    last_dried_at: spool?.last_dried_at ?? '', drying_note: spool?.drying_note ?? '', slot: spool?.slot ?? '',
  });
  const [busy, setBusy] = useState(false);
  const toast = useToast();
  const set = (k: keyof typeof f) => (e: { target: { value: string } }) => setF((x) => ({ ...x, [k]: e.target.value }));
  const num = (s: string) => (s.trim() === '' ? undefined : Number(s.replace(',', '.')));
  const save = () => {
    const body: SpoolInput = { material: f.material, brand: f.brand, name: f.name, color_hex: f.color_hex, net_weight_g: num(f.net_weight_g), remaining_g: num(f.remaining_g), price_per_kg: num(f.price_per_kg),
      opened_at: f.opened_at, last_dried_at: f.last_dried_at, drying_note: f.drying_note };
    if (f.slot !== (spool?.slot ?? '')) { if (f.slot) { body.slot = f.slot; body.printer_id = printerId; body.replace_existing = true; } else body.unassign = true; }
    setBusy(true);
    (spool ? filamentApi.updateSpool(spool.id, body) : filamentApi.createSpool(body)).then(() => { toast('ok', 'Bobine enregistrée'); onSaved(); }, (e) => { toast('err', describeError(e)); setBusy(false); });
  };
  return (
    <Dialog open onClose={onClose} title={spool ? 'Modifier la bobine' : 'Nouvelle bobine'} footer={<><Button onClick={onClose}>Annuler</Button><Button variant="primary" disabled={busy || !f.material} onClick={save}>Enregistrer</Button></>}>
      <div className="fil-form">
        <Field label="Matière"><Input value={f.material} onChange={set('material')} placeholder="PLA, PETG, ABS, TPU…" /></Field>
        <Field label="Marque"><Input value={f.brand} onChange={set('brand')} /></Field>
        <Field label="Nom de la couleur"><Input value={f.name} onChange={set('name')} /></Field>
        <Field label="Couleur (#RRGGBB)"><Input value={f.color_hex} onChange={set('color_hex')} placeholder="#FFFFFF" /></Field>
        <Field label="Poids net (g)"><Input inputMode="decimal" value={f.net_weight_g} onChange={set('net_weight_g')} /></Field>
        <Field label="Poids restant (g)"><Input inputMode="decimal" value={f.remaining_g} onChange={set('remaining_g')} placeholder="mesuré à la balance" /></Field>
        <Field label="Prix au kilo"><Input inputMode="decimal" value={f.price_per_kg} onChange={set('price_per_kg')} /></Field>
        <Field label="Emplacement"><Select value={f.slot} onChange={set('slot')} options={[{ value: '', label: 'Rangée' }, { value: 'ext', label: 'Bobine externe' }, ...[1, 2, 3, 4].flatMap((u) => [1, 2, 3, 4].map((b) => ({ value: `ams${u}:${b}`, label: `AMS ${u}, bac ${b}` })))]} disabled={!printerId} /></Field>
        <Field label="Ouverte le (AAAA-MM-JJ)"><Input value={f.opened_at} onChange={set('opened_at')} /></Field>
        <Field label="Séchée le (AAAA-MM-JJ)"><Input value={f.last_dried_at} onChange={set('last_dried_at')} /></Field>
        <Field label="Séchage (réglage, remarque)"><Input value={f.drying_note} onChange={set('drying_note')} placeholder="55 °C, 8 h" /></Field>
      </div>
      <p className="hint">Ce que vous saisissez ici n'est jamais écrasé par la lecture de l'AMS.</p>
    </Dialog>
  );
}

/* ---------------------------------------------------------------- Multicolore */

function ColorsTab({ printerId }: { printerId?: string }) {
  const jobs = useAsync((s) => api.printJobs(s), []);
  const candidates = (jobs.data?.print_jobs ?? []).filter((j) => ['VALIDATED', 'UPLOADED', 'AWAITING_PRINT_CONFIRMATION'].includes(j.state));
  const [jid, setJid] = useState('');
  const job = candidates.find((j) => j.id === jid) ?? candidates[0];
  const [plan, setPlan] = useState<ColorPlan>();
  const [wishes, setWishes] = useState<Record<number, string>>({});
  const [busy, setBusy] = useState(false);
  const toast = useToast();
  const run = (w: Record<number, string> = wishes) => {
    if (!job) return;
    const colors: ColorWish[] = Object.entries(w).filter(([, v]) => v).map(([k, v]) => ({ filament_slot: Number(k), color_hex: v }));
    setBusy(true);
    filamentApi.planColors({ print_job_id: job.id, printer_id: printerId, colors }).then(setPlan, (e) => toast('err', describeError(e))).finally(() => setBusy(false));
  };
  useEffect(() => { setPlan(undefined); setWishes({}); }, [job?.id]);
  return (
    <div className="fil-grid">
      <Panel title="Impression à colorer">
        <Async state={jobs} label="Chargement des impressions" empty={() => candidates.length === 0 ? <EmptyState title="Aucune impression découpée" text="Découpez d'abord un modèle multicolore (écran Impressions), puis revenez ici pour choisir les bobines." /> : null}>
          {() => (
            <>
              <Field label="Impression"><Select value={job?.id ?? ''} onChange={(e) => setJid(e.target.value)} options={candidates.map((j) => ({ value: j.id, label: `${j.name ?? j.id} · ${j.state}` }))} /></Field>
              <p className="hint">Les couleurs du fichier servent de point de départ. Pour une autre couleur, indiquez-la par filament (#RRGGBB) puis recalculez.</p>
              <Button variant="primary" disabled={busy || !job} onClick={() => run()}>Proposer l'affectation</Button>
            </>
          )}
        </Async>
      </Panel>
      {plan && <PlanView plan={plan} wishes={wishes} onWish={(slot, v) => setWishes((w) => ({ ...w, [slot]: v }))} onRecalc={() => run()} job={job} />}
    </div>
  );
}

function PlanView({ plan, wishes, onWish, onRecalc, job }: { plan: ColorPlan; wishes: Record<number, string>; onWish: (slot: number, v: string) => void; onRecalc: () => void; job?: PrintJob }) {
  const toast = useToast();
  const queue = () => {
    if (!job) return;
    filamentApi.queuePrint({ print_job_id: job.id, printer_id: plan.printer_id, assignments: plan.assignments }).then(() => toast('ok', 'Mise en file. Rien ne démarre sans votre accord.'), (e) => toast('err', describeError(e)));
  };
  return (
    <Panel title="Affectation proposée" actions={<Tag tone={plan.complete ? 'ok' : 'warn'}>{plan.complete ? 'complète' : 'à compléter'}</Tag>}>
      <div className="mat-table-wrap">
        <table className="mat-table" aria-label="Affectation filament vers emplacement">
          <thead><tr><th scope="col">Filament</th><th scope="col">Couleur voulue</th><th scope="col">Emplacement</th><th scope="col">Qualité</th><th scope="col">Matière</th><th scope="col">Purge</th></tr></thead>
          <tbody>
            {plan.slots.map((s) => (
              <tr key={s.filament_slot}>
                <th scope="row">{s.filament_slot}{s.parts ? ` · ${s.parts}` : ''}<span className="sub">{s.wanted_type}</span></th>
                <td><span className="fil-name"><Swatch color={s.wanted_color} /><Input aria-label={`Couleur voulue du filament ${s.filament_slot}`} className="fil-hex" value={wishes[s.filament_slot] ?? s.wanted_color ?? ''} onChange={(e) => onWish(s.filament_slot, e.target.value)} /></span></td>
                <td className="mono">{s.tray ? <span className="fil-name"><Swatch color={s.loaded_color} />{s.tray}</span> : <span className="unknown">aucune bobine</span>}{s.note && <span className="sub">{s.note}</span>}</td>
                <td><Tag tone={s.quality === 'exact' ? 'ok' : s.quality === 'aucune' ? 'err' : 'warn'}>{s.quality}</Tag></td>
                <td className="mono">{fmtNum(s.needed_g, 1)} g</td>
                <td className="mono">{fmtNum(s.purge_g, 1)} g{s.purge_cost !== undefined ? ` · ${fmtNum(s.purge_cost, 2)}` : ''}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="row-wrap" style={{ marginTop: 'var(--sp-3)' }}>
        <Button size="sm" onClick={onRecalc}>Recalculer avec ces couleurs</Button>
        <Button size="sm" variant="primary" disabled={!plan.complete} onClick={queue}>Mettre en file avec cette affectation</Button>
      </div>
      {plan.purge && (
        <p className="hint">Purge mesurée dans le G-code : {fmtNum(plan.purge.waste_g, 1)} g jetés sur {fmtNum(plan.purge.total_g, 1)} g ({fmtNum(plan.purge.waste_percent, 0)} %), {plan.purge.color_changes} changement(s) de filament
          {plan.purge_cost?.cost !== undefined ? `, coût de la matière jetée : ${fmtNum(plan.purge_cost.cost, 2)}${plan.purge_cost.complete ? '' : ' (partiel : prix de certaines bobines inconnu)'}` : ''}.</p>
      )}
      {plan.ams_mapping && <p className="hint mono">ams_mapping : [{plan.ams_mapping.join(', ')}]</p>}
      {plan.filament_check && plan.filament_check.verdict !== 'ok' && <Notice tone={plan.filament_check.verdict === 'insufficient' ? 'err' : 'warn'} title="Suffisance des bobines">{plan.filament_check.summary}</Notice>}
      {plan.warnings?.map((w) => <Notice key={w} title="À savoir">{w}</Notice>)}
    </Panel>
  );
}

/* ---------------------------------------------------------------- File d'attente */

function QueueTab() {
  const q = useAsync((s) => filamentApi.queue(true, s), []);
  const reloadRef = useRef(q.reload); reloadRef.current = q.reload;
  useEffect(() => { const t = window.setInterval(() => reloadRef.current(), 5000); return () => window.clearInterval(t); }, []);
  const jobs = useAsync((s) => api.printJobs(s), []);
  const jobsReload = useRef(jobs.reload); jobsReload.current = jobs.reload;
  useEffect(() => { const t = window.setInterval(() => jobsReload.current(), 5000); return () => window.clearInterval(t); }, []);
  const toast = useToast();
  const [jid, setJid] = useState('');
  const entries = q.data?.queue ?? [];
  const active = entries.filter((e) => !['done', 'failed', 'removed'].includes(e.state));
  const history = entries.filter((e) => ['done', 'failed', 'removed'].includes(e.state)).slice(0, 10);
  const queued = new Set(active.map((e) => e.print_job_id));
  const addable = (jobs.data?.print_jobs ?? []).filter((j) => !queued.has(j.id) && ['DRAFT', 'PREPARED', 'SLICING', 'VALIDATED', 'UPLOADED', 'AWAITING_PRINT_CONFIRMATION'].includes(j.state));
  const act = (p: Promise<unknown>, ok: string) => p.then(() => { toast('ok', ok); q.reload(); }, (e) => toast('err', describeError(e)));
  const move = (i: number, d: number) => {
    const ids = active.map((e) => e.id);
    const j = i + d; if (j < 0 || j >= ids.length) return;
    [ids[i], ids[j]] = [ids[j], ids[i]];
    act(filamentApi.reorder(ids), 'Ordre modifié');
  };
  const ready = active.filter((e) => e.state === 'ready');
  return (
    <div className="stack">
      {ready.length > 0 && (
        <Notice tone="info" title={ready.length === 1 ? 'Une impression est prête' : `${ready.length} impressions sont prêtes`}>
          Videz le plateau, puis ouvrez la page de confirmation pour lancer. Rien ne démarre sans votre accord, pour chaque impression.
        </Notice>
      )}
      <Panel title="File d'attente" actions={<Button size="sm" icon={<RefreshCw size={13} />} onClick={q.reload}>Actualiser</Button>}>
        <Async state={q} label="Chargement de la file" empty={() => active.length === 0 ? <EmptyState title="File vide" text="Ajoutez une impression découpée ci-dessous. La file prépare la suivante (envoi du fichier) mais ne la lance jamais seule." /> : null}>
          {() => (
            <ol className="fil-queue">
              {active.map((e, i) => <QueueRow key={e.id} e={e} job={jobs.data?.print_jobs.find((j) => j.id === e.print_job_id)} prog={jobs.data?.progress?.[e.print_job_id]} first={i === 0} last={i === active.length - 1} onMove={(d) => move(i, d)}
                onRemove={() => act(filamentApi.removeFromQueue(e.id), 'Retirée de la file')} onRenew={() => act(filamentApi.renewConfirmation(e.id), 'Nouveau lien de confirmation')} />)}
            </ol>
          )}
        </Async>
        <div className="fil-add">
          <Select aria-label="Impression à mettre en file" value={jid} onChange={(e) => setJid(e.target.value)} options={[{ value: '', label: 'Ajouter une impression…' }, ...addable.map((j) => ({ value: j.id, label: `${j.name ?? j.id} · ${j.state}` }))]} />
          <Button size="sm" variant="primary" disabled={!jid} onClick={() => { const j = addable.find((x) => x.id === jid); act(filamentApi.queuePrint({ print_job_id: jid, assignments: j?.assignments }), 'Mise en file'); setJid(''); }}>Mettre en file</Button>
        </div>
        <p className="hint">L'affectation filament → emplacement est reprise de l'impression ; sinon, calculez-la dans l'onglet Multicolore puis mettez en file depuis là.</p>
      </Panel>
      {history.length > 0 && (
        <Panel title="Récemment terminées">
          <ul className="fil-history">{history.map((e) => <li key={e.id}><Tag tone={QSTATE[e.state].tone}>{QSTATE[e.state].label}</Tag> {e.file_name ?? e.print_job_id} <span className="sub">{e.message}</span></li>)}</ul>
        </Panel>
      )}
    </div>
  );
}

function QueueRow({ e, job, prog, first, last, onMove, onRemove, onRenew }: { e: QueueEntry; job?: PrintJob; prog?: JobProgress; first: boolean; last: boolean; onMove: (d: number) => void; onRemove: () => void; onRenew: () => void }) {
  const st = QSTATE[e.state];
  return (
    <li className="fil-qrow">
      <span className="fil-rank mono">{e.position}</span>
      <div className="fil-qmain">
        <strong>{e.file_name ?? e.print_job_id}</strong>
        <span className="sub">{e.note ? `${e.note} · ` : ''}{e.estimated_minutes ? `${fmtNum(e.estimated_minutes, 0)} min` : ''}{e.estimated_grams ? ` · ${fmtNum(e.estimated_grams, 1)} g` : ''}</span>
        <span className="hint">{e.message}</span>
        {['slicing', 'uploading', 'printing'].includes(e.state) && <div className="fil-qbar"><ProgressBar label={`Avancement : ${e.file_name ?? e.print_job_id}`} source={printSource(job ?? { state: e.state === 'printing' ? 'PRINTING' : 'SLICING' }, prog)} /></div>}
      </div>
      <Tag tone={st.tone}>{st.label}</Tag>
      <div className="row-wrap">
        {e.state === 'ready' && (e.confirm_url
          ? <a className="btn btn-primary btn-sm" href={e.confirm_url} target="_blank" rel="noreferrer">Ouvrir la confirmation <ExternalLink size={12} aria-hidden /></a>
          : <Button size="sm" onClick={onRenew}>Nouveau lien de confirmation</Button>)}
        <Button size="sm" aria-label="Monter" disabled={first} onClick={() => onMove(-1)} icon={<ArrowUp size={13} />} />
        <Button size="sm" aria-label="Descendre" disabled={last} onClick={() => onMove(1)} icon={<ArrowDown size={13} />} />
        {e.state !== 'printing' && <Button size="sm" variant="ghost" aria-label="Retirer de la file" onClick={onRemove} icon={<Trash2 size={13} />} />}
      </div>
    </li>
  );
}
