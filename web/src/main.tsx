import { Component, StrictMode, lazy } from 'react';
import type { ReactNode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter, Route, Routes } from 'react-router-dom';
import '@shared/tokens.css';
import '@shared/fonts.css';
import '@shared/base.css';
import { Shell } from './Shell';
import { Button, ErrorState } from './components';
import { Home } from './pages/Home';
import { PrintersPage } from './pages/Printers';
import { SettingsPage } from './pages/settings/Appearance';
import { TariffsPage } from './pages/settings/Tariffs';
import { SecurityPage } from './pages/settings/Security';
import { SuiviPage } from './pages/settings/Suivi';
import { PrintSheetPage } from './pages/impressions/PrintSheet';
import { AdvancedPage } from './pages/settings/Advanced';
import { Library } from './pages/Library';
import { ModelPage } from './pages/Model';
import { TrashPage } from './pages/Trash';
import { ImpressionsPage, NewPrintPage } from './pages/Impressions';
import { AgentsPage } from './pages/Agents';
import { MatieresPage } from './pages/Matieres';
import { DesignPage } from './pages/Design';
import { GabaritsPage } from './pages/Gabarits';
import { NotFoundPage } from './pages/NotFound';
import { LegacyRedirect } from './pages/LegacyRedirects';
import { LEGACY } from './redirects';
import './pages/screens.css'; // en dernier : ses règles d'écran priment sur celles des composants
const PreparePage = lazy(() => import('./pages/Prepare').then((m) => ({ default: m.PreparePage })));
const ComparePage = lazy(() => import('./pages/Compare').then((m) => ({ default: m.ComparePage })));
const ExperimentsPage = lazy(() => import('./pages/Compare').then((m) => ({ default: m.ExperimentsPage })));
const ProjectsPage = lazy(() => import('./pages/Projects').then((m) => ({ default: m.ProjectsPage })));
const IssuesPage = lazy(() => import('./pages/Issues').then((m) => ({ default: m.IssuesPage })));

/** Évite la page blanche si un écran ou un chunk différé échoue au chargement. */
class Boundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  render() {
    if (!this.state.failed) return this.props.children;
    return <div style={{ padding: 24 }}><ErrorState title="Cet écran n'a pas pu se charger" text="Un fichier de l'interface est introuvable ou a échoué." impact="Le reste de Printalk n'est pas touché, et aucune donnée n'est perdue." action={<div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}><Button onClick={() => location.reload()}>Recharger</Button><a className="btn btn-secondary" href="/">Retour à l'accueil</a></div>} /></div>;
  }
}

/**
 * Routes de l'interface (G10). La table de navigation est web/src/nav.ts ; les anciennes URL sont dans web/src/redirects.ts
 * (une <Route> par ancienne URL, qui conserve les paramètres). Une route inconnue affiche « Introuvable » (plus de retour silencieux à l'accueil).
 * Aucune route ne peut commencer par un préfixe réservé au serveur (/setup, /confirm, /stop, /enroll, /oauth, /api, /mcp, /health…).
 */
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Boundary>
      <BrowserRouter>
        <Routes>
            <Route element={<Shell />}>
              <Route index element={<Home />} />
              <Route path="projets" element={<ProjectsPage />} />
              <Route path="bibliotheque" element={<Library />} />
              <Route path="bibliotheque/gabarits" element={<GabaritsPage />} />
              <Route path="bibliotheque/corbeille" element={<TrashPage />} />
              <Route path="modele/:id" element={<ModelPage />} />
              <Route path="modele/:id/preparer" element={<PreparePage />} />
              <Route path="modele/:id/essais" element={<ExperimentsPage />} />
              <Route path="modele/:id/essais/:exp" element={<ComparePage />} />
              <Route path="impressions" element={<ImpressionsPage />} />
              <Route path="impressions/nouvelle" element={<NewPrintPage />} />
              <Route path="impressions/:id" element={<PrintSheetPage />} />
              <Route path="imprimantes" element={<PrintersPage />} />
              <Route path="matieres" element={<MatieresPage />} />
              <Route path="reglages" element={<SettingsPage />} />
              <Route path="reglages/tarifs" element={<TariffsPage />} />
              <Route path="reglages/suivi" element={<SuiviPage />} />
              <Route path="reglages/securite" element={<SecurityPage />} />
              <Route path="reglages/agent-windows" element={<AgentsPage />} />
              <Route path="reglages/avance" element={<AdvancedPage />} />
              <Route path="reglages/avance/design" element={<DesignPage />} />
              <Route path="dysfonctionnements" element={<IssuesPage />} />
              {LEGACY.map((r) => <Route key={r.from} path={r.from.slice(1)} element={<LegacyRedirect />} />)}
              <Route path="*" element={<NotFoundPage />} />
            </Route>
        </Routes>
      </BrowserRouter>
    </Boundary>
  </StrictMode>,
);
