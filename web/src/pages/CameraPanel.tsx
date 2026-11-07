import { useEffect, useState } from 'react';
import { Camera, ExternalLink, Video, VideoOff } from 'lucide-react';
import { Button, Tag } from '../components';
import { BASE, CSRF_HEADER } from '../api/client';
import './camera.css';

interface DetectionSettings { enabled: boolean; interval_seconds: number; detector: { threshold: number; consecutive_alerts: number }; }
interface NotifyChannel { name: string; kind: string; enabled: boolean; ready: boolean }
interface NotifyInfo { channels: NotifyChannel[]; setup_url: string }

async function getJSON<T>(path: string): Promise<T | undefined> {
  try {
    const r = await fetch(BASE + path, { credentials: 'same-origin', headers: CSRF_HEADER });
    return r.ok ? ((await r.json()) as T) : undefined;
  } catch { return undefined; }
}

/** Caméra de l'imprimante : instantané, flux relayé par le serveur (une seule connexion à la caméra), détection d'échec
 *  et notifications. Rien ici ne lance ni n'arrête une impression : l'arrêt reste la page humaine /stop. */
export function CameraPanel({ printerId }: { printerId: string }) {
  const [live, setLive] = useState(false);
  const [snap, setSnap] = useState<string>();
  const [failed, setFailed] = useState(false);
  const [det, setDet] = useState<DetectionSettings>();
  const [nt, setNt] = useState<NotifyInfo>();
  useEffect(() => { void getJSON<DetectionSettings>('/api/camera/settings').then(setDet); void getJSON<NotifyInfo>('/api/notifications').then(setNt); }, []);
  useEffect(() => { setLive(false); setSnap(undefined); setFailed(false); }, [printerId]);
  const base = `${BASE}/api/printers/${printerId}/camera`;
  const take = () => { setFailed(false); setLive(false); setSnap(`${base}/snapshot?t=${Date.now()}`); };
  return (
    <div className="cam">
      <div className="cam-bar">
        <strong>Caméra</strong>
        <Button size="sm" icon={<Camera size={13} />} onClick={take}>Instantané</Button>
        <Button size="sm" icon={live ? <VideoOff size={13} /> : <Video size={13} />} onClick={() => { setFailed(false); setSnap(undefined); setLive(!live); }}>{live ? 'Arrêter le flux' : 'Flux en direct'}</Button>
      </div>
      {failed && <p className="hint" role="alert">Caméra indisponible : modèle sans flux JPEG (X1, P2, H2 : RTSP non pris en charge), LAN liveview désactivé sur l'imprimante, ou certificat de la caméra à valider sur <a href={`${BASE}/setup/camera/${printerId}`} target="_blank" rel="noreferrer">la page de validation</a>.</p>}
      {(live || snap) && <img className="cam-view" alt="Image de la caméra de l'imprimante" src={live ? `${base}/stream` : snap} onError={() => setFailed(true)} />}
      <p className="hint">Le flux n'est ouvert que pendant que vous le regardez. Une photo ne prouve pas que le plateau est vide : vous l'attestez sur la page de confirmation.</p>
      <dl className="kvl">
        <dt>Détection d'échec</dt>
        <dd>{det ? <>{det.enabled ? <Tag tone="ok">active</Tag> : <Tag tone="warn">désactivée</Tag>} <span className="mono">1 instantané / {det.interval_seconds} s · seuil {det.detector.threshold}</span> <span className="hint">alerte seulement, jamais d'arrêt automatique (réglage : demandez à Claude <code>configure_failure_detection</code>)</span></> : '…'}</dd>
        <dt>Notifications</dt>
        <dd>{nt ? (nt.channels.length === 0 ? <span className="hint">aucun canal (demandez à Claude <code>configure_notifications</code>)</span> : nt.channels.map((c) => <Tag key={c.name} tone={c.ready && c.enabled ? 'ok' : 'warn'}>{c.name} · {c.kind}{c.ready ? '' : ' · secrets à saisir'}</Tag>)) : '…'}{nt && <> <a href={`${BASE}/setup/notifications`} target="_blank" rel="noreferrer"><ExternalLink size={12} /> saisir les secrets</a></>}</dd>
      </dl>
    </div>
  );
}
