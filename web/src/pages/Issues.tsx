import { useState } from 'react';
import { Link } from 'react-router-dom';
import { ArrowLeft, ClipboardCopy } from 'lucide-react';
import { Button, EmptyState, Input, Panel, Select, Tag, useToast } from '../components';
import { describeError } from '../api/client';
import { useAsync } from '../api/hooks';
import { SEVERITY_LABEL, STATE_LABEL, issuesApi } from '../api/issues';
import type { Issue, IssueList, IssueState } from '../api/issues';
import { Async, Notice, fmtDate } from './common';
import './issues.css';

const SEV_TONE = { P0: 'err', P1: 'err', P2: 'warn', P3: undefined } as const;
const STATE_TONE: Record<IssueState, 'warn' | 'ok' | 'accent' | undefined> = { new: 'warn', seen: 'accent', fixed: 'ok', dismissed: undefined };

const STATE_FILTERS = [
  { value: 'open', label: 'Ouverts (nouveaux + vus)' }, { value: 'new', label: 'Nouveaux' }, { value: 'seen', label: 'Vus' },
  { value: 'fixed', label: 'Corrigés' }, { value: 'dismissed', label: 'Écartés' }, { value: 'all', label: 'Tous' },
];
const SEV_FILTERS = [{ value: '', label: 'Toutes les gravités' }, ...(['P0', 'P1', 'P2', 'P3'] as const).map((s) => ({ value: s, label: SEVERITY_LABEL[s] }))];

/** Une ligne du carnet : ne propose que des changements d'état du carnet. Aucun geste d'impression (lancer, arrêter, reprendre). */
function Row({ i, onChanged }: { i: Issue; onChanged: () => void }) {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const [commit, setCommit] = useState('');
  const [asking, setAsking] = useState(false);
  const set = async (state: IssueState, fix_commit?: string) => {
    setBusy(true);
    try { await issuesApi.update(i.id, { state, fix_commit }); setAsking(false); setCommit(''); onChanged(); } catch (e) { toast('err', describeError(e)); } finally { setBusy(false); }
  };
  return (
    <li className="iss-row">
      <div className="iss-main">
        <div className="iss-title"><Tag tone={SEV_TONE[i.severity]}>{i.severity}</Tag> <strong>{i.title}</strong></div>
        <div className="hint iss-meta">
          {i.tool ? <span className="mono">{i.tool}</span> : null}{i.tool ? ' · ' : ''}{i.occurrences} occurrence{i.occurrences > 1 ? 's' : ''}
          {i.regressions ? ` · revenu ${i.regressions} fois après correction` : ''} · dernière le {fmtDate(i.last_seen_at)}
          {i.print_job_id ? <> · impression <span className="mono">{i.print_job_id}</span></> : null}
          {i.printer_id ? <> · imprimante <span className="mono">{i.printer_id}</span></> : null}
        </div>
        {i.expected && <p className="iss-text"><b>Attendu :</b> {i.expected}</p>}
        {i.observed && <p className="iss-text"><b>Observé :</b> {i.observed}</p>}
        {i.fix_commit && <p className="iss-text">Corrigé par le commit <span className="mono">{i.fix_commit}</span>.</p>}
      </div>
      <div className="iss-side">
        <Tag tone={STATE_TONE[i.state]}>{STATE_LABEL[i.state]}</Tag>
        {asking ? (
          <div className="iss-ask">
            <Input aria-label="Référence du commit du correctif" placeholder="commit (ex. 3f9a1c2)" value={commit} onChange={(e) => setCommit(e.target.value)} />
            <Button size="sm" variant="primary" disabled={busy || commit.trim() === ''} onClick={() => set('fixed', commit.trim())}>Marquer corrigé</Button>
            <Button size="sm" variant="ghost" onClick={() => setAsking(false)}>Annuler</Button>
          </div>
        ) : (
          <div className="iss-acts">
            {i.state === 'new' && <Button size="sm" disabled={busy} onClick={() => set('seen')}>Marquer vu</Button>}
            {(i.state === 'new' || i.state === 'seen') && <Button size="sm" disabled={busy} onClick={() => setAsking(true)}>Corrigé…</Button>}
            {(i.state === 'new' || i.state === 'seen') && <Button size="sm" variant="ghost" disabled={busy} onClick={() => set('dismissed')}>Écarter</Button>}
            {(i.state === 'fixed' || i.state === 'dismissed') && <Button size="sm" disabled={busy} onClick={() => set('new')}>Rouvrir</Button>}
          </div>
        )}
      </div>
    </li>
  );
}

function Body({ data, reload }: { data: IssueList; reload: () => void }) {
  const c = data.counts;
  return (
    <>
      <div className="iss-counts" aria-label="Constats par état">
        {(['new', 'seen', 'fixed', 'dismissed'] as const).map((s) => <span key={s} className="tag"><b>{c[s] ?? 0}</b> {STATE_LABEL[s]}{s === 'new' && (c[s] ?? 0) > 1 ? 's' : ''}</span>)}
      </div>
      {data.issues.length === 0
        ? <EmptyState title="Aucun constat" text="Le carnet se remplit tout seul : Claude y consigne ce qu'il constate d'anormal (outil qui refuse à tort, réponse incohérente, fausse alerte, donnée périmée). Rien à corriger pour ces filtres." />
        : <ul className="iss-list" aria-label="Constats">{data.issues.map((i) => <Row key={i.id} i={i} onChanged={reload} />)}</ul>}
      {data.total > data.issues.length && <p className="hint">{data.total - data.issues.length} constat(s) de plus : affinez les filtres.</p>}
    </>
  );
}

/** Réglages > Dysfonctionnements : le carnet de bord tenu par l'agent, avec l'export Markdown à coller à l'agent qui code. */
export function IssuesPage() {
  const toast = useToast();
  const [state, setState] = useState('open');
  const [severity, setSeverity] = useState('');
  const q = useAsync((s) => issuesApi.list({ state, severity, limit: 200 }, s), [state, severity]);
  const copy = async () => {
    try {
      const md = await issuesApi.exportMarkdown({ state, severity });
      await navigator.clipboard.writeText(md);
      toast('ok', 'Export Markdown copié : collez-le à l\'agent qui code.');
    } catch (e) { toast('err', describeError(e)); }
  };
  return (
    <>
      <div className="page-head">
        <div><h1>Dysfonctionnements</h1><div className="sub">Le carnet de bord tenu par Claude : ce qu'il constate d'anormal dans Printalk.</div></div>
        <div className="head-acts"><Link className="btn btn-ghost" to="/reglages"><ArrowLeft size={15} aria-hidden />Réglages</Link></div>
      </div>
      <Notice tone="info" title="Comment ça marche">
        Claude consigne un constat dès qu'il voit un comportement anormal ; le serveur range, regroupe les doublons (le compteur monte) et masque les secrets. Ici, vous marquez les constats vus, corrigés (avec le commit) ou écartés, puis vous copiez l'export Markdown pour l'agent qui code. Aucun bouton de cette page ne lance, n'arrête ni ne reprend une impression.
      </Notice>
      <Panel title="Constats" actions={<Button size="sm" icon={<ClipboardCopy size={13} aria-hidden />} onClick={copy}>Copier l'export Markdown</Button>}>
        <div className="row-wrap iss-filters">
          <Select aria-label="État" value={state} onChange={(e) => setState(e.target.value)} options={STATE_FILTERS} />
          <Select aria-label="Gravité" value={severity} onChange={(e) => setSeverity(e.target.value)} options={SEV_FILTERS} />
        </div>
        <Async state={q} label="Lecture du carnet">{(d) => <Body data={d} reload={q.reload} />}</Async>
      </Panel>
    </>
  );
}
