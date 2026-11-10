import { ArrowUpRight, BellRing, Fingerprint, KeyRound, Monitor, ShieldCheck } from 'lucide-react';
import { PageHeader } from '../../components';
import { useConnection } from '../../api/hooks';
import { SettingsNav } from './SettingsNav';
import '../screens.css';

/**
 * Pages du serveur réservées à l'humain (leurs adresses ne changent pas : les liens et courriels déjà envoyés restent valides).
 * Cette page n'affiche AUCUN secret : elle ouvre seulement ces pages, où l'on saisit ou régénère code, passkey et canaux.
 */
const LINKS = [
  { href: '/setup/launch-code', icon: KeyRound, title: 'Code de lancement personnel', text: 'Le code que vous saisissez pour approuver un lancement, en plus de votre navigateur enregistré.' },
  { href: '/setup/passkeys', icon: Fingerprint, title: 'Passkeys', text: 'Approuver un lancement avec l\'empreinte, le visage ou la clé de sécurité de cet appareil.' },
  { href: '/setup/notifications', icon: BellRing, title: 'Notifications', text: 'Canaux qui vous préviennent d\'une fin d\'impression ou d\'un problème (les secrets restent sur le serveur).' },
  { href: '/enroll', icon: ShieldCheck, title: 'Enregistrer ce navigateur', text: 'Nécessaire pour approuver un lancement : un code à usage unique s\'obtient sur la machine du service.' },
  { href: '/agents/pair', icon: Monitor, title: 'Appairer un agent Windows', text: 'Relier un poste Windows (dossiers indexés, Bambu Studio) à Printalk.' },
] as const;

export function SecurityPage() {
  const c = useConnection();
  return (
    <>
      <PageHeader title="Sécurité" help="Ces pages sont servies par Printalk lui-même : lancer une impression exige toujours votre accord explicite."
        crumbs={[{ label: 'Réglages', to: '/reglages' }, { label: 'Sécurité' }]} below={<SettingsNav />} />
      <p className="set-enroll"><KeyRound size={15} aria-hidden />{c.browserEnrolled === false ? 'Ce navigateur n\'est pas encore enregistré.' : c.browserEnrolled ? 'Ce navigateur est enregistré.' : 'Enregistrement du navigateur : état inconnu.'}</p>
      <ul className="shortcuts" aria-label="Pages de sécurité">
        {LINKS.map((l) => {
          const I = l.icon;
          return <li key={l.href}><a href={l.href} className="panel shortcut"><span className="ico"><I aria-hidden /></span><span className="t"><b>{l.title}</b><small>{l.text}</small></span><ArrowUpRight size={15} aria-hidden /></a></li>;
        })}
      </ul>
    </>
  );
}
