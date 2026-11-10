import { useEffect, useRef, useState } from 'react';
import { ExternalLink, Plus, Printer as PrinterIcon } from 'lucide-react';
import { Button, EmptyState, ProgressBar, Tag } from '../components';
import { api, assetUrl } from '../api/client';
import { useAsync } from '../api/hooks';
import type { PrinterStatus, PrinterView } from '../api/printTypes';
import { Async, Notice, fmtDate } from './common';
import { AddPrinter } from './Print';
import { CameraPanel } from './CameraPanel';
import { printerPill } from '@shared/labels';
import './screens.css';

/** Vrai tant que l'imprimante est en train d'imprimer (ou en pause) : la barre d'avancement s'affiche alors. */
export const isPrinting = (d?: PrinterStatus) => !!d && d.reachable && (d.state === 'printing' || d.state === 'paused' || ['RUNNING', 'PAUSE'].includes((d.gcode_state ?? '').toUpperCase()));

/** Lecture régulière de l'état d'une imprimante (toutes les 5 s, comme l'ancienne carte). */
export function usePrinterStatus(printerId: string | undefined, everyMs = 5000) {
  const st = useAsync((s) => (printerId ? api.printerStatus(printerId, s) : Promise.resolve(undefined)), [printerId]);
  const reload = useRef(st.reload); reload.current = st.reload;
  useEffect(() => { if (!printerId) return; const t = window.setInterval(() => reload.current(), everyMs); return () => window.clearInterval(t); }, [printerId, everyMs]);
  return st;
}

function PrinterPanel({ p }: { p: PrinterView }) {
  const st = usePrinterStatus(p.id);
  const d = st.data;
  const pill = printerPill(d, !!st.error);
  return (
    <section className="panel prn" aria-label={`Imprimante ${p.name}`}>
      <div className="panel-h">
        <div className="prn-id"><div className="ico"><PrinterIcon aria-hidden /></div><div><h3>{p.name}</h3><div className="sub mono">{p.host}</div></div></div>
        <span className={`pill ${pill.cls}`} role="status"><i />{pill.label}</span>
      </div>
      <dl className="facts">
        <div><dt>Modèle</dt><dd>{p.model_name}</dd></div>
        <div><dt>Numéro de série</dt><dd className="mono">{p.serial}</dd></div>
        <div><dt>Buse</dt><dd className="mono">{p.nozzle_mm} mm</dd></div>
        <div><dt>AMS</dt><dd>{p.ams_units > 0 ? `${p.ams_units} unité${p.ams_units > 1 ? 's' : ''}` : 'aucun'}</dd></div>
        <div><dt>Version du logiciel</dt><dd className="mono">{d?.firmware ?? p.firmware ?? 'inconnue'}</dd></div>
        <div><dt>Code d'accès</dt><dd>{p.access_code_set ? <Tag tone="ok">enregistré</Tag> : <Tag tone="warn">à saisir</Tag>}</dd></div>
      </dl>
      {p.ams_units === 0 && <p className="sub prn-note">Aucun AMS déclaré : la bobine est chargée à la main, l'atelier la suit avec vous (voir Matières).</p>}
      {!p.access_code_set && p.setup_url && <Notice title="Code d'accès non saisi" actions={<a className="btn btn-secondary btn-sm" href={assetUrl(p.setup_url)} target="_blank" rel="noreferrer"><ExternalLink size={13} /> Ouvrir la page de saisie</a>}>La page locale du serveur affiche l'empreinte du certificat présenté par l'imprimante et recueille le code d'accès ; il n'est jamais renvoyé ni affiché dans cette interface. Tant que rien n'est épinglé, aucune connexion n'est tentée (navigateur enregistré requis).</Notice>}
      {isPrinting(d) && d && <div className="prn-live"><ProgressBar large label={`Impression en cours sur ${p.name}`} source={{ state: 'running', percent: d.progress_percent, remaining_minutes: d.remaining_minutes, phase_label: d.total_layers > 0 ? `${d.current_file ? d.current_file + ' · ' : ''}couche ${d.layer} sur ${d.total_layers}` : d.current_file }} /></div>}
      {p.access_code_set && <CameraPanel printerId={p.id} />}
      <div className="prn-status" role="status">
        {st.error && !d ? <span className="hint">Statut indisponible : {st.error.message}</span> : !d ? <span className="hint">Lecture de l'état…</span> : (
          <>
            {d.display && <p className="hint">{d.display}</p>}
            {d.explanation && <p className="hint">{d.explanation}</p>}
            {d.error && <p className="hint">{d.error}</p>}
            {d.reachable && <p className="hint mono">buse {Math.round(d.temps.nozzle)}/{Math.round(d.temps.nozzle_target)} °C · plateau {Math.round(d.temps.bed)}/{Math.round(d.temps.bed_target)} °C{d.hms_count > 0 && ` · ${d.hms_count} alerte(s) de l'imprimante`}</p>}
            {d.updated_at && <p className="hint">Relevé à {fmtDate(d.updated_at)} : un état ancien n'autorise jamais un lancement.</p>}
          </>
        )}
      </div>
    </section>
  );
}

export function PrintersPage() {
  const printers = useAsync((s) => api.printers(s), []);
  const [addOpen, setAddOpen] = useState(false);
  const n = printers.data?.printers.length ?? 0;
  return (
    <>
      <div className="page-head">
        <div><h1>Imprimantes</h1><div className="sub">{printers.data ? (n === 0 ? 'Aucune machine enregistrée' : `${n} machine${n > 1 ? 's' : ''} sur le réseau local`) : 'Registre des machines'}</div></div>
        <div className="head-acts"><Button variant="primary" icon={<Plus size={16} />} onClick={() => setAddOpen(true)}>Ajouter une imprimante</Button></div>
      </div>
      <Async state={printers} label="Chargement du registre" empty={(d) => d.printers.length === 0 ? <EmptyState title="Aucune imprimante enregistrée" text="Ajoutez votre imprimante (modèle, adresse sur le LAN, numéro de série) ; le code d'accès se saisit ensuite sur une page locale, jamais ici." action={<Button size="sm" variant="primary" icon={<Plus size={13} />} onClick={() => setAddOpen(true)}>Ajouter une imprimante</Button>} /> : null}>
        {(d) => (
          <div className="grid g2">
            {d.printers.map((p) => <PrinterPanel key={p.id} p={p} />)}
            <button type="button" className="panel add" onClick={() => setAddOpen(true)}><Plus aria-hidden /><span>Ajouter une autre Bambu Lab</span><small>X1, P1P, A1, A1 mini… toutes les FDM</small></button>
          </div>
        )}
      </Async>
      <AddPrinter open={addOpen} onClose={() => setAddOpen(false)} onAdded={() => { setAddOpen(false); printers.reload(); }} />
    </>
  );
}
