import { PageHeader } from '../../components';
import { InsightsCosts } from '../InsightsCosts';
import { SettingsNav } from './SettingsNav';
import '../insights.css';

/** Réglages › Tarifs (ex-Coûts › « Prix et coût ») : prix, puissances, usure et estimation d'un coût. C'est un réglage, pas un rapport. */
export function TariffsPage() {
  return (
    <>
      <PageHeader title="Tarifs" help="Vos prix de matière, d'électricité et d'usure : ils servent à estimer le coût de chaque impression. Les hypothèses sont toujours indiquées."
        crumbs={[{ label: 'Réglages', to: '/reglages' }, { label: 'Tarifs' }]} below={<SettingsNav />} />
      <InsightsCosts />
    </>
  );
}
