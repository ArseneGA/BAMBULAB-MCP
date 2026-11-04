import { NavLink, Outlet } from 'react-router-dom';
import { Boxes, Box, SlidersHorizontal, GitCompare, Layers, Printer, Moon, Sun, Palette } from 'lucide-react';
import { IconButton, StatusDot, ToastProvider } from './components';
import { useTheme } from './design/theme';
import { demoAvailability } from './data/demo';
import './shell.css';

export const spaces = [
  { path: 'bibliotheque', label: 'Bibliothèque', icon: Boxes },
  { path: 'modele', label: 'Modèle', icon: Box },
  { path: 'preparation', label: 'Préparation', icon: SlidersHorizontal },
  { path: 'comparaison', label: 'Comparaison', icon: GitCompare },
  { path: 'materiaux', label: 'Matériaux', icon: Layers },
  { path: 'impression', label: 'Impression', icon: Printer },
] as const;

export function DemoBanner() {
  return <div className="demo-banner" role="note">Prototype — données de démonstration, aucune action réelle</div>;
}

export function Shell() {
  const { theme, toggle } = useTheme();
  return (
    <ToastProvider>
      <div className="app">
        <DemoBanner />
        <header className="topbar">
          <NavLink to="/" className="brand" end><span className="brand-mark" aria-hidden />Atelier</NavLink>
          <nav className="nav" aria-label="Espaces de travail">
            {spaces.map(({ path, label, icon: I }) => (
              <NavLink key={path} to={`/${path}`} title={label} aria-label={label}><I size={15} aria-hidden /><span className="nav-label">{label}</span></NavLink>
            ))}
          </nav>
          <div className="topbar-end">
            <span className="pill" title="Serveur Linux"><StatusDot status={demoAvailability.linux} label="Linux" /></span>
            <span className="pill" title="Agent Windows"><StatusDot status={demoAvailability.windowsAgent} label="Windows" /></span>
            <span className="pill" title="Imprimante P1S"><StatusDot status={demoAvailability.printer} label="P1S" /></span>
            <NavLink to="/design" aria-label="Système de design" className="icon-btn"><Palette size={16} /></NavLink>
            <IconButton label={theme === 'light' ? 'Thème sombre' : 'Thème clair'} onClick={toggle}>{theme === 'light' ? <Moon size={16} /> : <Sun size={16} />}</IconButton>
          </div>
        </header>
        <main className="main"><Outlet /></main>
      </div>
    </ToastProvider>
  );
}
