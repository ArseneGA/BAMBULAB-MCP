# Backlog

Ordre = priorité. Chaque entrée : besoin, constat d'origine, critère de fin.

## 1. Envoi de fichiers direct vers le serveur (sans agent ni scp)

**Constat (2026-11-07)** : depuis Claude Desktop, Claude a généré 6 STL (presse de gaufrage) sur le PC Windows mais n'a pas pu les importer : le serveur n'importe que des fichiers déjà posés dans `BAMBU_IMPORT_ROOTS`, aucun outil ne permet d'envoyer un fichier, et l'agent Windows (seule autre voie) est bloqué sur un poste d'entreprise qui interdit les `.exe` non signés. Contournement utilisé : `scp` + `sudo cp` vers `/srv/atelier/import`.

**À faire**
- Outil MCP `upload_file` : envoi par morceaux (base64 ≤ ~100 000 caractères par appel), sha256 annoncé puis vérifié, taille max par format, contrôle de signature (STL/OBJ/3MF/STEP), reprise, puis import normal au catalogue (collection, étiquettes). Jamais d'écriture hors du magasin de blobs.
- Interface web : glisser-déposer et bouton « Envoyer des fichiers » dans la Bibliothèque (upload HTTP authentifié par le cookie de navigateur enregistré, protection CSRF existante).
- `get_capabilities` : exposer les dossiers d'import autorisés (chemins), pour que Claude les indique au lieu de deviner.

**Fini quand** : dans Claude Desktop, « importe ces fichiers » sur des STL produits dans la conversation aboutit sans scp ; l'e2e web couvre le glisser-déposer ; tests de sécurité (taille, format, sha faux, traversée) verts.

## 2. Vues MCP App à travers le pont `mcp-remote`

**Constat** : Claude Desktop est branché via `npx mcp-remote` (le pont `bambu-mcp.exe` est bloqué par la politique du poste). Il reste à vérifier que les ressources `ui://` et `_meta.ui` passent par ce pont (viewer 3D dans la conversation). Sinon : accès direct par URL (point 3) ou pont signé.

**Constat (2026-11-07)** : « Montre-moi la matrice mâle en 3D » dans Claude Desktop → Claude a renvoyé une image PNG (capture_preview) et un lien GLB, sans viewer 3D intégré. À diagnostiquer : appel à `get_model_details` (outil lié à `ui://bambu/model.html`) et transmission de la ressource UI par `mcp-remote`.

## 2 bis. Aperçu « sur le plateau » dans la conversation

Utilisateur : « dommage qu'on n'ait pas une vue du plateau directement là ». `capture_preview` doit pouvoir rendre la ou les pièces posées sur le plateau de l'imprimante cible (P1S 256 × 256 mm, grille, orientation et position réelles après découpe ou arrangement), y compris plusieurs pièces d'une même plaque, et l'aperçu des couches du G-code. Marche même sans MCP App (image), et sert de repli au viewer 3D.

## 3. Connecteur distant direct (URL `https://…/mcp`) avec OAuth 2.1

Pour claude.ai web/mobile sans installation : OAuth 2.1 dans bambud (connexion via navigateur enregistré, jetons révocables et à durée limitée), exposition Funnel limitée à `/mcp` + routes OAuth (jamais `/setup`, `/confirm`, `/stop`, `/agent/v1`, `/enroll`).

## 4. Pont et agent Windows signés

`bambu-mcp.exe` et `bambu-agent.exe` signés (ou autorisés par l'informatique) pour les postes verrouillés.
