import { Component, StrictMode, Suspense, lazy } from 'react';
import type { ReactNode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom';
import './design/tokens.css';
import './design/base.css';
import { Shell } from './Shell';
import { Button, ErrorState, LoadingState } from './components';
import { Home } from './pages/Home';
import { Library } from './pages/Library';
import { ModelPage } from './pages/Model';
import { TrashPage } from './pages/Trash';
import { MaterialsPage } from './pages/Materials';
import { PrintPage } from './pages/Print';
import { AgentsPage } from './pages/Agents';
import { DesignPage } from './pages/Design';
const PreparePage = lazy(() => import('./pages/Prepare').then((m) => ({ default: m.PreparePage })));
const ComparePage = lazy(() => import('./pages/Compare').then((m) => ({ default: m.ComparePage })));

/** Évite la page blanche si un écran ou un chunk différé échoue au chargement. */
class Boundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  render() {
    if (!this.state.failed) return this.props.children;
    return <div style={{ padding: 24 }}><ErrorState title="Cet écran n'a pas pu se charger" text="Un fichier de l'interface est introuvable ou a échoué. Rechargez la page." action={<Button onClick={() => location.reload()}>Recharger</Button>} /></div>;
  }
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Boundary>
      <BrowserRouter>
        <Suspense fallback={<LoadingState label="Chargement de l'écran" />}>
          <Routes>
            <Route element={<Shell />}>
              <Route index element={<Home />} />
              <Route path="bibliotheque" element={<Library />} />
              <Route path="modele" element={<Navigate to="/bibliotheque" replace />} />
              <Route path="modele/:id" element={<ModelPage />} />
              <Route path="corbeille" element={<TrashPage />} />
              <Route path="preparation" element={<PreparePage />} />
              <Route path="comparaison" element={<ComparePage />} />
              <Route path="materiaux" element={<MaterialsPage />} />
              <Route path="impression" element={<PrintPage />} />
              <Route path="agents" element={<AgentsPage />} />
              <Route path="design" element={<DesignPage />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Route>
          </Routes>
        </Suspense>
      </BrowserRouter>
    </Boundary>
  </StrictMode>,
);
