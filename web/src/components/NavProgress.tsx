import { useEffect, useState } from 'react';
import { useLocation } from 'react-router-dom';
import { Skeleton } from '@shared/components';

/** Barre fine en haut de l'écran : indéterminée (jamais de pourcentage inventé). */
export function NavProgress({ active }: { active: boolean }) {
  if (!active) return null;
  return <div className="nav-progress" role="progressbar" aria-label="Chargement de la page" />;
}

/** Vrai pendant un bref instant à chaque changement de route (le changement est ainsi visible même quand la page s'affiche aussitôt). */
export function useRouteBusy(ms = 450): boolean {
  const { pathname } = useLocation();
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    setBusy(true);
    const t = window.setTimeout(() => setBusy(false), ms);
    return () => window.clearTimeout(t);
  }, [pathname, ms]);
  return busy;
}

/** Repli des écrans chargés à la demande (React.lazy) : barre de chargement + squelette, à la place du plein écran « Chargement ». */
export function RouteFallback() {
  return (
    <>
      <NavProgress active />
      <div className="route-fallback"><Skeleton lines={4} label="Chargement de l'écran" /></div>
    </>
  );
}
