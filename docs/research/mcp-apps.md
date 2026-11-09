# MCP Apps (extension MCP) — consultée le 2026-11-06

Légende : [V] vérifié · [D] déduction · [?] inconnue.
Sources : https://modelcontextprotocol.io/extensions/apps/overview ; https://github.com/modelcontextprotocol/ext-apps (README, `specification/2026-01-26/apps.mdx`, `examples/threejs-server`) ; https://claude.com/docs/connectors/building/mcp-apps/{getting-started,quickstart.md,troubleshooting.md} ; `npm view`.

## 1. Paquet et versions
- [V] Paquet npm : **`@modelcontextprotocol/ext-apps`**. `npm view` (2026-11-06) : `latest` = **2.0.4** (modifié 2026-10-27) ; dist-tag `release-2.0` = 2.0.3. Release GitHub latest : v2.0.4. Engines node ≥ 20.
- [V] Peer deps de la 2.0.4 : `zod ^4.2.0`, `@modelcontextprotocol/server ^2.0.0`, `@modelcontextprotocol/client ^2.0.0`, `@modelcontextprotocol/core ^2.0.0` (nouveau SDK MCP v2), React optionnel.
- [V] Ligne **1.x** (doc Claude : vérifiée avec 1.7.5) se combine avec `@modelcontextprotocol/sdk` 1.x : `npm install @modelcontextprotocol/ext-apps@^1`. Choisir 2.x (SDK v2, exemple threejs) OU 1.x (SDK 1.x) de façon cohérente.
- [V] Sous-modules : `/server` (registerAppTool, registerAppResource, RESOURCE_MIME_TYPE), racine (classe `App`, PostMessageTransport), `/react` (`useApp`, `useHostStyles`), `/app-bridge` (côté hôte).
- Spec : `specification/2026-01-26/apps.mdx`.

## 2. Déclarer la ressource UI et lier l'outil
- [V] URI en `ui://…` ; mimeType **`text/html;profile=mcp-app`** (constante `RESOURCE_MIME_TYPE`). Le contenu est une page HTML (souvent bundle monofichier).
- [V] Lien outil : `_meta: { ui: { resourceUri: "ui://…" } }` sur la définition de l'outil (l'ancienne clé plate `ui/resourceUri` est aussi émise par le helper). `visibility?: ["model","app"]` (défaut les deux) : `["app"]` cache l'outil au modèle mais l'UI peut l'appeler.
- Exemple de la doc Claude (quickstart) :
  ```js
  registerAppTool(server, 'roll_dice', { title:'Roll dice', description:'…', inputSchema:{…},
    _meta: { ui: { resourceUri: DICE_UI } } },
    async ({sides,count}) => ({ content:[{type:'text',text:'…'}], structuredContent:{ sides,count,rolls,total } }));
  registerAppResource(server, 'Dice view', DICE_UI, { description:'…' }, async () => ({
    contents:[{ uri: DICE_UI, mimeType: RESOURCE_MIME_TYPE, text: DICE_HTML,
      _meta:{ ui:{ csp:{ resourceDomains:['https://unpkg.com'] } } } }] }));
  ```
- [V] Exemple Three.js (ext-apps/examples/threejs-server/server.ts, SDK v2) : `registerAppTool(server,"show_threejs_scene",{…, _meta:{ui:{resourceUri}}}, …)` + `registerAppResource(server, resourceUri, resourceUri, {mimeType: RESOURCE_MIME_TYPE}, …)` qui lit `dist/mcp-app.html` (build Vite monofichier). Three.js r181 est **bundlé** dans le HTML (pas de CDN), globals `THREE`, `OrbitControls`, `canvas`, `width`, `height`. Il réunit React + `useApp`.

## 3. API côté UI (classe `App`)
- [V] Création + handlers AVANT connect :
  ```js
  import { App } from "@modelcontextprotocol/ext-apps";
  const app = new App({ name: "My App", version: "1.0.0" });
  app.ontoolresult = (result) => { /* result.structuredContent, result.content */ };
  await app.connect();   // sans argument de transport : le SDK détecte l'hôte
  ```
  Les handlers `ontoolinput`, `ontoolresult` ne sont pas appelés avant `connect()`. Oublier `connect()` → app invisible (cause n°1 selon la doc de dépannage).
- [V] Handlers/méthodes vus : `ontoolinput({arguments})`, `ontoolinputpartial` (streaming des arguments), `ontoolresult`, `onhostcontextchanged`, `getHostContext()`, `callServerTool({name, arguments})` (retourne le résultat de l'outil, proxifié par l'hôte), `requestDisplayMode({mode})`, `sendSizeChanged`, et côté protocole : `ui/message`, `ui/open-link`, `ui/update-model-context`, `ui/request-display-mode`, `ui/notifications/size-changed` (auto via ResizeObserver, `autoResize` par défaut), `ui/notifications/tool-input|tool-input-partial|tool-result|host-context-changed`.
- [V] `HostContext` (spec) : `theme: "light"|"dark"`, `styles.variables` (variables CSS standardisées) + `styles.css.fonts`, `displayMode` (`inline|fullscreen|pip`), `availableDisplayModes`, `containerDimensions` (largeur/hauteur fixes ou `maxWidth`/`maxHeight`), `toolInfo`. Réagir via `app.onhostcontextchanged`.
- [V] Un tool call depuis l'UI = `tools/call` standard transmis au serveur MCP. Résultats > ~150 000 caractères (claude.ai/Desktop, sandbox code actif) : l'UI reçoit un pointeur de fichier au lieu du contenu → rester léger, paginer.

## 4. CSP, domaines, permissions
- [V] Sur la ressource : `_meta.ui.csp = { connectDomains[] (fetch/XHR/WebSocket → connect-src), resourceDomains[] (scripts, images, styles, fonts, media ; wildcards sous-domaine `https://*.exemple.com`), frameDomains[] (frame-src), baseUriDomains[] (base-uri) }` ; vide/omis = aucune connexion externe (défaut sûr, frame-src 'none'). `_meta.ui.permissions` (micro, caméra…) : à ne pas supposer accordées. Sandbox iframe : `allow-scripts allow-same-origin`.
- [V] Pour Claude : champ `_meta.ui.domain` optionnel = 32 premiers hex du SHA-256 de l'URL du serveur + `.claudemcpcontent.com`. **Non disponible pour un serveur local stdio** (« local connectors have no URL to hash »).
- [D] Impact Bambu : le navigateur de l'iframe ne peut pas joindre l'imprimante (LAN, TCP) → toute I/O passe par `callServerTool` ; inutile de déclarer connectDomains. Bundler Three.js et les loaders dans le HTML évite tout resourceDomains.

## 5. Tester localement dans Claude Desktop
- [V] Paramètres > Développeur > Edit Config → `claude_desktop_config.json`, serveur en stdio (le doc Claude liste des exemples) :
  ```json
  { "mcpServers": { "map": { "command": "npx", "args": ["-y", "@modelcontextprotocol/server-map", "--stdio"] } } }
  ```
  Puis redémarrer, ou menu Developer > Reload MCP Configuration. Dev local d'un exemple : `"command":"bash","args":["-c","cd …/threejs-server && npm run build >&2 && node dist/index.js --stdio"]` (logs vers stderr !). Claude demande la permission d'afficher l'app (Allow / Always allow).
- [V] Pour déboguer : Aide > Dépannage > Enable Developer Mode, puis Ctrl+Shift+I (Win) ; l'app est dans l'iframe interne (iframe imbriquée). Le MCP Inspector / hôte `examples/basic-host` existe aussi (non essayé).
- [V] Un serveur HTTP local n'est pas joignable par claude.ai (appel depuis l'infra Anthropic) → tunnel ou `mcp-remote` ; en revanche stdio local fonctionne avec Desktop.
- [V] Claude Code n'affiche pas l'UI (texte seul) ; hôtes supportés : Claude, Claude Desktop, VS Code Copilot, M365 Copilot, Goose, Postman, MCPJam, Archestra.

## 6. Limites et pièges connus
- [V] Résultat d'outil > ~150k car. ; iOS : pas de `Referer` cross-origin (filtrer sur `Origin`) ; iframe hauteur 0 si `sendSizeChanged` avec hauteur 0 ; pas de transport pour `ui.domain` en stdio.
- [V] L'`App` est facultatif (protocole postMessage utilisable directement).
- [?] Non lu : guide design Claude (modes d'affichage, mobile, variables de style) — URL https://claude.com/docs/connectors/building/mcp-apps/design-guidelines.md ; limites de taille du HTML de la ressource ; WebGL dans l'iframe de Desktop sur Windows (l'exemple Three.js suggère que ça marche, non testé ici) ; persistance d'état (réhydratation à la réouverture d'une conversation).
- [D] Pour notre atelier 3D : construire l'UI en bundle monofichier (Vite + vite-plugin-singlefile) ; un outil `show_*` (visibilité modèle) renvoie `structuredContent` léger + outils `visibility:["app"]` pour charger le maillage/les mises à jour d'état par morceaux (< 150k car. par résultat ; STL/3mf en base64 découpé ou par id) ; polling état imprimante via `callServerTool`.

## 7. Diagnostic : la vue 3D n'apparaît pas dans Claude Desktop via `npx mcp-remote` (2026-11-07, backlog #3)

Expérience : vrai `mcp-remote` (npx) lancé en stdio contre un bambud de test (`--api-token` + catalogue `seed-demo`), piloté comme le ferait Claude Desktop (`initialize` avec `capabilities.extensions["io.modelcontextprotocol/ui"]`, puis `tools/list`, `resources/list`, `resources/read ui://bambu/model.html`, `tools/call search_models`).

| Contrôle | Résultat |
|---|---|
| `tools/list` expose `_meta.ui.resourceUri` | **oui** : `search_models` → `ui://bambu/search.html`, `get_model_details` → `ui://bambu/model.html`, `request_print_confirmation`, `show_atelier` ; clé plate historique `ui/resourceUri` émise aussi ; `get_mesh_chunk` / `get_print_confirmation_view` en `visibility:["app"]` |
| `resources/list` | quatre ressources `ui://bambu/*`, MIME `text/html;profile=mcp-app`, `_meta.ui = {csp:{}, prefersBorder:true}` |
| `resources/read ui://bambu/model.html` à travers mcp-remote | **oui, intact** : 1 047 815 caractères reçus (bundle complet) ; mcp-remote ne modifie ni `_meta` ni le contenu des ressources, c'est un simple relais JSON-RPC |
| `tools/call search_models` | `content` texte + `structuredContent` (aucun `_meta` de résultat requis) |
| Documentation Anthropic | [Get started with MCP Apps](https://claude.com/docs/connectors/building/mcp-apps/getting-started) recommande explicitement `mcp-remote` pour tester un serveur MCP App distant ; format `_meta.ui.resourceUri` + MIME `text/html;profile=mcp-app` = celui du helper officiel (`registerAppTool`/`registerAppResource`, ext-apps 2.x). `openai/outputTemplate` est le format de l'Apps SDK d'OpenAI, pas nécessaire pour Claude. |
| Authentification | `mcp-remote` sans `--header` retombe en flux OAuth : il a **découvert** bambud (métadonnées RFC 9728/8414), **s'est enregistré** (DCR) et a construit l'URL d'autorisation (voir `docs/OAUTH.md`). Avec `--header "Authorization:${VAR}"` (sans espace dans l'argument, valeur dans une variable d'environnement), la connexion se fait avec le Bearer statique |

**Conclusion** : le format côté serveur est conforme et traverse `mcp-remote`. La cause trouvée côté bambud est un **binaire construit sans la vue** : `internal/mcpserver/ui/views/mcp-app.html` n'est pas versionné (`.gitignore`), un simple `go build` embarque alors une page de repli de 438 caractères « Vue non construite » au lieu du bundle de 1 Mio. Avec le bundle (`npm run build` dans `web/`, ou `scripts/build.ps1 -Task release` / `make release` / l'image Docker), la lecture renvoie la vue complète. Corrections livrées :

- `GET /health` annonce `mcp_app_views: true|false` et bambud journalise un **avertissement au démarrage** quand la vue n'est pas embarquée ;
- les descriptions de `search_models` et `get_model_details` disent explicitement de les préférer pour « montre-moi / voir en 3D » (viewer interactif), pour que le modèle de langage ne se contente pas d'une description texte ;
- test `TestViewerToolsPreferredForShowMeIn3D`.

À vérifier côté poste (non reproductible ici, pas de Claude Desktop piloté) si la vue manque encore après ces points : (1) `curl -s http://127.0.0.1:7878/health` → `mcp_app_views:true` sur l'instance réellement jointe par mcp-remote ; (2) Claude Desktop récent (MCP Apps) et `npx -y mcp-remote@latest` ; (3) la permission « Allow / Always allow » d'affichage de l'app apparaît dans la conversation ; (4) Aide > Dépannage > Activer le mode développeur, Ctrl+Maj+I : chercher l'iframe imbriquée (hauteur 0, erreur de console) ; (5) résultat d'outil < ~150 000 caractères (c'est le cas : `get_mesh_chunk` découpe le GLB en tranches de 96 000) ; (6) pour `ui.domain` (connecteur distant), non utilisé ici : inutile tant que la vue n'ouvre pas de flux OAuth propre.

## 8. Diagnostic : la carte de confirmation reste un tableau « Requête / Réponse » (2026-11-08)

Légende : [V] vérifié dans le code / la spécification / la documentation · [H] hypothèse.

- [V] `@modelcontextprotocol/ext-apps` 2.0.4 (`dist/src/server/index.js`) : `registerAppTool` n'ajoute que le lien d'outil (`_meta.ui.resourceUri`, avec la clé plate `ui/resourceUri` recopiée dans l'autre sens si une seule est fournie) ; `RESOURCE_MIME_TYPE = "text/html;profile=mcp-app"`. Notre serveur émet les deux clés (`internal/mcpserver/app.go`), sur la définition de l'outil et dans le résultat (`withView`), et la ressource avec le bon MIME : rien à corriger de ce côté.
- [V] Annonce du client : `EXTENSION_ID = "io.modelcontextprotocol/ui"` ; `getUiCapability(clientCapabilities)` lit `capabilities.extensions["io.modelcontextprotocol/ui"]` = `{ mimeTypes: [...] }` qui « doit inclure `text/html;profile=mcp-app` » (`spec.types.d.ts`, `McpUiClientCapabilities`). L'exemple de la bibliothèque s'en sert pour choisir un repli texte : c'est **facultatif** côté serveur ; le rendu est décidé par l'hôte à partir de `tools/list` + `resources/read`.
- [V] bambud ne conditionnait rien à cette annonce. Il la **lit** maintenant (`internal/mcpserver/clientcaps.go`, `clientViewSupport`) pour deux usages seulement : un journal à la connexion (`client MCP initialisé … ui_extension=…`) et le TEXTE du résultat (court, avec le lien et le résumé quand l'annonce manque). Aucun effet sur la sécurité.
- [V] Documentation Anthropic (getting-started, troubleshooting) : exemples **stdio locaux** dans `claude_desktop_config.json` qui affichent des vues ; `mcp-remote` recommandé pour **tester un serveur distant** ; `_meta.ui.domain` indisponible pour un serveur local (non utilisé par bambud). Rien n'y dit qu'un serveur local ne peut pas afficher de vue.
- [V] Le tableau « Requête / Réponse » est l'affichage standard d'un appel d'outil : il prouve que la vue n'a pas été montée, pas pourquoi.
- [H] Derrière `mcp-remote`, l'`initialize` vu par bambud n'est peut-être pas celui de Claude Desktop (annonce d'extension perdue) ; ou Claude Desktop n'affiche les vues que pour certains types de serveurs/versions. Non reproductible ici (pas de Claude Desktop piloté). Procédure de tri dans `docs/UTILISATION.md` § 4 bis et `docs/OAUTH.md` (journal `ui_extension=`, exemple officiel `server-map --stdio`, connecteur distant).

## 9. Aperçus sans vue, viewer 3D, trajectoires, progression (2026-11-08)

Principe : tout fonctionne SANS vue (image PNG/GIF renvoyée comme contenu image MCP, comme `capture_preview` le faisait déjà) ; la vue est un bonus.

- **Transport du GLB vers la vue (choix justifié).** La CSP de la vue est vide (`connectDomains` absent : `fetch` interdit) et l'iframe de l'hôte ne joint pas `localhost` de façon fiable ; l'URL signée de `get_preview_asset` n'est accessible que sur le réseau privé et expire. Deux voies restent : (1) outil réservé à l'application (`visibility:["app"]`) qui rend le GLB en base64 par tranches de 72 000 octets (96 000 caractères) ; (2) `resources/read` d'une ressource `bambu://model/{id}.glb` renvoyant un `blob`. La (2) rend le GLB en UN résultat (jusqu'à plusieurs Mio de base64, bien au-delà du seuil d'environ 150 000 caractères au-delà duquel Claude remplace le résultat par un fichier) et la lecture de ressources par la vue passe par l'hôte sans garantie de bornes. On garde donc la voie (1), `get_mesh_chunk`, déjà en place et testée, avec reprise par tranche et empreinte commune. Plafond : `MaxMeshBytes` = 6 Mio (GLB déjà décimé à 200 000 triangles au plus) ; au-delà la vue renvoie vers `capture_preview(views=all)`. three.js n'est pas rechargé : la vue réutilise `web/src/viewer` (`GlbView`, orbite, zoom, vues iso/face/dessus/côté, sous-parties).
- **Sans vue** : `capture_preview(model_id, views=[all])` rend une planche de quatre vues PNG en un appel ; `get_model_details` et `get_preview_asset` le disent dans leur texte.
- **Couche 1 réelle** : `capture_preview(mode=first_layer)` (îlots de contact du G-code, aire en mm², emprise en gris clair ; avant découpe, section du maillage à Z≈0). Le mode `plate` dit désormais dans sa légende qu'il montre l'emprise projetée, pas le contact.
- **Couche par couche** : `capture_preview(layers=[…] | every_n, max_tiles ≤ 16)` = planche-contact ; `format=gif` = animation bornée (640 px, 48 images, 4 Mio). Vue `ui://bambu/layers.html` liée à `show_layers` (qui joint aussi la planche PNG) ; segments par l'outil réservé `get_layer_paths` (polylignes entières de 0,05 mm, 16 000 points au plus, Douglas-Peucker au-delà).
- **Progression** : vue `ui://bambu/progress.html` liée à `get_job_status`, `get_upload_status` et `wait_for_job` (le lien est posé par `addTool` sur la définition ET le résultat, sans toucher aux handlers) ; elle se rafraîchit en rappelant l'outil d'état, lit `percent`, `done_bytes`, `total_bytes`, `eta_seconds`, `phase`, `phase_label` et retombe sur `progress_detail` quand ils manquent.
- **Non vérifié ici** : l'affichage réel de ces vues dans Claude Desktop (cause d'absence des vues encore à l'étude, § 7-8). Les tests couvrent le rendu (Go), les liens `tools/list`, les ressources et les composants (vitest, hôte simulé).

## 10. Carte de suivi en direct `ui://bambu/print-live.html` (2026-11-09)

Réponse à « ça en est où ? » : une carte (caméra, progression, températures, étapes, alertes, boutons) ; **sans vue, le texte reste complet et une image est jointe**.

- **Liens** : `get_print_status` ET `wait_for_print_change` (définition `tools/list`, clé plate comprise, et `_meta` du résultat, via `toolViewMeta` / `addTool`). Aucun des deux n'avait de vue (les vues « progress » et « layers » sont liées à d'autres outils) : pas d'arbitrage. Les deux rendent le même `PrintStatus`, que `normalizeLive` (live.tsx) reconnaît avant la progression d'un travail (`job` avec `printer_id`/état d'impression).
- **Outils réservés à la vue** (`visibility:["app"]`, lecture seule) : `get_live_frame(printer_id)` (JPEG réduit à 640 px, ≤ 64 000 octets, ≈ 85 000 caractères base64, `cached`, `next_after_ms`) et `get_live_state(print_job_id, with_final_photo)` (le `PrintStatus` complet + `ends_at_local` via `localtime` + `height_mm` ≈ hauteur totale × couche / couches + résumé final : durée réelle et grammes du journal des impressions, sinon estimation de la découpe, photo de fin).
- **Charge sur la caméra (port 6000 de la P1S)** : `camera.Service.LiveFrame` fait UNE capture par fenêtre de 2 s et par imprimante, sous le même verrou que `Capture` (jamais deux connexions simultanées), quel que soit le nombre de vues ou d'appels de `get_print_status` ; un échec est rendu tel quel 3 s (pas de martèlement) ; rien n'est enregistré. Plafond : au plus une connexion courte toutes les 2 s tant qu'une vue visible et active tourne, zéro sinon.
- **Rafraîchissement** : image toutes les 2,5 s, état toutes les 5 s, boucles séquentielles ; arrêt quand `document.visibilityState` est `hidden` ou que la carte sort de l'écran (`IntersectionObserver`), et après 5 minutes sans interaction (bouton « Reprendre ») ; arrêt à l'état terminal après une dernière lecture avec la photo de fin. Caméra en échec : nouvel essai espacé de 10 s.
- **Arrêter / Reprendre** : « Arrêter » (visible seulement pendant `PRINTING`) appelle `cancel_print`, qui ne fait que CRÉER la demande ; la carte affiche alors `StopConfirm` (code de lancement saisi dans le champ ou lien du navigateur). « Reprendre » (impression en pause) appelle `resume_print` et affiche la demande (code via `validate_action_with_code`, ou lien). **Pas de bouton « Pause »** : aucune demande d'action de pause n'existe côté serveur (`actions.go` : reprise, chargement, nettoyage) ; en ajouter une est un effet physique nouveau, hors de ce chantier.
- **Repli sans vue** : `get_print_status` (`with_photo`, défaut vrai si l'impression est `PRINTING`, `false` pour du texte seul) garde son texte en premier bloc, inchangé, puis un bloc texte de légende et un contenu image MCP (même image partagée, 640 px, ≤ 64 Ko) ; panne de caméra = une ligne « Photo de la caméra indisponible : … », jamais une erreur.
- **Non vérifié ici** : l'affichage réel dans Claude Desktop (§ 7-8), le rendu visuel sur un vrai écran (clair/sombre, mobile) : les tests couvrent le composant avec un hôte simulé.
