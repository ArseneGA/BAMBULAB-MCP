import { Link } from 'react-router-dom';
import { FolderKanban } from 'lucide-react';
import { EmptyState, PageHeader } from '../components';

/**
 * Projets : page d'attente de G10 U1. Les projets (G9, PJ2) arrivent dans une autre vague ;
 * en attendant, l'entrée du menu existe et affiche un état vide propre (ce que c'est, pourquoi c'est vide, une action).
 * Quand `Projects.tsx` de G9 est fusionnée, la route `projets` de main.tsx pointe vers elle et ce fichier disparaît.
 */
export function ProjectsPlaceholder() {
  return (
    <>
      <PageHeader title="Projets" help="Un projet regroupe les pièces d'un même objet et suit son avancement." />
      <EmptyState title="Aucun projet pour l'instant"
        text="Les projets arrivent bientôt : ils réuniront les pièces d'un même objet (par exemple une étagère et ses supports) et diront combien de pièces sont imprimées. En attendant, vos modèles restent dans la bibliothèque."
        action={<Link className="btn btn-primary btn-sm" to="/bibliotheque"><FolderKanban size={13} aria-hidden />Ouvrir la bibliothèque</Link>} />
    </>
  );
}
