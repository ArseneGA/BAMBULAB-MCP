import { Link, NavLink, Outlet } from 'react-router-dom';
import { Boxes, SlidersHorizontal, GitCompare, Layers, Printer, Disc3, Moon, Sun, Palette, Trash2, Gauge, Shapes } from 'lucide-react';
import { IconButton, StatusDot, ToastProvider } from './components';
import type { Status } from './components';
import { useTheme } from './design/theme';
import { ConnectionProvider, useConnection } from './api/hooks';
import { EnrollGate, ServerBanner } from './pages/common';
import './shell.css';

export const spaces = [
  { path: 'bibliotheque', label: 'Bibliothèque', icon: Boxes, live: true },
  { path: 'preparation', label: 'Préparation', icon: SlidersHorizontal, live: true },
  { path: 'comparaison', label: 'Comparaison', icon: GitCompare, live: true },
  { path: 'materiaux', label: 'Matériaux', icon: Layers, live: true },
  { path: 'impression', label: 'Impression', icon: Printer, live: true },
  { path: 'bobines', label: 'Bobines et file', icon: Disc3, live: true },
  { path: 'tableau-de-bord', label: 'Tableau de bord', icon: Gauge, live: true },
  { path: 'gabarits', label: 'Gabarits', icon: Shapes, live: true },
] as const;

function Pills() {
  const c = useConnection();
  const server: Status = c.server === 'online' ? 'online' : c.server === 'checking' ? 'unavailable' : 'offline';
  const win: Status = c.windows === 'online' ? 'online' : 'offline';
  const cad: Status = c.cad === 'online' ? 'online' : 'unavailable';
  return (
    <>
      <span className="pill" title={c.server === 'online' ? `Service bambud ${c.health?.version ?? ''}` : 'Service bambud'}><StatusDot status={server} label={c.server === 'checking' ? 'Connexion' : 'Linux'} /></span>
      {c.browserEnrolled === false && <a className="pill enroll" href="/enroll" title="Les pages de confirmation d'impression exigent un navigateur enregistré (code à usage unique)">Enregistrer ce navigateur</a>}
      <Link to="/agents" className="pill" title={c.agents.length === 0 ? 'Aucun agent Windows appairé' : c.agents.map((a) => `${a.name} : ${a.online ? 'en ligne' : 'hors ligne'}${a.studio_running ? ', Bambu Studio ouvert' : a.studio_installed ? ', Bambu Studio installé' : ''}`).join(' ; ')}><StatusDot status={win} label={c.agents.length === 0 ? 'Windows' : c.agents.length === 1 ? `Windows : ${c.agents[0].name}` : `Windows ×${c.agents.length}`} /></Link>
      <span className="pill" title={c.cad === 'online' ? 'Backend CAO (STEP) disponible' : (c.cadNote ?? 'Backend CAO absent : les fichiers STEP ne sont pas convertis')}><StatusDot status={cad} label="CAO" /></span>
    </>
  );
}

/** Sans session de navigateur ni jeton, l'API répond 401 : l'écran d'enregistrement remplace les pages. */
function Main() {
  const c = useConnection();
  return <main className="main">{c.authRequired ? <EnrollGate /> : <Outlet />}</main>;
}

export function Shell() {
  const { theme, toggle } = useTheme();
  return (
    <ToastProvider>
      <ConnectionProvider>
        <div className="app">
          <ServerBanner />
          <header className="topbar">
            <NavLink to="/" className="brand" end><span className="brand-mark" aria-hidden />Atelier</NavLink>
            <nav className="nav" aria-label="Espaces de travail">
              {spaces.map(({ path, label, icon: I, live }) => (
                <NavLink key={path} to={`/${path}`} title={live ? label : `${label} (bientôt branché)`} aria-label={live ? label : `${label}, bientôt branché`}>
                  <I size={15} aria-hidden /><span className="nav-label">{label}</span>{!live && <span className="nav-soon" aria-hidden />}
                </NavLink>
              ))}
            </nav>
            <div className="topbar-end">
              <Pills />
              <NavLink to="/corbeille" aria-label="Corbeille et doublons" title="Corbeille et doublons" className="icon-btn"><Trash2 size={16} /></NavLink>
              <NavLink to="/design" aria-label="Système de design" title="Système de design" className="icon-btn"><Palette size={16} /></NavLink>
              <IconButton label={theme === 'light' ? 'Thème sombre' : 'Thème clair'} onClick={toggle}>{theme === 'light' ? <Moon size={16} /> : <Sun size={16} />}</IconButton>
            </div>
          </header>
          <Main />
        </div>
      </ConnectionProvider>
    </ToastProvider>
  );
}
