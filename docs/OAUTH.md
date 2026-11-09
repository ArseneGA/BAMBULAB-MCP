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
                  │   (CSP de cette page : form-action 'self' + origine exacte de la redirect_uri de la demande ;
                  │    Chrome applique le form-action du document qui soumet aux redirections du POST)
                  └▶ POST « Autoriser » ─▶ 200 page « Retour à Claude… » (lien + meta refresh, sans script) vers
                     https://claude.ai/api/mcp/auth_callback?code=…&state=…&iss=…  (rejeu < 2 min : « déjà donnée »)
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

> **Préférer le § 3 bis (écouteur public `--public-listen`)** : la liste blanche est dans bambud et testée, au lieu de dépendre de règles de proxy. Ce § 3 reste valable pour un reverse proxy qui filtre lui-même les chemins.

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

## 3 bis. Surface publique (`--public-listen`) et « Passer en connecteur distant »

> **Remplace la configuration chemin par chemin du § 3.** Plutôt que de filtrer des chemins dans le proxy (un oubli expose `/confirm`), bambud ouvre un **second écouteur** dont le routeur ne connaît que la liste blanche ci-dessous ; le Funnel ne pointe que sur lui. Spécification : `docs/specs/G1-connecteur-distant.md` F1, F2, F7. Code : `internal/httpapi/public.go`, `publicrate.go`, `cmd/bambud/doctor.go`.

### 3 bis.1 Ce que l'écouteur public sert (et rien d'autre)

| Chemin | Méthodes | Remarque |
|---|---|---|
| `/mcp` | POST, GET, DELETE | jeton OAuth ou Bearer statique ; sans jeton valide : 401 + `resource_metadata` |
| `/.well-known/oauth-protected-resource[/mcp]`, `/.well-known/oauth-authorization-server` | GET | découverte |
| `/oauth/register`, `/oauth/token` | POST | DCR, jetons |
| `/oauth/authorize` | GET | page « Continuer » |
| `/oauth/consent/{id}` | GET, POST | cookie d'un **navigateur enregistré** (voir 3 bis.3) |
| `/health` | GET | réduit à `{"status":"ok"}` (ni version, ni schéma, ni état des vues) |
| `/about`, `/privacy`, `/icon.svg`, `/robots.txt` | GET | pages statiques en français, sans script ni ressource externe ; contact = `BAMBU_CONTACT_EMAIL` (sinon « contact non configuré ») |

**Tout le reste répond 404** (pas 401, pour ne rien révéler), avec ou sans Bearer : `/confirm`, `/stop`, `/action`, `/setup/*`, `/enroll`, `/agents`, `/api/*` (dont `/api/system/busy`), `/agent/v1`, `/agent/v2`, la SPA, tout autre `.well-known`. Le filtre passe **avant** le garde (Host, Origin, jeton). Aucun jeton OAuth ne vaut hors `/mcp`. L'écouteur principal est **inchangé**. Tests : `internal/httpapi/public_test.go` (liste blanche exacte, flux OAuth complet sur l'écouteur public, limites), `cmd/bambud/doctor_test.go`.

### 3 bis.2 Réglages

```
BAMBU_ADDR=127.0.0.1:7878                                  # écouteur principal (inchangé, privé)
BAMBU_API_TOKEN=<au moins 16 caractères>                   # OBLIGATOIRE dès que BAMBU_PUBLIC_LISTEN est défini
BAMBU_PUBLIC_URL=https://atelier.tail4442b5.ts.net:8443    # adresse PRIVÉE (tailnet) : liens /confirm /stop /action, cookies, passkeys
BAMBU_PUBLIC_LISTEN=127.0.0.1:7879                         # second écouteur, surface publique (vide = absent)
BAMBU_PUBLIC_LISTEN_URL=https://atelier.tail4442b5.ts.net  # adresse PUBLIQUE vue d'Internet : émetteur OAuth et ressource /mcp
BAMBU_ALLOWED_HOSTS=atelier.tail4442b5.ts.net
BAMBU_CONTACT_EMAIL=                                       # facultatif, affiché par /privacy
BAMBU_PUBLIC_RATE_LIMIT=register=10,token=60,mcp=60        # facultatif (valeurs par défaut)
```

Équivalents en ligne de commande : `--public-listen`, `--public-listen-url`, `--public-rate-limit`. **Pourquoi un réglage de plus que `BAMBU_PUBLIC_URL`** : Funnel n'accepte que les ports 443, 8443 et 10000, et un même nom d'hôte ne peut pas servir à la fois l'adresse privée et l'adresse publique sur le port 443. L'adresse publique prend donc 443 (URL de connecteur propre, sans port) et l'adresse privée passe sur 8443. `BAMBU_PUBLIC_URL` garde **un seul rôle : les liens que l'humain suit** ; `BAMBU_PUBLIC_LISTEN_URL` donne l'émetteur et la ressource de l'écouteur public. Si les deux étaient identiques, bambud le signale au démarrage (les liens pointeraient vers la surface publique, qui répond 404).

**Limites de débit** (par adresse ; 429 + `Retry-After` ; journal sans adresse, une ligne par minute et par catégorie) : `register` = enregistrements par **heure** (défaut 10), `token` = échanges de jetons par **minute** (60), `mcp` = appels `/mcp` **sans jeton valide** par minute (60) ; un client authentifié n'est jamais compté. Valeurs entières de 1 à 100 000. Ce qui existait déjà n'est pas doublé : `internal/oauth` limite déjà l'enregistrement en rafale (30 puis 1 toutes les 3 s) et `/oauth/authorize` (30 puis 1 toutes les 2 s) ; les plafonds ajoutés sont l'enregistrement **à l'heure**, `/oauth/token` et `/mcp` anonyme. *À vérifier* : l'adresse du client est lue dans `X-Forwarded-For` quand la connexion vient du loopback (proxy de confiance par défaut) ; si Funnel ne transmet pas l'adresse réelle, toutes les requêtes partagent une adresse et les plafonds deviennent globaux (relever `token`/`register` si Claude est limité à tort).

### 3 bis.3 Enregistrer le navigateur pour le consentement (point délicat)

`/oauth/consent/{id}` exige le cookie d'un navigateur enregistré, et `/enroll` **n'existe pas** sur l'écouteur public (404). Le cookie (`bambu_session`) est lié à un **nom d'hôte** (cookie sans attribut `Domain`) et **pas à un port** : un cookie posé par `https://atelier.tail4442b5.ts.net:8443/enroll` (écouteur principal, via `tailscale serve`) est renvoyé par le navigateur à `https://atelier.tail4442b5.ts.net/…` (écouteur public, Funnel). La session vit en base, partagée par les deux écouteurs : le consentement est donc accepté. Prouvé par `TestPublicFullOAuthFlowAndMCP` (cookie obtenu sur l'écouteur principal, consentement sur l'écouteur public, avec un jar de cookies qui ignore les ports comme un navigateur). Conséquences :

1. Enregistrer le navigateur **depuis le tailnet**, sur l'adresse **privée** (`https://atelier.tail4442b5.ts.net:8443/enroll`), avec le code de `bambud enroll`. Le nom d'hôte doit être exactement celui de l'adresse publique.
2. Faire la connexion de Claude **dans ce même navigateur** (navigateur par défaut du PC ; sur téléphone, enregistrer d'abord le navigateur du téléphone, tailnet actif). Sans cela, `/oauth/consent` affiche « Navigateur non enregistré » (403, avec explication) au lieu d'un lien mort.
3. Le tailnet n'est pas nécessaire pendant l'utilisation courante (le jeton OAuth se rafraîchit seul) ; il l'est pour enregistrer un navigateur et pour approuver un lancement.

**Les liens de l'humain restent privés.** `confirm_url`, `stop_url`, `action_url` et les liens de secours sont construits depuis `BAMBU_PUBLIC_URL` (adresse privée `:8443`, joignable seulement depuis le tailnet), jamais depuis l'écouteur public : ils fonctionnent depuis le PC ou le téléphone de l'utilisateur sur le tailnet, et sont **inutilisables depuis Internet** (le port 8443 n'est pas publié par Funnel). Le jeton de Claude ne permet ni de les ouvrir ni d'approuver (`TestNoMCPToolCanApproveAConfirmation`).

### 3 bis.4 Procédure pas à pas : passer en connecteur distant

Exemple avec le serveur `atelier.tail4442b5.ts.net` (remplacer par votre nom). Exécuter les commandes `tailscale` sur le serveur ; `sudo` si le compte n'est pas opérateur Tailscale.

0. **Pré-requis.** bambud à jour (cette version). Dans la console d'administration Tailscale : HTTPS activé pour le tailnet et Funnel autorisé pour ce nœud (attribut `funnel` dans la politique d'accès) ; sans cela `tailscale funnel` l'explique.
1. **Réglages bambud** (fichier d'environnement du service) : les variables de 3 bis.2, puis `sudo systemctl restart atelier` (adapter le nom du service). bambud refuse de démarrer sans `BAMBU_API_TOKEN` ; il journalise « surface publique restreinte active ».
2. **Tailscale** (syntaxe de la documentation Tailscale lue le 2026-11-09 : `tailscale funnel [--bg] [--https=<port>] <cible>`, ports Funnel 443, 8443 ou 10000, cible `http://127.0.0.1:<port>`, arrêt = même commande suivie de `off`). **À vérifier sur le serveur** avec `tailscale funnel --help` et `tailscale serve --help` (la version installée peut différer ; en particulier : ports acceptés par `serve`, coexistence de `serve` et de `funnel` sur deux ports du même nœud) :
   ```sh
   tailscale serve status                                           # état actuel (probablement https 443 -> 7878, tailnet)
   sudo tailscale serve --https=443 off                             # libère le 443 de l'ancienne adresse privée
   sudo tailscale serve  --bg --https=8443 http://127.0.0.1:7878    # adresse PRIVÉE (tailnet seulement) -> écouteur principal
   sudo tailscale funnel --bg --https=443  http://127.0.0.1:7879    # adresse PUBLIQUE -> écouteur public SEULEMENT
   tailscale serve status && tailscale funnel status                # « Funnel on » doit figurer sur le 443 et PAS sur le 8443
   ```
   Si `serve` refuse le 8443, utiliser 10000 (et adapter `BAMBU_PUBLIC_URL`). Ne **jamais** pointer le Funnel sur `127.0.0.1:7878`.
3. **Contrôle externe** (depuis un poste hors tailnet si possible ; le serveur lui-même convient aussi) :
   ```sh
   bambud doctor connector --url https://atelier.tail4442b5.ts.net --token-file /chemin/vers/jeton
   ```
   Le fichier contient `BAMBU_API_TOKEN` (une ligne) ; le jeton n'est jamais affiché. Tous les points doivent être `OK` (un avertissement « vue peut-être non construite » signale un binaire sans l'interface MCP App : reconstruire avec `scripts/build.sh release`). Sans `--token-file`, les contrôles MCP sont ignorés. Un `KO` « chemin interdit exposé » veut dire que le Funnel pointe sur le mauvais écouteur : l'arrêter immédiatement (`sudo tailscale funnel --https=443 off`).
4. **Enregistrer le navigateur** (3 bis.3) : `bambud enroll` sur le serveur ; ouvrir `https://atelier.tail4442b5.ts.net:8443/enroll` dans le navigateur qui servira au consentement, depuis le tailnet ; saisir le code.
5. **Ajouter le connecteur dans Claude** : Paramètres > Connecteurs > **Ajouter un connecteur personnalisé**, URL `https://atelier.tail4442b5.ts.net/mcp`, ID et secret client **vides**. Le navigateur s'ouvre : **Continuer**, vérifier l'application et l'adresse de retour `claude.ai`, **Autoriser**.
6. **Retirer l'entrée `mcp-remote`** de `claude_desktop_config.json` (Windows : `%APPDATA%\Claude\claude_desktop_config.json`, clé de `mcpServers` qui lance `npx mcp-remote …`), puis quitter et relancer Claude Desktop (sinon deux serveurs « Atelier » coexistent).
7. **Tester la carte** : demander une confirmation de lancement (imprimante simulée ou vraie, sans valider) ou « où en est l'impression ? » ; la carte doit s'afficher dans la conversation. Sinon : `journalctl -u atelier | grep -E "MCP (tools/call|resources/read)"` (un `resources/read` doit suivre le `tools/call`), puis `docs/research/mcp-apps.md` § 11 ; le lien `/confirm/…` (privé, tailnet) donne le même contenu.
8. **Retour arrière** : (a) arrêter la publication : `sudo tailscale funnel --https=443 off` (ou `tailscale funnel reset`, qui efface toute la configuration Funnel : revérifier ensuite `tailscale serve status`) ; (b) retirer `BAMBU_PUBLIC_LISTEN`, `BAMBU_PUBLIC_LISTEN_URL`, remettre `BAMBU_PUBLIC_URL` sur l'ancienne adresse et redémarrer bambud ; (c) si l'ancienne adresse privée était sur 443 : `sudo tailscale serve --https=8443 off` puis `sudo tailscale serve --bg --https=443 http://127.0.0.1:7878` ; (d) `bambud oauth --revoke-all` et retirer le connecteur dans Claude ; (e) remettre l'entrée `mcp-remote`.

**Variante sans toucher à l'adresse privée** : laisser `tailscale serve` privé sur 443 (`BAMBU_PUBLIC_URL` inchangé) et publier l'écouteur public sur le port **8443** (`tailscale funnel --bg --https=8443 http://127.0.0.1:7879`, `BAMBU_PUBLIC_LISTEN_URL=https://atelier.tail4442b5.ts.net:8443`, URL du connecteur `…:8443/mcp`). *Non vérifié* : que Claude accepte un port dans l'URL d'un connecteur personnalisé.

**Passkeys** (`docs/SECURITY.md` § 13) : l'identifiant de partie de confiance est le nom d'hôte, dérivé de `BAMBU_PUBLIC_URL` ; l'origine attendue en dérive aussi (avec le port `:8443`). *À vérifier* après le changement d'adresse privée : si une passkey existante n'est plus acceptée, la ré-enregistrer sur la nouvelle adresse (`/setup/passkeys`).

**Ce qu'un Funnel expose réellement** : l'existence du service (page `/about`), l'OAuth (enregistrement anonyme limité, consentement derrière votre navigateur enregistré) et `/mcp` derrière jeton. Surveiller : `bambud oauth --list`, le journal d'audit (`oauth.*`), les lignes « limite de débit atteinte ». Un jeton d'API faible est la première chose qu'un attaquant essaierait : le générer aléatoire (≥ 32 caractères).

## 4. Limites connues

- **CIMD** (client_id = URL) non implémenté ; DCR suffit à Claude (qui s'enregistre à chaque nouvelle connexion : les clients inutilisés sont purgés au bout d'une heure).
- Pas de révocation RFC 7009 (`/oauth/revoke`) : révocation par `bambud oauth`.
- Les clients navigateur (MCP Inspector…) envoient `Origin` : la protection cross-origin de bambud les refuse sur `POST /oauth/*` sauf `TrustedOrigins` ; Claude, `mcp-remote` et Claude Code appellent depuis un serveur ou un processus local, sans `Origin`.
- Non vérifié contre un vrai claude.ai (impossible sans exposer le service) : les exigences ci-dessus viennent de la documentation d'Anthropic.
- Une seule portée ; pas d'élévation de portée (`insufficient_scope`) puisqu'il n'y a rien à élever.
- L'état en attente (demandes d'autorisation, codes) est en mémoire : un redémarrage de bambud pendant le consentement oblige à relancer la connexion.
