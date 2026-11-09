import { lazy, Suspense, useMemo, useState } from 'react';
import { httpGcodeSource } from '../api/gcodeApi';

// three.js et le moteur de la visionneuse ne sont téléchargés que si l'utilisateur ouvre le panneau.
const Viewer = lazy(() => import('../viewer/gcode/GcodeViewer').then((m) => ({ default: m.GcodeViewer })));

/**
 * Aperçu 3D couche par couche de la découpe d'une impression (fiche d'impression / Préparer) : même composant que la vue MCP
 * App, alimenté par /api/gcode/*. Fermé par défaut : la lecture du G-code ne se fait qu'à l'ouverture.
 */
export function GcodePanel({ printJobId, plate }: { printJobId: string; plate?: number }) {
  const [open, setOpen] = useState(false);
  const source = useMemo(() => httpGcodeSource({ printJobId, plate }), [printJobId, plate]);
  return (
    <details className="gv-panel" onToggle={(e) => setOpen((e.currentTarget as HTMLDetailsElement).open)}>
      <summary>Aperçu 3D couche par couche de la découpe</summary>
      {open && (
        <Suspense fallback={<p className="hint" role="status">Chargement de la visionneuse…</p>}>
          <Viewer source={source} title="Trajectoires du G-code" />
        </Suspense>
      )}
    </details>
  );
}
