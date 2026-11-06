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
