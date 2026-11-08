import { useEffect, useMemo, useRef, useState } from 'react';
import { ExternalLink, RefreshCw } from 'lucide-react';
import { Button, EmptyState, List, Panel, ProgressBar, StatusDot, Tag, useToast } from '../components';
import { api, describeError } from '../api/client';
import { useAsync } from '../api/hooks';
import type { AgentStatus } from '../api/printTypes';
import type { AgentTask, InventoryItem } from '../api/extraTypes';
import { Async, Notice, fmtBytes, fmtDate, shortSha } from './common';
import './agents.css';

const ITEM_STATE: Record<string, { label: string; tone?: 'ok' | 'warn' | 'err' }> = {
  inventoried: { label: 'Windows seulement', tone: 'warn' }, requested: { label: 'copie demandée', tone: 'warn' }, uploaded: { label: 'copié sur Linux', tone: 'ok' }, error: { label: 'échec de copie', tone: 'err' },
};
const TASK_STATE: Record<string, { label: string; tone?: 'ok' | 'warn' | 'err' }> = {
  queued: { label: 'en file', tone: 'warn' }, assigned: { label: 'confiée', tone: 'warn' }, running: { label: 'en cours', tone: 'warn' }, succeeded: { label: 'terminée', tone: 'ok' },
  failed: { label: 'échec', tone: 'err' }, cancelled: { label: 'annulée' }, unknown: { label: 'incertaine', tone: 'err' },
};

/** Agent(s) Windows : état, inventaire des fichiers non copiés, demande de copie contrôlée et suivi des travaux. */
export function AgentsPage() {
  const agents = useAsync((s) => api.agents(s), []);
  const [sel, setSel] = useState('');
  const list = agents.data?.agents ?? [];
  const agent = list.find((a) => a.id === sel) ?? list[0];
  return (
    <>
      <div className="page-head"><span className="eyebrow">Espace</span><h1>Agent Windows</h1><p>L'agent indexe vos dossiers Windows et copie, à votre demande, les fichiers choisis vers le stockage du serveur. Il ne peut ni lancer ni arrêter une impression.</p></div>
      <div className="stack">
        <Async state={agents} label="Chargement des agents" empty={(d) => d.agents.length === 0 ? (
          <EmptyState title="Aucun agent Windows appairé" text="Sans agent, le serveur travaille seul sur son propre stockage. L'appairage se fait sur une page du serveur réservée au navigateur enregistré (jeton à usage unique), ou avec « bambud agent pair » sur la machine du service."
            action={<a className="btn btn-primary btn-sm" href="/agents/pair" target="_blank" rel="noreferrer"><ExternalLink size={13} aria-hidden /> Ouvrir la page d'appairage</a>} />
        ) : null}>
          {(d) => (
            <div className="ag-layout">
              <Panel title={`Agents (${d.agents.length})`} actions={<a className="btn btn-secondary btn-sm" href="/agents/pair" target="_blank" rel="noreferrer"><ExternalLink size={13} aria-hidden /> Appairer</a>} flush>
                <List label="Agents" items={d.agents} getKey={(a) => a.id} selectedKey={agent?.id} onSelect={(a) => setSel(a.id)}
                  render={(a) => <span className="ag-item"><strong>{a.name}</strong><StatusDot status={a.online ? 'online' : 'offline'} label={a.online ? 'en ligne' : 'hors ligne'} /></span>} />
              </Panel>
              {agent && <AgentSheet key={agent.id} a={agent} />}
            </div>
          )}
        </Async>
        <Tasks />
      </div>
    </>
  );
}

function AgentSheet({ a }: { a: AgentStatus }) {
  return (
    <div className="stack">
      <Panel title={a.name} actions={<StatusDot status={a.online ? 'online' : 'offline'} label={a.online ? 'en ligne' : 'hors ligne'} />}>
        <dl className="kvl">
          <dt>Dernière activité</dt><dd className="mono">{a.last_seen ? fmtDate(a.last_seen) : 'jamais vu'}</dd>
          <dt>Version de l'agent</dt><dd className="mono">{a.agent_version ?? <span className="unknown">inconnue</span>}</dd>
          <dt>Bambu Studio</dt><dd>{a.studio_running ? <Tag tone="ok">ouvert</Tag> : a.studio_installed ? <Tag>installé</Tag> : <Tag tone="warn">absent</Tag>}{a.studio_version && <span className="mono"> {a.studio_version}</span>}</dd>
          <dt>Dossiers partagés</dt><dd>{a.shared_folders?.length ? <span className="mono">{a.shared_folders.join(', ')}</span> : <span className="unknown">aucun déclaré</span>}</dd>
          <dt>Travaux pris en charge</dt><dd className="mono">{a.tasks?.length ? a.tasks.join(', ') : '—'}</dd>
        </dl>
        {!a.online && <p className="hint" style={{ margin: 'var(--sp-3) 0 0' }}>Hors ligne : l'inventaire ci-dessous est le dernier connu, et aucune copie ne démarre avant le retour de l'agent.</p>}
      </Panel>
      <Inventory a={a} />
    </div>
  );
}

function Inventory({ a }: { a: AgentStatus }) {
  const toast = useToast();
  const [filter, setFilter] = useState<'inventoried' | 'all'>('inventoried');
  const inv = useAsync((s) => api.agentInventory(a.id, { state: filter === 'all' ? undefined : filter, limit: 200 }, s), [a.id, filter]);
  const [picked, setPicked] = useState<Record<string, boolean>>({});
  const [busy, setBusy] = useState(false);
  const key = (i: InventoryItem) => `${i.root_label}\u0000${i.rel_path}`;
  const items = inv.data?.items ?? [];
  const chosen = useMemo(() => items.filter((i) => picked[key(i)] && ['inventoried', 'error'].includes(i.state)), [items, picked]);
  const roots = [...new Set(chosen.map((i) => i.root_label))];

  const request = async () => {
    setBusy(true);
    try {
      let files = 0, bytes = 0;
      for (const r of roots) {
        const res = await api.agentSelect(a.id, r, chosen.filter((i) => i.root_label === r).map((i) => i.rel_path));
        files += res.files; bytes += res.bytes;
      }
      toast('ok', `Copie demandée : ${files} fichier(s), ${fmtBytes(bytes)}. Suivi dans « Travaux de l'agent ».`);
      setPicked({}); inv.reload();
    } catch (e) { toast('err', describeError(e)); } finally { setBusy(false); }
  };

  return (
    <Panel title="Inventaire Windows" actions={<Button size="sm" variant="ghost" icon={<RefreshCw size={13} />} onClick={inv.reload}>Actualiser</Button>}>
      <Async state={inv} label="Lecture de l'inventaire" empty={(d) => d.items.length === 0 ? <p className="hint" style={{ margin: 0 }}>{filter === 'inventoried' ? "Aucun fichier en attente de copie : tout ce que l'agent a inventorié est déjà sur le serveur, ou rien n'a encore été inventorié." : "Inventaire vide : l'agent n'a encore rien indexé."}</p> : null}>
        {(d) => (
          <div className="stack">
            <p className="hint" style={{ margin: 0 }}>{d.summary.files} fichier(s) indexé(s), {fmtBytes(d.summary.bytes)} · encore Windows seulement : <strong>{fmtBytes(d.summary.windows_only_bytes)}</strong>{Object.entries(d.summary.by_state).map(([k, n]) => ` · ${ITEM_STATE[k]?.label ?? k} : ${n}`).join('')}</p>
            <div className="row-wrap">
              <label className="ag-filter">Afficher <select className="input" value={filter} onChange={(e) => setFilter(e.target.value as typeof filter)} aria-label="Filtre de l'inventaire"><option value="inventoried">À copier</option><option value="all">Tous</option></select></label>
              <Button variant="primary" size="sm" disabled={chosen.length === 0 || busy || !a.online} onClick={request}>{busy ? 'Demande…' : `Copier ${chosen.length || ''} fichier(s) vers Linux`}</Button>
              {!a.online && <span className="hint">L'agent doit être en ligne.</span>}
            </div>
            <div className="ag-table-wrap">
              <table className="ag-table" aria-label="Fichiers inventoriés">
                <thead><tr><th scope="col"><span className="sr-only">Choisir</span></th><th scope="col">Fichier</th><th scope="col">Taille</th><th scope="col">État</th></tr></thead>
                <tbody>
                  {d.items.map((i) => {
                    const copyable = ['inventoried', 'error'].includes(i.state);
                    return (
                      <tr key={key(i)}>
                        <td><input type="checkbox" disabled={!copyable} checked={!!picked[key(i)]} onChange={(e) => setPicked((p) => ({ ...p, [key(i)]: e.target.checked }))} aria-label={`Choisir ${i.rel_path}`} /></td>
                        <th scope="row"><span className="mono">{i.rel_path}</span><span className="sub mono">{i.root_label} · sha256 {shortSha(i.sha256)}…</span></th>
                        <td className="mono">{fmtBytes(i.size_bytes)}</td>
                        <td><Tag tone={ITEM_STATE[i.state]?.tone}>{ITEM_STATE[i.state]?.label ?? i.state}</Tag>{i.note && <span className="hint"> {i.note}</span>}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
            <p className="hint" style={{ margin: 0 }}>La copie est contrôlée : le serveur vérifie la taille et l'empreinte de chaque fichier reçu avant de l'ajouter au catalogue. Les originaux Windows ne sont ni déplacés ni modifiés.</p>
          </div>
        )}
      </Async>
    </Panel>
  );
}

function Tasks() {
  const st = useAsync((s) => api.agentTasks(s), []);
  const reload = useRef(st.reload); reload.current = st.reload;
  const active = st.data?.tasks.some((t) => ['queued', 'assigned', 'running'].includes(t.state));
  useEffect(() => { if (!active) return; const t = window.setInterval(() => reload.current(), 3000); return () => window.clearInterval(t); }, [active]);
  return (
    <Panel title="Travaux de l'agent" actions={<Button size="sm" variant="ghost" icon={<RefreshCw size={13} />} onClick={st.reload}>Actualiser</Button>}>
      <Async state={st} label="Chargement des travaux" empty={(d) => d.tasks.length === 0 ? <p className="hint" style={{ margin: 0 }}>Aucun travail confié à un agent.</p> : null}>
        {(d) => (
          <ul className="ag-tasks" aria-label="Travaux">
            {d.tasks.map((t: AgentTask) => (
              <li key={t.id}>
                <strong>{t.kind}</strong> <Tag tone={TASK_STATE[t.state]?.tone}>{TASK_STATE[t.state]?.label ?? t.state}</Tag>
                {['queued', 'assigned', 'running'].includes(t.state) && <ProgressBar source={t} label={`Avancement de ${t.kind}`} />}
                {t.message && <span className="hint"> {t.message}</span>}
                <span className="mono dim"> {t.id}</span>
              </li>
            ))}
          </ul>
        )}
      </Async>
      <Notice tone="info" title="Ce que l'agent ne fait jamais">Lancer ou arrêter une impression : ces gestes restent sur les pages de confirmation du serveur.</Notice>
    </Panel>
  );
}
