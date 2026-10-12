# medXfer — Plan détaillé du jalon J4a (protocole de transfert v1)

> **Source :** feuille de route §3 « J4a » ; bible chapitres 8 (protocole), 12 (`LIM-04` à `LIM-08`) et 17 (`DEV-03`, `DEV-04`, `DEV-07` à `DEV-09`).
> **Objectif :** un seul protocole, borné et vérifié de bout en bout. Concrètement :
> - le contrôle passe en trames de longueur bornée ;
> - **un seul port par appareil**, et les connexions de données s'annoncent par `DATA_JOIN` ;
> - une seule offre couvre un fichier, plusieurs fichiers ou un dossier ;
> - chaque chunk porte son SHA-256, et chaque fichier se termine par un `digest` ;
> - des mesures par étape indiquent ce qui limite le débit.
>
> **Durée estimée :** 6 à 9 jours, plutôt le haut de la fourchette (le daemon doit être migré, voir D1). Une PR par lot, CI verte sur les trois systèmes avant fusion.

## 0. État de départ (constaté sur `main` après J3)

| Constat | Conséquence pour J4a |
|---|---|
| Le contrôle (`session.Channel`) est un flux JSON lu par `json.Decoder`, **sans borne de longueur** : un pair peut faire grossir la mémoire du lecteur à volonté. | WP-4.1 : trames `CONTROL` bornées (`XFER-01`, `LIM-04`). |
| Côté données, deux charges utiles sont allouées à la taille annoncée **sans borne** : les métadonnées de poignée de main (`protocol.ReadHandshakePayload`) et le message de reprise (`TypeResume`, côté émetteur). | WP-4.1 : toute lecture est bornée avant allocation. |
| Un transfert = un fichier = **un port** : `Sender.ServeAndSend*` ouvre son propre listener. Les lots utilisent les ports `port+idx` l'un après l'autre ; le daemon incrémente `activePort` ; `xfer send` ouvre un port de données plus un port d'appairage. | WP-4.2 : un seul listener par appareil et `DATA_JOIN` (`CHAN-01`, `CHAN-06`). |
| `Receiver.Pull` fait jusqu'à 30 tentatives de connexion sans `Timeout` : plusieurs minutes de blocage face à un pare-feu qui ignore les paquets (#48). | WP-4.2 : délai de connexion et reprise à délai croissant (`XFER-05`). |
| Les lots passent par `batch_offer`, puis un `batch_item` et un transfert du moteur **par fichier** (`item_complete`, `batch_complete`). Pause et saut par fichier sont gérés par le daemon. Une course dans cette file a bloqué la CI macOS (#54, en quarantaine). | WP-4.3 : une seule offre, l'ordonnancement est piloté par le receveur. |
| Les dossiers peuvent aussi passer par `StreamTar` (flux tar brut sur un port de transfert), qui perd des fichiers en fin de lot (#9). | Décision D3. |
| L'émetteur compte chaque chunk servi, y compris les renvois, et le message de reprise fait avancer son compteur : progression au-delà de 100 % (#33, vue aussi sans reprise). | WP-4.3 : progression comptée sur les chunks uniques. |
| Intégrité : CRC32 par chunk et `FileID` = MD5(nom, taille, date). Pas de hash de bout en bout ; une source modifiée pendant l'envoi n'est pas détectée. | WP-4.3 et WP-4.4 : SHA-256 par chunk, `digest` de fin, détection de la modification (`XFER-03`, `XFER-04`, `XFER-07`). |
| Taille de chunk acceptée : 64 Kio à 8 Mio (tampons de 8 Mio). La bible (`XFER-02`, 📏) dit 256 Kio à 16 Mio. | Décision D4. |
| `pkg/diag` n'échantillonne que la progression ; `netbench` est un outil à part (TCP brut). Aucune mesure par étape. | WP-4.5 (`DEV-03`, `DEV-04`, `DEV-08`, `DEV-09`). |
| Les gestionnaires de `pkg/api` deviennent l'API du cœur en J6a, sans WebSocket (rapport G2a §7). | Ils sont **migrés** vers le nouveau protocole, pas contournés. |
| Issues rattachées à J4a : #33, #48, #54 ; #9 selon D3. | Fermées par les lots indiqués. |

## 1. Ordre d'exécution et dépendances

```
WP-4.0 gate G6 (mesurée, ce plan) ──► WP-4.1 trames bornées ──► WP-4.2 un seul port + DATA_JOIN
                                                                       │
                                                                       ▼
                                      WP-4.4 digest, source modifiée ◄── WP-4.3 offre unique, SHA-256 par chunk
                                                                       │
                                                                       ▼
                                                    WP-4.5 mesures par étape, offre bench, échelle R1–R4
```

Ordre : **4.0 → 4.1 → 4.2 → 4.3 → 4.4 → 4.5**. La partie « échelle R1–R2 » de WP-4.5 peut commencer pendant 4.3, car elle ne dépend que du canal.

**Garde-fou de performance, sur chaque PR à partir de 4.2 :** même mesure en boucle locale qu'en J3 (512 Mio, TLS, #58 : 914 ms). Une régression de plus de 10 % bloque la PR.

## 2. Les lots

### WP-4.0 — Gate G6 : SHA-256 ou BLAKE3 · fait

**Mesure** (11/10/2026) : banc jetable, tranches de 4 Mio, Go 1.24.9, BLAKE3 avec `github.com/zeebo/blake3` v0.2.4.

| Appareil | SHA-256, 1 cœur | SHA-256, parallèle | BLAKE3, 1 cœur | BLAKE3, parallèle |
|---|---|---|---|---|
| Samsung SM-A576B (Exynos 1580, 3 cœurs visibles dans Termux), arm64 | **536 Mo/s** | 1 453 à 1 916 Mo/s | 60 Mo/s | 226 à 278 Mo/s |
| PC i5-8350U, Windows 11 | 399 Mo/s | 1 269 à 1 436 Mo/s | 3 199 Mo/s | 10 264 à 11 947 Mo/s |
| Portable i5-2520M (2011), Zorin OS | 155 Mo/s | 265 Mo/s | 711 Mo/s | 1 825 Mo/s |

- **Critère de la gate :** sur le téléphone cible, le hash doit tenir au moins 1,5 fois le débit du lien. SHA-256 sur un seul cœur fait 536 Mo/s, soit plus de 4 fois un lien gigabit (125 Mo/s) et plus de 1,5 fois tout Wi-Fi actuel.
- **Pourquoi pas BLAKE3 :** en Go, il n'a pas d'accélération sur arm64 ; il serait **9 fois plus lent** sur le téléphone. Il ne gagne que sur le PC, où SHA-256 suffit déjà.
- **Décision :** **SHA-256** (`crypto/sha256`), sans dépendance. La gate G6 est fermée dans la bible avec ce plan, et `XFER-03` passe à ✅.

### WP-4.1 — Trames bornées (`XFER-01`, `LIM-04`) · M

**Codec de trames v1** (`pkg/protocol`) :
- en-tête de 8 octets inchangé (bible §8.1) ;
- `ReadFrame(r, limits)` refuse une longueur supérieure au maximum de son type **avant** d'allouer quoi que ce soit ;
- maxima : `CONTROL` 1 Mio (`LIM-04`), `DATA_JOIN` 16 octets, `CHUNK_REQ` 8 octets, `CHUNK` 44 octets plus la taille de chunk, `CHUNK_ERR` 10 octets. Une version ou un type inconnus ferment la connexion.

**Contrôle :** `session.Channel` lit et écrit des trames `CONTROL`.
- Le JSON reste le même (`session.Message`) : appairage, daemon et `xfer node` ne changent pas.
- Un `type` inconnu reçoit `error{code: "unsupported"}` (bible §8.2), et les champs JSON inconnus sont ignorés.

**Anciennes trames de données :** poignée de main et reprise, qui disparaissent en 4.3, sont bornées dès maintenant. Métadonnées : 64 Kio au plus ; reprise : exactement 40 octets.

**Fuzzing natif** du lecteur de trames (`FuzzReadFrame`), commencé ici (bible §14.2 ; le reste du fuzzing en J4b, WP-4.10).

**Tests :**
- une trame `CONTROL` de 1 Mio + 1 octet est refusée sans allocation proportionnelle ;
- mauvais magic, mauvaise version, trame tronquée, type inconnu ;
- tous les tests d'appairage, du daemon et de `xfer node` passent sans modification.

### WP-4.2 — Un seul port par appareil et `DATA_JOIN` (`CHAN-01`, `CHAN-06`, `XFER-05`, #48) · L

**Nouveau paquet `pkg/transfer` (couche E)**, sans dépendance vers `pkg/api` (D1).

**`Hub`, propriétaire du listener de l'appareil :** après le handshake TLS (`securechan`), il lit la première trame.
- `CONTROL` : la connexion part vers le contrôle (appairage, session), comme aujourd'hui.
- `DATA_JOIN{transfer_id}` : la connexion est remise au transfert enregistré, **seulement si la clé du pair est celle de la session qui a créé ce transfert** (`CHAN-06`). Sinon, fermeture.
- Sans `DATA_JOIN` valide dans les 5 s : fermeture.
- Les transferts sont enregistrés par un `transfer_id` de 128 bits aléatoires.

**Émetteur :** `engine.Sender` n'ouvre plus de listener. Il sert les chunks sur les connexions que lui remet le `Hub`.

**Receveur :** il se connecte au port de l'appareil du pair avec un délai de connexion (5 s, réglable), envoie `DATA_JOIN` en premier, puis se reconnecte avec un délai croissant : 50 ms jusqu'à 1 s, 8 tentatives (`XFER-05`, #48).

**Appelants :**
- **Daemon :** le listener 18887 devient le seul port pair à pair (contrôle et données). `activePort` et le champ `data_port` des offres disparaissent.
- **`xfer send` :** un seul listener sert l'appairage (`pair_secret`, `pair_hello`) et les données. Le `p` de l'URI est ce port ; `PairPort` et `DataPort` fusionnent dans `Port`.
- **`xfer node` :** son listener sert aussi les données.
- Dans ce lot, un transfert porte encore **un seul fichier**, avec les anciennes trames de chunk. Un lot devient une suite de transferts identifiés par `transfer_id` au lieu de ports. L'offre unique arrive en 4.3.

**Tests :**
- `DATA_JOIN` venant d'une autre clé, `transfer_id` inconnu, première trame d'un autre type, ou rien pendant 5 s : refusés ;
- le daemon n'ouvre qu'un seul port pair à pair ;
- la connexion vers une adresse « trou noir » échoue dans le délai (#48).

**Essai réel :** PC ↔ téléphone. Sous Windows, une seule règle de pare-feu (un port) suffit.

### WP-4.3 — Une seule offre, le receveur pilote, SHA-256 par chunk (`XFER-02`, `LIM-05` à `LIM-08`, #33, #54) · L

**Messages `CONTROL` :**
- `offer{transfer_id, items[{item, path, size, mtime}], total_bytes, chunk_size, kind}`. Au-delà de 1 Mio, l'offre est paginée (D2).
- `accept{transfer_id, chunk_size}`, `reject`, `pause`, `resume`, `cancel` ;
- `skip{transfer_id, item}` (D6) ;
- `done{transfer_id, status, items[{item, status}]}`, `error`.

**Trames de données :**
- `CHUNK_REQ{item, index}` ;
- `CHUNK{item, index, length, sha256}` suivi des données, avec un en-tête de 44 octets ;
- le SHA-256 est calculé par l'émetteur et vérifié **avant écriture** par le receveur ; le CRC32 disparaît.

**Ordonnancement :**
- le receveur ouvre N connexions (4 par défaut, de 1 à 8, `LIM-07`), avec au plus 2 requêtes en vol par connexion ;
- il demande toujours le plus petit `(item, index)` manquant ;
- mémoire bornée à N × 2 × chunk ;
- plusieurs fichiers sont en cours en même temps : plus d'attente entre deux petits fichiers.

**Pause, saut, limites :**
- La pause et la reprise d'un fichier ou du transfert entier se décident côté receveur : il cesse ou reprend ses requêtes.
- Une demande de l'émetteur (pause, saut d'un fichier) est transmise au receveur par `CONTROL`.
- `LIM-08` : un seul transfert actif par appareil de confiance ; les autres attendent.

**Progression :** côté receveur, les octets uniques écrits ; côté émetteur, les chunks uniques servis. Elle ne dépasse jamais 100 % (#33).

**Migration :**
- **Daemon :** disparaissent `batch_offer`, `batch_item`, `item_complete`, `batch_complete` et la file de lot. Les actions de l'interface (`skip_file`, `pause_file`, `resume_file`, `pause`, `resume`, `cancel`, `respond_offer`) gardent leur nom et leur forme ; elles pilotent maintenant l'ordonnanceur.
- **`xfer node`, `xfer send`, `xfer recv` :** un dossier part en une seule offre ; plus de boucle par port.
- **Ancienne API :** `ServeAndSend*`, `Pull*` et les trames de poignée de main sont **supprimées** avec leurs tests, remplacés par des tests de `pkg/transfer`.

**Reprise pendant J4a :** elle reste par fichier, sur les fichiers d'état `.medxfer` actuels. J4b les remplace par le journal et la clé de reprise.

**Tests :**
- un dossier de 1 000 petits fichiers en une offre, tailles mélangées ;
- saut et pause d'un fichier, pause du transfert entier, annulation ;
- `LIM-05` : 100 001 éléments ou un manifeste de plus de 16 Mio sont refusés ;
- `LIM-07` (bornes du nombre de flux), `LIM-08` ;
- mémoire bornée ;
- progression jamais au-delà de 100 % (#33) ;
- `TestBatchQueueDynamicAdvanceOnPause` réécrit pour le nouveau modèle, quarantaine retirée (#54).

### WP-4.4 — `digest` de fin, chunk faux, source modifiée (`XFER-03`, `XFER-04`, `XFER-07`) · M

**Chunk dont le hash ne correspond pas :** il n'est pas écrit. Il est redemandé, sur une autre connexion si possible, 3 fois au plus ; après cela, le fichier échoue (`XFER-04`).

**Fin d'un fichier :**
- le receveur envoie `DIGEST_REQ` sur une connexion de données (D7) ;
- l'émetteur répond `digest = SHA-256("medxfer-v1-filedigest" ‖ n ‖ hash_0 ‖ … ‖ hash_{n-1})` ;
- en cas de différence, le fichier est repris à zéro (bible §8.3).
- Les chunks déjà écrits lors d'une session précédente (reprise) sont relus et hachés pour le `digest`. En J4b, le journal gardera leurs hash et cette relecture disparaîtra.

**Source modifiée :**
- l'offre porte la taille et la date de chaque fichier ;
- l'émetteur les revérifie avant `digest` et après chaque erreur de lecture ;
- en cas de changement, il répond `CHUNK_ERR SOURCE_CHANGED` et le fichier échoue proprement (`XFER-07`).

**Tests :**
- un émetteur de test qui envoie un mauvais hash : rien n'est écrit, la redemande a lieu, et le fichier échoue au 3ᵉ essai ;
- une source modifiée pendant l'envoi donne `SOURCE_CHANGED` ;
- un `.part` altéré sur le disque entre deux sessions : `digest` différent, et le fichier est repris à zéro.

### WP-4.5 — Mesures par étape, offre `bench`, échelle R1 à R4 (`DEV-03`, `DEV-04`, `DEV-07` à `DEV-09`) · M

**Mesures par chunk** (`DEV-03`), émises par `pkg/transfer` vers ses écouteurs. `diag` s'y abonne et ne décide de rien (`DEV-01`).
- Côté receveur : attente réseau (de `CHUNK_REQ` à `CHUNK`), hash, écriture, `fsync` par commit.
- Côté émetteur : lecture disque, hash, écriture réseau.
- `diag` agrège par étape le temps, les octets et les percentiles p50 et p95. La version du schéma du rapport monte.

**Étape limitante** (`DEV-04`) : l'heuristique de la bible est validée par des scénarios synthétiques. Il faut la retrouver dans **100 %** des cas :
- disque lent (`faultfs`) ;
- réseau bridé (`faultconn`) ;
- hash ralenti (crochet de test).

**Offre `bench`** (`DEV-08`) : `kind: "bench"` traverse le même canal, les mêmes connexions et les mêmes hash qu'un transfert réel, avec un puits nul. Options `--source zero|random|file` et `--sink null|disk` (`DEV-09`).

**`xfer bench ladder`**, marches R1 à R4 :
- R1 : TCP brut sur N flux, données aléatoires, sans contenu d'utilisateur ;
- R2 : plus le TLS épinglé ;
- R3 : plus les trames et le SHA-256, avec un puits nul ;
- R4 : plus l'écriture disque sans `fsync`.

R5, R6 et `bench matrix` viennent en J4b (WP-4.9). Résultats en `--json`.

## 3. Critère de sortie de J4a

1. **Un seul chemin de transfert.** Tous les transferts (fichier, fichiers, dossier) passent par une seule offre, un seul port par appareil, `DATA_JOIN`, le SHA-256 par chunk et le `digest`. L'ancienne API du moteur n'existe plus.
2. **Essai réel PC ↔ téléphone :** un dossier de nombreux petits fichiers et un gros fichier, avec des hash identiques, à travers une seule règle de pare-feu.
3. **Mesures :** le rapport d'un transfert contient les mesures par étape, et l'étape limitante est retrouvée dans 100 % des scénarios synthétiques.
4. **Performance :** le débit en boucle locale (512 Mio, TLS) ne baisse pas de plus de 10 % par rapport à J3.
5. **Issues et gate :** #33, #48 et #54 sont fermées ; G6 est fermée dans la bible.

## 4. Hors périmètre (J4b)

- Journal de 40 octets par chunk, clé de reprise, commit groupé avec `fsync` ordonné, finalisation (WP-4.6).
- `os.Root`, collisions à la racine, doublons à la casse près (WP-4.7).
- Disque plein, et la sonde disque qui écrit dans le dossier de réception (#36) (WP-4.8).
- R5, R6 et `bench matrix`, puis le choix des valeurs par défaut des flux et de la taille de chunk (WP-4.9).
- Chaos, et fuzzing du journal et de `SafeRelPath` (WP-4.10).
- Mesure finale de `StreamTar` (WP-4.11, selon D3) ; reprise interrompue (#46).

## 5. Décisions à valider avec ce plan

| # | Proposition | Raison |
|---|---|---|
| D1 | Le protocole v1 vit dans un nouveau paquet **`pkg/transfer`**, sans dépendance vers `pkg/api`. `pkg/api` et `cmd/xfer` en deviennent des clients. L'ancien chemin du moteur est supprimé à la fin de WP-4.3. Le stockage (`DiskManager`, `safepath`, collisions) reste dans `pkg/engine` jusqu'à J4b. | Même principe qu'en J3 : le code qui survivra dans la bibliothèque de J6a ne dépend pas du daemon. Le daemon est migré parce que ses gestionnaires deviennent l'API du cœur. |
| D2 | Une offre de plus de 1 Mio est **paginée** : `offer` (en-tête et premiers éléments), puis des pages `offer_items` de 1 Mio au plus. Le total est borné à 100 000 éléments et 16 Mio de manifeste. | Concilie `LIM-04` (`CONTROL` ≤ 1 Mio) et `LIM-05` (manifeste jusqu'à 16 Mio) sans exception à la règle des trames bornées. |
| D3 | **`StreamTar` retiré du chemin des transferts en WP-4.2**, ce qui ferme #9. Avant le retrait, on mesure une base de référence en boucle locale : dossier de petits fichiers, tar contre lot actuel. WP-4.11 (J4b) compare cette base à l'offre unique. `StreamTar` ne revient, sous forme d'un type de connexion de données, que si l'écart est net. | La bible le met déjà en ⏸ pour la v1 : l'offre unique supprime l'attente entre petits fichiers. Le porter vers `DATA_JOIN` coûterait du code pour une fonction probablement retirée et aujourd'hui défectueuse (#9). La mesure faite avant le retrait garde la décision fondée. |
| D4 | Taille de chunk acceptée en J4a : **256 Kio à 8 Mio**, 4 Mio par défaut. Le minimum suit `XFER-02` ; le maximum reste à 8 Mio (taille des tampons actuels). | `XFER-02` est 📏 : `bench matrix` (J4b) balaie 1 à 8 Mio, et sa conclusion fixera les bornes. Pas besoin de tampons de 16 Mio d'ici là (mémoire sur mobile : N × 2 × chunk). |
| D5 | Pas de compatibilité avec les versions d'avant J4a : l'ALPN reste `medxfer/1`, et la v1 est le protocole de ce jalon. | Aucune version n'est publiée ; les appareils d'essai sont mis à jour ensemble. `XFER-06` s'appliquera à partir de la première version publiée. |
| D6 | On ajoute **`skip{transfer_id, item}`** au vocabulaire `CONTROL`. La pause et la reprise d'un seul fichier sont des décisions locales du receveur ; une demande venue de l'émetteur lui est transmise. | L'interface sait déjà sauter ou mettre en pause un fichier d'un lot (`skip_file`, `pause_file`, `resume_file`) ; la liste de la bible (§8.2) ne couvre que le transfert entier. La bible §8.2 est complétée dans la PR de WP-4.3. |
| D7 | `digest_req` et `digest` deviennent les trames de données **`DIGEST_REQ`** (0x24) et **`DIGEST`** (0x25), envoyées sur les connexions du transfert ; une erreur répond `CHUNK_ERR` à l'index `0xFFFFFFFF`. Un élément d'un seul chunk reçu en entier dans la session se passe de `DIGEST_REQ`. | `pkg/transfer` ne dépend pas de la session de contrôle (D1) : la vérification de fin marche à l'identique pour `xfer send`, `xfer node` et le daemon. Les connexions de données sont déjà chiffrées et liées au transfert (`CHAN-06`). Pour un seul chunk, le digest découle du hash déjà vérifié : pas d'aller-retour de plus par petit fichier. |
