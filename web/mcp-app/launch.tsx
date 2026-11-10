import { useCallback, useEffect, useRef, useState } from 'react';
import { AlertTriangle, Check, Lock } from 'lucide-react';
import type { ConfirmationView, SafetyView } from '../src/api/printTypes';

/**
 * Confirmation de lancement et d'arrêt DANS la vue MCP App, par CODE DE LANCEMENT PERSONNEL (docs/SECURITY.md § 12).
 *
 * Modèle de menace : le serveur ne distingue pas un appel de la vue d'un appel du modèle. La sécurité repose sur le
 * code, secret de l'humain (6 à 8 chiffres, défini sur la page /setup/launch-code), pas sur cette vue. En conséquence :
 *  - le code n'est jamais gardé : effacé du champ dès l'envoi, jamais écrit dans le DOM, un journal, le contexte du
 *    modèle (aucun updateModelContext / sendMessage ici) ni un lien ;
 *  - une mise en garde permanente rappelle de ne jamais le taper dans la conversation ;
 *  - sans code défini (ou verrouillé), la vue retombe sur le lien du navigateur, toujours disponible.
 */

export interface ToolResultLike { structuredContent?: unknown; content?: Array<{ type: string; text?: string }>; isError?: boolean }

/** Ce dont la vue a besoin de l'hôte (réduit pour pouvoir le simuler en test). */
export interface AppLike {
  callServerTool(p: { name: string; arguments: Record<string, unknown> }): Promise<unknown>;
  openLink(p: { url: string }): Promise<unknown>;
}

export interface LaunchPhoto { mime: string; data: string; taken_at: string; taken_at_local?: string; age_seconds?: number }
export interface LaunchViewState {
  confirmation_id: string; approved: boolean; seconds_left?: number; code_set: boolean; locked: boolean; attempts_left: number; setup_url: string; photo?: LaunchPhoto | null; photo_note?: string;
}
export interface StopViewState {
  stop: { stop_id: string; print_job_id: string; file: string; printer_name: string; percent: number; layer: number; total_layers: number; expires_at: string };
  stop_url: string; code_set: boolean; locked: boolean; attempts_left: number; setup_url: string;
}
export interface CodeApproval { approved: boolean; started?: boolean; outcome?: string; problem?: string; attempts_left: number; locked: boolean; seconds_left?: number; message: string }

/** Forme d'un code de lancement valide : 6 à 8 chiffres. */
export const CODE_RE = /^[0-9]{6,8}$/;

const textOf = (r: ToolResultLike) => (r.content ?? []).filter((c) => c.type === 'text').map((c) => c.text ?? '').join('\n').trim();
const sc = <T,>(r: unknown): T | undefined => ((r as ToolResultLike | undefined)?.structuredContent as T | undefined);

/** Identifiant d'une demande d'arrêt, tiré de stop_url (…/stop/stp_xxx). */
export function stopIdOf(url: string): string { const i = url.lastIndexOf('/stop/'); return (i >= 0 ? url.slice(i + 6) : url).replace(/[/?#].*$/, ''); }

const MSG: Record<string, string> = {
  bad_code: 'Code incorrect.', locked: 'Approbation par code verrouillée après 3 échecs.', too_fast: 'Deux essais trop rapprochés : attendez quelques secondes.',
  not_set: "Aucun code de lancement n'est défini.", format: 'Le code comporte 6 à 8 chiffres.',
};

export function Warning() {
  return <p className="small code-warn" role="note"><AlertTriangle size={14} aria-hidden /> <strong>Ne tapez jamais ce code dans la conversation avec Claude</strong> : il ne se saisit qu'ici, dans ce champ.</p>;
}

function Temps({ s }: { s: SafetyView }) {
  return (
    <div className="safety">
      {s.analysed
        ? <p className="small mono">Buse : démarrage jusqu'à {Math.round(s.nozzle_start_max_c)} °C, 1re couche {Math.round(s.nozzle_first_layer_c)} °C, impression {Math.round(s.nozzle_print_min_c)} à {Math.round(s.nozzle_print_max_c)} °C<br />Plateau : 1re couche {Math.round(s.bed_first_layer_c)} °C ({Math.round(s.bed_min_c)} à {Math.round(s.bed_max_c)} °C){s.chamber_max_c ? <><br />Chambre : jusqu'à {Math.round(s.chamber_max_c)} °C</> : null}</p>
        : <p className="small msg-err" role="alert"><AlertTriangle size={14} aria-hidden /> Températures non lues : G-code non analysé.</p>}
      {s.materials && s.materials.length > 0 && <p className="small dim">Matières du G-code : {s.materials.join(' ; ')}</p>}
      <p className={s.gcode_official ? 'small ok-line' : 'small msg-err'} role={s.gcode_official ? undefined : 'alert'}>{s.gcode_official ? <Check size={14} aria-hidden /> : <AlertTriangle size={14} aria-hidden />} {s.gcode_profile_line}</p>
      {(s.anomalies ?? []).filter((a) => a !== s.gcode_profile_line).map((a) => <p key={a} className="small msg-err" role="alert"><AlertTriangle size={14} aria-hidden /> Anomalie du G-code : {a}</p>)}
    </div>
  );
}

function Summary({ c }: { c: ConfirmationView }) {
  const b = c.binding;
  return (
    <>
      <dl className="dims mono confirm">
        <div><dt>Fichier</dt><dd>{b.file}</dd></div>
        <div><dt>Imprimante</dt><dd>{b.printer_name} ({b.printer_model})</dd></div>
        <div><dt>Empreinte</dt><dd>{b.sha256.slice(0, 16)}…</dd></div>
        <div><dt>Plaque</dt><dd>{b.plate}</dd></div>
        <div><dt>Matières</dt><dd>{b.materials.map((m) => `f${m.filament_slot} → ${m.tray} : ${m.material_id}`).join(' ; ') || '—'}</dd></div>
        {b.estimated_minutes ? <div><dt>Durée estimée</dt><dd>{Math.round(b.estimated_minutes)} min{b.estimated_grams ? ` · ${b.estimated_grams.toFixed(1)} g` : ''}</dd></div> : null}
        <div><dt>Mise à niveau du plateau</dt><dd>{b.bed_leveling === false ? <strong>non (désactivée pour cette impression)</strong> : 'oui (automatique au démarrage)'}</dd></div>
        {b.flow_cali !== undefined && <div><dt>Calibration du débit</dt><dd>{b.flow_cali ? 'oui' : <strong>non (désactivée pour cette impression)</strong>}</dd></div>}
        {b.vibration_cali !== undefined && <div><dt>Calibration des vibrations</dt><dd>{b.vibration_cali ? 'oui' : <strong>non (désactivée pour cette impression)</strong>}</dd></div>}
        {b.timelapse !== undefined && <div><dt>Timelapse</dt><dd>{b.timelapse ? <strong>oui (enregistré pendant l'impression)</strong> : 'non'}</dd></div>}
        <div><dt>Valable jusqu'à</dt><dd>{c.expires_at_local || new Date(c.expires_at).toLocaleTimeString('fr-FR')}</dd></div>
      </dl>
      {c.safety && <Temps s={c.safety} />}
      {(b.alerts ?? []).map((a) => <p key={a} className="small msg-err" role="alert"><AlertTriangle size={14} aria-hidden /> Alerte matière : {a}</p>)}
      {(b.adhesion_warnings ?? []).map((a) => <p key={a} className="small msg-err" role="alert"><AlertTriangle size={14} aria-hidden /> <strong>Risque de décollement.</strong> {a}</p>)}
      {(b.overhang_warnings ?? []).map((a) => <p key={a} className="small msg-err" role="alert"><AlertTriangle size={14} aria-hidden /> <strong>Surplombs sans supports.</strong> {a}</p>)}
      {(b.slender_warnings ?? []).map((a) => <p key={a} className="small msg-err" role="alert"><AlertTriangle size={14} aria-hidden /> <strong>Troncs ou piliers trop élancés.</strong> {a}</p>)}
    </>
  );
}

/** Champ de code + bouton. onSubmit reçoit le code ; le champ est vidé AVANT l'appel. */
export function CodeForm({ label, button, needPlate, busy, onSubmit }: { label: string; button: string; needPlate: boolean; busy: boolean; onSubmit: (code: string, plate: boolean) => void }) {
  const [code, setCode] = useState('');
  const [plate, setPlate] = useState(false);
  const ok = CODE_RE.test(code) && (!needPlate || plate) && !busy;
  const go = () => { if (!ok) return; const typed = code; setCode(''); onSubmit(typed, plate); };
  return (
    <form className="codeform" onSubmit={(e) => { e.preventDefault(); go(); }} autoComplete="off">
      {needPlate && <label className="check"><input type="checkbox" checked={plate} onChange={(e) => setPlate(e.target.checked)} /> Le plateau est vide et dégagé, l'imprimante est surveillée.</label>}
      <label className="codefield">{label}
        <input type="password" name="launch-pin" inputMode="numeric" pattern="[0-9]{6,8}" minLength={6} maxLength={8} autoComplete="off" spellCheck={false} data-lpignore="true" value={code} aria-label={label}
          onChange={(e) => setCode(e.target.value.replace(/[^0-9]/g, '').slice(0, 8))} />
      </label>
      <div className="acts"><button type="submit" className="primary" disabled={!ok}>{button}</button></div>
    </form>
  );
}

export function outcomeMessage(r: CodeApproval): string {
  if (r.approved) return r.message;
  const base = MSG[r.problem ?? ''] ?? r.message;
  return r.problem === 'bad_code' && !r.locked ? `${base} Essais restants avant verrouillage : ${r.attempts_left}.` : base;
}

/** Lien de secours (toujours disponible) : ouvre la page du navigateur enregistré. */
export function BrowserLink({ app, url, text }: { app: AppLike; url: string; text: string }) {
  const [err, setErr] = useState<string>();
  const open = async () => {
    setErr(undefined);
    try { const r = await app.openLink({ url }); if ((r as { isError?: boolean } | undefined)?.isError) setErr("L'hôte a refusé d'ouvrir le lien : copiez l'adresse ci-dessous dans votre navigateur."); }
    catch { setErr("Ouverture de lien indisponible dans cet hôte : copiez l'adresse ci-dessous dans votre navigateur."); }
  };
  return (
    <>
      <div className="acts"><button type="button" className="primary" onClick={open}>{text}</button></div>
      <p className="small dim">Adresse : <span className="mono">{url}</span></p>
      {err && <p className="msg msg-err" role="alert"><AlertTriangle size={14} aria-hidden /> {err}</p>}
    </>
  );
}

const originOf = (u: string) => { try { return new URL(u).origin; } catch { return ''; } };

/** Confirmation de lancement : résumé, températures, photo, case « plateau vide », code, bouton Lancer. */
export function LaunchConfirm({ app, first, pollMs = 3000 }: { app: AppLike; first: ConfirmationView; pollMs?: number }) {
  const [c, setC] = useState(first);
  const [ls, setLs] = useState<LaunchViewState | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<{ ok: boolean; text: string }>();
  const [photoBusy, setPhotoBusy] = useState(false);
  const alive = useRef(true);
  const expired = new Date(c.expires_at).getTime() < Date.now();
  const approved = c.approved || ls?.approved === true;
  const setupUrl = ls?.setup_url || `${originOf(c.confirm_url)}/setup/launch-code`;

  const [tick, setTick] = useState(() => Date.now());
  const photoRecvAt = useRef(Date.now());

  /** fresh=true : « Reprendre la photo » force une nouvelle prise de vue (sinon le serveur réutilise une photo de moins de 15 s). */
  const refresh = useCallback(async (withPhoto: boolean, fresh = false) => {
    try {
      const r = await app.callServerTool({ name: 'get_launch_view_state', arguments: { confirmation_id: first.confirmation_id, with_photo: withPhoto, ...(fresh ? { fresh_photo: true } : {}) } });
      const s = sc<LaunchViewState>(r);
      if (alive.current && s && !(r as ToolResultLike).isError) {
        if (withPhoto) { photoRecvAt.current = Date.now(); setTick(Date.now()); }
        setLs((prev) => ({ ...s, photo: withPhoto ? s.photo : prev?.photo, photo_note: withPhoto ? s.photo_note : prev?.photo_note }));
      }
    } catch { /* l'état reste inconnu : la vue retombe sur le lien du navigateur */ }
  }, [app, first.confirmation_id]);

  useEffect(() => { alive.current = true; void refresh(true); return () => { alive.current = false; }; }, [refresh]);
  useEffect(() => { const t = window.setInterval(() => setTick(Date.now()), 5000); return () => window.clearInterval(t); }, []);
  useEffect(() => {
    if (approved) return;
    const t = window.setInterval(() => { void refresh(false); }, pollMs);
    return () => window.clearInterval(t);
  }, [approved, refresh, pollMs]);

  const submit = async (code: string, plate: boolean) => {
    setBusy(true); setNote(undefined);
    try {
      const res = await app.callServerTool({ name: 'approve_print_with_code', arguments: { confirmation_id: c.confirmation_id, code, plate_clear: plate } });
      const out = sc<CodeApproval>(res);
      if (!out || (res as ToolResultLike).isError) { setNote({ ok: false, text: textOf(res as ToolResultLike) || 'Refusé.' }); return; }
      setNote({ ok: out.approved, text: outcomeMessage(out) });
      if (out.approved) setC((x) => ({ ...x, approved: true }));
      setLs((s) => (s ? { ...s, attempts_left: out.attempts_left, locked: out.locked } : s));
    } catch (e) { setNote({ ok: false, text: (e as Error).message }); } finally { setBusy(false); }
  };
  const reloadPhoto = async () => { setPhotoBusy(true); await refresh(true, true); setPhotoBusy(false); };
  const ageS = ls?.photo ? Math.max(0, Math.round((ls.photo.age_seconds ?? Math.max(0, (photoRecvAt.current - new Date(ls.photo.taken_at).getTime()) / 1000)) + (tick - photoRecvAt.current) / 1000)) : 0;
  const ageLabel = ageS < 90 ? `il y a ${ageS} s` : ageS < 5400 ? `il y a ${Math.round(ageS / 60)} min` : `il y a ${Math.round(ageS / 3600)} h`;

  const codeUsable = ls?.code_set === true && !ls.locked;
  return (
    <>
      <header className="head"><h1>Confirmation de lancement</h1></header>
      <Summary c={c} />
      {ls?.photo ? (
        <figure className="plate">
          <img src={`data:${ls.photo.mime};base64,${ls.photo.data}`} alt="Photo du plateau prise par la caméra de l'imprimante" />
          <figcaption className="small dim">Prise à {ls.photo.taken_at_local || new Date(ls.photo.taken_at).toLocaleTimeString('fr-FR')}, <strong>{ageLabel}</strong>. Vérifiez que le plateau est vide et dégagé ; si vous venez de le dégager, reprenez la photo. <button type="button" className="linkbtn" disabled={photoBusy} onClick={reloadPhoto}>Reprendre la photo</button></figcaption>
        </figure>
      ) : ls?.photo_note ? <p className="small dim">{ls.photo_note}</p> : null}
      {approved ? (
        <p className="msg" role="status"><Check size={14} aria-hidden /> Accord enregistré, pour cette impression seulement. Le lancement part tout seul (rien à dire à Claude) : le résultat s'affiche ci-dessous.</p>
      ) : expired ? (
        <p className="msg msg-err" role="alert"><AlertTriangle size={14} aria-hidden /> Confirmation expirée : demandez-en une nouvelle.</p>
      ) : codeUsable ? (
        <>
          <p className="small">Vérifiez le résumé, cochez « plateau vide », saisissez votre code de lancement personnel puis lancez. L'accord ne vaut que pour cette impression et 2 minutes.</p>
          <CodeForm label="Code de lancement (6 à 8 chiffres)" button="Lancer l'impression" needPlate busy={busy} onSubmit={submit} />
          <Warning />
          {ls && ls.attempts_left < 3 && <p className="small dim">Essais restants avant verrouillage : {ls.attempts_left}.</p>}
          <details className="alt"><summary className="small">Utiliser le navigateur à la place</summary>
            <BrowserLink app={app} url={c.confirm_url} text="Ouvrir la page de confirmation" /></details>
        </>
      ) : (
        <>
          {ls?.locked ? <p className="msg msg-err" role="alert"><Lock size={14} aria-hidden /> Approbation par code verrouillée après 3 échecs. Déverrouillez-la sur la page <span className="mono">/setup/launch-code</span> (navigateur enregistré) ou avec <span className="mono">bambud launch-code --unlock</span>.</p> : null}
          {ls && !ls.code_set && !ls.locked ? (
            <p className="msg msg-err" role="status"><AlertTriangle size={14} aria-hidden /> Définissez votre code de lancement sur <span className="mono">{setupUrl}</span> (navigateur enregistré) pour lancer directement depuis cette carte ; cette carte se mettra à jour d'elle-même. En attendant, utilisez le lien du navigateur ci-dessous.</p>
          ) : null}
          {ls && !ls.code_set && !ls.locked && <BrowserLink app={app} url={setupUrl} text="Ouvrir la page du code de lancement" />}
          <BrowserLink app={app} url={c.confirm_url} text="Ouvrir la page de confirmation" />
          <p className="small dim">Dans le navigateur, vous cochez « plateau vide » et confirmez sur la page locale (sans code).</p>
          {ls?.locked && <BrowserLink app={app} url={setupUrl} text="Ouvrir la page du code de lancement" />}
        </>
      )}
      {note && <p className={note.ok ? 'msg' : 'msg msg-err'} role={note.ok ? 'status' : 'alert'}>{note.ok ? <Check size={14} aria-hidden /> : <AlertTriangle size={14} aria-hidden />} {note.text}</p>}
    </>
  );
}

/** Validation d'arrêt : même mécanisme, sans photo ni case « plateau vide ». */
export function StopConfirm({ app, stopUrl, question, pollMs = 5000 }: { app: AppLike; stopUrl: string; question?: string; pollMs?: number }) {
  const stopId = stopIdOf(stopUrl);
  const [st, setSt] = useState<StopViewState | null>(null);
  const [gone, setGone] = useState(false);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<{ ok: boolean; text: string }>();
  const [stopped, setStopped] = useState(false);
  const refresh = useCallback(async () => {
    try {
      const r = await app.callServerTool({ name: 'get_stop_view_state', arguments: { stop_id: stopId } });
      if ((r as ToolResultLike).isError) { setGone(true); return; }
      const s = sc<StopViewState>(r); if (s) setSt(s);
    } catch { /* inconnu */ }
  }, [app, stopId]);
  useEffect(() => { void refresh(); }, [refresh]);
  useEffect(() => { if (stopped || gone) return; const t = window.setInterval(() => { void refresh(); }, pollMs); return () => window.clearInterval(t); }, [stopped, gone, refresh, pollMs]);

  const submit = async (code: string) => {
    setBusy(true); setNote(undefined);
    try {
      const res = await app.callServerTool({ name: 'approve_stop_with_code', arguments: { stop_id: stopId, code } });
      const out = sc<CodeApproval>(res);
      if (!out || (res as ToolResultLike).isError) { setNote({ ok: false, text: textOf(res as ToolResultLike) || 'Refusé.' }); return; }
      setNote({ ok: out.approved, text: outcomeMessage(out) });
      if (out.approved) setStopped(true);
      setSt((s) => (s ? { ...s, attempts_left: out.attempts_left, locked: out.locked } : s));
    } catch (e) { setNote({ ok: false, text: (e as Error).message }); } finally { setBusy(false); }
  };
  const usable = st?.code_set === true && !st.locked;
  return (
    <>
      <header className="head"><h1>Arrêt d'impression</h1></header>
      {st && (
        <dl className="dims mono confirm">
          <div><dt>Fichier</dt><dd>{st.stop.file}</dd></div>
          <div><dt>Imprimante</dt><dd>{st.stop.printer_name}</dd></div>
          <div><dt>Avancement</dt><dd>{st.stop.percent} % (couche {st.stop.layer} / {st.stop.total_layers})</dd></div>
        </dl>
      )}
      {question && <p className="small">{question}</p>}
      {stopped ? <p className="msg" role="status"><Check size={14} aria-hidden /> Impression arrêtée.</p>
        : gone ? <p className="msg msg-err" role="alert"><AlertTriangle size={14} aria-hidden /> Cette demande d'arrêt est expirée ou l'impression n'est plus en cours.</p>
        : usable ? (
          <>
            <p className="small msg-err"><AlertTriangle size={14} aria-hidden /> L'arrêt est définitif : le travail en cours est perdu.</p>
            <CodeForm label="Code de lancement (6 à 8 chiffres)" button="Arrêter l'impression" needPlate={false} busy={busy} onSubmit={(code) => submit(code)} />
            <Warning />
            <details className="alt"><summary className="small">Utiliser le navigateur à la place</summary><BrowserLink app={app} url={stopUrl} text="Ouvrir la page d'arrêt" /></details>
          </>
        ) : (
          <>
            {st?.locked ? <p className="msg msg-err" role="alert"><Lock size={14} aria-hidden /> Approbation par code verrouillée : déverrouillez-la sur <span className="mono">/setup/launch-code</span> ou utilisez la page d'arrêt.</p> : null}
            <BrowserLink app={app} url={stopUrl} text="Ouvrir la page d'arrêt" />
          </>
        )}
      {note && <p className={note.ok ? 'msg' : 'msg msg-err'} role={note.ok ? 'status' : 'alert'}>{note.ok ? <Check size={14} aria-hidden /> : <AlertTriangle size={14} aria-hidden />} {note.text}</p>}
    </>
  );
}
