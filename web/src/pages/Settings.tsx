import { Link } from 'react-router-dom';
import { ArrowUpRight, Monitor, Palette, Shapes, Trash2, Layers, KeyRound } from 'lucide-react';
import { Segmented, StatusDot } from '../components';
import type { Status } from '../components';
import { useTheme } from '@shared/theme';
import type { Theme } from '@shared/theme';
import { useConnection } from '../api/hooks';
import { LOGO_URL, PRODUCT_NAME, TAGLINE } from '../brand';
import './screens.css';

const LINKS = [
  { to: '/agents', icon: Monitor, title: 'Agent Windows', text: 'Dossiers Windows indexés, travaux confiés à l\'agent, appairage.' },
  { to: '/corbeille', icon: Trash2, title: 'Corbeille et doublons', text: 'Modèles supprimés récupérables, doublons exacts du catalogue.' },
  { to: '/materiaux', icon: Layers, title: 'Matériaux et machines', text: 'Modèles d\'imprimantes Bambu connus et fiches de matières.' },
  { to: '/gabarits', icon: Shapes, title: 'Gabarits et calibration', text: 'Pièces paramétriques, variantes, jeux de calibration.' },
  { to: '/design', icon: Palette, title: 'Système de design', text: 'Jetons, composants et états de l\'interface, clair et sombre.' },
] as const;

/** Réglages : apparence, état du service et accès aux écrans annexes (agent Windows, corbeille, design…). */
export function SettingsPage() {
  const { theme, setTheme } = useTheme();
  const c = useConnection();
  const server: Status = c.server === 'online' ? 'online' : c.server === 'checking' ? 'unavailable' : 'offline';
  const win: Status = c.windows === 'online' ? 'online' : c.windows === 'unused' ? 'unused' : c.windows === 'unavailable' ? 'unavailable' : 'offline';
  const cad: Status = c.cad === 'online' ? 'online' : 'unavailable';
  return (
    <>
      <div className="page-head"><div><h1>Réglages</h1><div className="sub">Apparence, état du service et écrans annexes.</div></div></div>
      <div className="grid g2">
        <section className="panel" aria-label="Apparence">
          <div className="panel-h"><h3>Apparence</h3></div>
          <div className="set-brand"><img src={LOGO_URL} alt="" width={48} height={48} /><div><b>{PRODUCT_NAME}</b><div className="sub">{TAGLINE}</div></div></div>
          <Segmented<Theme> label="Thème" value={theme} onChange={setTheme} options={[{ value: 'light', label: 'Clair' }, { value: 'dark', label: 'Sombre' }]} />
          <p className="sub">Le choix est mémorisé dans ce navigateur ; par défaut, celui du système.</p>
        </section>
        <section className="panel" aria-label="État du service">
          <div className="panel-h"><h3>Service</h3></div>
          <div className="set-status">
            <StatusDot status={server} label={c.server === 'online' ? `bambud ${c.health?.version ?? ''}`.trim() : c.server === 'checking' ? 'Connexion…' : 'bambud injoignable'} />
            <StatusDot status={win} label={c.windows === 'online' ? 'Agent Windows en ligne' : c.windows === 'unused' ? 'Windows : non utilisé' : c.windows === 'unavailable' ? 'Agent Windows : état inconnu' : 'Agent Windows hors ligne'} />
            <StatusDot status={cad} label={c.cad === 'online' ? 'Backend CAO disponible' : 'Backend CAO absent (STEP)'} />
          </div>
          {c.cad !== 'online' && c.cadNote && <p className="sub">{c.cadNote}</p>}
          {c.server === 'online' && !c.importEnabled && c.importNote && <p className="sub">{c.importNote}</p>}
          <p className="set-enroll"><KeyRound size={15} aria-hidden />{c.browserEnrolled === false ? <a href="/enroll">Enregistrer ce navigateur</a> : c.browserEnrolled ? 'Ce navigateur est enregistré.' : 'Enregistrement du navigateur : état inconnu.'}</p>
        </section>
      </div>
      <h2 className="shortcuts-h">Écrans annexes</h2>
      <ul className="shortcuts" aria-label="Écrans annexes">
        {LINKS.map((l) => {
          const I = l.icon;
          return <li key={l.to}><Link to={l.to} className="panel shortcut"><span className="ico"><I aria-hidden /></span><span className="t"><b>{l.title}</b><small>{l.text}</small></span><ArrowUpRight size={15} aria-hidden /></Link></li>;
        })}
      </ul>
    </>
  );
}
