# Ignorer un objet raté en cours d'impression : essai sur la vraie P1S

Ce document est le **mode d'emploi de l'essai humain** de l'étape SK0 (`docs/specs/SK-ignorer-un-objet.md`). Il sert à répondre à une seule question avant d'aller plus loin : **la P1S en réseau local accepte-t-elle l'ordre « ignorer cet objet » ?** Bambu le permet officiellement sur d'autres machines ; pour la P1S en réseau local (sans le cloud), personne ne sait. L'essai le dira.

## Ce que fait la commande, en deux phrases

Sur une plaque qui porte plusieurs objets séparés, vous pouvez dire à l'imprimante de **ne plus imprimer un de ces objets** pendant qu'elle imprime ; les autres continuent. C'est **irréversible** : un objet ignoré ne peut pas être « remis » dans l'impression.

Rien d'autre n'est envoyé : pas de pause, pas de reprise, pas d'arrêt, pas de G-code. Et **Claude ne peut pas le faire à votre place** : aucun outil de la conversation n'envoie cet ordre. Il ne part que si **vous** tapez la commande sur le terminal du serveur et que **vous** répondez `o` à la question.

## 1. Préparer la plaque d'essai

Il faut une plaque de **3 petits objets séparés** :

1. Dans Bambu Studio, mettez **3 cubes de 20 mm**, écartés d'au moins 10 mm les uns des autres, **sur la même plaque**. Ce sont **3 objets distincts** : ne les groupez pas, ne les assemblez pas en une seule pièce.
2. Vérifiez que l'étiquetage des objets est actif : onglet **Processus**, section **Autres**, réglage **« Étiqueter les objets »** (« Label objects ») coché. C'est le cas par défaut dans les profils Bambu, mais **vérifiez** : sans ce réglage, l'imprimante ne sait pas où commence et finit chaque objet, et il n'y a rien à ignorer.
3. Découpez (Slice), puis enregistrez ou envoyez le fichier comme d'habitude (ou faites-le découper par Claude : le principe est le même).

**Comment vérifier que l'étiquetage est bien dans le fichier découpé** (sur un fichier `.gcode.3mf`, au choix) :

- une fois l'impression lancée (étape 2), la commande `--list` de l'étape 3 doit afficher **vos 3 objets** avec leurs noms ; si elle répond « aucun objet étiqueté », l'étiquetage était désactivé : recommencez la découpe ;
- ou, sur un ordinateur Linux, avant de lancer : `unzip -p plaque.gcode.3mf Metadata/plate_1.gcode | grep -c "start printing object"` doit afficher un nombre **supérieur à 0** (il y a un repère par objet et par couche).

## 2. Lancer l'impression, normalement

Lancez comme d'habitude : confirmation humaine, plateau vide attesté, etc. Rien ne change pour le lancement. **Aucun lancement n'est automatique.**

Laissez l'imprimante monter à environ **20 % de la hauteur** de l'impression (les 3 cubes ont déjà quelques couches, il en reste largement).

## 3. Trouver l'impression et les objets

Sur le **serveur** (là où tourne `bambud`), dans un terminal :

```bash
# jeton d'API du service (ne pas l'afficher, ne pas le coller dans une conversation)
export BAMBU_API_TOKEN="$(sudo grep '^BAMBU_API_TOKEN=' /etc/atelier/atelier.env | cut -d= -f2-)"
```

- **`--job`** : l'identifiant de l'impression, de la forme `pj_…`. Demandez à Claude « quel est l'identifiant de l'impression en cours ? » (outils `get_print_status` ou `list_print_history`), ou lisez-le dans l'adresse de la fiche de l'impression dans le site (`/impressions/pj_…`).
- **`--ids`** : les identifiants des objets. Ils ne sont **pas** 1, 2, 3 : ce sont des numéros choisis par Bambu Studio (par exemple 130, 174, 200). Pour les voir, **sans rien envoyer** :

```bash
/opt/atelier/bin/bambud skip-objects --job pj_XXXX --list
```

Réponse attendue (exemple) :

```
Impression pj_XXXX « plaque-3-cubes.gcode.3mf » : état PRINTING, imprimante printing
Objets de la plaque (identifiants à donner à --ids) :
  id 130      « Cube 1 »
  id 174      « Cube 2 »
  id 200      « Cube 3 »
On peut ignorer un objet maintenant.
```

Si la dernière ligne dit « On ne peut pas ignorer d'objet maintenant : … », la raison est écrite (impression pas en cours, transport non pris en charge, etc.). Corrigez, ou notez-la comme résultat.

Si le service n'écoute pas sur `127.0.0.1:7878`, ajoutez `--addr hôte:port` (ou la variable `BAMBU_ADDR`). La commande ne parle qu'au service **en marche sur la même machine** ; elle n'ouvre ni la base de données ni l'imprimante elle-même.

## 4. Ignorer un objet (vers 20 %)

Choisissez **un** cube, par exemple le deuxième, et tapez :

```bash
/opt/atelier/bin/bambud skip-objects --job pj_XXXX --ids 174
```

La commande vérifie que 174 est bien dans le fichier de cette impression, qu'il n'est pas déjà ignoré, et qu'il resterait au moins un autre objet. Puis elle affiche :

```
Impression pj_XXXX « plaque-3-cubes.gcode.3mf ».
Continueront d'être imprimés : « Cube 1 » (id 130), « Cube 3 » (id 200).
Attention : la tour de purge continue ; un support partagé ou un objet collé à un voisin peut laisser un support orphelin.
Ignorer 1 objet (« Cube 2 », id 174) ? Cette action est irréversible [o/N]
```

**Relisez le nom et l'identifiant.** Tapez `o` puis Entrée pour confirmer. Toute autre réponse (y compris Entrée seule) **n'envoie rien**.

À savoir :

- il n'existe pas d'option « oui à tout » (`--yes`) : la confirmation au clavier est obligatoire ; si la commande n'est pas lancée depuis un vrai terminal, elle refuse ;
- l'ordre part **une seule fois** et n'est **jamais renvoyé** ;
- la commande attend ensuite jusqu'à 20 secondes (`--wait 30s` pour plus long) que l'imprimante confirme.

## 5. Ce qu'il faut observer

| Résultat affiché | Ce que cela veut dire | À faire |
|---|---|---|
| `Statut : verified` et `s_obj de l'imprimante : [174]` | L'imprimante liste l'objet comme ignoré : **elle a pris l'ordre**. | Passer aux vérifications ci-dessous. |
| `Statut : sent_unconfirmed` (code de sortie 3) | L'ordre est parti mais l'imprimante n'a rien changé en 20 s. **Rien n'est renvoyé.** Un message de l'imprimante peut être cité (« Réponse de l'imprimante : … »). | Regarder l'imprimante : l'objet est-il quand même ignoré ? Relancer `--list` après une minute. Noter le message. |
| « refusé : … » | Avant tout envoi (identifiant inconnu, impression pas en cours…). | Lire le message, corriger. |

Vérifications à faire **pendant** que l'impression continue :

1. **`s_obj`** : relancer `--list` (le cube ignoré porte la mention `[déjà ignoré]`) ou demander à Claude `get_print_status` : le champ `skipped_objects` doit contenir `174`.
2. **La photo de la caméra** (`get_camera_snapshot`) et **votre regard** devant la machine : la tête continue-t-elle d'imprimer **les deux autres cubes** ? **Le cube ignoré cesse-t-il de grandir** (la couche suivante ne le touche plus) ?
3. **La vue directe** de l'imprimante : pas d'erreur à l'écran, pas de pause inattendue, pas de bruit anormal.
4. **La fin** : les deux autres cubes se terminent-ils correctement (hauteur complète, pas de décollement dû au cube absent) ? L'impression se termine-t-elle sans erreur ?

Toutes les étapes sont aussi inscrites dans l'**historique de l'impression** (événements `skip_requested`, `skip_approved`, `skip_sent`, `skip_verified` ou `skip_unconfirmed`) et dans le **journal d'audit** (`print.skip_requested`, `print.skip_approved` par `human:cli`, `print.skip_sent`, `print.skip_verified`). Aucun secret n'y figure : seulement des identifiants et des noms d'objets.

## 6. Ce qu'il faut noter

Remplissez le tableau en bas de ce fichier (ou envoyez-le à Claude) :

- **modèle** de l'imprimante (P1S) et **version du firmware** (écran de la machine, ou `get_printer_status` : champ `firmware`) ;
- **Bambu Studio** : version utilisée pour la découpe, réglage « Étiqueter les objets » coché ou non ;
- **résultat de la commande** : `verified`, `sent_unconfirmed` ou refus (avec le message exact) ;
- `s_obj` après l'ordre ;
- **l'objet ignoré a-t-il vraiment cessé d'être imprimé** (photo / vue directe) ;
- **les deux autres objets ont-ils fini** correctement ;
- tout **écart** : pause, message d'erreur à l'écran, support laissé en l'air, temps de réaction anormal.

## 7. Revenir en arrière en cas de problème

- **Il n'existe pas d'annulation de l'ignorance** : ni côté imprimante ni côté Printalk. Un objet ignoré le reste.
- Si l'impression part mal (objets qui se décollent, bruit, erreur) : **arrêtez-la** par le chemin habituel. Demandez à Claude d'annuler l'impression (outil `cancel_print`) : il vous donne un lien `/stop/…` à ouvrir **dans votre navigateur enregistré**, avec le même niveau de validation qu'un arrêt normal. Vous pouvez aussi l'arrêter sur l'écran de l'imprimante, ou depuis Bambu Handy.
- Si l'imprimante affiche une erreur après l'ordre, traitez-la comme n'importe quelle erreur d'impression (page d'aide `/action/…` pour reprendre après une pause, par exemple) ; **ne relancez pas la commande** sans avoir regardé `s_obj` avec `--list`.
- Après l'essai : laissez l'imprimante refroidir, retirez les pièces, et notez le résultat.

## 8. Limites connues de cette étape

- C'est un **essai** : pas de bouton dans le site, pas d'outil pour Claude, pas de page de validation. Ces éléments viendront (étapes B et C de la spec) **si l'essai réussit**.
- La **tour de purge** (nettoyage) ne s'ignore pas.
- Un objet qui touche un voisin, ou dont les supports sont partagés, peut laisser un support orphelin ou abîmer le voisin : l'avertissement est affiché, aucun calcul n'est fait.
- Si l'imprimante est servie par un **agent Windows** (transport « agent »), la commande refuse : « non pris en charge par ce transport ».
- Ne marche que **pendant l'impression** (ni en préparation, ni en pause).
- On ne peut pas ignorer **tous** les objets restants : utilisez l'arrêt.

## Résultats de l'essai (à remplir)

| Date | Imprimante / firmware | Studio / étiquetage | Résultat commande | `s_obj` | Objet ignoré cesse d'être imprimé ? | Les 2 autres finissent ? | Écarts, messages |
|---|---|---|---|---|---|---|---|
| | | | | | | | |

**Conclusion à reporter** : *P1S en LAN = pris en charge* (on livre SK1 à SK6 pour la P1S) ou *non pris en charge* (on livre SK1 à SK6 pour les modèles officiellement pris en charge, et la P1S affiche « non pris en charge en réseau local par ce firmware »).
