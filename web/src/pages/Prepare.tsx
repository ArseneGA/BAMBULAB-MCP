import { useEffect, useMemo, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { CheckCircle2, GitCompare, Plus, Trash2 } from 'lucide-react';
import { Button, EmptyState, Field, Input, Panel, Select, Tag, Toggle } from '../components';
import { api, describeError } from '../api/client';
import { useAsync, useConnection } from '../api/hooks';
import type { Goal, GoalCriteriaCatalog, CriterionInfo, CriterionInput } from '../api/goalTypes';
import { GlbView } from '../viewer/LazyGlbView';
import { Async, Notice, PendingBanner, fmtDims } from './common';
import './model.css';
import './prepare.css';

interface Row { uid: string; type: string; text: string; priority: number; nonNeg: boolean; params: Record<string, string> }
interface Draft { text: string; material: string; rows: Row[]; machine: string; count: number }
const DEFAULT_MACHINE = 'Bambu Lab P1S 0.4 nozzle';
const key = (id: string) => `atelier-goal-draft:${id}`;
const uid = () => Math.random().toString(36).slice(2, 8);

function loadDraft(id: string): Draft {
  try { const s = sessionStorage.getItem(key(id)); if (s) return JSON.parse(s) as Draft; } catch { /* stockage indisponible */ }
  return { text: '', material: '', rows: [], machine: DEFAULT_MACHINE, count: 3 };
}

export function PreparePage() {
  const [sp, setSp] = useSearchParams();
  const id = sp.get('model');
  if (!id) return <PickModel onPick={(m) => setSp({ model: m })} />;
  return <PrepareView key={id} id={id} />;
}

function PickModel({ onPick }: { onPick: (id: string) => void }) {
  const st = useAsync((s) => api.models({ limit: 100, sort: 'name' }, s), []);
  return (
    <>
      <div className="page-head"><span className="eyebrow">Espace</span><h1>Préparation</h1><p>Choisissez le modèle à préparer : l'objectif de finition et les variantes portent sur une version précise du catalogue.</p></div>
      <PendingBanner screen="Préparation" partial />
      <Async state={st} label="Chargement du catalogue" empty={(d) => d.models.length === 0 ? <EmptyState title="Le catalogue est vide" text="Importez d'abord un modèle depuis la bibliothèque." action={<Link to="/bibliotheque"><Button size="sm">Ouvrir la bibliothèque</Button></Link>} /> : null}>
        {(d) => (
          <Panel title="Modèles">
            <Field label="Modèle"><Select value="" onChange={(e) => e.target.value && onPick(e.target.value)} options={[{ value: '', label: 'Choisir…' }, ...d.models.map((m) => ({ value: m.id, label: `${m.name} (${m.formats.join(', ').toUpperCase()})` }))]} /></Field>
          </Panel>
        )}
      </Async>
    </>
  );
}

function PrepareView({ id }: { id: string }) {
  const nav = useNavigate();
  const conn = useConnection();
  const model = useAsync((s) => api.model(id, s), [id]);
  const catalog = useAsync((s) => api.goalCriteria(s), []);
  const [draft, setDraft] = useState<Draft>(() => loadDraft(id));
  const [goal, setGoal] = useState<Goal>();
  const [busy, setBusy] = useState<'goal' | 'exp' | null>(null);
  const [error, setError] = useState<string>();
  useEffect(() => { try { sessionStorage.setItem(key(id), JSON.stringify(draft)); } catch { /* ignore */ } }, [id, draft]);

  const patch = (p: Partial<Draft>) => { setDraft((d) => ({ ...d, ...p })); setGoal(undefined); };
  const setRow = (uidv: string, p: Partial<Row>) => patch({ rows: draft.rows.map((r) => (r.uid === uidv ? { ...r, ...p } : r)) });
  const slicing = conn.capabilities?.features.find((f) => f.id === 'goals_experiments');

  const m = model.data;
  const cur = m?.files.find((f) => f.id === m.current_file_id) ?? m?.files[m.files.length - 1];
  const version = m?.versions[m.versions.length - 1];

  const validate = async () => {
    setBusy('goal'); setError(undefined);
    const criteria: CriterionInput[] = draft.rows.map((r) => ({
      type: r.type, text: r.text || undefined, priority: r.priority, non_negotiable: r.nonNeg || undefined,
      params: Object.fromEntries(Object.entries(r.params).filter(([, v]) => v !== '')),
    }));
    try { setGoal(await api.setGoal({ original_text: draft.text.trim(), model_id: id, material: draft.material || undefined, criteria })); }
    catch (e) { setError(describeError(e)); } finally { setBusy(null); }
  };

  const createExp = async () => {
    if (!goal) return;
    setBusy('exp'); setError(undefined);
    try {
      const x = await api.createExperiment({ model_id: id, model_version_id: version?.id, expected_model_revision: version?.id, goal_id: goal.id, base: { machine: draft.machine.trim() || undefined }, candidates: draft.count });
      try { localStorage.setItem('atelier-last-experiment', JSON.stringify({ model_id: id, experiment_id: x.id })); } catch { /* ignore */ }
      nav(`/comparaison?experiment=${encodeURIComponent(x.id)}`);
    } catch (e) { setError(describeError(e)); setBusy(null); }
  };

  const loader = useMemo(() => async (signal: AbortSignal) => { if (!m?.preview.url) throw new Error("Pas d'aperçu"); return api.glb(m.preview.url, signal); }, [m?.preview.url]);
  const ready = draft.text.trim() !== '' && draft.rows.length > 0;

  return (
    <>
      <PendingBanner screen="Préparation" partial />
      <Async state={model} label="Chargement du modèle">
        {(d) => (
          <>
            <div className="mdl-head">
              <div>
                <nav className="mdl-crumb" aria-label="Fil d'Ariane"><Link to="/bibliotheque">Bibliothèque</Link><span aria-hidden>/</span><Link to={`/modele/${d.model.id}`}>{d.model.name}</Link><span aria-hidden>/</span><span>Préparation</span></nav>
                <h1>Préparation</h1>
                <p className="hint" style={{ margin: 0 }}>{d.model.name} · version <span className="mono">{version?.label}</span> · <span className="mono">{cur?.name}</span> · {fmtDims(d.analysis.size_mm ?? d.model.dimensions)}</p>
              </div>
            </div>
            <div className="mdl">
              <div className="mdl-stage">
                <div className="mdl-view">
                  {d.preview.state === 'ready' ? <GlbView sourceKey={d.preview.sha256 ?? d.model.id} loader={loader} ariaLabel={`Aperçu 3D de ${d.model.name}`} />
                    : <div className="v3 v3-placeholder"><div className="state" style={{ background: 'var(--surface)' }}><h3>Aperçu indisponible</h3><p>{d.preview.note ?? `État : ${d.preview.state}`}</p></div></div>}
                </div>
                {cur && !['stl', 'obj', '3mf'].includes(cur.format) && <Notice title="Format non découpable">Seuls STL, OBJ et 3MF peuvent être découpés pour comparer des variantes (STEP refusé).</Notice>}
                <p className="hint">Les réglages sont globaux à la pièce : les zones sont notées dans l'objectif mais ne produisent pas de variante par zone.</p>
              </div>

              <div className="mdl-side">
                <Panel title="Objectif de finition">
                  <div className="prep-form">
                    <Field label="Demande, telle que vous la formulez (conservée mot pour mot)">
                      <textarea className="input prep-text" rows={3} value={draft.text} onChange={(e) => patch({ text: e.target.value })} placeholder="Ex. : solide mais rapide à imprimer, surface propre sur la face visible" />
                    </Field>
                    <p className="hint" style={{ margin: 0 }}>Le texte n'est pas interprété par le serveur : traduisez-le en critères du catalogue ci-dessous.</p>
                    <Async state={catalog} label="Chargement du catalogue de critères">
                      {(c) => <Criteria c={c} draft={draft} patch={patch} setRow={setRow} goal={goal} />}
                    </Async>
                    <Button variant="primary" icon={<CheckCircle2 size={15} />} disabled={!ready || busy !== null} onClick={validate}>{busy === 'goal' ? 'Validation…' : "Valider l'objectif"}</Button>
                  </div>
                </Panel>

                {goal && <GoalResult goal={goal} onAnswer={(critId, param, value) => { const i = goal.criteria.findIndex((c) => c.id === critId); const row = draft.rows[i]; if (row) setRow(row.uid, { params: { ...row.params, [param]: value } }); }} />}

                <Panel title="Variantes">
                  <div className="prep-form">
                    <Field label="Preset machine Bambu Studio"><Input value={draft.machine} onChange={(e) => patch({ machine: e.target.value })} spellCheck={false} /></Field>
                    <Field label="Nombre de variantes (hors référence)"><Select value={String(draft.count)} onChange={(e) => setDraft((x) => ({ ...x, count: Number(e.target.value) }))} options={[1, 2, 3, 4, 5, 6].map((n) => ({ value: String(n), label: String(n) }))} /></Field>
                    {slicing && !slicing.available && <Notice title="Découpe indisponible">{slicing.note}</Notice>}
                    <Button variant="primary" icon={<GitCompare size={15} />} disabled={!goal || busy !== null || slicing?.available === false} onClick={createExp}>{busy === 'exp' ? 'Création…' : "Créer l'expérience et comparer"}</Button>
                    {!goal && <p className="hint" style={{ margin: 0 }}>Validez d'abord l'objectif. Chaque variante est découpée par Bambu Studio (quelques secondes à minutes) et comparée sur des mesures étiquetées.</p>}
                  </div>
                </Panel>
                {error && <Notice tone="err" title="Opération refusée">{error}</Notice>}
              </div>
            </div>
          </>
        )}
      </Async>
    </>
  );
}

function Criteria({ c, draft, patch, setRow, goal }: { c: GoalCriteriaCatalog; draft: Draft; patch: (p: Partial<Draft>) => void; setRow: (uid: string, p: Partial<Row>) => void; goal?: Goal }) {
  const byId = new Map(c.criteria.map((x) => [x.id, x]));
  const [add, setAdd] = useState('');
  return (
    <>
      <Field label="Matériau disponible">
        <Select value={draft.material} onChange={(e) => patch({ material: e.target.value })} options={[{ value: '', label: 'Non précisé (celui du profil machine)' }, ...c.material_families.map((f) => ({ value: f, label: f }))]} />
      </Field>
      <div className="prep-rows" role="list" aria-label="Critères">
        {draft.rows.length === 0 && <p className="hint" style={{ margin: 0 }}>Aucun critère. Ajoutez-en au moins un.</p>}
        {draft.rows.map((r, i) => {
          const info: CriterionInfo | undefined = byId.get(r.type);
          const gc = goal?.criteria[i];
          return (
            <div key={r.uid} className="prep-row" role="listitem">
              <div className="prep-row-head">
                <strong>{info?.label ?? r.type}</strong>
                {info && <Tag tone={info.evaluation.status === 'evaluable' ? 'ok' : 'warn'}>{info.evaluation.status === 'evaluable' ? `mesurable (${info.evaluation.by.join(', ')})` : 'à vérifier par essai'}</Tag>}
                {gc && gc.status !== info?.evaluation.status && <Tag tone="warn">{gc.status === 'evaluable' ? 'évaluable' : 'non évaluable ici'}</Tag>}
                <Button size="sm" variant="ghost" icon={<Trash2 size={13} />} aria-label={`Retirer ${info?.label ?? r.type}`} onClick={() => patch({ rows: draft.rows.filter((x) => x.uid !== r.uid) })} />
              </div>
              {info && <p className="hint" style={{ margin: 0 }}>{info.description}</p>}
              <div className="prep-row-grid">
                <Field label="Priorité"><Select value={String(r.priority)} onChange={(e) => setRow(r.uid, { priority: Number(e.target.value) })} options={[1, 2, 3, 4, 5].map((n) => ({ value: String(n), label: n === 1 ? '1 (la plus haute)' : n === 5 ? '5 (la plus basse)' : String(n) }))} /></Field>
                <Toggle checked={r.nonNeg} onChange={(v) => setRow(r.uid, { nonNeg: v })} label="Non négociable" />
              </div>
              {Object.entries(info?.params ?? {}).map(([p, spec]) => (
                <Field key={p} label={`${p}${spec.required ? ' (requis)' : ''} : ${spec.description}`}>
                  {spec.values ? <Select value={r.params[p] ?? ''} onChange={(e) => setRow(r.uid, { params: { ...r.params, [p]: e.target.value } })} options={[{ value: '', label: 'Non précisé' }, ...spec.values.map((v) => ({ value: v, label: v }))]} />
                    : <Input value={r.params[p] ?? ''} onChange={(e) => setRow(r.uid, { params: { ...r.params, [p]: e.target.value } })} />}
                </Field>
              ))}
              <Field label="Votre formulation (facultatif)"><Input value={r.text} onChange={(e) => setRow(r.uid, { text: e.target.value })} /></Field>
            </div>
          );
        })}
      </div>
      <div className="prep-add">
        <Select aria-label="Ajouter un critère" value={add} onChange={(e) => setAdd(e.target.value)} options={[{ value: '', label: 'Ajouter un critère…' }, ...c.criteria.map((x) => ({ value: x.id, label: x.label }))]} />
        <Button icon={<Plus size={14} />} disabled={!add} onClick={() => { patch({ rows: [...draft.rows, { uid: uid(), type: add, text: '', priority: 3, nonNeg: false, params: {} }] }); setAdd(''); }}>Ajouter</Button>
      </div>
    </>
  );
}

function GoalResult({ goal, onAnswer }: { goal: Goal; onAnswer: (criterion: string, param: string, value: string) => void }) {
  const clean = goal.ambiguities.length === 0 && goal.conflicts.length === 0 && goal.non_evaluable.length === 0 && !goal.warnings?.length;
  return (
    <Panel title="Objectif validé">
      <div className="prep-form">
        <p style={{ margin: 0 }}><Tag tone="ok">enregistré</Tag> <span className="mono">{goal.id}</span> · règles {goal.rules_version}{goal.material_family ? ` · matériau ${goal.material_family}` : ''}</p>
        {clean && <p className="hint" style={{ margin: 0 }}>Aucune ambiguïté, aucun conflit : tous les critères sont mesurables.</p>}
        {goal.ambiguities.map((a, i) => (
          <Notice key={i} title={a.question}>
            {a.why}
            {a.options && a.param && <span className="prep-opts"> {a.options.map((o) => <Button key={o} size="sm" onClick={() => onAnswer(a.criterion, a.param!, o)}>{o}</Button>)} (répondre invalide la validation)</span>}
          </Notice>
        ))}
        {goal.conflicts.map((c, i) => (
          <Notice key={i} title={`Compromis : ${c.type_a} et ${c.type_b}`}>
            {c.why} {c.note}
            <ul style={{ margin: 'var(--sp-1) 0 0', paddingLeft: 'var(--sp-4)' }}>{c.proposals.map((p) => <li key={p}>{p}</li>)}</ul>
          </Notice>
        ))}
        {goal.non_evaluable.map((n) => <p key={n.criterion} className="hint" style={{ margin: 0 }}><Tag tone="warn">à vérifier par essai</Tag> {n.label} : {n.reason}{n.test && <> Essai : {n.test}</>}</p>)}
        {goal.warnings?.map((w) => <p key={w} className="hint" style={{ margin: 0 }}>{w}</p>)}
      </div>
    </Panel>
  );
}
