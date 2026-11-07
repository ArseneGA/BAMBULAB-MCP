import { useMemo, useState } from 'react';
import { Panel, Segmented } from '../components';
import { useAsync } from '../api/hooks';
import { insightsApi } from '../api/insights';
import type { DashRow, Dashboard } from '../api/insights';
import { Async, Notice, fmtNum } from './common';

type Range = '7' | '30' | '90' | '365';
const isoDay = (d: Date) => d.toISOString().slice(0, 10);
const pct = (v?: number) => (v === undefined ? 'n/a' : `${fmtNum(v * 100, 0)} %`);
const money = (v: number, cur: string) => `${fmtNum(v, 2)} ${cur}`;

function Tile({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return <div className="ins-tile"><span className="ins-tile-label">{label}</span><strong className="ins-tile-value">{value}</strong>{sub && <span className="ins-tile-sub">{sub}</span>}</div>;
}

/** Histogramme des heures machine : réussies (teinte principale) et perdues (rouge), valeurs lisibles en tableau. */
function Bars({ rows }: { rows: DashRow[] }) {
  const max = Math.max(1, ...rows.map((r) => r.hours));
  const step = Math.ceil(rows.length / 14);
  return (
    <div className="ins-bars" role="img" aria-label={`Heures machine par période, ${rows.length} périodes, maximum ${fmtNum(max, 1)} h`}>
      {rows.map((r, i) => (
        <div className="ins-bar-col" key={r.key} title={`${r.name} : ${fmtNum(r.hours, 1)} h, ${r.prints} impression(s), ${fmtNum(r.failed_hours, 1)} h perdues`}>
          <div className="ins-bar-stack" style={{ height: `${(r.hours / max) * 100}%` }}>
            <div className="ins-bar-lost" style={{ flexGrow: r.failed_hours }} />
            <div className="ins-bar-ok" style={{ flexGrow: Math.max(0, r.hours - r.failed_hours) }} />
          </div>
          <span className="ins-bar-label">{i % step === 0 ? r.name.slice(r.name.length > 7 ? 5 : 0) : ''}</span>
        </div>
      ))}
    </div>
  );
}

function Breakdown({ title, rows, cur, projects }: { title: string; rows: DashRow[]; cur: string; projects?: boolean }) {
  if (rows.length === 0) return null;
  return (
    <Panel title={title}>
      <div className="ins-table-wrap">
        <table className="ins-table" aria-label={title}>
          <thead><tr><th scope="col">Nom</th><th scope="col">Impr.</th><th scope="col">Réussite</th><th scope="col">Heures</th><th scope="col">Filament</th><th scope="col">Coût estimé</th>{projects && <th scope="col">À venir (estimé)</th>}</tr></thead>
          <tbody>{rows.map((r) => (
            <tr key={r.key}>
              <th scope="row">{r.name}</th><td>{r.prints}</td><td>{pct(r.success_rate)}</td><td>{fmtNum(r.hours, 1)}</td><td>{fmtNum(r.grams, 0)} g</td><td>{money(r.cost, cur)}</td>
              {projects && <td>{r.pending_jobs ? `${money(r.pending_estimate ?? 0, cur)} (${r.pending_jobs})` : '—'}</td>}
            </tr>
          ))}</tbody>
        </table>
      </div>
    </Panel>
  );
}

function Body({ d }: { d: Dashboard }) {
  const t = d.totals;
  return (
    <>
      <div className="ins-tiles">
        <Tile label="Heures machine" value={`${fmtNum(t.machine_hours, 1)} h`} />
        <Tile label="Impressions" value={String(t.prints)} sub={`${t.succeeded} réussies · ${t.partial} partielles · ${t.failed} ratées · ${t.cancelled} annulées`} />
        <Tile label="Taux de réussite" value={pct(t.success_rate)} sub="annulations exclues" />
        <Tile label="Filament" value={`${fmtNum(t.filament_grams, 0)} g`} />
        <Tile label="Coût cumulé (estimé)" value={money(t.cost, d.currency)} />
        {t.avg_rating !== undefined && <Tile label="Note moyenne" value={`${fmtNum(t.avg_rating, 1)} / 5`} sub={`${t.rated_count} fiche(s) notée(s)`} />}
      </div>
      <Panel title="Heures machine par période" actions={<span className="legend"><i className="ins-sw ok" /> réussies <i className="ins-sw lost" /> perdues</span>}>
        {d.by_period.length === 0 ? <p className="hint">Aucune période.</p> : <Bars rows={d.by_period} />}
      </Panel>
      <div className="ins-two">
        <Breakdown title="Par matière" rows={d.by_material} cur={d.currency} />
        <Breakdown title="Par imprimante" rows={d.by_printer} cur={d.currency} />
      </div>
      <Breakdown title="Par projet (collection)" rows={d.by_project} cur={d.currency} projects />
      <Notice tone="info" title="Comment lire ces chiffres">
        <ul className="ins-notes">{d.notes.map((n) => <li key={n}>{n}</li>)}</ul>
      </Notice>
    </>
  );
}

export function InsightsDashboard() {
  const [range, setRange] = useState<Range>('30');
  const q = useMemo(() => {
    const to = new Date();
    const from = new Date(to.getTime() - Number(range) * 86400000);
    return { from: isoDay(from), to: isoDay(to) };
  }, [range]);
  const state = useAsync((s) => insightsApi.dashboard(q, s), [q.from, q.to]);
  return (
    <>
      <Segmented<Range> label="Période" value={range} onChange={setRange} options={[{ value: '7', label: '7 jours' }, { value: '30', label: '30 jours' }, { value: '90', label: '90 jours' }, { value: '365', label: '12 mois' }]} />
      <Async state={state} label="Calcul du tableau de bord">
        {(d) => <Body d={d} />}
      </Async>
    </>
  );
}
