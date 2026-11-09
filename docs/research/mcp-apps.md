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

## 11. Journal des requêtes MCP : le client lit-il la vue ? (2026-11-09)

Symptôme : le client annonce `ui_extension=true` (ligne « client MCP initialisé ») mais aucune vue ne s'affiche. Il faut savoir si l'hôte fait un `resources/read` de la vue après un `tools/call`. Le serveur journalise désormais, au niveau INFO, chaque requête MCP significative (middleware de réception du SDK, `internal/mcpserver/mcplog.go`) : `tools/call`, `resources/read`, `resources/list`, `resources/templates/list`, `prompts/list`, `prompts/get`.

Lire les lignes : `journalctl -u atelier | grep -E "MCP (tools/call|resources/read)"`

Exemple d'une session saine (l'hôte appelle l'outil, puis lit la vue) :

```
level=INFO msg="MCP tools/call" tool=search_models session=3f9a1c2b duration_ms=4 ok=true meta_ui=true bytes=50 blocks=1 block_types=text
level=INFO msg="MCP resources/read" uri=ui://bambu/search.html session=3f9a1c2b duration_ms=0 ok=true mime="text/html;profile=mcp-app" bytes=442 contents=1
```

Interpréter :

- `tools/call` avec `meta_ui=true` **sans** `resources/read` de `ui://…` juste après : le serveur a bien lié l'outil à la vue, mais l'hôte (ou le relais `mcp-remote`) ne la demande pas : problème côté hôte, relais, ou vue désactivée dans l'hôte. `meta_ui=false` : le résultat ne porte pas le lien (outil non lié, ou en erreur) ; le lien de la définition (`tools/list`) reste ce que lisent la plupart des hôtes.
- `resources/read` avec `ok=true`, `mime="text/html;profile=mcp-app"` et `bytes` non nul : la vue a été servie ; si rien ne s'affiche, l'échec est dans le rendu (CSP, iframe) côté hôte. `bytes` d'environ 1 à 3 ko = page de repli « Vue non construite » (binaire compilé sans la vue).
- `resources/list` / `resources/templates/list` : `count` = nombre d'éléments ; absence totale de `resources/*` pendant toute une session = l'hôte ne s'intéresse pas aux vues.
- `session` : 8 premiers caractères de l'identifiant de session (`-` en stdio ou sans session) ; sert à relier les lignes d'une même conversation.

Garanties : jamais d'argument d'outil, de contenu de résultat, de jeton, de code de lancement ni d'en-tête dans ces lignes ; les messages d'erreur ne sont pas repris (`error_type` seulement). Les outils réservés aux vues appelés en boucle (`get_live_frame`, `get_live_state`, `get_launch_view_state`, `get_print_confirmation_view`, `get_mesh_chunk`, `get_layer_paths`) sont au niveau DEBUG à chaque appel (`BAMBU_LOG_LEVEL=debug`) et, au niveau INFO, une seule ligne agrégée par minute et par outil : `msg="MCP tools/call (outil de vue, agrégé)" tool=get_live_frame calls=24 …`. Couper le tout : `BAMBU_LOG_MCP=off` (défaut : actif).


## 12. Socle de design commun et cartes-formulaires (2026-11-09)

Légende : [V] vérifié dans le code / par les tests · [H] hypothèse non vérifiée dans un hôte réel.

**Socle** (`web/shared`, voir son README) : jetons vert Printalk clair/sombre, DM Sans embarquée (37 Kio, la CSP interdit les polices distantes), composants (`Card`, `Badge`, `Button` 34 px, champs avec unité, `Segmented`, `Table`, `ProgressBar`, `Notice`, `Toast`, icônes au trait) utilisés par le site ET les cartes, sans duplication. [V] Thème : `applyHostContext` lit `theme` de l'hôte (sinon la luminance de `--color-background-primary`), `followSystemTheme` retombe sur `prefers-color-scheme` ; les variables de l'hôte sont recopiées sous `--host-*` (elles n'écrasent pas la charte). Taille du bundle des cartes : 1 100 502 → 1 191 787 octets (+ 91 Ko : police 49 Ko en base64 + formulaires).

**Carte-formulaire** `ui://bambu/form.html` (même bundle, même mécanisme que les autres vues). [V] Principe : un outil visible du modèle appelé SANS ses champs obligatoires renvoie, au lieu d'une erreur, `structuredContent.form` (spécification : champs, types, unités, bornes, valeurs initiales, `missing`, liens, rappel de sécurité) et un texte de repli COMPLET (champs, valeurs, consigne de rappel de l'outil). La carte pré-remplit, valide côté client (obligatoire, motif, bornes, entier, couleur), puis appelle l'outil réservé `submit_*` (`visibility:["app"]`) qui exécute la MÊME fonction que l'outil normal (même validation serveur), affiche `structuredContent.form_result` (résumé, détails, notes, liens, image) et informe le modèle par `ui/update-model-context` (`App.updateModelContext`, sans déclencher de tour : `sendMessage` aurait relancé le modèle) avec la phrase « l'utilisateur a validé … » SANS les valeurs saisies. Appelé avec tous ses champs, l'outil agit comme avant et ajoute seulement `form_result`.

| Outil visible | Déclenche la carte quand… | Outil de soumission | Notes |
|---|---|---|---|
| `add_printer` | `name`, `model_id`, `host` ou `serial` manque | `submit_add_printer` | champs de la modale du site ; lien vers `/setup/printer/{id}` à la validation ; le code d'accès n'est jamais un champ |
| `set_cost_settings` | aucun champ | `submit_cost_settings` | kWh, prix du kilo par matière, usure, marge d'alerte de bobine ; n'envoie que les valeurs modifiées |
| `configure_notifications` | aucun champ | `submit_notifications` | canaux EXISTANTS (actif, évènements) ; lien vers `/setup/notifications` pour destinations et secrets |
| `update_spool` | ni `material` ni `spool_id`, ou `spool_id` seul | `submit_spool` | matière, couleur (pastille + usuelles), poids, prix |
| `set_manufacturing_goals` | pas de `criteria` | `submit_goals` | segmentés solide / surface propre / rapide / translucide / économe (priorité), matière |
| `create_from_template` | ni `params`, ni `name`, ni `model_id` (et pas `no_form`) | `submit_part_template` + `preview_part_template` | champs générés depuis `describe_part_template` (unité, bornes, défauts) ; aperçu SVG du gabarit mis à jour pendant la saisie et vignette de la pièce créée dans le résultat |

[H] Non vérifié dans Claude Desktop (affichage réel des cartes, § 7-8). [D] L'aperçu est une vignette SVG (le moteur de gabarits ne produit que du SVG), pas un PNG. Ajouter un formulaire = décrire ses champs côté Go (`forms.go`, `form_tools.go`) : le rendu front est générique.
## 12. Visionneuse 3D couche par couche, envoi de fichiers depuis Claude, cartes de dépôt et d'aperçu (2026-11-09)

**Visionneuse « comme Bambu Studio »** (`ui://bambu/gcode.html`, liée à `show_gcode_preview(print_job_id|artifact_id, plate)`).

- **Outil visible** `show_gcode_preview` : résumé texte (couches, durée, filament) + planche-contact PNG (repli sans vue) + `structuredContent` `{kind:"gcode_preview", artifact_sha256, plate, layers_total, features, …}`. Sa suggestion (`GcodeSuggestion`) est dans le texte de `execute_headless_slice` et `execute_plate_slice`, dans leurs descriptions et dans les **instructions du serveur** : « après la découpe, montre le résultat avec show_gcode_preview ; ne fabrique pas tes propres rendus ».
- **Outils réservés à la vue** (`visibility:["app"]`) : `get_gcode_layers` (colonnes par couche : Z, durée, filament, points ; types ; budgets ; emprise) et `get_gcode_slice(from_layer, with_travel)` (couches consécutives, `next` = couche suivante, 0 = fini). Mêmes données en HTTP pour l'interface web : `GET /api/gcode/layers` et `/api/gcode/slice`.
- **Lecture du G-code** (`internal/printing/gcode_toolpath.go`) : UNE lecture en flux par plaque, mise en cache (3 plaques) ; chaînage des segments par type `; FEATURE:` et vitesse (arrondie à 5 mm/s), déplacements à vide (pseudo-type `Travel`, masqués par défaut et non servis sans `with_travel`), Z par `; Z_HEIGHT:`, longueur de filament par couche (E), durée par couche = distance / vitesse programmée, **ramenée à la durée annoncée par le slicer** (`total estimated time`) quand elle existe (`time_source: "slicer"`), sinon estimation sans accélérations (`"estimation"`). Les arcs G2/G3 sont ramenés à leur corde (Bambu Studio n'en émet pas par défaut).
- **Budgets documentés** : ≤ **2 000 000 points** pour toute la plaque ; ≤ **10 000 points d'extrusion par couche** (+ 2 000 de déplacements) ; budget par couche = `clamp(2 000 000 / nombre de couches, 200, 10 000)` ; simplification Douglas-Peucker par couche (tolérance doublée de 0,05 mm jusqu'à 8 mm au plus, annoncée dans `tolerance_mm`) ; ≤ **6 000 couches** ; **tranche de ≤ 12 000 points ≈ 100 000 caractères JSON** (coordonnées entières de 0,05 mm, premier point absolu puis différences ; une couche n'est jamais coupée) ; au plus ~170 tranches pour un modèle au plafond. Côté vue : **tubes** (prismes à 12 sommets, flancs assombris) jusqu'à **250 000 segments**, **traits** au-delà (mémoire GPU) ; une géométrie par couche, visibilité par couche (`visible`), couche du haut tronquée par `drawRange` (curseur fin, ordre d'impression conservé), types masqués par reconstruction du tampon d'indices.
- **Chargement progressif** : la vue demande le résumé, crée la scène, puis enchaîne les tranches (`next`) en ajoutant les couches au fur et à mesure (barre « n / N couches ») ; les curseurs fonctionnent déjà pendant le chargement ; les déplacements à vide ne sont chargés (second passage) que la première fois qu'on les affiche.
- **Commandes** : curseur double de couches (min ≤ max), curseur fin dans la couche, lecture (≈ 25 s de bout en bout), vues iso / dessus / face, orbite et zoom (OrbitControls), légende cliquable (un bouton par type : paroi extérieure/intérieure/en surplomb, remplissage clairsemé/plein, surface dessus/dessous, pont, support, interface de support, bordure, jupe, tour d'essuyage, lissage, déplacements à vide), couleur par type / **vitesse** / **hauteur**, infos par couche (Z, durée estimée, filament) et de la sélection.
- **Composant partagé** : `web/src/viewer/gcode/GcodeViewer.tsx` (données injectées par `GcodeSource`, scène par fabrique : three.js n'est chargé qu'à l'affichage) ; vue MCP App `web/mcp-app/gcode.tsx` (outils réservés) ; interface web `web/src/pages/GcodePanel.tsx` sur la fiche d'impression (routes `/api/gcode/*`, `React.lazy`).

**Envoi de fichiers depuis Claude.** Constat de terrain : « 35 000 caractères, trop fragile ». (1) La description d'`upload_file` dit maintenant de découper à 75 000 octets, qu'un fichier de moins de 75 000 octets tient en UN appel, comment reprendre (`get_upload_status` → `next_offset`) et que l'empreinte finale est contrôlée. (2) `create_upload_link(filename, size?, sha256?)` renvoie une URL à usage unique (`PUT /upload/{jeton}`, 10 min, taille et types bornés) et la commande `curl -T` exacte : le fichier part en un seul PUT depuis l'environnement de code et entre comme `import_model` ; suivi par `get_upload_status(link_id)`. Sécurité : `docs/SECURITY.md` § 18, surface publique : `docs/OAUTH.md` § 3 bis.1 bis. (3) Carte `ui://bambu/drop.html` (`show_file_drop`) : zone de glisser-déposer, envoi par morceaux de 72 000 octets par l'outil réservé `drop_file_chunk` (même logique qu'`upload_file`), empreinte sha256 calculée dans la vue (fichiers jusqu'à 128 Mio depuis la carte), barre de progression, reprise à `next_offset`, puis l'aperçu de la pièce.

**Aperçus automatiques.** Après un import ou un envoi réussi (`upload_file` terminé, `get_upload_status` d'un lien terminé, `import_model`, `import_from_url`), le texte du résultat propose `get_model_details(model_id)` (viewer 3D existant) et le résultat porte le lien vers la vue du modèle (sur le RÉSULTAT seulement : lier `upload_file` à la vue ouvrirait une carte à chaque morceau) ; la vue reconnaît ces résultats (`file.model_id` + `status=complete` ou `done`) et ouvre directement la fiche de la pièce.

**Non vérifié ici** : l'affichage dans Claude Desktop (§ 7-8) ; la lecture d'un vrai `.gcode.3mf` de Bambu Studio (les tests utilisent des archives synthétiques aux marqueurs `; CHANGE_LAYER`, `; Z_HEIGHT:`, `; FEATURE:` de Bambu) ; l'accès réseau de l'environnement de code de Claude vers l'adresse publique (`curl`). Le rendu WebGL a été contrôlé dans le navigateur intégré avec des trajectoires synthétiques (tubes, traits, déplacements, vues).
