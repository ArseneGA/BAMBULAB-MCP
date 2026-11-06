# Copilote Bambu Studio — dossier de départ pour Claude Code

Version : 4 novembre 2026. Statut : cadrage et instructions de réalisation, pas logiciel implémenté.

## 1. Objectif utilisateur et décisions prises

Construire une plateforme généraliste de gestion, création et préparation de fichiers 3D, hébergée sur un ancien ordinateur Linux en mode serveur, utilisable depuis Claude Desktop sous Windows via MCP avec une interface interactive intégrée, pour passer d'une idée, d'un fichier STL/STEP/3MF ou du projet déjà ouvert dans Bambu Studio à une pièce préparée pour une Bambu Lab P1S.

L'utilisateur veut créer/modifier la géométrie, analyser la fabricabilité, optimiser les réglages selon le matériau et la finition, modifier le projet ouvert (objets, supports, coutures), comparer automatiquement plusieurs variantes, envoyer le résultat à la P1S et pouvoir démarrer depuis la conversation.

Séquence de lancement demandée : préparer → valider le fichier → envoyer → demander explicitement si l'utilisateur veut imprimer → lancer après sa réponse positive. Une autorisation pour une impression ne vaut pas pour les suivantes. L'envoi seul ne déclenche aucun chauffage ni mouvement.

Exigence centrale : outil généraliste, extensible à différents matériaux, géométries, usages et finitions compatibles avec la machine. Aucun matériau ou effet particulier ne doit structurer ou limiter l'architecture. L'utilisateur décrit librement son résultat ; le système identifie les critères pertinents, les contraintes et les compromis. Le PETG transparent est uniquement un exemple facultatif parmi d'autres, sans priorité imposée.

## 2. Première mission à donner à Claude Code

**Instruction prioritaire de l'utilisateur : commence par les maquettes de l'interface. Le premier livrable est une proposition visuelle haute fidélité, ultra moderne, précise et singulière. « AI slop » interdit.** Ne commence pas par compiler Bambu Studio, déployer le serveur, construire le backend ou connecter l'imprimante. Une vérification rapide des contraintes de l'hôte est utile ; elle ne doit pas remplacer le travail de design.

### 2.1 Premier livrable : maquettes et prototype navigable

Lis tout le dossier puis conçois un véritable outil de travail 3D, pas une landing page ni un tableau de bord SaaS générique. Livrer une direction artistique cohérente et des maquettes haute fidélité accompagnées d'un prototype frontend navigable, avec données de démonstration clairement identifiées. Le prototype doit être consultable sans serveur Linux, sans clé API et sans imprimante. Garder le prototype de design séparé de l'intégration technique réelle.

Concevoir les deux contextes : une vue compacte intégrée à la conversation Claude, respectant le cadre de l'hôte, et un atelier web plus ample dans le navigateur. Ne pas dessiner un panneau ou un mode d'affichage Claude non vérifié comme une capacité acquise. Présenter le contexte de conversation autour de la vue compacte comme une simulation illustrative.

Écrans à maqueter : bibliothèque avec recherche et collections ; fiche modèle avec viewer 3D, fichiers et versions ; préparation avec sélection d'objet/zone et réglages ; comparaison de variantes ; matériaux et objectifs de finition ; transfert, confirmation de lancement et suivi d'impression. Les relier par un parcours lisible : retrouver/importer → inspecter → préciser le résultat → comparer → préparer/envoyer → confirmer le lancement.

Le prototype doit réellement permettre navigation, sélection, recherche sur les données de démonstration, changement de variante et ouverture des panneaux. Si un viewer est présent, utiliser une géométrie locale navigable pour rotation/zoom plutôt qu'une image présentée comme interactive. Les calculs, la connexion Studio et l'impression restent explicitement simulés ; aucun clic de démonstration ne contacte la P1S.

### 2.2 Direction visuelle : exigence anti « AI slop »

- Priorité au modèle 3D, à la lisibilité des fichiers et à la précision des contrôles. Hiérarchie typographique nette, alignements rigoureux, densité utile, proportions et espacements intentionnels.
- Palette retenue et cohérente, contrastes accessibles, accent fonctionnel limité ; thèmes clair/sombre conçus avec soin. Iconographie homogène et libellés français concrets.
- Interdire les dégradés violet/bleu par défaut, halos néon, glassmorphism systématique, blobs, effets 3D décoratifs et accumulation de cartes arrondies sans raison fonctionnelle. Aucun de ces artifices ne vaut une direction artistique.
- Pas de bandeau marketing, slogans creux, emojis décoratifs, faux indicateurs, score magique de qualité, badges « AI powered » ou pourcentages de résistance/transparence inventés.
- Éviter le squelette interchangeable « sidebar + quatre KPI + grille de cartes ». Employer listes, arborescences, grilles ou panneaux uniquement là où ils servent le travail sur les fichiers et la géométrie.
- Les animations expliquent une transition, une sélection ou un état ; elles restent discrètes, rapides et désactivables. L'interface doit demeurer fluide sur du matériel modeste.
- Définir les états sélectionné, survol, focus clavier, chargement, vide, erreur, hors ligne et conflit de version. Windows éteint, fichier non encore copié et imprimante indisponible doivent avoir un traitement lisible.
- Le design doit avoir une personnalité propre à un atelier de fabrication contemporain. Justifier brièvement les choix par l'usage ; ne pas copier l'identité d'un produit existant.

### 2.3 Remise et contrôle visuel

Fournir : prototype consultable, captures des écrans principaux dans les deux contextes, parcours annoté et mini système de design (typographie, couleurs, espacements, contrôles, états). Contrôler les écrans dans un navigateur, corriger débordements, textes tronqués, contrastes et vues compactes. Ne pas livrer uniquement une description textuelle, un wireframe gris ou une planche de composants.

Présenter ce premier livrable visuel à l'utilisateur avant de poursuivre les grands travaux de backend et d'intégration. Consigner les hypothèses et les fonctions simulées pour permettre une revue concrète. Une maquette n'est pas une preuve de compatibilité MCP Apps ni de fonctionnement de Studio ; conserver les validations techniques suivantes.

### 2.4 Étape technique suivante

Après cette première livraison de maquettes, inventorier le serveur Linux (architecture CPU, RAM, stockage, système) et le poste Windows, prouver l'affichage d'une MCP App avec un modèle 3D réel dans Claude Desktop, puis examiner le dépôt officiel https://github.com/bambulab/BambuStudio. Identifier un tag/commit de référence et consigner son SHA. Compiler la version de référence sur les plateformes nécessaires sans modification avant d'ajouter une passerelle. Le serveur peut utiliser un worker Linux de slicing qualifié ; le contrôle du projet ouvert sous Windows exige un agent sur ce poste.

Produis une matrice vérifiée des capacités : fonction demandée, accès existant (CLI, projet 3MF, code C++, interface graphique), symboles/fichiers concernés, limites et preuve d'exécution. N'invente aucune API de Bambu Studio. Les noms d'outils du présent dossier sont des contrats proposés.

Développe ensuite la première tranche verticale : Claude Desktop → interface MCP Apps → catalogue serveur → visualisation 3D d'un fichier réel → modification d'un paramètre d'un objet sur copie → worker de slicing → validation → ouverture du résultat dans Studio. Préserve l'objectif final de contrôle du projet ouvert. Complète progressivement chaque jalon et rapporte les preuves, les limites et les blocages réels.

## 3. Architecture cible : serveur Linux, interface intégrée et agent Windows

- Serveur Linux permanent : catalogue, métadonnées, fichiers, révisions, orchestration, travaux persistants et adaptateur P1S.
- Frontend React/TypeScript avec viewer Three.js, partagé entre application web autonome et vues MCP Apps intégrées à Claude. Adapter chaque hôte, ne pas tenter d'embarquer sans adaptation une application web complète dans un iframe.
- Serveur MCP TypeScript et extension officielle @modelcontextprotocol/ext-apps. Figer une combinaison SDK compatible après lecture des docs actuelles ; ne pas copier un import ancien sans validation.
- Workers Python pour géométrie/CAO ; workers de slicing CLI Linux ou Windows selon capacité mesurée.
- Agent Windows : synchronisation d'artefacts, lancement des outils locaux, contrôle de la session Bambu Studio et passerelle C++ si nécessaire.
- Pilotage visuel complémentaire explicitement configuré ; ne pas supposer un accès souris/clavier intrinsèque à Claude Desktop.
- Passerelle C++ limitée aux fonctions de Studio qui nécessitent un accès interne, après audit.

Connexion initiale recommandée : petit connecteur MCP local stdio sur Windows, servant de passerelle vers le service Linux sur le LAN. Il relaie outils, ressources UI et négociation des capacités. Valider réellement cette chaîne dans Claude Desktop ; le support générique de MCP Apps ne remplace pas ce test. Un connecteur distant Streamable HTTP est une autre voie, avec authentification et accessibilité adaptée.

Attention : les connecteurs distants personnalisés de Claude sont contactés depuis l'infrastructure Anthropic. Une IP privée du LAN ne suffit pas. Ne pas présumer qu'un VPN privé du poste ou les tunnels proposés pour l'API sont automatiquement accessibles à l'application Claude. Si cette voie est choisie, qualifier un accès HTTPS authentifié ; n'exposer ni fichiers bruts ni imprimante.

Pour l'IPC local de la passerelle C++ Windows : évaluer des named pipes avec contrôle d'accès. Définir cadrage des messages, version, tailles, erreurs, timeout et annulation. Les communications Linux/agent sont distinctes, authentifiées et versionnées ; utiliser des identifiants d'artefacts et des transferts contrôlés, pas des chemins supposés communs.

Les mutations de Studio passent par son thread GUI et ses mécanismes existants d'invalidation. Vérifier undo/redo, refuser les révisions périmées et sérialiser les écritures. Linux ne lit pas magiquement l'état d'une application Windows.

Sur matériel ancien : un service de contrôle léger, base SQLite et stockage sur disque comme option initiale ; file persistante et un worker lourd à la fois par défaut. Éviter les microservices et dépendances supplémentaires sans justification. L'intégration Manyfold peut exiger d'autres services : mesurer l'ensemble avant de la retenir. L'IA n'est pas exécutée localement par défaut. Le viewer calcule sur le poste client et utilise des dérivés allégés ; conserver toujours les originaux. Quand Windows est éteint, seuls le contrôle de sa session Studio et ses workers deviennent indisponibles. Le catalogue, la création/modification sur les backends Linux qualifiés, le slicing Linux, la comparaison séquentielle et le transfert/suivi P1S doivent fonctionner sur Linux pour les cas de référence validés. Mesurer les limites de taille/complexité et les annoncer ; ne pas promettre des performances à partir de la seule RAM.

## 4. Repères source : début d'audit, pas cartographie complète

Références consultées dans la conversation :

- https://github.com/bambulab/BambuStudio/blob/master/src/BambuStudio.hpp : classe CLI et structures d'assemblage, paramètres d'objets et plages de hauteur ; présence d'une structure ne prouve pas qu'une option CLI exploitable existe.
- https://github.com/bambulab/BambuStudio/blob/master/src/slic3r/GUI/Plater.hpp : éditeur, événements, liens vers travaux et gestion des objets.
- https://github.com/bambulab/BambuStudio/blob/master/src/slic3r/Utils/NetworkAgent.hpp : interface de communication, opérations de transfert et lancement distinctes.
- https://github.com/bambulab/BambuStudio/wiki/Command-Line-Usage : CLI documentée et nécessité de configurations complètes.

Les liens master sont mobiles : revalider les symboles au SHA choisi. Examiner ensuite libslic3r, les profils, la sérialisation 3MF, les gizmos de peinture et le traitement asynchrone en suivant les appels réels. Vérifier les instructions AGENTS.md/CLAUDE.md locales s'il en existe.

Le dépôt est la base du travail, mais le composant réseau propriétaire et le firmware ne deviennent pas ouverts parce que Studio est recompilé. Vérifier la licence et les conditions de redistribution si une diffusion du fork est envisagée.

## 5. Géométrie et CAO

Pour une idée : générer une définition paramétrique avec dimensions et hypothèses explicites, conserver le modèle source éditable et exporter un format adapté au slicer. Évaluer un backend CAO local tel que FreeCAD ou CadQuery avant choix ; Blender peut compléter le travail de maillage. Ne pas multiplier les dépendances sans besoin.

Pour STEP : préserver autant que possible la géométrie et ses unités, puis tesseller avec une tolérance contrôlée. Pour STL : demander/confirmer les unités si ambiguës ; distinguer réparation de maillage et modification de conception. Pour 3MF : préserver les objets, parties, transformations, affectations matière et métadonnées nécessaires.

Mesurer dimensions, composants, bords ouverts, dégénérescences et épaisseurs estimées avec limites de confiance. Les forces et la fonction mécanique ne se déduisent pas avec certitude d'un maillage : recueillir les hypothèses qui changent la conception. Ne pas présenter un score heuristique comme une certification mécanique.

## 6. Contrats MCP proposés

Chaque outil utilise des identifiants gérés par le service, valide son schéma et retourne des données structurées. Les outils longs retournent job_id ; le travail survit à une déconnexion de Claude Desktop. Les logs vont sur stderr, jamais sur stdout du transport stdio.

| Outil | Entrées principales | Résultat et règle |
|---|---|---|
| inspect_environment | aucune donnée sensible | Versions, exécutables, capacités, éléments manquants |
| get_capabilities | session Studio éventuelle | Fonctions réellement disponibles et limites |
| create_parametric_model | description, dimensions, contraintes | Modèle source, export, hypothèses, révision |
| import_model | fichier autorisé, unités éventuelles | Identifiant modèle et rapport d'import |
| get_project_state | session_id | Révision, objets, plateaux, paramètres effectifs |
| analyze_geometry | model_revision, cible machine | Mesures, zones, problèmes, confiance |
| set_manufacturing_goals | matériau, finitions, priorités, zones | Objectif structuré et ambiguïtés restantes |
| modify_geometry | model_revision, opérations typées | Nouvelle révision, aperçu et différences |
| modify_slicing_profile | base_revision, modifications typées | Profil complet validé, provenance et différences |
| modify_open_project | session_id, expected_revision, opérations | Nouvelle révision, état relu, différences |
| create_experiment | projet figé, candidats, budget | Expérience et travaux indépendants |
| execute_headless_slice | révisions, plateau | job_id et état initial |
| get_job_status | job_id | État, journaux bornés, résultats, erreurs |
| compare_variants | experiment_id | Tableau des mesures et critères estimés |
| capture_preview | projet ou artifact_id, vue, couches | Image avec légende et paramètres de rendu |
| apply_variant | variante, expected_project_revision | Application contrôlée au projet final |
| validate_print_artifact | artifact_id, printer_id | Rapport et compatibilité vérifiée |
| upload_print_artifact | artifact_id, printer_id, idempotency_key | Référence distante et état du transfert |
| get_printer_status | printer_id | État et fraîcheur des informations |
| start_print | référence déposée, sélection plateau/matière, autorisation | Lancement après accord et vérifications |
| record_print_result | expérience, candidat, photos, observations | Retour d'expérience lié aux réglages réels |
| cancel_job | job_id | Annulation adaptée à la phase, résultat explicite |

Les supports peints et coutures peintes doivent être représentés par régions/facettes stables sur une révision géométrique précise. Toute transformation ou retessellation exige de vérifier la validité de ce mapping. Ne pas inventer un format de peinture puis supposer que Studio le comprend.

## 7. Agents : stratégie de développement et stratégie d'exécution

Distinguer les sous-agents utilisés par Claude Code pour développer du logiciel des agents qui fonctionneront ensuite dans le produit. Un serveur MCP n'engendre pas automatiquement des agents.

Dans le produit : un orchestrateur attribue des tâches à des agents spécialisés (géométrie, usage mécanique, surface/supports, expériences, vérification). Ils reçoivent des données figées et renvoient des propositions structurées. Ils ne modifient pas simultanément le projet ouvert et ne lancent jamais directement une impression.

Prévoir deux modes d'exécution à qualifier : orchestration par un client Claude qui sait déléguer, ou orchestrateur local utilisant une intégration Anthropic officiellement prise en charge. Vérifier les accès et coûts séparément : ne pas supposer qu'un abonnement à l'application inclut des appels API ou une exécution en arrière-plan illimités. Aucun identifiant ne doit être demandé dans la conversation ou écrit dans Git.

Pour la première version, rendre les outils utiles avec un seul agent. Ajouter ensuite la comparaison concurrente et les spécialisations. Limiter nombre de candidats, durée, appels modèle et ressources CPU/RAM. Un contrôleur unique applique la décision finale.

## 8. Moteur généraliste : matériaux, usages et résultats recherchés

### 8.1 Modèle de données extensible

Séparer quatre dimensions : matériau disponible, fonction de la pièce, finition recherchée et contraintes de production. Ne pas coder une branche métier spécifique à chaque demande ; utiliser des objectifs structurés et des capacités déclarées. Permettre plusieurs objectifs par pièce ou par zone, avec priorités, seuils acceptables et critères non négociables.

Fiche matériau : famille, fabricant, référence, formulation/couleur, diamètre, recommandations fabricant, état de préparation de la bobine, calibrations réalisées et dates. La famille seule ne garantit pas que deux bobines ont les mêmes propriétés. Fiche machine : P1S, firmware, buse (diamètre/type), plateau, AMS éventuel et capacités vérifiées. Les valeurs inconnues restent explicites.

La bibliothèque doit pouvoir accueillir PLA, PETG, ABS/ASA, TPU et autres familles, ainsi que des formulations chargées ou particulières, sans prétendre que chaque référence est compatible avec la P1S, sa buse ou son système d'alimentation. L'ajout d'une référence ou d'un objectif passe par des données et règles versionnées ; de nouveaux algorithmes peuvent demander une extension logicielle. Aucun matériau inconnu ne reçoit silencieusement les paramètres d'un autre.

### 8.2 Interprétation des demandes

| Demande illustrative | Critères à formaliser | Preuve ou mesure à rechercher |
|---|---|---|
| Une pièce esthétique mate, brillante ou avec peu de lignes | Aspect souhaité, faces visibles, post-traitement accepté | Comparaison de surfaces dans des conditions répétables |
| Un support ou une pince qui résiste à un effort | Direction/valeur des efforts, déformation acceptable, fixation | Calcul adapté et/ou essai mécanique |
| Deux éléments qui s'emboîtent précisément | Dimensions fonctionnelles, jeux, type d'assemblage | Mesures dimensionnelles et essai d'assemblage |
| Une pièce souple ou un joint | Flexibilité, récupération, compression, environnement | Essai fonctionnel défini pour l'usage |
| Une pièce exposée à la chaleur, à l'extérieur ou à un produit | Conditions réelles et durée d'exposition | Données de référence et essai pertinent |
| Une grande pièce avec peu de déformation | Dimensions finales, planéité, surfaces d'appui | Mesures et observation après impression |
| Une impression rapide ou économe | Temps/matière maximum et qualité minimale | Estimations du slicer puis retour réel |
| Une pièce transparente ou diffusante | Vision nette, diffusion, brillance, zones et épaisseur | Éprouvettes et observation optique comparative |

Ces lignes expriment des besoins et des méthodes d'évaluation, pas des recettes universelles ni des garanties de résultat.

Pipeline : extraire les objectifs → lire la géométrie et le contexte → identifier les capacités et données disponibles → choisir une base compatible → proposer des candidats justifiés → slicer et comparer → présenter les compromis → appliquer → exploiter les résultats observés.

Si plusieurs objectifs sont incompatibles, proposer des solutions de compromis ou des variantes par zone. Demander uniquement la priorité qui change effectivement le choix. Ne pas imposer au client une liste fermée de finitions ; garder sa formulation originale et signaler les objectifs non encore évaluables.

### 8.3 Connaissances et expérimentation

Avant de proposer des valeurs numériques de procédé, consulter les recommandations actuelles du fabricant et des références techniques primaires pertinentes ; documenter source, date et domaine de validité. Réutiliser les calibrations validées seulement dans des conditions comparables. Construire des candidats bornés autour de profils compatibles, en conservant les réglages de démarrage et limites machine validés.

Distinguer mesure géométrique, estimation du slicer, heuristique et propriété physique observée. Une preview ne démontre ni résistance, ni brillance, ni étanchéité, ni transparence. Définir des éprouvettes adaptées au critère, avec identifiant, géométrie et paramètres conservés. Faire varier un nombre limité de facteurs par campagne. Les retours alimentent une mémoire contextualisée, sans transférer aveuglément les résultats à un autre matériau ou une autre géométrie.

### 8.4 Exemple facultatif : PETG transparent

Une demande de transparence peut viser la lisibilité à travers une paroi, la diffusion lumineuse ou une surface brillante avec peu de lignes. Les traiter séparément ; la diffusion peut entrer en conflit avec la vision nette. Comparer sur une géométrie et un éclairage connus. Photos et appréciations donnent des indicateurs comparatifs, pas des mesures optiques certifiées. Ce cas ne conditionne pas la livraison du moteur généraliste.

## 9. Slicing reproductible

Figer binaire, profils et ressources de la même version. Vérifier --help sur le binaire réellement installé. Résoudre l'héritage vers des configurations complètes sans copier arbitrairement des fragments de presets. Garder les paramètres effectifs et leurs provenances.

Lancer par tableau d'arguments, sans shell. Répertoire par travail, entrées immuables, quotas, timeout, collecte des sorties, arrêt de l'arbre de processus sous Windows si nécessaire. Ne pas supposer qu'un exécutable headless n'a aucune dépendance graphique.

Après succès : vérifier l'archive 3MF, ses métadonnées et les trajectoires attendues pour le plateau retenu ; vérifier compatibilité modèle machine, buse, plateau et affectation matière. Le suffixe .gcode.3mf et le code de sortie zéro ne suffisent pas. Comparer les métriques disponibles sans inventer de prédiction de résistance ou de transparence.

Conserver modèle/projet, profils effectifs, version du slicer, commande, environnement pertinent, journaux, durée, matière estimée, artefact et empreintes. Distinguer reproductibilité fonctionnelle et identité binaire d'une archive pouvant contenir des dates.

## 10. Dépôt et démarrage P1S

Qualifier le firmware et ses possibilités LAN/Developer Mode ou voie officielle. Ne pas contourner les mécanismes d'autorisation. La voie d'intégration officielle peut avoir des contraintes GUI ; l'auditer avant de promettre une exécution totalement automatisée.

Séparer transfert de fichier (protocole FTP/FTPS selon compatibilité vérifiée), commande et télémétrie (MQTT/MQTTS selon compatibilité). Qualifier ports, certificats et authentification ; ne pas diffuser de secrets dans les logs ou les outils MCP. Garder les secrets dans un stockage adapté à chaque hôte, avec droits minimaux, et ne pas les transmettre au frontend.

États métier proposés : DRAFT → PREPARED → SLICING → VALIDATED → UPLOADING → UPLOADED → AWAITING_PRINT_CONFIRMATION → START_REQUESTED → PRINTING → COMPLETED ; sorties FAILED/CANCELLED/START_UNKNOWN selon phase.

La confirmation lie l'imprimante, l'empreinte du fichier, le plateau choisi et l'affectation matière. Changer un élément invalide cette confirmation. L'outil de lancement doit exiger le contexte d'autorisation fourni par le mécanisme de confirmation retenu ; un simple booléen inventé par un agent n'est pas une preuve suffisante. Qualifier les confirmations offertes par le client ; si nécessaire fournir une petite interface locale de confirmation. Une fois le fichier envoyé, Claude demande « Le fichier X est envoyé sur la P1S. Veux-tu lancer cette impression ? »

Réserver l'imprimante et vérifier son état avant démarrage. Distinguer l'accusé de transport, l'acceptation de la commande et l'état PRINTING observé. En cas de perte réseau après start, interroger l'état et réconcilier avant de réessayer ; ne pas garantir exactly-once si le protocole ne le permet pas. Une imprimante idle ne prouve pas que le plateau est vide : intégrer cette information aux conditions de lancement.

## 11. Jalons et critères de réception

| Jalon | Démonstration exigée |
|---|---|
| D0 — maquettes en premier | Direction visuelle ultra moderne et singulière, prototype navigable des six espaces, vues Claude compacte et web, états importants et captures contrôlées ; données et opérations simulées identifiées, aucun backend ni lancement réel requis |
| J0 — architecture et UI | Matériel Linux/Windows inventorié, MCP App visible dans Claude Desktop via la chaîne retenue, viewer d'un modèle réel et sélection partagée ; accès distant/local distingués |
| J1 — catalogue et audit | Import/indexation non destructifs, recherche, doublons exacts, références de fichiers ; versions et SHA consignés, compilation de référence et matrice de couverture sourcée |
| J2 — tranche verticale | Modifier un objet sur copie, slicer, inspecter et ouvrir le résultat ; original conservé |
| J3 — projet ouvert | Lecture de l'état réel, modification avec révision, mise à jour Studio et retour arrière qualifié |
| J4 — géométrie et réglages locaux | Idée paramétrique + import, transformations, modificateurs et fonctions supports/coutures prouvées individuellement |
| J5 — variantes | Au moins trois candidats isolés, tableau des métriques disponibles, application du candidat choisi |
| J6 — P1S | Transfert sans lancement, accord demandé, démarrage observé, comportement après timeout vérifié |
| J7 — moteur généraliste | Démontrer au moins trois usages distincts et deux références de matériaux compatibles ; objectifs, candidats et retours contextualisés ; un matériau ou critère inconnu est signalé ; le PETG transparent reste facultatif |
| J8 — distribution hybride | Installation Linux et agent Windows, sauvegarde/restauration ; démonstration de préparation, slicing, comparaison et transfert avec Windows réellement éteint, puis mode assisté ; accès client indépendant qualifié, reprise et mises à jour |

Les preuves d'impression réelle nécessitent l'imprimante et l'utilisateur ; un mock ne vaut pas un essai matériel. Ne pas déclarer « Studio entièrement contrôlable » sans inventaire et couverture vérifiée de toutes les fonctions revendiquées.

## 12. Instructions à reprendre dans CLAUDE.md

- Première tâche obligatoire : livrer les maquettes et le prototype d'interface décrits en section 2, ultra modernes et sans « AI slop », avant les grands travaux techniques. Le jalon D0 précède J0 ; il ne remplace pas les preuves d'intégration.
- Maintenir l'objectif complet et généraliste ; ne pas spécialiser le produit autour du PETG transparent. Découper le développement en jalons vérifiables.
- Lire le code avant de choisir les points d'intégration. Établir un rapport de capacité avec symbole, chemin, SHA et preuve pour chaque outil.
- Compiler la référence avant le patch. Garder une modification C++ limitée et explicable.
- Ne pas supposer un système de plugins générique, des API privées stables, ou une compatibilité réseau à partir du seul modèle d'imprimante.
- Ne pas inventer de paramètres ou de signatures. Générer les contrats à partir du schéma choisi et valider les valeurs effectives.
- Préserver les projets et presets utilisateur ; travailler sur copies/révisions. Sérialiser les écritures.
- Tester notamment : révision concurrente, annulation, archive invalide, paramètres incompatibles, interruption après upload, commande start incertaine et absence de confirmation.
- Aucun lancement automatique sans l'accord demandé pour ce travail. Aucun accès aux secrets dans les rapports d'agents.
- Distinguer faits mesurés, prédictions, hypothèses et validations physiques.
- Plusieurs agents peuvent développer des modules distincts avec contrats communs ; un responsable intègre et vérifie la tranche complète.
- Ne pas demander à l'utilisateur de choisir une bibliothèque ou de diagnostiquer une erreur de build que tu peux résoudre. Demander uniquement les informations qui changent réellement le résultat ou les accès indisponibles.
- Fournir un état honnête : implémenté, essayé, simulé, restant. Livrer commandes PowerShell exactes et configuration Claude Desktop adaptées à l'installation réelle.

## 13. Informations à relever au démarrage

Décisions confirmées : serveur Linux disposant d'environ 8 à 16 Go de RAM (quantité exacte à mesurer) ; modes Linux seul et Linux avec renfort Windows requis ; fichiers 3D actuellement sur Windows. L'autonomie de préparation sur Linux fait partie des critères de livraison, pas d'une extension facultative.

Modèle du serveur Linux, CPU/architecture, RAM, distribution et version, stockage libre et support de sauvegarde ; versions Windows, Studio, Claude Desktop et Claude Code ; disponibilité du poste Windows ; emplacement, taille et nombre des fichiers existants ; accès souhaité LAN ou extérieur ; chemins des exécutables et outils de compilation ; firmware P1S ; buse, plateau et AMS éventuel ; matériaux disponibles et premiers projets ; méthode d'accès aux agents et budget d'expérimentation.

Ces informations ne bloquent pas l'audit source ni les contrats. Ne pas transformer un champ inconnu en valeur supposée. Une installation fraîche du projet ne doit effectuer aucun transfert ou démarrage sans demande utilisateur.

## 14. Gestionnaire de fichiers 3D et interface dans Claude — extension centrale du périmètre

### 14.1 Expérience attendue

Le produit est un atelier 3D avec catalogue, viewer, conception, préparation et impression. La conversation et l'interface opèrent sur les mêmes identifiants et révisions. Exemple : « Retrouve ma pince, ouvre la dernière version, renforce cette zone et compare deux variantes. » Un clic utilisateur doit actualiser le contexte utile au modèle ; une action de Claude doit actualiser la vue sans perdre la sélection.

Vues : bibliothèque (grille/liste, recherche, tags, collections, filtres), fiche modèle (viewer, dimensions, fichiers associés, provenance, licence), projet (objets, zones, objectifs et paramètres effectifs), comparaison (variantes et compromis), tâches/impression (états, erreurs et lancement après accord), matériaux/calibrations.

Prévoir rotation, zoom, vues standards, sélection d'objet/zone et comparaison synchronisée. Mesures et coupe sont des capacités à développer et valider ; une coupe visuelle n'est pas une analyse mécanique. La peinture exacte de supports/coutures et le G-code interactif sont des modules spécifiques, pas des fonctionnalités offertes automatiquement par Three.js.

### 14.2 Modèle documentaire et opérations

Distinguer collection, modèle logique, fichier source, version immuable, projet Studio, variante, profil effectif, artefact slicé et impression physique. Relier la chaîne de provenance. Un nouveau nom n'est pas une nouvelle géométrie ; deux fichiers de géométrie identique peuvent avoir des métadonnées et droits différents.

Capacités demandées : import, scan de dossiers, tags/collections, recherche, renommage/déplacement maîtrisés, doublons exacts par empreinte, export groupé, corbeille et restauration. Similarité géométrique = fonction ultérieure avec indice de confiance, jamais suppression automatique.

Commencer par indexation en lecture seule des dossiers existants. Proposer ensuite un import géré ou une organisation du disque explicitement choisie ; ne pas réorganiser silencieusement la collection. Déplacer/renommer ne doit pas casser les projets et références. Stocker les gros fichiers hors de la base. Prévoir pagination, index incrémental et vignettes en arrière-plan, limites d'extraction d'archives et validation des formats.

Conserver les originaux STL/STEP/3MF/OBJ, les sources CAO et les éventuelles dépendances. Un GLB ou maillage simplifié sert à la visualisation uniquement et ne remplace pas le modèle de fabrication. Une archive 3MF peut inclure plusieurs objets et des données que le viewer ne lit pas : annoncer ce qui est réellement représenté. Prévoir sauvegarde des fichiers ET de la base, et vérifier la restauration.

Outils à ajouter : search_models, list_collection, get_model_details, import_files, scan_folder, tag_models, link_model_versions, find_exact_duplicates, preview_file_operations, apply_file_operations, restore_from_trash, get_preview_asset. Chaque mutation expose portée et différence et conserve un journal. Les actions de masse utilisent un aperçu et les suppressions restent récupérables.

### 14.3 Technologie MCP Apps et preuve précoce

Utiliser le standard MCP Apps : ressource UI déclarée, rendu isolé par l'hôte et échanges via les API du SDK. Négocier les capacités ; fallback textuel et lien vers la web app si une fonction UI manque. Ne pas promettre un panneau latéral permanent ou un mode plein écran sans support constaté sur le client installé.

Vérifier sur la version actuelle de Claude : manifeste de ressources, ui.domain si requis, CSP, thèmes, tailles/modes d'affichage, autorisations des outils et communication UI-hôte. Les noms et formats exacts viennent de la documentation actuelle, pas d'un schéma inventé. Une app publique dans un annuaire n'est pas nécessairement la même procédure qu'une intégration personnelle locale.

Preuve J0 : afficher un véritable fichier 3D du serveur dans Claude Desktop, faire tourner le modèle, sélectionner un objet, transmettre cette sélection au contexte, lire un détail via un outil et rafraîchir l'UI. Tester ensuite un fichier volumineux et un redémarrage. Ne pas attendre la fin du développement pour découvrir une incompatibilité de transport ou de sandbox.

Gros modèles : le serveur garde les fichiers ; le modèle IA reçoit métadonnées et observations utiles, pas des millions de triangles textuels. Préférer dérivés et chargement à la demande. Qualifier le transport des assets (ressources MCP fragmentées ou endpoint autorisé et authentifié), tailles, CSP et mémoire ; pas de gros blobs base64 dans chaque résultat. Le drag-and-drop d'un fichier Windows vers le serveur est à tester dans le sandbox ; fallback par le navigateur ou l'agent local.

### 14.4 Réutilisation de projets existants : décision à mesurer

Manyfold est le premier candidat pour le catalogue auto-hébergé : visualisation, indexation, tags, collections, relations et REST API sont documentés. Vérifier les endpoints et formats nécessaires, les performances sur le vieux serveur et les possibilités de personnalisation. Ne pas supposer que sa gestion de versions immuables ou son slicing intégré sont achevés : sa page de fonctionnalités les distingue de ses fonctions déjà livrées.

Comparer une intégration Manyfold derrière un adaptateur à un catalogue minimal sur mesure. Ne pas entretenir deux catalogues concurrents : désigner la source de vérité des métadonnées, le propriétaire des fichiers et les responsabilités du stockage des versions. Si Manyfold est retenu, l'interface MCP Apps appelle ses fonctions via le service ; ne pas supposer qu'un simple iframe de sa web app sera accepté par Claude.

Spoolman est un candidat optionnel pour l'inventaire des bobines ; ne pas le rendre obligatoire avant besoin et vérifier ses API. Inventaire, quantité réellement disponible, matière chargée et calibrations sont des informations différentes. Aucune intégration ne garantit seule l'état de l'AMS.

### 14.5 Critères de qualité produit

- UI française cohérente, thème clair/sombre, états de chargement et erreurs actionnables ; labels orientés utilisateur.
- Bibliothèque utilisable sans appel au LLM pour chaque clic.
- Sélection et révision explicites ; prévenir toute application d'un réglage à la mauvaise pièce.
- Sauvegarde automatique de l'état métier, reconnexion et reprise ; aucune tâche dépendante de la durée de vie d'un iframe.
- Adapter le détail des previews au client et limiter la concurrence au matériel disponible.
- Mode navigateur complet et UI Claude partagent les règles métier ; leurs sessions sont authentifiées séparément si nécessaire.
- Disponibilité claire : Linux en ligne, agent Windows en ligne/hors ligne, imprimante joignable, capacités correspondantes.
- « Top du top » se traduit par couverture vérifiée, fluidité mesurée sur la collection réelle, traçabilité et résultats physiques documentés ; pas par le nombre d'agents ou de services.

### 14.6 Sources de référence vérifiées le 4 novembre 2026

- MCP Apps et support des hôtes : https://modelcontextprotocol.io/extensions/apps/overview
- Guide officiel Claude, essais locaux et proxy vers serveur distant : https://claude.com/docs/connectors/building/mcp-apps/getting-started
- SDK, exemples Three.js et aide pour agents de développement : https://github.com/modelcontextprotocol/ext-apps
- Connecteurs distants contactés depuis le cloud : https://support.claude.com/en/articles/11175166-get-started-with-custom-connectors-using-remote-mcp
- Fonctions livrées et roadmap Manyfold : https://manyfold.app/features
- Code Manyfold : https://github.com/manyfold3d/manyfold
- Inventaire bobines Spoolman : https://github.com/Donkie/Spoolman

La documentation peut évoluer ; revérifier les procédures exactes et versions avant installation. Les deux pages Anthropic sur extensions/plug-ins et connexions locales ne présentent pas toujours leurs surfaces de la même manière : utiliser le guide MCP Apps propre à Claude et une preuve sur le client installé pour trancher.

## 15. Modes d'exécution et import initial — décisions confirmées

### 15.1 Linux seul : une capacité à livrer

Serveur Linux : environ 8 à 16 Go de RAM. Mesurer CPU, architecture, RAM effective et stockage avant de sélectionner les paquets. Partir d'un seul worker lourd ; comparer les candidats séquentiellement. Définir des budgets de temps, mémoire, espace temporaire et complexité ; ne pas laisser un slicing épuiser le service de catalogue.

Le socle doit préparer un travail sans poste Windows : modèle déjà importé ou source paramétrique compatible, objectifs, modifications prises en charge côté Linux, candidats, slicing, validation et dépôt P1S. La disponibilité effective du mode d'autorisation réseau doit être qualifiée. Pas de dépendance cachée à un disque partagé Windows, une session GUI Windows, un convertisseur installé seulement sur ce poste ou un connecteur local arrêté.

Les opérations propres au projet actuellement ouvert dans Studio sous Windows attendent son retour ; les copies stockées sur Linux restent traitables. Distinguer clairement « modifier une révision sauvegardée » et « modifier la session ouverte ». Préserver les branches et proposer une réconciliation au retour de Windows, sans écraser ses changements locaux.

### 15.2 Linux avec renfort Windows

L'agent annonce sa disponibilité et ses capacités. Le serveur peut lui attribuer CAO, conversion, slicing ou interaction Studio. Chaque travail lie son exécuteur et ses versions. Avant l'envoi, l'agent reçoit les entrées par identifiants/empreintes ; le résultat est rapatrié sur Linux avant de devenir une version publiée.

La perte de l'agent ne déclenche pas une seconde impression. Pour un calcul, marquer l'état inconnu, réconcilier puis reprendre/recréer sur Linux si possible. Prévoir reprise ou relance sans doublonner la publication. Ne pas transférer automatiquement un travail physique start_print entre exécuteurs.

Les sorties Linux/Windows peuvent différer : les qualifier sur un corpus commun et consigner les différences, sans exiger des archives identiques octet pour octet.

### 15.3 Accéder au service quand Windows est éteint

Si Claude Desktop est installé uniquement sur le poste Windows éteint, cette interface et son connecteur local ne peuvent évidemment pas servir d'accès. Le connecteur stdio est donc une voie pour le mode assisté, pas une solution suffisante aux deux modes.

Livrer une web app accessible depuis un autre appareil sur le réseau pour gérer les fichiers et travaux sans Windows. Cela n'offre pas à elle seule une conversation Claude.

Pour converser avec Claude depuis un autre appareil et garder les outils/UI, qualifier également un connecteur MCP distant authentifié accessible au client via la voie officiellement prise en charge. Le serveur Linux héberge ce point d'entrée ; vérifier exigences HTTPS, authentification, accès aux ressources UI et modalités de publication/exposition. Ne pas ouvrir de service public avant choix et configuration de l'accès. Ne pas assimiler un VPN du téléphone à une connectivité depuis le cloud Anthropic.

Démontrer séparément : travail poursuivi après fermeture de Claude ; web app avec Windows éteint ; utilisation conversationnelle et UI depuis l'autre appareil lorsque l'accès distant est configuré. La méthode d'accès hors LAN reste à déterminer sans bloquer le développement du socle.

### 15.4 Fichiers actuellement sur Windows

Premier démarrage : sélection par l'utilisateur de dossiers Windows, inventaire en lecture seule, estimation du volume et de l'espace nécessaire, puis copie contrôlée des modèles choisis vers le stockage Linux. Conserver les originaux Windows, la structure relative, la provenance, les noms et les dépendances. Vérifier les empreintes après transfert ; reprendre les imports interrompus.

Le catalogue Linux ne doit pas se contenter de référencer des chemins Windows qui disparaissent quand le poste est éteint. Un fichier indexé mais non encore copié est explicitement marqué « disponible uniquement sur Windows » et ne fait pas partie des capacités hors ligne.

Linux devient la source de vérité des fichiers importés et de leurs versions gérées. Prévoir exports/copies de travail vers Windows et enregistrement d'une nouvelle révision au retour. Pas de synchronisation bidirectionnelle aveugle, pas de propagation des suppressions et pas de réorganisation des originaux. Les conflits produisent des révisions distinctes avec choix explicite de fusion ou de variante.

Ajouter les états d'import, l'espace disque libre et l'état des transferts à l'interface. Une copie sur le serveur ne constitue pas une sauvegarde : conserver une stratégie de sauvegarde séparée et un essai de restauration.
