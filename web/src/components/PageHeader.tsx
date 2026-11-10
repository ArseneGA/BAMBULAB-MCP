import type { ReactNode } from 'react';
import { Breadcrumb } from './Breadcrumb';
import type { Crumb } from '../nav';

/**
 * En-tête de page standard (G10 UX1) : fil d'Ariane (dès la profondeur 2), titre h1, phrase d'aide d'UNE ligne,
 * UNE action principale à droite (bouton plein ; les autres sont secondaires), puis les onglets ou la navigation de section.
 * Remplace le surtitre « Espace » et les h1 écrits à la main. Un seul h1 par écran.
 */
export function PageHeader({ title, help, actions, crumbs, below }: { title: ReactNode; help?: ReactNode; actions?: ReactNode; crumbs?: Crumb[]; below?: ReactNode }) {
  return (
    <header className="page-head">
      <Breadcrumb items={crumbs} />
      <div className="page-head-row">
        <div className="page-head-text">
          <h1>{title}</h1>
          {help && <p className="sub">{help}</p>}
        </div>
        {actions && <div className="head-acts">{actions}</div>}
      </div>
      {below}
    </header>
  );
}
