import { AlertTriangle, Droplets } from 'lucide-react';
import type { AMSTrayView, AMSView, Spool } from '../../src/api/filamentApi';
import type { CardProps } from './call';
import { fmtGrams, fmtPct, isRec, num, safeColor } from './format';
import { LiteBadge, LiteCard, LiteGauge, LiteState, LiteSwatch, type Tone } from './lite';

/** Carte Bobines : list_spools (inventaire) et get_ams_state (emplacements de l'AMS rapprochés de l'inventaire). Lecture seule. */
export type SpoolsData = { kind: 'spools'; spools: Spool[]; marginPercent?: number; marginGrams?: number; note?: string } | { kind: 'ams'; ams: AMSView };

export function normalizeSpools(s: unknown): SpoolsData | undefined {
  if (!isRec(s)) return undefined;
  if (Array.isArray(s.trays) && typeof s.printer_id === 'string' && typeof s.reachable === 'boolean') return { kind: 'ams', ams: s as unknown as AMSView };
  if (Array.isArray(s.spools) && s.spools.every((x) => isRec(x) && typeof x.id === 'string')) {
    const m = isRec(s.alert_margin) ? s.alert_margin : {};
    return { kind: 'spools', spools: s.spools as Spool[], marginPercent: num(m.margin_percent), marginGrams: num(m.margin_grams), note: typeof s.note === 'string' ? s.note : undefined };
  }
  return undefined;
}

const SOURCE: Record<string, string> = { human: 'saisi', ams: 'lu sur l\'AMS', consumption: 'décompté', default: 'présumé neuf' };
const pctOf = (sp: Spool): number | null => {
  if (num(sp.remaining_percent) !== undefined && sp.remaining_percent >= 0) return sp.remaining_percent;
  return sp.net_weight_g > 0 ? (sp.remaining_g / sp.net_weight_g) * 100 : null;
};
const spoolName = (sp: Spool) => [sp.brand, sp.name].filter(Boolean).join(' ') || sp.material;
const gaugeTone = (st: string): Tone => (st === 'empty' ? 'fail' : st === 'low' ? 'warn' : 'run');

function SpoolRow({ sp }: { sp: Spool }) {
  const p = pctOf(sp);
  const alert = sp.status === 'low' || sp.status === 'empty';
  return (
    <li className="lc-row" aria-label={`Bobine ${spoolName(sp)}`}>
      <LiteSwatch color={sp.color_hex} size={28} label={sp.name ? `couleur ${sp.name}` : undefined} />
      <div className="lc-row-main">
        <span className="lc-row-name">{spoolName(sp)}</span>
        <span className="lc-row-meta">{sp.material}{sp.slot ? ` · ${sp.slot}` : ' · rangée'}{(sp as Spool & { translucent?: boolean }).translucent ? ' · translucide' : ''} · restant {SOURCE[sp.remaining_source] ?? sp.remaining_source}</span>
        <LiteGauge value={p} tone={gaugeTone(sp.status)} label={`Reste de ${spoolName(sp)}`} />
      </div>
      <div className="lc-row-end">
        <b>{fmtGrams(sp.remaining_g)}</b>
        <span className="lc-dim">{p !== null ? fmtPct(p) : 'poids net inconnu'}</span>
        {alert && <LiteBadge tone={sp.status === 'empty' ? 'fail' : 'warn'}><AlertTriangle size={11} aria-hidden /> {sp.status === 'empty' ? 'Vide' : 'Manque'}</LiteBadge>}
      </div>
    </li>
  );
}

const MATCH: Record<string, [string, Tone]> = {
  assigned: ['Inscrite', 'ok'], rfid_assigned: ['RFID', 'ok'], mismatch: ['Divergence', 'warn'], suggested: ['Bobine proposée', 'warn'], unregistered: ['Non inscrite', 'warn'], empty: ['Vide', 'off'],
};

function Tray({ t, active }: { t: AMSTrayView; active: boolean }) {
  const color = safeColor(t.color) ?? safeColor(t.spool?.color_hex);
  const remain = t.empty ? null : t.spool ? pctOf(t.spool) : num(t.remain_percent) !== undefined && t.remain_percent >= 0 ? t.remain_percent : null;
  const grams = t.spool?.remaining_g ?? t.effective_remaining_g ?? t.ams_estimate_g;
  const [mLabel, mTone] = MATCH[t.match] ?? [t.match, 'off' as Tone];
  const low = t.spool && (t.spool.status === 'low' || t.spool.status === 'empty');
  return (
    <li className="lc-row" aria-label={`Emplacement ${t.slot}`}>
      <LiteSwatch color={color} size={28} label={t.empty ? 'emplacement vide' : undefined} />
      <div className="lc-row-main">
        <span className="lc-row-name">{t.slot}</span>
        <span className="lc-row-meta">{t.empty ? 'Emplacement vide' : [t.type, t.sub_brand].filter(Boolean).join(' · ') || 'matière non lue'}{t.spool ? ` · ${spoolName(t.spool)}` : ''}</span>
        {!t.empty && <LiteGauge value={remain} tone={low ? gaugeTone(t.spool!.status) : 'run'} label={`Reste à l'emplacement ${t.slot}`} />}
        {(t.warnings ?? []).map((w, i) => <span key={i} className="lc-row-meta" style={{ color: 'var(--c-warn)' }}>{w}</span>)}
      </div>
      <div className="lc-row-end">
        {!t.empty && <b>{grams !== undefined ? fmtGrams(grams) : fmtPct(remain ?? undefined) ?? '—'}</b>}
        {!t.empty && remain !== null && grams !== undefined && <span className="lc-dim">{fmtPct(remain)}</span>}
        {active && <LiteBadge tone="run">Actif</LiteBadge>}
        <LiteBadge tone={mTone}>{mLabel}</LiteBadge>
        {low && <LiteBadge tone="warn"><AlertTriangle size={11} aria-hidden /> Manque</LiteBadge>}
      </div>
    </li>
  );
}

export function SpoolsCard({ data }: CardProps<SpoolsData>) {
  if (data.kind === 'ams') {
    const a = data.ams;
    const hum = Object.entries(a.humidity_levels ?? {});
    return (
      <LiteCard title="AMS" sub={`Imprimante ${a.printer_id}`} label="État de l'AMS"
        aside={<>{!a.reachable && <LiteBadge tone="fail">Injoignable</LiteBadge>}{a.reachable && a.stale && <LiteBadge tone="warn">Valeurs périmées</LiteBadge>}{hum.map(([k, v]) => <LiteBadge key={k} title="Indice d'humidité : 1 sec, 5 humide"><Droplets size={11} aria-hidden /> AMS {k} : {v}/5</LiteBadge>)}</>}>
        {a.error && <LiteState kind="error">{a.error}</LiteState>}
        {!a.ams_present && !a.error ? <LiteState kind="empty">Aucun AMS ni bobine externe dans le dernier rapport de l'imprimante.</LiteState> : a.trays.length > 0 && (
          <ul className="lc-list" aria-label="Emplacements">{a.trays.map((t) => <Tray key={t.slot} t={t} active={t.active || a.active_slot === t.slot} />)}</ul>
        )}
        {(a.notes ?? []).slice(0, 2).map((n, i) => <p key={i} className="lc-note">{n}</p>)}
      </LiteCard>
    );
  }
  const low = data.spools.filter((s) => s.status === 'low' || s.status === 'empty').length;
  const total = data.spools.reduce((a, s) => a + (s.archived ? 0 : s.remaining_g), 0);
  if (data.spools.length === 0) return <LiteCard title="Bobines" label="Bobines"><LiteState kind="empty">Aucune bobine dans l'inventaire.</LiteState></LiteCard>;
  return (
    <LiteCard title="Bobines" label="Bobines" sub={`${data.spools.length} bobine(s) · ${fmtGrams(total)} de filament`}
      aside={low > 0 ? <LiteBadge tone="warn"><AlertTriangle size={11} aria-hidden /> {low} presque vide(s)</LiteBadge> : <LiteBadge tone="ok">Stock suffisant</LiteBadge>}>
      <ul className="lc-list" aria-label="Inventaire des bobines">{data.spools.map((s) => <SpoolRow key={s.id} sp={s} />)}</ul>
      {data.marginPercent !== undefined && <p className="lc-note">Alerte de manque : marge de {data.marginPercent} %{data.marginGrams ? ` ou ${data.marginGrams} g` : ''}.</p>}
    </LiteCard>
  );
}
