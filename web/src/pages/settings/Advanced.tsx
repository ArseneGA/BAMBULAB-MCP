import { Link } from 'react-router-dom';
import { ArrowUpRight, Bug, Palette } from 'lucide-react';
import { PageHeader, Panel, StatusDot } from '../../components';
import type { Status } from '../../components';
import { useConnection } from '../../api/hooks';
import { PRODUCT_NAME } from '../../brand';
import { SettingsNav } from './SettingsNav';
import '../screens.css';

/** Réglages › Avancé : état détaillé du service, version, et les écrans réservés aux développeurs (dysfonctionnements, système de design). */
export function AdvancedPage() {
  const c = useConnection();
  const server: Status = c.server === 'online' ? 'online' : c.server === 'checking' ? 'unavailable' : 'offline';
  const win: Status = c.windows === 'online' ? 'online' : c.windows === 'unused' ? 'unused' : c.windows === 'unavailable' ? 'unavailable' : 'offline';
  const cad: Status = c.cad === 'online' ? 'online' : 'unavailable';
  return (
    <>
      <PageHeader title="Avancé" help="État détaillé du service, version, et écrans destinés au suivi technique."
        crumbs={[{ label: 'Réglages', to: '/reglages' }, { label: 'Avancé' }]} below={<SettingsNav />} />
      <div className="stack">
        <Panel title="État du service">
          <div className="set-status">
            <StatusDot status={server} label={c.server === 'online' ? `${PRODUCT_NAME} ${c.health?.version ?? ''}`.trim() + ' : en ligne' : c.server === 'checking' ? 'Connexion…' : `${PRODUCT_NAME} est injoignable`} />
            <StatusDot status={win} label={c.windows === 'online' ? 'Agent Windows en ligne' : c.windows === 'unused' ? 'Windows : non utilisé (le mode Linux seul est normal)' : c.windows === 'unavailable' ? 'Agent Windows : état inconnu' : 'Agent Windows hors ligne'} />
            <StatusDot status={cad} label={c.cad === 'online' ? 'Fichiers STEP : conversion disponible' : 'Fichiers STEP : conversion non installée'} />
          </div>
          {c.cad !== 'online' && c.cadNote && <p className="sub">{c.cadNote}</p>}
          {c.server === 'online' && !c.importEnabled && c.importNote && <p className="sub">{c.importNote}</p>}
          <p className="sub">Version : {c.health?.version ?? 'inconnue'}</p>
        </Panel>
        <ul className="shortcuts" aria-label="Écrans de suivi technique">
          <li><Link to="/dysfonctionnements" className="panel shortcut"><span className="ico"><Bug aria-hidden /></span><span className="t"><b>Dysfonctionnements</b><small>Carnet de bord tenu par Claude : ce qui a mal fonctionné, à corriger.</small></span><ArrowUpRight size={15} aria-hidden /></Link></li>
          <li><Link to="/reglages/avance/design" className="panel shortcut"><span className="ico"><Palette aria-hidden /></span><span className="t"><b>Système de design</b><small>Jetons, composants et états de l'interface, clair et sombre.</small></span><ArrowUpRight size={15} aria-hidden /></Link></li>
        </ul>
      </div>
    </>
  );
}
