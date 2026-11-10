import { PageHeader, Panel, Segmented } from '../../components';
import { useTheme } from '@shared/theme';
import type { Theme } from '@shared/theme';
import { LOGO_URL, PRODUCT_NAME, TAGLINE } from '../../brand';
import { SettingsNav } from './SettingsNav';
import '../screens.css';

/** Réglages › Apparence (page d'accueil des réglages) : thème clair ou sombre, mémorisé dans ce navigateur. */
export function SettingsPage() {
  const { theme, setTheme } = useTheme();
  return (
    <>
      <PageHeader title="Réglages" help="Apparence, tarifs, sécurité, agent Windows et état du service." below={<SettingsNav />} />
      <Panel title="Apparence">
        <div className="set-brand"><img src={LOGO_URL} alt="" width={48} height={48} /><div><b>{PRODUCT_NAME}</b><div className="sub">{TAGLINE}</div></div></div>
        <Segmented<Theme> label="Thème" value={theme} onChange={setTheme} options={[{ value: 'light', label: 'Clair' }, { value: 'dark', label: 'Sombre' }]} />
        <p className="sub">Le choix est mémorisé dans ce navigateur ; par défaut, celui du système.</p>
      </Panel>
    </>
  );
}
