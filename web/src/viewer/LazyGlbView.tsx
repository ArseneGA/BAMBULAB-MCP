import { lazy, Suspense } from 'react';
import type { GlbViewProps } from './GlbView';
import './viewer.css';

const View = lazy(() => import('./GlbView').then((m) => ({ default: m.GlbView })));

/** Three.js n'est téléchargé que par les écrans qui affichent un aperçu réel. */
export function GlbView(props: GlbViewProps) {
  return (
    <Suspense fallback={<div className={['v3', props.compact && 'v3-compact', props.className].filter(Boolean).join(' ')}><div className="v3-overlay" role="status" aria-busy="true"><span className="spinner" aria-hidden /><span>Chargement du viewer 3D</span></div></div>}>
      <View {...props} />
    </Suspense>
  );
}
