import { useEffect, useState } from 'react';
import { Printer as PrinterIcon } from 'lucide-react';
import type { PrinterStatus, PrinterView } from '../../src/api/printTypes';
import { useToolCall, type CardProps } from './call';
import { fmtDate, isRec, num, str } from './format';
import { LiteBadge, LiteCard, LiteState, toneOfState, type Tone } from './lite';

/**
 * Carte Imprimantes : list_printers. Chaque machine affiche son état (relu par get_printer_status, une fois à l'ouverture, en lecture
 * seule), son firmware, ses AMS et sa dernière impression ; un clic ouvre get_printer_status. Le code d'accès n'est jamais renvoyé
 * par l'outil et n'est jamais affiché ; ni l'adresse série ni l'adresse réseau ne sont reprises dans la carte.
 */
export interface PrintersData { printers: PrinterView[]; note?: string }

export function normalizePrinters(s: unknown): PrintersData | undefined {
  if (!isRec(s) || !Array.isArray(s.printers) || !s.printers.every((p) => isRec(p) && typeof p.id === 'string' && typeof p.name === 'string')) return undefined;
  return { printers: s.printers as PrinterView[], note: str(s.note) };
}

const STATE_FR: Record<string, string> = { idle: 'Libre', printing: 'En impression', preparing: 'Préparation', paused: 'En pause', error: 'Erreur', finished: 'Terminée', offline: 'Hors ligne' };
const JOB_FR: Record<string, string> = { COMPLETED: 'terminée', FAILED: 'en échec', CANCELLED: 'annulée', PRINTING: 'en cours' };

type Live = { status?: PrinterStatus; failed?: boolean };

export function PrintersCard({ app, data, onOpen }: CardProps<PrintersData>) {
  const { busy, error, run } = useToolCall(app, onOpen);
  const [live, setLive] = useState<Record<string, Live>>({});

  useEffect(() => {
    let alive = true;
    (async () => {
      for (const p of data.printers) {
        if (!p.access_code_set) { if (alive) setLive((l) => ({ ...l, [p.id]: { failed: true } })); continue; }
        try {
          const r = await app.callServerTool({ name: 'get_printer_status', arguments: { printer_id: p.id } });
          const sc = isRec(r) ? r.structuredContent : undefined;
          if (!alive) return;
          setLive((l) => ({ ...l, [p.id]: isRec(sc) && typeof sc.state === 'string' ? { status: sc as unknown as PrinterStatus } : { failed: true } }));
        } catch { if (alive) setLive((l) => ({ ...l, [p.id]: { failed: true } })); }
      }
    })();
    return () => { alive = false; };
  }, [app, data.printers]);

  if (data.printers.length === 0) return <LiteCard title="Imprimantes" label="Imprimantes"><LiteState kind="empty">Aucune imprimante enregistrée.</LiteState></LiteCard>;

  return (
    <LiteCard title="Imprimantes" sub={`${data.printers.length} machine(s)`} label="Imprimantes du parc">
      <ul className="lc-list" aria-label="Machines">
        {data.printers.map((p) => {
          const l = live[p.id];
          const st = l?.status;
          let label = 'Lecture de l\'état…'; let tone: Tone = 'off';
          if (!p.access_code_set) { label = 'Code d\'accès à saisir'; tone = 'warn'; }
          else if (l?.failed) { label = 'État inconnu'; tone = 'off'; }
          else if (st) {
            if (!st.reachable) { label = 'Injoignable'; tone = 'fail'; }
            else { label = STATE_FR[st.state] ?? st.state; tone = st.state === 'error' ? 'fail' : toneOfState(st.state === 'idle' ? 'IDLE' : st.state); }
          }
          const pct = st?.reachable && st.state === 'printing' ? num(st.progress_percent) : undefined;
          const last = st?.last_job;
          const fw = st?.firmware ?? p.firmware;
          return (
            <li key={p.id}>
              <button type="button" className="lc-row" disabled={busy !== null} aria-busy={busy === p.id} onClick={() => { void run(p.id, 'get_printer_status', { printer_id: p.id }); }} aria-label={`État détaillé de ${p.name}`}>
                <PrinterIcon size={26} aria-hidden style={{ color: 'var(--c-primary)' }} />
                <span className="lc-row-main">
                  <span className="lc-row-name">{p.name}</span>
                  <span className="lc-row-meta">{p.model_name} · buse {String(p.nozzle_mm).replace('.', ',')} mm · {p.ams_units > 0 ? `${p.ams_units} AMS` : 'sans AMS'}{fw ? ` · firmware ${fw}` : ''}</span>
                  {st?.current_file && pct !== undefined && <span className="lc-row-meta">{st.current_file}</span>}
                  {last && <span className="lc-row-meta">Dernière impression : {last.name} ({JOB_FR[last.state] ?? last.state}{fmtDate(last.at) ? `, ${fmtDate(last.at)}` : ''}){last.stopped_by_user ? ' · arrêtée par l\'utilisateur' : ''}</span>}
                  {!last && st && <span className="lc-row-meta">Aucune impression enregistrée.</span>}
                  {st?.error && <span className="lc-row-meta" style={{ color: 'var(--c-fail)' }}>{st.error}</span>}
                </span>
                <span className="lc-row-end">
                  <LiteBadge tone={tone}>{label}{pct !== undefined ? ` · ${Math.round(pct)} %` : ''}</LiteBadge>
                </span>
              </button>
            </li>
          );
        })}
      </ul>
      {error && <LiteState kind="error">{error}</LiteState>}
      <p className="lc-note">Choisir une machine ouvre son état détaillé. Le code d'accès n'apparaît jamais ici.</p>
    </LiteCard>
  );
}
