import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { Camera, Image as ImageIcon, Printer as PrinterIcon } from 'lucide-react';
import { ProgressBar, Toggle } from '../../components';
import { assetUrl, BASE, api } from '../../api/client';
import { useAsync } from '../../api/hooks';
import { dashboardApi, lastMilestone } from '../../api/dashboardApi';
import type { JobPhoto } from '../../api/dashboardApi';
import type { PrintJob, PrinterStatus, PrinterView } from '../../api/printTypes';
import { finishAt, formatMinutes } from '../../api/progress';
import { printerPill } from '@shared/labels';
import { isPrinting } from '../Printers';
import { fmtTime, useVisiblePolling } from './logic';

const PHOTO_AUTO_KEY = 'printalk.photo-auto-30s';
const readAuto = () => { try { return localStorage.getItem(PHOTO_AUTO_KEY) === '1'; } catch { return false; } };

/**
 * Photo du plateau : instantané du serveur, à la demande (bouton « Photo »). Le rafraîchissement automatique toutes les 30 s
 * est une option, DÉSACTIVÉE par défaut, qui ne tourne que pendant une impression et onglet visible (le flux n'est jamais ouvert en arrière-plan).
 */
function Snapshot({ printer, printing }: { printer: PrinterView; printing: boolean }) {
  const [stamp, setStamp] = useState(() => Date.now());
  const [failed, setFailed] = useState(false);
  const [auto, setAuto] = useState(readAuto);
  useVisiblePolling(() => setStamp(Date.now()), 30_000, auto && printing && printer.access_code_set);
  if (!printer.access_code_set) return <div className="live-cam live-cam-empty"><Camera aria-hidden /><span>Caméra disponible une fois le code d'accès saisi.</span></div>;
  const setA = (v: boolean) => { setAuto(v); try { localStorage.setItem(PHOTO_AUTO_KEY, v ? '1' : '0'); } catch { /* stockage indisponible */ } };
  return (
    <div className="dash-cam">
      <div className="live-cam">
        {failed
          ? <div className="live-cam-empty"><Camera aria-hidden /><span>Photo du plateau indisponible pour cette imprimante.</span></div>
          : <img src={`${BASE}/api/printers/${encodeURIComponent(printer.id)}/camera/snapshot?t=${stamp}`} alt={`Photo du plateau de ${printer.name}`} onError={() => setFailed(true)} onLoad={() => setFailed(false)} />}
        <button type="button" className="btn btn-sm cam-btn" onClick={() => { setFailed(false); setStamp(Date.now()); }}><Camera size={14} aria-hidden />Photo</button>
      </div>
      {printing && <Toggle checked={auto} onChange={setA} label="Actualiser la photo toutes les 30 s" />}
    </div>
  );
}

/** Dernière photo de palier (journal photo à chaque 10 %) avec son pourcentage ; « photo non disponible » sinon. */
function Milestone({ job }: { job: PrintJob }) {
  const [photos, setPhotos] = useState<JobPhoto[] | null>(null);
  const load = () => { dashboardApi.jobPhotos(job.id).then((r) => setPhotos(r.photos), () => setPhotos([])); };
  const loadRef = useRef(load); loadRef.current = load;
  useEffect(() => { loadRef.current(); }, [job.id]);
  useVisiblePolling(() => loadRef.current(), 30_000);
  const m = photos ? lastMilestone(photos) : undefined;
  if (!photos) return null;
  return m ? (
    <figure className="dash-mile">
      <img src={assetUrl(m.url)} alt={`Photo de l'impression à ${m.percent} %`} loading="lazy" />
      <figcaption>Dernier palier : {m.percent} %</figcaption>
    </figure>
  ) : (
    <div className="dash-mile dash-mile-none" role="note"><ImageIcon size={16} aria-hidden /><span>Palier : photo non disponible</span></div>
  );
}

const temps = (d: PrinterStatus) => `${Math.round(d.temps.nozzle)} / ${Math.round(d.temps.nozzle_target)} °C`;

/** Retour de l'état de l'imprimante vers l'Accueil (titre d'onglet, alertes) ; appelé à chaque relevé. */
export interface LiveReport { printerId: string; status?: PrinterStatus; failed: boolean }

export function LiveCard({ printer, job, big, onReport }: { printer: PrinterView; job?: PrintJob; big: boolean; onReport?: (r: LiveReport) => void }) {
  const st = useAsync((s) => api.printerStatus(printer.id, s), [printer.id]);
  const reload = useRef(st.reload); reload.current = st.reload;
  useVisiblePolling(() => reload.current(), 5000);
  const d = st.data;
  const lastOk = useRef<Date | undefined>(undefined);
  const seen = useRef<unknown>(undefined);
  if (d && d !== seen.current) { seen.current = d; lastOk.current = new Date(); }
  useEffect(() => { onReport?.({ printerId: printer.id, status: d, failed: !!st.error }); }, [d, st.error]); // eslint-disable-line react-hooks/exhaustive-deps
  const pill = printerPill(d, !!st.error);
  const printing = isPrinting(d);
  const eta = d && d.remaining_minutes > 0 ? d.remaining_minutes * 60 : undefined;
  const title = job?.name ?? d?.current_file;
  const stale = !!st.error && !!d;

  if (!big && !printing) {
    return (
      <section className="dash-live-mini" aria-label={`Imprimante ${printer.name}`}>
        <span className="ico"><PrinterIcon aria-hidden /></span>
        <b>{printer.name}</b>
        <span className={`pill ${pill.cls}`} role="status"><i />{pill.label}</span>
        {d?.reachable && <span className="sub mono">buse {temps(d)}</span>}
        {stale && <span className="sub">Dernière mise à jour à {fmtTime(lastOk.current)}</span>}
      </section>
    );
  }
  return (
    <section className={`panel dash-live${printing ? ' dash-live-big' : ''}`} aria-label={`Imprimante ${printer.name} en direct`}>
      <div className="panel-h">
        <div className="prn-id"><div className="ico"><PrinterIcon aria-hidden /></div><div><h3>{printer.name}</h3><div className="sub">{printer.model_name}</div></div></div>
        <span className={`pill ${pill.cls}`} role="status"><i />{pill.label}</span>
      </div>
      <div className="live">
        <div>
          {printing && d ? (
            <>
              {title && <div className="live-title">{job ? <Link to={`/impressions/${encodeURIComponent(job.id)}`}>{title}</Link> : title}</div>}
              <ProgressBar large label={`Impression en cours sur ${printer.name}`} source={{ state: 'running', percent: d.progress_percent, remaining_minutes: d.remaining_minutes, phase_label: d.total_layers > 0 ? `couche ${d.layer} sur ${d.total_layers}` : undefined }} hideEta />
              <dl className="kv2">
                <div><dt>Couche</dt><dd>{d.total_layers > 0 ? `${d.layer} / ${d.total_layers}` : 'inconnue'}</dd></div>
                <div><dt>Temps restant</dt><dd>{formatMinutes(d.remaining_minutes) ?? 'inconnu'}</dd></div>
                <div><dt>Fin estimée</dt><dd>{finishAt(eta) ?? 'inconnue'}</dd></div>
                <div><dt>Buse / plateau</dt><dd className="mono">{temps(d)} · {Math.round(d.temps.bed)} / {Math.round(d.temps.bed_target)} °C</dd></div>
                {d.hms_count > 0 && <div><dt>Alertes de l'imprimante</dt><dd>{d.hms_count}</dd></div>}
              </dl>
              {job && <Milestone job={job} />}
            </>
          ) : (
            <>
              <div className="live-title">{!d ? 'Lecture de l\'état…' : !d.reachable ? 'Imprimante injoignable' : 'Aucune impression en cours'}</div>
              {d?.display && <p className="sub">{d.display}</p>}
              {d?.explanation && <p className="sub">{d.explanation}</p>}
              {d?.reachable && (
                <dl className="kv2">
                  <div><dt>Buse</dt><dd className="mono">{temps(d)}</dd></div>
                  <div><dt>Plateau</dt><dd className="mono">{Math.round(d.temps.bed)} / {Math.round(d.temps.bed_target)} °C</dd></div>
                  {d.last_job && <div><dt>Dernière impression</dt><dd>{d.last_job.name}</dd></div>}
                  {d.hms_count > 0 && <div><dt>Alertes de l'imprimante</dt><dd>{d.hms_count}</dd></div>}
                </dl>
              )}
              {d?.reachable && <p className="sub">Prête : choisissez un modèle, préparez-le ; le lancement se confirme sur une page du serveur.</p>}
            </>
          )}
          <p className="sub live-stamp" aria-live="off">{d ? `${stale ? 'Service injoignable : dernière mise à jour' : 'Mis à jour'} à ${fmtTime(lastOk.current)}` : ''}</p>
        </div>
        <Snapshot printer={printer} printing={printing} />
      </div>
    </section>
  );
}
