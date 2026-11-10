import { useEffect, useState } from 'react';
import { Button, NumberField, PageHeader, Panel, Toggle, useToast } from '../../components';
import { describeError } from '../../api/client';
import { useAsync } from '../../api/hooks';
import { dashboardApi } from '../../api/dashboardApi';
import type { CameraSettings } from '../../api/dashboardApi';
import { DORMANT_DEFAULT, readDormantDays, writeDormantDays } from '../dashboard/logic';
import { Async } from '../common';
import { SettingsNav } from './SettingsNav';
import '../screens.css';

/** Bornes acceptées (les mêmes que le serveur pour la caméra) ; une valeur hors bornes est refusée avec un message, jamais corrigée en silence. */
export const DORMANT_MAX = 3650;
export const RETENTION_MAX = 3650;
export const CAP_MAX_MB = 1_048_576;
export const dormantError = (n: number | undefined): string | undefined => (n === undefined || !Number.isInteger(n) || n < 1 || n > DORMANT_MAX ? `Entre 1 et ${DORMANT_MAX} jours.` : undefined);
export const retentionError = (n: number | undefined): string | undefined => (n === undefined || !Number.isInteger(n) || n < 0 || n > RETENTION_MAX ? `Entre 0 (illimité) et ${RETENTION_MAX} jours.` : undefined);
export const capError = (n: number | undefined): string | undefined => (n === undefined || !Number.isInteger(n) || n < 0 || n > CAP_MAX_MB ? `Entre 0 (aucun plafond) et ${CAP_MAX_MB} Mo.` : undefined);

/** Seuil « projet dormant » : mémorisé dans ce navigateur (aucune route serveur) ; l'Accueil l'applique dans « À faire ». */
function DormantPanel() {
  const toast = useToast();
  const [saved, setSaved] = useState(readDormantDays);
  const [v, setV] = useState<number | undefined>(saved);
  const err = dormantError(v);
  return (
    <Panel title="Projet dormant">
      <p className="sub">Un projet sans activité depuis ce nombre de jours apparaît dans « À faire » sur l'Accueil.</p>
      <NumberField label="Jours sans activité" unit="jours" value={v} onChange={setV} min={1} max={DORMANT_MAX} step={1} error={err} hint={`Valeur appliquée : ${saved} jours (par défaut ${DORMANT_DEFAULT}). Mémorisée dans ce navigateur.`} />
      <Button variant="primary" disabled={!!err} onClick={() => { writeDormantDays(v!); setSaved(v!); toast('ok', `Réglage enregistré : projet dormant après ${v} jours.`); }}>Enregistrer</Button>
    </Panel>
  );
}

/** Journal photo à chaque 10 % (D8) : réglages existants du serveur (progress_*). La caméra n'est lue qu'en lecture, jamais commandée. */
function PhotosPanel({ s, onSaved }: { s: CameraSettings; onSaved: () => void }) {
  const toast = useToast();
  const [on, setOn] = useState(s.progress_photos);
  const [days, setDays] = useState<number | undefined>(s.progress_retention_days);
  const [cap, setCap] = useState<number | undefined>(s.progress_cap_mb);
  const [busy, setBusy] = useState(false);
  useEffect(() => { setOn(s.progress_photos); setDays(s.progress_retention_days); setCap(s.progress_cap_mb); }, [s]);
  const bad = retentionError(days) ?? capError(cap);
  const save = async () => {
    setBusy(true);
    try { await dashboardApi.saveCameraSettings({ progress_photos: on, progress_retention_days: days!, progress_cap_mb: cap! }); toast('ok', 'Réglage enregistré : photos de paliers.'); onSaved(); }
    catch (e) { toast('err', describeError(e)); } finally { setBusy(false); }
  };
  return (
    <Panel title="Photos de paliers">
      <p className="sub">Pendant une impression, une photo du plateau est rangée à chaque 10 %. Les photos à 100 % et celles des impressions jugées réussies sont toujours conservées.</p>
      <Toggle checked={on} onChange={setOn} label="Prendre une photo à chaque 10 %" />
      <NumberField label="Conservation des photos intermédiaires" unit="jours" value={days} onChange={setDays} min={0} max={RETENTION_MAX} step={1} error={retentionError(days)} hint={`0 = illimité. Valeur appliquée : ${s.progress_retention_days === 0 ? 'illimité' : `${s.progress_retention_days} jours`}.`} />
      <NumberField label="Plafond de volume" unit="Mo" value={cap} onChange={setCap} min={0} max={CAP_MAX_MB} step={1} error={capError(cap)} hint={`0 = aucun plafond. Valeur appliquée : ${s.progress_cap_mb === 0 ? 'aucun plafond' : `${s.progress_cap_mb} Mo`}.`} />
      <Button variant="primary" disabled={!!bad || busy} onClick={save}>{busy ? 'Enregistrement…' : 'Enregistrer'}</Button>
    </Panel>
  );
}

/** Réglages › Suivi : seuil « projet dormant » et conservation des photos de paliers. */
export function SuiviPage() {
  const cam = useAsync((s) => dashboardApi.cameraSettings(s), []);
  return (
    <>
      <PageHeader title="Suivi" help="Quand un projet est dormant, et combien de temps garder les photos prises pendant les impressions."
        crumbs={[{ label: 'Réglages', to: '/reglages' }, { label: 'Suivi' }]} below={<SettingsNav />} />
      <div className="stack">
        <DormantPanel />
        <Async state={cam} label="Chargement des réglages des photos">{(s) => <PhotosPanel s={s} onSaved={cam.reload} />}</Async>
      </div>
    </>
  );
}
