# medXfer — Plan du lot « Noms des appareils de confiance » (avant J6b)

> **Source :** demande de l'utilisateur du 18 octobre 2026 : « les appareils qui se font déjà confiance devraient voir leur nom dans les scans au lieu de medXfer-xxxx » ; bible chapitre 5 (`DISC-06`, `DISC-07`), chapitre 6 (§6.5, `PAIR-07`, `PAIR-10`) ; issue #136.
> **Objectif :** dans une liste de découverte, un appareil de **confiance mutuelle** apparaît sous **son nom** (« Naruto », « Galaxy A57 »), un inconnu sous `medXfer-xxxx`. Le nom ne circule **jamais en clair** sur le réseau, et personne ne peut suivre un appareil grâce à ce que la découverte annonce.
>
> **Critère de sortie :** un appareil de confiance mutuelle est reconnu dans les scans de la ligne de commande et du cœur, par une étiquette tournante et non par son adresse ; l'annonce ne contient ni nom, ni clé, ni identifiant stable ; les noms enregistrés ont un sens (plus de « localhost »). Vérifié par les tests et par des essais réels.
>
> **Durée estimée :** 2 à 3 jours. Une PR par lot, CI verte sur les trois systèmes avant fusion.

## 0. État de départ (constaté sur `main` le 18 octobre 2026)

| Constat | Conséquence |
|---|---|
| L'annonce porte un nom générique `medXfer-xxxx`, le rôle et le port (`DISC-06`). | Bon pour la vie privée, mais tous les appareils se ressemblent dans une liste. |
| Un appareil de confiance est reconnu **par sa dernière adresse IP**, en local (`DISC-07`, `trustedAt`, `trusted_alias` de `peers_list`). | Une adresse change (DHCP) et peut être prise par un autre : un intrus apparaîtrait sous le nom d'un appareil de confiance. La connexion le démasque (clé vérifiée, `ID-05`), mais la liste aurait trompé l'utilisateur. |
| Le nom enregistré est celui envoyé à l'appairage : `os.Hostname()` dans `send` et `recv`, soit **« localhost »** sous Android (#136) ; le nom générique `medXfer-xxxx` dans `xfer node`. | Le nom affiché serait souvent inutile. |
| Aucune commande ne permet de choisir le nom de son appareil, ni de renommer un appareil de confiance. | À ajouter. |

## 1. Le modèle cible

### 1.1 Une étiquette tournante par paire de confiance

- **Le secret :** quand la confiance mutuelle s'établit (`trust_commit`, §6.5), l'offrant tire une **clé de reconnaissance** aléatoire de 32 octets, `K_AB`, et l'envoie dans le `trust_commit`. Chaque côté la range dans l'entrée de l'autre (`trusted.json`). Elle circule dans un canal déjà authentifié.
- **L'étiquette :** A annonce, pour chaque appareil de confiance B, `tag = HMAC-SHA-256(K_AB, "medXfer-tag-v1" ‖ fp_A ‖ période)[:8]`, encodée en base32. La période vaut `⌊heure Unix / 600⌋` : l'étiquette change toutes les 10 minutes.
- **La reconnaissance :** B calcule l'étiquette attendue de chacun de ses appareils de confiance, pour la période courante et la précédente (décalage d'horloge). S'il la trouve dans une annonce, il affiche le nom qu'il a enregistré pour cet appareil.
- **Pour un inconnu :** des valeurs aléatoires qui changent toutes les 10 minutes. Il ne peut ni relier deux annonces, ni suivre l'appareil d'un réseau à l'autre.
- **Oublier un appareil** (`xfer trust forget`) arrête d'annoncer son étiquette et de la chercher : la reconnaissance cesse dans les deux sens, comme la confiance (`PAIR-09`).

### 1.2 Des noms qui ont un sens

- Chaque appareil a un **nom** :
  - choisi par l'utilisateur (`xfer name "PC d'Amadou"`, ou `set_config device_name` dans le cœur) ;
  - à défaut, le nom de la machine ;
  - sous Android, le modèle du téléphone (`ro.product.model`, ou le nom commercial), jamais « localhost ».
- Ce nom part dans les messages d'appairage **après** les confirmations (`PAIR-07`, inchangé), aussi en mode node. Il devient l'alias enregistré.
- `xfer trust rename EMPREINTE NOM` change l'alias **chez soi** : il ne part nulle part.

## 2. Ordre d'exécution

```
WP-N.1 étiquette et clé ──► WP-N.3 scans (CLI, cœur) ──► WP-N.4 guide, essais, rapport
WP-N.2 noms ──────────────┘
```

## 3. Les lots

### WP-N.1 — Étiquettes de reconnaissance · M

- `pkg/pairing` : `trust_commit` porte `K_AB` ; `TrustTalk` l'écrit avec l'entrée.
- `pkg/identity` : `Peer.TagKey`, un nouveau champ de `trusted.json`. Les entrées anciennes, sans ce champ, restent valables mais ne sont pas reconnues.
- `pkg/discovery` : champ `t` dans le TXT mDNS (`t=…`, une valeur par appareil de confiance, 16 au plus) et dans la balise de repli. Fonctions de calcul et de reconnaissance de l'étiquette.
- **Bible :** `DISC-07` reformulée (étiquette au lieu de l'adresse) ; nouvelle exigence `DISC-11` ; §6.5 complété ; dossier G4 informé (HMAC-SHA-256 de la bibliothèque standard, aucune primitive écrite à la main).
- **Tests :**
  - l'étiquette change de période en période ;
  - deux appareils de confiance se reconnaissent, période précédente comprise ;
  - un inconnu ne reconnaît rien ;
  - après un oubli, plus de reconnaissance ;
  - l'annonce ne contient ni nom, ni clé, ni identifiant stable d'une période à l'autre.

### WP-N.2 — Noms des appareils · S

- `xfer name [NOM]` : afficher ou choisir le nom de cet appareil (dans la configuration du cœur).
- Nom par défaut : le nom de la machine ; sous Android, le modèle (`getprop`), jamais « localhost » (Closes #136).
- `send`, `recv`, `pair`, `node` et le cœur envoient ce nom à l'appairage.
- `xfer trust rename EMPREINTE NOM`.

### WP-N.3 — Les scans montrent les noms · S

- **Ligne de commande** (`xfer recv`, `scan` de `xfer node`) : `[1] Naruto (trusted) 10.86.69.174:18887` ; les inconnus restent `medXfer-xxxx`. La reconnaissance par adresse (`trustedAt`) disparaît.
- **Cœur :** `peers_list` remplit `trusted_alias` par l'étiquette, et non plus par l'adresse. Le nom du champ ne change pas : le schéma reste en v2.
- **Annonce :** la liste des étiquettes est mise à jour quand une confiance est établie ou oubliée.

### WP-N.4 — Guide, essais réels, rapport · S

- **Guide :** §1, §3, §5 et §6.
- **Essais :** téléphone ↔ Zorin ↔ PC Windows :
  - après une confiance mutuelle, chacun voit le nom de l'autre dans le scan ;
  - un appareil sans confiance reste `medXfer-xxxx` ;
  - après `trust forget`, le nom disparaît des deux côtés ;
  - le changement d'adresse ne trompe plus la liste.

## 4. Hors périmètre

- Les noms dans l'annonce elle-même : ils n'y seront jamais (`DISC-06`).
- Le renouvellement de la clé de reconnaissance d'une paire : il se fait en reprenant la confiance (oubli, puis nouvelle demande).
- L'interface Flutter : J6b, qui affichera `trusted_alias`.

## 5. Décisions (validées le 18 octobre 2026)

| # | Décision | Raison |
|---|---|---|
| N1 | **Reconnaissance par étiquette tournante**, plus du tout par l'adresse IP. | Une adresse se prend ; une étiquette se calcule seulement avec la clé partagée. |
| N2 | **Une clé par paire** (`K_AB`), échangée dans le `trust_commit`, et une étiquette par appareil de confiance dans l'annonce (16 au plus, les plus récemment vus). | Oublier un appareil coupe aussi la reconnaissance ; une clé commune à tous les appareils de confiance ne permettrait pas de la retirer à un seul. |
| N3 | **Période de 10 minutes**, période précédente acceptée. | Assez court pour empêcher le suivi, assez long pour tolérer des horloges un peu décalées. |
| N4 | Les entrées existantes, sans clé, **ne sont pas reconnues** : il faut reprendre la confiance une fois (`trust`, `--trust`). | Les listes ont été remises à vide après les essais ; aucune migration n'est nécessaire. |
| N5 | **Nom de l'appareil :** choisi par `xfer name` ; sinon le nom de la machine ; sous Android, le modèle. Il n'est envoyé qu'après les confirmations. | Plus de « localhost » ; `PAIR-07` inchangée. |
| N6 | **`xfer trust rename`** change l'alias chez soi seulement. | Chacun nomme les appareils comme il veut, sans rien diffuser. |
