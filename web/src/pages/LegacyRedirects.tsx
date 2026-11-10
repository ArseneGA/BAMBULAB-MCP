import { useEffect } from 'react';
import { Navigate, useLocation, useNavigate } from 'react-router-dom';
import { api } from '../api/client';
import { legacyTarget } from '../redirects';
import { Skeleton } from '../components';

/**
 * Route générique des anciennes URL (G10 UX2) : redirige (replace) vers la nouvelle destination en CONSERVANT les paramètres.
 * `/comparaison?experiment=ID` lit d'abord l'essai pour connaître son modèle ; en cas d'échec, repli sur la bibliothèque.
 */
export function LegacyRedirect() {
  const { pathname, search } = useLocation();
  const target = legacyTarget(pathname, search);
  if (!target) return <Navigate to="/" replace />; // ne devrait pas arriver : seules les routes de la table pointent ici
  if ('to' in target) return <Navigate to={target.to} replace />;
  return <LegacyCompare experiment={target.lookupExperiment} fallback={target.fallback} />;
}

function LegacyCompare({ experiment, fallback }: { experiment: string; fallback: string }) {
  const nav = useNavigate();
  useEffect(() => {
    const ac = new AbortController();
    api.experiment(experiment, ac.signal).then(
      (x) => nav(`/modele/${encodeURIComponent(x.model_id)}/essais/${encodeURIComponent(experiment)}`, { replace: true }),
      (e) => { if ((e as Error).name !== 'AbortError') nav(fallback, { replace: true }); },
    );
    return () => ac.abort();
  }, [experiment, fallback, nav]);
  return <Skeleton lines={2} label="Ouverture de l'essai de réglages" />;
}
