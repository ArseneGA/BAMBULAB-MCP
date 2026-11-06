# Le jour du branchement de la P1S

Procédure à dérouler **avec vous devant l'imprimante**. Jusqu'ici, tout a été validé contre un simulateur (`docs/ETAT.md`) : ce jour-là, on découvre ce que le simulateur ne pouvait pas prouver. Chaque étape a un **point d'arrêt** : si le résultat n'est pas celui annoncé, on s'arrête, on note, on ne force pas.

Règles du jour :

- **Aucune impression n'est lancée avant l'étape 8**, et l'étape 8 exige votre accord explicite, un plateau vérifié à l'œil, un fichier court et sans risque, la main près du bouton d'arrêt de l'imprimante.
- Le code d'accès de l'imprimante se saisit **uniquement sur la page web `/setup/printer/…`**. Il ne se dicte pas à Claude, ne s'écrit dans aucun fichier du dépôt ni aucune conversation.
- On note tout dans un fichier de notes (modèle en fin de document) : il alimentera les « à valider sur la vraie P1S » de `docs/jalons/J7.md` §5.

## 0. Avant de commencer (5 min)

| Vérification | Comment | Attendu |
|---|---|---|
| Le service tourne | `curl -s http://IP:7878/health` | `"status":"ok"` |
| Une sauvegarde récente existe | `sudo systemctl start atelier-backup.service` puis `ls` du dossier de sauvegarde | nouvelle archive + `.sha256` |
| Votre navigateur est enregistré | `sudo atelier-admin enroll --list` | une ligne « actif » ; sinon `sudo atelier-admin enroll` puis `http://IP:7878/enroll` |
| Claude voit l'Atelier | demander « Quel est l'état de l'Atelier ? » | `get_capabilities` : `slicing` disponible, `printer_lan` annoncé « validé contre le simulateur seulement » |
| Une découpe de test fonctionne | demander de découper un petit modèle pour la P1S **sans l'envoyer** | archive `.gcode.3mf` validée |

Point d'arrêt : si le slicing est indisponible, corriger d'abord (`docs/INSTALL.md` §G).

## 1. Préparer l'imprimante (vous, à l'écran de la P1S)

1. Plateau **vide et dégagé**, imprimante sous surveillance pendant toute la procédure.
2. Réglages → Réseau : activer **LAN Only** (mode LAN), redémarrer si demandé.
3. Activer le **Developer Mode** (P1S : firmware ≥ 01.08.02.00 ; si l'option n'existe pas, noter le firmware et **s'arrêter** — on ne contourne pas les mécanismes d'autorisation de Bambu).
4. Relever : **adresse IP** (réservez-la sur la box pour qu'elle ne change plus), **code d'accès LAN** (8 caractères, à l'écran), **numéro de série** (étiquette, ou Réglages → Appareil), **version du firmware**.
5. Matière : notez ce qui est chargé (type, couleur, emplacement AMS ou bobine externe).

Branchement réseau : la P1S et le serveur sont sur le même réseau local. Le serveur n'accepte que des hôtes d'imprimante en adresse privée / lien local / `.local`.

## 2. Déclarer l'imprimante (Claude, sans le code d'accès)

Dites par exemple : « Ajoute ma P1S : IP 192.168.1.50, numéro de série 01P00A…, buse 0,4, pas d'AMS. »

Claude appelle `list_printer_models` puis `add_printer` (nom, modèle `p1s`, hôte, série, buse, AMS déclarés). **Aucun paramètre ne porte le code d'accès ni l'empreinte du certificat.** La réponse contient un lien `…/setup/printer/<id>`.

Point d'arrêt : refus « hôte non autorisé » → l'adresse n'est pas privée ; vérifiez l'IP. N'utilisez pas `--allow-any-printer-host` sauf test en loopback.

## 3. Page `/setup` : empreinte du certificat et code d'accès (vous seul)

Ouvrez le lien dans votre navigateur **enregistré**.

1. Le **serveur** (pas Claude) lit le certificat TLS présenté par l'imprimante — rien n'est encore envoyé — et affiche : l'**empreinte SHA-256**, le **nom du certificat (CN)** et le **numéro de série attendu**.
2. **Vérifiez que le CN correspond au numéro de série que vous avez relevé sur la machine.** La page vous avertit en cas de différence. C'est la vérification d'identité disponible ici : la P1S n'affiche pas l'empreinte de son certificat sur son écran, donc il n'existe pas de second canal indépendant ; on repose sur un réseau local de confiance et sur la détection de tout changement ultérieur (une empreinte différente de celle épinglée est signalée).
3. Saisissez le **code d'accès** : cela **épingle exactement l'empreinte affichée** et enregistre le code dans le coffre chiffré (AES-256-GCM). Tant que rien n'est épinglé, aucune connexion n'est tentée.

Point d'arrêt : CN ≠ série, ou empreinte qui change d'une visite à l'autre → ne validez pas ; vérifiez qu'il s'agit bien de la bonne machine (IP).

## 4. Lecture seule (10–15 min)

Demandez : « Quel est l'état de ma P1S ? » (`get_printer_status`). Attendu : `idle`, `connected`, âge de l'information faible (< 30 s), pas d'erreur HMS. `plate_empty` reste toujours **« inconnu »** : l'imprimante ne peut pas le savoir, c'est vous qui l'attestez plus tard.

À noter pendant ~10 minutes en laissant la machine au repos :

- le firmware ; la forme réelle des rapports (`gcode_state`, présence de `total_layer_num`, format de `hms` / `print_error`) ;
- la **cadence** des rapports en idle (si l'état devient « périmé » en idle, il faudra allonger `MaxStartStatusAge`, voir J7 §5 point 6) ;
- toute déconnexion.

Point d'arrêt : `offline` avec une raison (authentification, TLS, délai) → corriger avant d'aller plus loin ; la raison est dans la réponse, jamais le code.

## 5. Envoi sans lancement

Choisissez un fichier **inoffensif** : un cube de 20 mm (importez un STL, ou demandez `create_parametric_model` avec le gabarit `box` si le worker CAO est installé).

Dites : « Découpe ce cube pour la P1S, valide-le, et dépose-le sur l'imprimante **sans le lancer**. »

Claude enchaîne `execute_headless_slice` → `validate_print_artifact` → `upload_print_artifact` (avec une clé d'idempotence et l'affectation filament → emplacement → matière). L'envoi n'utilise que FTPS : **l'imprimante ne chauffe ni ne bouge**. Claude termine par : « Le fichier X est envoyé sur la P1S. Veux-tu lancer cette impression ? » — **répondez non** pour l'instant.

Contrôles :

- l'état de l'imprimante reste `idle` (`get_printer_status`) ; rien ne chauffe ;
- le fichier apparaît sur l'imprimante (carte SD / liste des fichiers ou « stockage » de l'écran) ;
- notez le chemin et le nom distants réellement utilisés, et si la reprise de session TLS sur FTPS a été nécessaire (J7 §5 points 3 et 7) ;
- relancez le même envoi (même clé d'idempotence) : « fichier déjà présent », pas de second transfert.

Point d'arrêt : échec FTPS → ne tentez pas de contourner ; notez l'erreur exacte (elle ne contient jamais de secret) et arrêtez-vous.

## 6. Test de refus (dry-run du lancement)

À faire au début de l'étape 8, **après** que Claude a obtenu le lien de confirmation (8.1) et **avant** que vous cliquiez : demandez-lui d'appeler quand même `start_print`. Il doit refuser (« confirmation non approuvée par l'humain »), et aucune commande n'est envoyée à l'imprimante (état toujours `idle`). C'est le test qui montre que l'accord ne vient pas de Claude. Faites-le aussi une fois après un clic vieux de plus de 2 minutes : l'accord doit avoir expiré.

## 7. Test de l'application dans Claude Desktop

Dans Claude Desktop, demandez « Ouvre l'atelier » (outil `show_atelier`) puis, une fois `request_print_confirmation` appelée (étape 8), « Montre la confirmation de lancement » (`get_print_confirmation_view`). Vérifiez que la vue s'affiche **dans** la conversation et qu'elle est **en lecture seule** : rien dans la vue ne doit pouvoir lancer ou arrêter une impression. Le contenu exact des vues MCP App est celui du chantier `web/` (voir `docs/ETAT.md`). Si Claude Desktop n'affiche pas la vue intégrée, notez-le : le lien web `/confirm/…` reste la seule voie d'accord, le lancement ne dépend pas de la vue. À noter aussi : l'indicateur `visibility: ["app"]` de ces outils dépend de l'hôte et n'a pas été qualifié avec Claude Desktop.

## 8. Première impression réelle (accord explicite, présence obligatoire)

Conditions, toutes réunies : plateau vérifié visuellement par vous, bonne matière chargée, fichier court (cube 20 mm), main près du bouton d'arrêt.

1. Dites : « Oui, je veux lancer ce cube sur la P1S. » Claude appelle `request_print_confirmation` et vous donne un lien `…/confirm/<id>`.
2. **Dans votre navigateur enregistré**, ouvrez le lien. Vérifiez le résumé (fichier, imprimante, empreinte, plateau, matières, durée), cochez « le plateau est vide et dégagé », cliquez « Lancer l'impression ». Votre accord vaut **2 minutes**.
3. Dites à Claude de lancer : `start_print`. Issues possibles : `printing` (c'est parti), `start_unknown` (liaison perdue après la commande : ne **relancez pas**, demandez `reconcile_print`), `rejected`, `not_sent` (rien n'est parti ; il faudra re-cliquer).
4. Suivez : « Où en est l'impression ? » (`get_print_status` : couche, %, temps restant). Notez : l'URL `project_file` qui a fonctionné, la réponse de l'imprimante, les états traversés (PREPARE peut durer plusieurs minutes), le comportement de l'`ams_mapping`.

**Arrêter** : toujours possible au bouton de l'imprimante. Via Claude : `cancel_print` ne stoppe rien lui-même, il crée une demande d'arrêt et renvoie un lien `…/stop/<id>` à valider dans votre navigateur enregistré.

À la fin : vérifiez le résultat réel (qualité, dimensions) et dites-le à Claude (`record_print_result`) : c'est la mémoire des essais.

## 9. Pause, reprise, perte réseau (sous surveillance, optionnel)

Pendant une impression de test : pause/reprise depuis l'imprimante (l'Atelier doit refléter l'état). Pour la perte réseau : couper le Wi-Fi du serveur quelques secondes juste après un lancement de test doit produire `start_unknown` puis, après `reconcile_print`, l'état réel. Ne le faites que sous surveillance et sur un fichier court.

## 10. Test « Windows éteint »

Objectif : montrer que le serveur travaille seul.

1. Éteignez le PC Windows (Claude Desktop et agent arrêtés).
2. Depuis un **autre appareil** du réseau (téléphone, tablette) avec un navigateur enregistré (`sudo atelier-admin enroll` → `/enroll`), ouvrez l'interface web de l'Atelier à `http://IP:7878/` (servie par bambud ; livrée par le chantier `web/`, voir `docs/ETAT.md` — sans elle, les pages `/enroll`, `/confirm`, `/stop`, `/setup` et l'API `/api/…` restent disponibles) : consultez le catalogue, lancez une découpe, suivez le travail, regardez l'imprimante.
3. Vérifiez que `get_capabilities` (ou l'interface) indique `mode: linux_seul` et l'agent « hors ligne », et qu'un import « uniquement sur Windows » reste marqué comme tel (non disponible hors ligne).
4. Rallumez Windows : l'agent se reconnecte ; ses travaux perdus sont `unknown` puis réconciliés — rien n'est relancé tout seul, aucune impression en double.

La conversation avec Claude, elle, exige un client MCP : Claude Desktop n'est pas disponible Windows éteint. Un accès conversationnel depuis un autre appareil demande un connecteur MCP distant authentifié (non configuré, voir `docs/ETAT.md`).

## 11. Fin de journée

- `sudo atelier-admin verify` puis une sauvegarde manuelle (`sudo systemctl start atelier-backup.service`).
- Reporter vos notes dans `docs/jalons/J7.md` §5 (points validés / infirmés) et corriger `docs/ETAT.md` : ce qui passe de « simulé » à « essayé sur matériel ».
- Remettre l'imprimante dans l'état voulu (plateau, matière).

## Modèle de notes

```
Date :            Firmware P1S :          Developer Mode : oui/non   LAN Only : oui/non
IP :              Série :                 Buse :   AMS :
Étape 3 : CN = série ? oui/non ; empreinte stable sur 2 visites ? oui/non
Étape 4 : cadence des rapports en idle : ___ s ; gcode_state vu : ___ ; total_layer_num : oui/non ; format hms : ___
Étape 5 : chemin distant : ___ ; reprise TLS FTPS nécessaire : oui/non ; EPSV : oui/non ; idempotence : ok/ko
Étape 8 : url project_file : ___ ; réponse : ___ ; états : ___ ; durée de PREPARE : ___ ; ams_mapping : ok/ko
Étape 9/10 : résultats : ___
Anomalies / décisions :
```
