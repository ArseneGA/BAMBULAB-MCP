import { Link } from 'react-router-dom';
import { ClipboardCheck } from 'lucide-react';
import { api } from '../api/client';
import { useAsync } from '../api/hooks';
import type { PrintJob } from '../api/printTypes';
import { Notice } from './common';

/** Impression terminée : le retour d'expérience se saisit dans la comparaison de l'expérience du modèle. */
export function CompletedBox({ job }: { job: PrintJob }) {
  const exps = useAsync((s) => (job.model_id ? api.experiments(job.model_id, s) : Promise.resolve({ experiments: [] })), [job.model_id]);
  const last = exps.data?.experiments[0];
  return (
    <Notice tone="info" title="Impression terminée" actions={last ? <Link className="btn btn-primary btn-sm" to={`/modele/${encodeURIComponent(last.model_id)}/essais/${encodeURIComponent(last.id)}`}><ClipboardCheck size={13} aria-hidden /> Enregistrer le résultat réel</Link> : undefined}>
      {last ? 'Consignez ce que la pièce a réellement donné (réussite, mesures, réglages) pour nourrir la mémoire des essais : le formulaire est dans les essais de réglages de ce modèle.'
        : exps.loading ? "Recherche de l'essai de ce modèle…" : "Aucun essai de réglages n'existe pour ce modèle : le retour d'expérience se rattache à une variante comparée. Créez-en un depuis la préparation pour pouvoir consigner un résultat."}
    </Notice>
  );
}
