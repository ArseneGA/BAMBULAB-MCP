import { SectionNav } from '../../components';

/** Sous-pages de Réglages : tout ce qui se règle une fois. */
export function SettingsNav() {
  return (
    <SectionNav label="Sections des réglages" items={[
      { to: '/reglages', label: 'Apparence' },
      { to: '/reglages/tarifs', label: 'Tarifs' },
      { to: '/reglages/suivi', label: 'Suivi' },
      { to: '/reglages/securite', label: 'Sécurité' },
      { to: '/reglages/agent-windows', label: 'Agent Windows' },
      { to: '/reglages/avance', label: 'Avancé', end: false },
    ]} />
  );
}
