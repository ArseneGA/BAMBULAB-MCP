import { lazy, Suspense } from 'react';
import { LoadingState } from '../components';
import type { Viewer3DProps } from './Viewer3D';
import './viewer.css';

export type { CameraState } from './Viewer3D';

const Viewer = lazy(() => import('./Viewer3D').then((m) => ({ default: m.Viewer3D })));

/** Charge three.js à la demande : seules les pages qui affichent un viewer le téléchargent. */
export function Viewer3D(props: Viewer3DProps) {
  return (
    <Suspense fallback={<div className={['v3', 'v3-loading', props.compact && 'v3-compact', props.className].filter(Boolean).join(' ')}><LoadingState label="Chargement du viewer 3D" /></div>}>
      <Viewer {...props} />
    </Suspense>
  );
}
