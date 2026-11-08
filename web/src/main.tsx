import { Component, StrictMode, Suspense, lazy } from 'react';
import type { ReactNode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom';
import './design/tokens.css';
import './design/base.css';
import { Shell } from './Shell';
import { Button, ErrorState, LoadingState } from './components';
import { Home } from './pages/Home';
import { PrintersPage } from './pages/Printers';
import { SettingsPage } from './pages/Settings';
import { Library } from './pages/Library';
import { ModelPage } from './pages/Model';
import { TrashPage } from './pages/Trash';
import { MaterialsPage } from './pages/Materials';
import { PrintPage } from './pages/Print';
import { AgentsPage } from './pages/Agents';
import { FilamentPage } from './pages/Filament';
import { DesignPage } from './pages/Design';
import { GabaritsPage } from './pages/Gabarits';
import './pages/screens.css'; // en dernier : ses règles d'écran priment sur celles des composants
const PreparePage = lazy(() => import('./pages/Prepare').then((m) => ({ default: m.PreparePage })));
const ComparePage = lazy(() => import('./pages/Compare').then((m) => ({ default: m.ComparePage })));
const InsightsPage = lazy(() => import('./pages/Insights').then((m) => ({ default: m.InsightsPage })));

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
              <Route path="impressions" element={<PrintPage />} />
              <Route path="impression" element={<Navigate to="/impressions" replace />} />
              <Route path="imprimantes" element={<PrintersPage />} />
              <Route path="couts" element={<InsightsPage />} />
              <Route path="tableau-de-bord" element={<Navigate to="/couts" replace />} />
              <Route path="reglages" element={<SettingsPage />} />
              <Route path="bobines" element={<FilamentPage />} />
              <Route path="file" element={<FilamentPage initial="file" />} />
              <Route path="gabarits" element={<GabaritsPage />} />
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
