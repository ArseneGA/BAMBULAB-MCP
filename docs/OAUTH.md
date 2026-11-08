# Connecteur distant de Claude : OAuth 2.1 intégré (backlog #4)

Date : 2026-11-07. Code : `internal/oauth` (serveur d'autorisation), `internal/httpapi/oauth.go` (routes et pages), `cmd/bambud/oauth_cli.go` (`bambud oauth`), migration `0190_oauth.sql`. Tests : `internal/httpapi/oauth_test.go`, `cmd/bambud/oauth_cli_test.go`.

Objectif : ajouter bambud comme **connecteur personnalisé** dans claude.ai, l'application mobile et Claude Desktop (Paramètres > Connecteurs > Ajouter un connecteur personnalisé, URL `https://<votre-nom-public>/mcp`), sans `mcp-remote` ni jeton collé. Le Bearer statique (`BAMBU_API_TOKEN`) reste valide exactement comme avant (pont `bambu-mcp`, `mcp-remote --header`, Claude Code).

## 1. Ce que Claude exige (sources lues le 2026-11-07)

Sources : [Authentication for connectors](https://claude.com/docs/connectors/building/authentication) (Anthropic), [Spécification d'autorisation MCP, version brouillon](https://modelcontextprotocol.io/specification/draft/basic/authorization) et [Client Registration](https://modelcontextprotocol.io/specification/draft/basic/authorization/client-registration.md), [aide Claude « connecteurs personnalisés »](https://support.claude.com/en/articles/11503834-building-custom-connectors-via-remote-mcp-servers). Colonne « bambud » : ce qui est implémenté.

| Exigence | Source | bambud |
|---|---|---|
| Une réponse **401** (pas 200) avec `WWW-Authenticate: Bearer resource_metadata="…"` déclenche la connexion | Claude/auth « Serve discovery metadata » ; MCP RFC 9728 | `/mcp` sans jeton valide : 401 + `Bearer realm="bambud", resource_metadata="<base>/.well-known/oauth-protected-resource", scope="mcp"` |
| Métadonnée de ressource protégée RFC 9728 ; `resource` **identique à l'URL saisie** dans Claude (chemin compris) ; `authorization_servers` : Claude n'utilise que la **première** entrée | Claude/auth | `/.well-known/oauth-protected-resource` (et `…/mcp`) : `resource = <base>/mcp`, `authorization_servers = [<base>]` |
| Métadonnées du serveur d'autorisation RFC 8414 (ou OIDC Discovery), joignables depuis les IP d'Anthropic (`160.79.104.0/21`) | Claude/auth ; MCP | `/.well-known/oauth-authorization-server` (même hôte) |
| **DCR RFC 7591** (`registration_endpoint`) *ou* CIMD (annoncer `client_id_metadata_document_supported` **et** `none` dans `token_endpoint_auth_methods_supported`) ; sinon repli sur DCR. Claude s'enregistre en **client public** à chaque nouvelle connexion | Claude/auth ; MCP (DCR « déprécié » mais maintenu) | **DCR** : `POST /oauth/register`. CIMD non implémenté (il obligerait bambud à aller chercher une URL fournie par un tiers : surface SSRF inutile). Clients publics seulement (`token_endpoint_auth_method=none`) |
| **PKCE S256** sur chaque demande ; annoncer `code_challenge_methods_supported: ["S256"]` | Claude/auth ; MCP | obligatoire (`plain` et absence refusés) ; annoncé |
| Redirection **exacte** `https://claude.ai/api/mcp/auth_callback` (claude.ai, Desktop, mobile, Cowork) — « peut migrer vers claude.com » ; Claude Code : boucle locale `http://localhost:<port>/callback` / `http://127.0.0.1:<port>/callback` (port libre, RFC 8252) | Claude/auth « Callback URLs » ; aide Claude | liste blanche : les deux callbacks `claude.ai` et `claude.com`, la boucle locale `http` (port ignoré à l'autorisation), plus `BAMBU_OAUTH_REDIRECTS` / `--oauth-redirects` (URI exactes). Toute autre redirection est refusée à l'enregistrement |
| `/token` accepte `application/x-www-form-urlencoded` ; erreurs `invalid_grant` (pas `invalid_request`) pour un refresh invalide ; **rotation** des refresh tokens pour les clients publics (nouveau jeton dans la réponse qui invalide l'ancien) | Claude/auth « Token refresh » ; MCP (OAuth 2.1 §4.3) | oui ; rejeu d'un ancien refresh = **révocation de l'autorisation entière** |
| Latence : découverte, enregistrement, jeton ≤ 10 s ; refresh ≤ 30 s | Claude/auth | opérations locales SQLite, quelques ms |
| Claude ajoute `offline_access` si `scopes_supported` de l'AS le liste | Claude/auth | non listé : le refresh token est émis d'office, `offline_access` toléré dans la demande |
| Paramètre `resource` (RFC 8707) dans autorisation et jeton ; jeton lié à sa ressource | MCP | vérifié ; un jeton émis pour un hôte ne vaut pas pour un autre |
| `iss` dans la réponse d'autorisation (RFC 9207), `authorization_response_iss_parameter_supported: true` | MCP | oui |
| Écran de consentement : afficher le nom d'hôte de la redirection, avertir si boucle locale | Claude/auth ; MCP | oui |
| Jeton jamais dans l'URL | MCP | Bearer d'en-tête seulement |

Autres faits utiles : le secret client est facultatif dans la boîte « connecteur personnalisé » (laisser vide) ; le callback est le même pour toutes les surfaces hébergées ; Claude Code a son propre document CIMD et une redirection loopback.

## 2. Ce que fait bambud

```
Claude ── POST /mcp ─────────────▶ 401 + WWW-Authenticate (resource_metadata)
       ── GET  /.well-known/oauth-protected-resource ─▶ {resource, authorization_servers:[base]}
       ── GET  /.well-known/oauth-authorization-server
       ── POST /oauth/register (JSON, client public) ─▶ client_id
       ── navigateur : GET /oauth/authorize?…&code_challenge=…&resource=… ─▶ page « Continuer »
                  └▶ GET /oauth/consent/{id}  (cookie du navigateur ENREGISTRÉ, sinon /enroll)
                  └▶ POST « Autoriser » ─▶ 303 https://claude.ai/api/mcp/auth_callback?code=…&state=…&iss=…
       ── POST /oauth/token (code + code_verifier) ─▶ access_token (1 h) + refresh_token (30 j, rotatif)
       ── POST /mcp  Authorization: Bearer bat_… ─▶ MCP
```

**Sécurité** (complète `docs/SECURITY.md` §2.6) :

- **Consentement humain obligatoire** : `/oauth/consent/{id}` est une page humaine (`humanauth.Require`) : cookie d'un navigateur enregistré (`bambud enroll`), `Sec-Fetch-Site: same-origin` + `Sec-Fetch-Mode: navigate`, nonce de formulaire à usage unique, demande à usage unique expirant en 10 min. Le cookie étant `SameSite=Strict`, la navigation venue de Claude (autre site) n'en porte pas : `/oauth/authorize` affiche donc une page intermédiaire dont le lien « Continuer » est une navigation interne qui, elle, porte le cookie. Sans navigateur enregistré, la page renvoie vers `/enroll`.
- **Portée minimale** : une seule portée `mcp`. Le jeton OAuth n'est accepté **que sur `/mcp`** (jamais sur `/api/*`, ni pour les pages `/confirm` `/stop` `/setup`). Sur `/mcp` il a les droits du Bearer statique, ni plus ni moins : il ne peut **jamais** approuver un lancement ni un arrêt (ces gestes n'existent pas sur `/mcp`, voir `TestNoMCPToolCanApproveAConfirmation`).
- Jetons **hachés** (SHA-256) en base ; jamais en clair ni dans le journal. Accès : 1 h ; rafraîchissement : 30 jours, **rotation à chaque usage**, rejeu = autorisation révoquée. Code d'autorisation : 60 s, usage unique, rejeu = autorisation révoquée.
- Clients **publics** seulement, redirections en **liste blanche** ; `redirect_uri` inconnue ou client inconnu : page d'erreur, **jamais de redirection**. Table des clients bornée (200 ; clients sans autorisation purgés après 1 h).
- Journal d'audit : `oauth.approved`, `oauth.denied`, `oauth.grant_created`, `oauth.grant_revoked`, `oauth.code_replay_revoked`, `oauth.refresh_replay_revoked`.

**Gestion** (terminal de l'humain, même base que le service) :

```
bambud oauth --list                 # autorisations : id, état, dernière utilisation, application
bambud oauth --revoke g_AbC         # préfixe accepté ; les jetons cessent de valoir immédiatement
bambud oauth --revoke-all
bambud enroll --list / --revoke     # navigateurs enregistrés (inchangé)
```

## 3. Déploiement avec Tailscale Funnel

Le principe : **n'exposer sur Internet que ce dont Claude a besoin**, pas l'interface web ni l'API.

| Chemin | Pourquoi | Protégé par |
|---|---|---|
| `/mcp` | le protocole MCP | jeton OAuth ou Bearer statique |
| `/.well-known/*` | découverte (métadonnées publiques, sans donnée) | rien (publiques par nature) |
| `/oauth/*` | enregistrement, autorisation, jeton, consentement | PKCE ; consentement derrière le cookie d'un navigateur enregistré |

**Ne pas exposer** : `/`, `/api/*` (données du catalogue), `/enroll`, `/confirm`, `/stop`, `/setup`, `/agents`, `/agent/v1`, `/health`. L'interface et `/enroll` restent sur l'adresse privée (tailnet, LAN, localhost). Le cookie d'un navigateur enregistré est lié à **un nom d'hôte** : Claude ouvrant le navigateur sur l'adresse publique, ce navigateur doit avoir été enregistré **sur cette adresse** (`<BAMBU_PUBLIC_URL>/enroll`). `/enroll` n'étant pas publié par le proxy ci-dessus, l'enregistrer d'abord depuis le tailnet avec la même adresse (Tailscale résout le nom public à l'intérieur du tailnet), ou publier temporairement `/enroll` le temps de l'enregistrement.

Réglage de bambud (Funnel termine le TLS et transmet en `http://127.0.0.1:7878`) :

```
BAMBU_ADDR=127.0.0.1:7878
BAMBU_API_TOKEN=<au moins 16 caractères>             # toujours un jeton derrière un proxy
BAMBU_PUBLIC_URL=https://atelier.example.ts.net      # HTTPS, sans chemin ; seule l'origine
BAMBU_ALLOWED_HOSTS=atelier.example.ts.net           # (déduit de BAMBU_PUBLIC_URL, redondant mais explicite)
```

`BAMBU_PUBLIC_URL` donne l'**émetteur** (`issuer`), la ressource (`<url>/mcp`) et les liens des métadonnées ; il doit être exactement l'adresse que l'on saisit dans Claude (moins `/mcp`). Avec plusieurs URL (`https://atelier.example.ts.net,http://192.168.1.50:7878`) la première sert aux liens, et l'émetteur est celui dont le nom d'hôte correspond à la requête. Le proxy doit **conserver l'en-tête `Host`**.

Exemple de configuration Funnel, un chemin par commande (syntaxe Tailscale ≥ 1.80 ; à vérifier avec `tailscale funnel --help` et `tailscale serve status`) :

```sh
tailscale funnel --bg --https=443 --set-path=/mcp         http://127.0.0.1:7878/mcp
tailscale funnel --bg --https=443 --set-path=/.well-known http://127.0.0.1:7878/.well-known
tailscale funnel --bg --https=443 --set-path=/oauth       http://127.0.0.1:7878/oauth
tailscale funnel status
```

Vérifier de l'extérieur (le chemin de la cible ne doit pas être doublé : si `/oauth/oauth/token` apparaît, retirer le chemin de la cible, `http://127.0.0.1:7878`) :

```sh
curl -i  https://atelier.example.ts.net/mcp                                 # 401 + resource_metadata
curl -s  https://atelier.example.ts.net/.well-known/oauth-protected-resource
curl -s  https://atelier.example.ts.net/.well-known/oauth-authorization-server
curl -i  https://atelier.example.ts.net/api/capabilities                   # doit être inaccessible (404/403 du proxy)
```

> Ce dépôt n'a jamais exposé quoi que ce soit : le réglage ci-dessus est **documenté, pas testé** contre un vrai Funnel. Les flux OAuth ont été testés de bout en bout en local (tests Go ; découverte et enregistrement vérifiés avec le vrai `mcp-remote`).

### Connexion côté Claude

1. Sur le poste de bambud : `bambud enroll` puis, dans le navigateur qui servira à consentir, ouvrir `<BAMBU_PUBLIC_URL>/enroll` et saisir le code.
2. Claude (claude.ai, Desktop ou mobile) > Paramètres > Connecteurs > **Ajouter un connecteur personnalisé** : URL `https://atelier.example.ts.net/mcp`, ID/secret client **vides**.
3. Le navigateur s'ouvre sur bambud : **Continuer**, vérifier l'application et l'adresse de retour (`claude.ai`), **Autoriser**.
4. Révoquer : `bambud oauth --revoke <id>` ; déconnecter aussi le connecteur dans Claude.

### Pourquoi brancher le connecteur distant : avoir les vues (carte de confirmation, visionneuse 3D)

Constat (2026-11-08) : derrière `cmd /c npx -y mcp-remote http://…:7878/mcp --allow-http --header Authorization:…`, Claude Desktop affichait le résultat de `request_print_confirmation` en tableau « Requête / Réponse » au lieu de la carte `ui://bambu/print-confirm.html`.

- **Vérifié (code, tests, spécification, SDK `@modelcontextprotocol/ext-apps` 2.0.4)** : le serveur annonce correctement la vue (`_meta.ui.resourceUri` + clé historique `ui/resourceUri`, MIME `text/html;profile=mcp-app`) ; la spécification fait annoncer la prise en charge par le **client** (`capabilities.extensions["io.modelcontextprotocol/ui"].mimeTypes`), et c'est facultatif côté serveur ; bambud ne conditionne rien à cette annonce et la **journalise** désormais (`client MCP initialisé … ui_extension=…`). La documentation d'Anthropic montre aussi des serveurs stdio locaux qui affichent des vues ; `mcp-remote` y est présenté comme moyen de **tester** un serveur distant.
- **Non prouvé** : la cause exacte côté Claude Desktop. Hypothèse : le chemin « serveur local + `mcp-remote` » ne reçoit pas le même traitement qu'un connecteur distant (annonce d'extension non relayée, ou version de Desktop). À trancher avec le journal (`ui_extension=`), puis avec l'exemple officiel `@modelcontextprotocol/server-map --stdio` (s'il affiche sa vue dans le même Desktop, le problème est sur le chemin bambud/`mcp-remote`).
- **Marche à suivre pour essayer le connecteur distant** : (1) `tailscale funnel` sur **`/mcp`, `/.well-known/*` et `/oauth/*` seulement** (voir § 3 ; ne jamais exposer `/confirm`, `/stop`, `/setup`, `/enroll`) ; `BAMBU_PUBLIC_URL` en `https://…` en premier ; (2) `bambud enroll` et enregistrement du navigateur sur l'URL https ; (3) Claude > Paramètres > Connecteurs > Ajouter un connecteur personnalisé > `https://<nom>.ts.net/mcp`, ID/secret client vides ; autoriser dans le navigateur ; (4) **retirer l'entrée `mcp-remote`** de `claude_desktop_config.json` (deux serveurs « Atelier » sinon) ; (5) demander une confirmation de lancement : la carte doit apparaître ; sinon relever `ui_extension=` dans les journaux et utiliser le lien `/confirm/…` (même contenu, même sécurité).
- Avertissement : un Funnel expose `/mcp` à Internet. La barrière est l'OAuth (ou le Bearer) ; l'approbation d'un lancement reste humaine (code de lancement ou page enregistrée), jamais un outil appelé par le modèle.

## 4. Limites connues

- **CIMD** (client_id = URL) non implémenté ; DCR suffit à Claude (qui s'enregistre à chaque nouvelle connexion : les clients inutilisés sont purgés au bout d'une heure).
- Pas de révocation RFC 7009 (`/oauth/revoke`) : révocation par `bambud oauth`.
- Les clients navigateur (MCP Inspector…) envoient `Origin` : la protection cross-origin de bambud les refuse sur `POST /oauth/*` sauf `TrustedOrigins` ; Claude, `mcp-remote` et Claude Code appellent depuis un serveur ou un processus local, sans `Origin`.
- Non vérifié contre un vrai claude.ai (impossible sans exposer le service) : les exigences ci-dessus viennent de la documentation d'Anthropic.
- Une seule portée ; pas d'élévation de portée (`insufficient_scope`) puisqu'il n'y a rien à élever.
- L'état en attente (demandes d'autorisation, codes) est en mémoire : un redémarrage de bambud pendant le consentement oblige à relancer la connexion.
