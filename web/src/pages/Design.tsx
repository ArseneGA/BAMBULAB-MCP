import { useState } from 'react';
import { Download, Plus, Trash2, Settings, FileBox, Folder } from 'lucide-react';
import {
  Button, EmptyState, ErrorState, Field, IconButton, Input, List, LoadingState, Panel, SearchInput, Segmented, Select, Slider,
  StatusDot, Tabs, Tag, Toggle, Tooltip, Tree, useToast,
} from '../components';
import { colorTokens, radii, spacingScale } from '@shared/theme';
import { Notice } from './common';
import { useTheme } from '@shared/theme';
import './design.css';

/** Exemples FICTIFS, propres à cette page de spécimen : ils n'existent dans aucun catalogue. */
const specimenModels = [
  { id: 'm01', name: 'Exemple A', format: 'STEP' },
  { id: 'm02', name: 'Exemple B', format: '3MF' },
  { id: 'm03', name: 'Exemple C', format: 'STL' },
  { id: 'm04', name: 'Exemple D', format: 'STL' },
];

function Specimen({ forcedTheme }: { forcedTheme?: 'light' | 'dark' }) {
  const toast = useToast();
  const [seg, setSeg] = useState<'a' | 'b' | 'c'>('a');
  const [tab, setTab] = useState<'one' | 'two'>('one');
  const [on, setOn] = useState(true);
  const [h, setH] = useState(0.2);
  const [q, setQ] = useState('');
  const [sel, setSel] = useState('m01');
  const [node, setNode] = useState('n2');
  return (
    <div className="specimen" data-theme={forcedTheme}>
      <h2>{forcedTheme === 'dark' ? 'Thème sombre' : 'Thème clair'}</h2>

      <section>
        <span className="eyebrow">Couleurs</span>
        <div className="swatches">
          {colorTokens.map((c) => (
            <div key={c} className="swatch"><span style={{ background: `var(${c})` }} /><code>{c}</code></div>
          ))}
        </div>
      </section>

      <section>
        <span className="eyebrow">Typographie</span>
        <h1>Titre de fiche modèle</h1>
        <p>Grotesque pour l'interface, lisible à petite taille. Les valeurs et cotes passent en mono :</p>
        <p className="mono">10.00 × 20.00 × 30.00 mm · 0.20 mm · 9f2c1a0b</p>
      </section>

      <section>
        <span className="eyebrow">Espacement (base 4 px) et rayons</span>
        <div className="row">{spacingScale.map((s) => <div key={s.name} className="sp"><i style={{ width: s.px, height: s.px }} /><code>{s.px}</code></div>)}</div>
        <div className="row">{radii.map((r) => <div key={r} className="sp"><i className="rad" style={{ borderRadius: `var(${r})` }} /><code>{r}</code></div>)}</div>
      </section>

      <section>
        <span className="eyebrow">Boutons</span>
        <div className="row">
          <Button variant="primary" icon={<Plus size={14} />}>Importer</Button>
          <Button>Secondaire</Button>
          <Button variant="ghost">Fantôme</Button>
          <Button variant="danger" icon={<Trash2 size={14} />}>Mettre à la corbeille</Button>
          <Button variant="primary" disabled>Désactivé</Button>
          <Button disabled>Désactivé</Button>
          <IconButton label="Télécharger"><Download size={16} /></IconButton>
          <IconButton label="Réglages" pressed><Settings size={16} /></IconButton>
          <IconButton label="Indisponible" disabled><Settings size={16} /></IconButton>
        </div>
      </section>

      <section>
        <span className="eyebrow">Champs et sélecteurs</span>
        <div className="grid2">
          <SearchInput value={q} onChange={setQ} placeholder="Rechercher un modèle" />
          <Field label="Collection"><Select options={[{ value: 'a', label: 'Collection A' }, { value: 'b', label: 'Collection B' }]} /></Field>
          <Field label="Nom (invalide)"><Input aria-invalid defaultValue="" placeholder="Obligatoire" /></Field>
          <Field label="Désactivé"><Input disabled value="Lecture seule" readOnly /></Field>
          <Segmented label="Vue" value={seg} onChange={setSeg} options={[{ value: 'a', label: 'Grille' }, { value: 'b', label: 'Liste' }, { value: 'c', label: 'Arbre', disabled: true }]} />
          <Toggle checked={on} onChange={setOn} label="Supports automatiques" />
          <Toggle checked={false} onChange={() => {}} label="Désactivé" disabled />
          <Slider label="Hauteur de couche" value={h} onChange={setH} min={0.08} max={0.32} step={0.02} unit="mm" />
        </div>
      </section>

      <section>
        <span className="eyebrow">Tags, états, infobulle</span>
        <div className="row">
          <Tag>STL</Tag><Tag tone="accent">STEP</Tag><Tag tone="ok">copié sur Linux</Tag><Tag tone="warn">Windows seulement</Tag><Tag tone="err">conflit</Tag>
          <Tooltip text="Infobulle au survol et au focus"><Button size="sm">Survolez-moi</Button></Tooltip>
        </div>
        <div className="row">
          <StatusDot status="online" /><StatusDot status="offline" /><StatusDot status="unavailable" /><StatusDot status="conflict" />
        </div>
      </section>

      <section>
        <span className="eyebrow">Onglets, liste, arbre, panneau</span>
        <Tabs label="Exemple" value={tab} onChange={setTab} tabs={[{ value: 'one', label: 'Fichiers' }, { value: 'two', label: 'Versions' }]} />
        <div className="grid2">
          <Panel title="Liste" flush>
            <List label="Modèles" items={specimenModels} getKey={(m) => m.id} selectedKey={sel} onSelect={(m) => setSel(m.id)}
              render={(m) => <><FileBox size={14} aria-hidden /><span style={{ flex: 1 }}>{m.name}</span><Tag>{m.format}</Tag></>} />
          </Panel>
          <Panel title="Arbre" flush>
            <Tree label="Collections" selectedId={node} onSelect={setNode} nodes={[
              { id: 'n1', label: <><Folder size={14} aria-hidden />Collection A</>, children: [{ id: 'n2', label: 'Sous-collection 1' }, { id: 'n3', label: 'Sous-collection 2' }] },
              { id: 'n4', label: <><Folder size={14} aria-hidden />Collection B</> },
            ]} />
          </Panel>
        </div>
      </section>

      <section>
        <span className="eyebrow">Toast et états</span>
        <div className="row">
          <Button size="sm" onClick={() => toast('ok', 'Message de succès (exemple).')}>Toast succès</Button>
          <Button size="sm" onClick={() => toast('err', "Message d'erreur (exemple).")}>Toast erreur</Button>
        </div>
        <div className="grid2">
          <EmptyState title="Aucun modèle" text="Aucun résultat pour cette recherche." action={<Button size="sm">Réinitialiser</Button>} />
          <LoadingState label="Indexation en cours" />
          <ErrorState title="Fichier illisible" text="L'archive 3MF est invalide." action={<Button size="sm">Réessayer</Button>} />
        </div>
      </section>
    </div>
  );
}

export function DesignPage() {
  const { theme, setTheme } = useTheme();
  return (
    <>
      <div className="page-head">
        <span className="eyebrow">Système de design</span>
        <h1>Tokens et composants</h1>
        <p>Les deux thèmes sont affichés côte à côte ; la bascule change le thème de toute l'application.</p>
        <div><Segmented label="Thème de l'application" value={theme} onChange={setTheme} options={[{ value: 'light', label: 'Clair' }, { value: 'dark', label: 'Sombre' }]} /></div>
      </div>
      <Notice tone="info" title="Page de spécimen : valeurs fictives">Les noms, cotes et messages ci-dessous illustrent les composants du système de design. Aucun ne provient du catalogue, d'une imprimante ou d'une bobine.</Notice>
      <div className="specimens">
        <Specimen forcedTheme="light" />
        <Specimen forcedTheme="dark" />
      </div>
    </>
  );
}
