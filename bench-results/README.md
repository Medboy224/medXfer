# Mesures archivées (`bench-results/`)

Les mesures sur appareils réels (bible §17.4, DEV-21) sont gardées ici pour comparer les appareils et les versions. Chaque fichier `matrix-<appareil>-<système>-<date>.json` est la sortie de `xfer bench matrix` côté **receveur**.

## 27 novembre 2026 — R-5a : l'attente du disque et le déchiffrement, mesurés à part (#304)

**But :** voir ce que R-5a change à l'étape limitante, avec le banc de la revue (`xfer bench ladder` en boucle locale). Sorties brutes : [`r5a-stages-20261127.txt`](r5a-stages-20261127.txt).

**Méthode :**
- `xfer bench ladder`, serveur et pair sur le même appareil (`--listen 127.0.0.1`), 1 Gio par marche, 4 connexions, chunks de 2 Mio.
- `main`, puis la branche R-5a, sur le PC (Windows 11, processeur Intel sans instructions SHA) et sur l'A57 (Android 16, dans Termux).
- Sur l'A57, une version de R-5a avec une ligne temporaire, non commitée, affiche le temps de chaque étape du receveur.

**Résultats :**

| Appareil, marche R6 | `main` | R-5a |
|---|---|---|
| PC | 297 MB/s, limité par `hash` | 289 et 261 MB/s, limité par `hash` |
| A57 | 168 MB/s, limité par `net_wait` | 171 MB/s, limité par `net_wait` |

Temps des étapes du receveur sur l'A57, marche R6, avec R-5a (somme des 4 connexions) :

| `net_wait` | `tls` | `disk_write` | `hash` | `fsync` |
|---|---|---|---|---|
| 10,4 s | 6,3 s | 4,5 s | 3,7 s | 0,1 s |

**Lecture :**
- Le constat de la revue, « limiting: fsync » à 440–510 MB/s, ne se reproduit sur aucun des deux appareils. Sur le PC, le SHA-256 limite avant le disque ; sur l'A57, c'est l'arrivée des données. Les tests de R-5a le couvrent à la place : `TestOnlyTheWritersWaitIsMeasured`, et le scénario « slow fsync » de `TestLimitingStageIsFound`.
- Sur l'A57, 6,3 s de déchiffrement comptaient comme du réseau avant R-5a : environ un tiers de l'ancien `net_wait`.
- `net_wait` reste l'étape la plus longue. En boucle locale sur un seul téléphone, le travail de l'émetteur (chiffrement, lecture, hash) ralentit l'arrivée des données.
- L'attente réelle du disque n'est que de 0,1 s : les commits qui tournent en arrière-plan ne comptent plus.
- Les débits ne changent pas, à la dispersion près. Le second passage du PC était plus lent sur toutes les marches, R3 comprise, qui ne touche pas au disque.

## 27 novembre 2026 — R-3b : événements d'un fichier sans la file (#297)

**But :** voir ce que change le schéma v6 pour une application qui reçoit beaucoup de petits fichiers, sur le téléphone le plus lent. Sorties brutes : [`r3b-events-tecno-20261127.txt`](r3b-events-tecno-20261127.txt).

**Méthode :**
- Le téléphone : le TECNO BF6 (Android 12, 32 bits, 2 Gio de mémoire), avec une version profile de l'application.
- Deux versions comparées : `main` (schéma 5), puis la branche de la PR #340 (schéma 6). Le PC garde la même version.
- Le PC envoie 2 000 fichiers aléatoires de 4 à 64 Kio (70 Mo) par le Wi-Fi. Pendant tout le transfert, on fait défiler la liste des fichiers sur le TECNO, sans pause ni saut.
- Ce qui est relevé :
  - les images, par `lib/perf.dart`, toutes les 2 s ;
  - les images sautées que compte Android ;
  - le temps processeur de chaque fil, à la fin ;
  - la durée vue par l'émetteur.

**Résultats :**

| | avant (`main`) | après (schéma 6) |
|---|---|---|
| Images dessinées | 840 en 137 s | 3 582 en 143 s |
| Images de plus de 100 ms | 141 | 73 |
| Image la plus longue | 15,75 s | 0,59 s |
| Images sautées (Android) | 1 522 | 87, au lancement |
| Fenêtre « L'application ne répond pas » | affichée | jamais |
| Processeur : fil principal | 1 min 47 s | 1 min 14 s |
| Processeur : fil Dart de fond le plus chargé | 1 min 08 s | 6 s |
| Durée du transfert | 105,9 s | 108,8 s |
| Impression pendant le défilement | l'appli ne répond plus | 4 sur 5 |

**Lecture :**
- **Le fil Dart de fond :** sans doute le lecteur d'événements, qui décodait la file entière à chaque événement de fichier. Il passe de plus d'une minute à 6 s.
- **L'écran :** il ne gèle plus.
- **La durée :** elle ne change pas, car le réseau et le stockage la fixent.
- **Ce qui reste :** des images lentes pendant la première minute du transfert, avec 45 à 70 ms de construction (p90), puis environ 23 ms. C'est à voir dans le lot sur la fluidité (WP-8.5).

## 27 novembre 2026 — R-3a : lots de petits fichiers sur FUSE, `fsync` par fichier (#302)

**But :** mesurer, sur les mêmes téléphones qu'en R-0, ce que coûte le nouveau chemin des lots sur le stockage partagé d'Android (FUSE). Ce chemin fait un `fsync` par fichier, 8 à la fois, au lieu de se fier à `syncfs`. Sorties brutes : [`r3a-flush-a57-20261127.txt`](r3a-flush-a57-20261127.txt), [`r3a-flush-tecno-20261127.txt`](r3a-flush-tecno-20261127.txt).

**Méthode :**
- Le binaire de test de `pkg/store` est compilé pour Android et lancé par `adb shell`, avec `MEDXFER_TEST_FUSE_DIR` sur un dossier de `/sdcard/Download`.
- `TestBatchFlushTimeOnSharedStorage` chronomètre `Batch.Flush` pour 200 fichiers de 16 Kio, par l'ancien chemin puis par le nouveau. Le temps comprend la synchronisation, la fermeture, le renommage et la synchronisation du dossier.
- Trois passages.

**Résultats sur l'A57 :**

| Chemin | ms par fichier (3 passages) |
|---|---|
| ancien (`syncfs`, repli un par un) | 8,44 ; 8,58 ; 7,80 |
| nouveau (`fsync` par fichier, 8 à la fois) | 8,03 ; 8,72 ; 8,89 |

**Résultats sur le TECNO BF6** (Android 12, noyau 5.4, 32 bits) :

| Chemin | ms par fichier (3 passages) |
|---|---|
| ancien (`syncfs`, qui y répond « succès » sans rien écrire) | 19,68 ; 17,59 ; 21,57 |
| nouveau (`fsync` par fichier, 8 à la fois) | 21,71 ; 19,71 ; 22,23 |

**Lecture :**
- Sur l'A57, les deux chemins prennent le même temps, à la dispersion près. Sur le TECNO, le nouveau coûte environ 1 à 2 ms de plus par fichier (5 à 10 %) : c'est le prix d'une durabilité réelle, l'ancien n'écrivant rien.
- Le TECNO est 2 à 2,5 fois plus lent que l'A57 sur ces petits fichiers.
- L'ancien chemin ne rendait pourtant rien durable : `syncfs` y a répondu « succès », et aucun fichier n'était synchronisé. R-0 a montré qu'un `syncfs` sur FUSE n'écrit rien. Le nouveau chemin rend les fichiers durables sans surcoût mesurable.
- Le temps d'un petit fichier sur FUSE est donc surtout ailleurs que dans la synchronisation, qui ne pèse qu'environ 0,3 à 0,5 ms par fichier en R-0 : fermeture, renommage, dossier. C'est à mesurer à part.

## 25 novembre 2026 — R-0 : `syncfs` sur le stockage partagé d'Android (#302)

**But :** savoir si `syncfs` rend vraiment durables les petits fichiers reçus dans le stockage partagé d'Android, qui passe par FUSE (lot R-0 du plan de revue, décision R3). Sorties brutes : [`r0-syncfs-a57-20261125.txt`](r0-syncfs-a57-20261125.txt), [`r0-syncfs-tecno-20261125.txt`](r0-syncfs-tecno-20261125.txt).

**Méthode :**
- Un programme Go jetable compilé pour Android :
  - sur le Galaxy A57 (Android 16, 64 bits), lancé dans Termux par SSH ;
  - sur le TECNO BF6 (Android 12, 32 bits, noyau Linux 5.4), lancé par `adb shell`, dossier interne `/data/local/tmp`.
- Deux dossiers : `/storage/emulated/0/Download/…` (FUSE, `f_type` 0x65735546) et le dossier interne de Termux (f2fs, monté avec `fsync_mode=nobarrier`).
- Pour chaque mesure, la mémoire en attente d'écriture (`Dirty` + `Writeback` de `/proc/meminfo`) est relevée avant et après. Une synchronisation qui n'écrit rien revient tout de suite et la laisse où elle était.
- Trois passages. Tout fichier écrit est supprimé avant l'étape suivante.

**Résultats sur l'A57** (médianes des trois passages) :

| Mesure | Stockage partagé (FUSE) | Dossier interne (f2fs) |
|---|---|---|
| `syncfs` après 256 Mio | 0,0 ms, erreur `ENOENT`, ~58 Mo toujours en attente | 77 ms, ~0,3 Mo en attente |
| `fsync` du fichier de 256 Mio | 93 ms, écrit | 59 ms, écrit |
| `syncfs` après 500 fichiers de 16 Kio | 0,0 ms, ~10 Mo toujours en attente | 74 ms, écrit |
| `fsync` des 500 fichiers, l'un après l'autre | 1 030 ms (~2 ms par fichier), écrit | 178 ms, écrit |
| `fsync` des 500 fichiers, 8 à la fois | 164 ms (~0,33 ms par fichier), écrit | — |

**Résultats sur le TECNO BF6** (médianes des trois passages) :

| Mesure | Stockage partagé (FUSE) | Dossier interne (f2fs) |
|---|---|---|
| `syncfs` après 256 Mio | 0,0 ms, **sans erreur**, ~63 Mo toujours en attente | 340 ms, écrit |
| `fsync` du fichier de 256 Mio | 312 ms, écrit | 299 ms, écrit |
| `syncfs` après 500 fichiers de 16 Kio | 0,0 ms, **sans erreur**, rien d'écrit | 100 ms, écrit |
| `fsync` des 500 fichiers, l'un après l'autre | 1 646 ms (~3,3 ms par fichier), écrit | 665 ms, écrit |
| `fsync` des 500 fichiers, 8 à la fois | 261 ms (~0,5 ms par fichier), écrit | 182 ms, écrit |

**Conclusions :**
- **Sur FUSE, `syncfs` n'écrit rien.** Sur l'A57, il renvoie `ENOENT`. Sur le TECNO (noyau 5.4, sans `syncfs` pour FUSE), **il renvoie un succès sans rien écrire**.
- **Sur l'A57, les fichiers reçus restent durables**, mais lentement. `Batch.Flush` passe au `fsync` de chaque fichier quand `syncfs` échoue, l'un après l'autre (~2 ms par fichier).
- **Sur le TECNO, ils ne le sont pas.** `syncfs` réussit, alors `Batch.Flush` n'a rien à rattraper : il renomme les fichiers alors que leurs données sont encore en mémoire. Une coupure de courant peut les laisser vides ou incomplets. Le défaut de #302 est donc reproduit.
- **Le gain des lots du 13 octobre (0,37 à 0,73 ms par fichier)** a été mesuré dans le dossier interne de Termux. Il ne s'est jamais appliqué au dossier de réception de l'application, qui est sur FUSE.
- **Pour R-3 (décision R3)** : sur FUSE, détecté par `statfs`, ne pas appeler `syncfs` ; faire le `fsync` de chaque fichier, 8 à la fois. C'est 6 fois plus rapide que le repli actuel sur les deux téléphones, et durable. On ne peut pas se fier au retour de `syncfs` sur FUSE.

## 13 octobre 2026 — valeurs par défaut (WP-4.9, D8 du plan J4b)

**Conditions de mesure :**
- PC Windows (« Naruto ») et Samsung Galaxy A57 sous Termux (Android 16), reliés par le hotspot direct du téléphone (environ 6 à 13 MB/s) ;
- marche R6 : le transfert complet, avec offre, journal, `fsync`, `digest` et renommage ;
- 256 Mio par passage.

| Fichier | Sens | Passages par case | Débits médians | Dispersion maximale |
|---|---|---|---|---|
| `matrix-Naruto-windows-20261013-115050.json` | téléphone → PC | 1 | 11,5 à 13,0 MB/s | inconnue (1 passage) |
| `matrix-localhost-android-20261013-121346.json` | PC → téléphone | 3 | 6,8 à 12,3 MB/s | jusqu'à ×3 dans une même case |

**Dossier de 2 000 petits fichiers** (`small2000`, PC → téléphone, `xfer recv --workers N`) :

| Flux | 1 | 2 | 4 | 8 |
|---|---|---|---|---|
| Durée | 16,1 s / 16,7 s | 9,0 s / 6,9 s | 4,6 s | 4,3 s / 4,8 s |

**Décision du matin : on garde 4 flux × 4 Mio.** Elle a été revue l'après-midi, en 5 GHz (voir plus bas).

- **Le lien limite tout.** L'étape limitante est `net_wait` partout. Côté PC, les 16 combinaisons sont à ±6 % les unes des autres.
- **La règle tombe dans le bruit.** Elle propose 1 × 1 Mio dans un sens et 2 × 2 Mio dans l'autre. Mais les écarts entre cases sont plus petits que la dispersion entre passages d'une même case, qui dépasse de loin la tolérance de 5 %.
- **Moins de 4 flux est nettement plus lent.** C'est le cas pour un dossier de petits fichiers (1 flux : ×3,5 ; 2 flux : ×1,5 à ×2) comme en réception sur le téléphone (1 flux : 7,7 à 10,5 MB/s, contre 10,9 à 12,3 MB/s avec 4 flux).
- **Au-delà, rien ne change.** 8 flux n'apportent rien par rapport à 4, et aucune taille de chunk ne se distingue.

Depuis ces mesures, `bench matrix` indique la dispersion de chaque case et déclare la recommandation **non fiable** si un seul passage a été fait, ou si la dispersion dépasse la tolérance. Il faudra refaire la matrice sur un lien plus rapide et plus stable (Wi-Fi 5 GHz, Ethernet), avec `--repeat 3`, avant de changer ces valeurs.

## 13 octobre 2026, après-midi — 5 GHz en Wi-Fi Direct, valeurs par défaut 4 × 2 Mio

Le téléphone crée un groupe Wi-Fi Direct en 5 GHz, et le PC s'y connecte. Le lien est en 802.11ac, canal 161 à 866,7 Mbit/s, puis canal 149 à 650 Mbit/s, avec 2,7 ms d'aller-retour. Le PC reçoit sur C: (NTFS, SSD). Un script mesure seul, pendant que le PC n'a pas Internet. Fichiers : `5ghz-20261013/`.

**Échelle, 4 flux × 4 Mio, 256 Mio** (`ladder-*.json`) :

| Sens | R1 TCP brut | R2 + TLS | R3 + SHA-256 | R4 + disque | R5 + journal et `fsync` | R6 transfert complet |
|---|---|---|---|---|---|---|
| Téléphone → PC | 42,9 MB/s | 42,1 | 42,3 | 43,0 | 42,4 | 36,0 |
| PC → téléphone | 35,6 MB/s | 36,7 | 35,2 | 35,1 | 35,8 | 45,2 |

- Le lien limite tout, comme en 2,4 GHz, mais 3,5 fois plus haut.
- Aucune couche de medXfer ne coûte de façon mesurable : TLS, SHA-256, écriture, journal, `fsync`.
- R6 atteint 84 % de R1 dans un sens. Dans l'autre, 127 % s'explique par le bruit.
- L'objectif de la bible §14.3 (au moins 80 %) est atteint.

**Matrices R6, 3 passages par case, enchaînés case par case** (`matrix-*.json`) :
- **Téléphone → PC :** tout est plat, de 37,4 à 43,4 MB/s.
- **PC → téléphone :** les mesures sont bruitées, jusqu'à 61 % d'écart dans une case. Deux tendances ressortent : les chunks de 8 Mio sont toujours lents (26 à 28 MB/s), ceux de 2 Mio toujours bons (40 à 48 MB/s). 4 × 4 Mio varie de 24,6 à 43,7 MB/s.

**Mesure ciblée, 4 × 2 Mio contre 4 × 4 Mio** (`ab-4x2-4x4/`) :
- Les deux cases alternent sur 7 tours dans chaque sens : 2 puis 4, puis 4 puis 2, et ainsi de suite.
- Une baisse passagère du lien touche donc les deux, et chaque tour donne une paire mesurée au même moment.

| Sens | 4 × 2 Mio (médiane) | 4 × 4 Mio (médiane) | Rapport 4×2 / 4×4 dans un même tour (médiane) |
|---|---|---|---|
| Téléphone → PC | 38,8 MB/s | 37,8 MB/s | 0,977 |
| PC → téléphone | 52,3 MB/s | 52,0 MB/s | 1,005 |

**Décision (D8) : 4 flux × 2 Mio par défaut.**
- Le débit est le même que 4 × 4 Mio, à 2,5 % près, dans les deux sens.
- Il faut moitié moins de mémoire : 16 Mio de tampons par transfert au lieu de 32.
- C'est la règle de la bible : à moins de 5 % du meilleur, avec le moins de mémoire.
- En 2,4 GHz non plus, 4 × 2 Mio n'a jamais fait moins bien.
- Les 31,0 MB/s de 4 × 4 Mio dans la matrice venaient d'une baisse du lien : la mesure ciblée donne 52,0 MB/s.
- Depuis, `bench matrix` alterne ses passages de la même façon, un tour sur deux en ordre inverse.

**Petits fichiers, PC → téléphone** (pour WP-4.11) :
- 2 000 fichiers de 4 Kio mettent 6 à 7 s avec 4 ou 8 flux, comme en 2,4 GHz. Avec 1 flux : 12 à 15 s.
- 100 000 fichiers de 1 à 16 Kio (872 Mo) mettent 338 s, soit 296 fichiers/s et 2,6 MB/s.
- Le lien n'y change rien. Le coût est dans le stockage du receveur, environ 13 ms par fichier et par flux : création, `fsync` des données, renommage, `fsync` du dossier (bible §9.6).
- Côté PC, 2 000 fichiers reçus mettent 7 s en 5 GHz.

## 13 octobre 2026, soir — petits fichiers (WP-4.11) : durabilité par lots, 8 connexions

**Où passe le temps d'un petit fichier** (`TestSmallFilesProfile`, 2 000 fichiers de 1 à 16 Kio, boucle locale, en millisecondes par fichier) :

| Variante | Téléphone (Android) | PC (Windows, C:) |
|---|---|---|
| Normal, 4 connexions | 4,12 | 3,62 |
| Normal, 8 connexions | 3,36 | 2,22 |
| Sans `fsync`, 4 connexions | 0,72 | 2,00 |
| Puits nul (protocole seul) | 0,19 | 0,09 |

- **Sur le téléphone, les `fsync`** (du fichier et de son dossier) font plus de 80 % du temps.
- **Sous Windows**, la création de chaque fichier coûte environ 2 ms même sans `fsync` (NTFS, antivirus). Seul le parallélisme y aide.

**Durabilité par lots, mesurée sur le stockage seul** (2 000 fichiers, 4 en parallèle, en millisecondes par fichier) :

| Méthode | Téléphone | PC (Windows) |
|---|---|---|
| Un `fsync` par fichier et par dossier | 3,2 à 3,4 | 1,9 à 2,4 |
| Un `syncfs` par lot de 256, puis renommages | 0,37 à 0,73 | 2,3 à 2,4 (fichiers synchronisés en parallèle, sans `syncfs`) |
| Aucune synchronisation (non durable) | 0,17 à 0,36 | 2,2 |

**Après le changement**, la même mesure de profil sur le téléphone donne 0,84 ms par fichier, contre 4,12 avant.

**100 000 fichiers de 1 à 16 Kio (872 Mo), du PC vers le téléphone, hotspot 2,4 GHz :**

| Version | Durée | Fichiers/s |
|---|---|---|
| Avant (`fsync` par fichier, 4 connexions) | 9 min 58 s | 167 |
| Lot seul, 4 connexions | 3 min 26 s | 486 |
| **Lot et 8 connexions (automatique)** | **2 min 34 s** | **649** |

En 2,4 GHz, la nouvelle version va déjà plus vite que l'ancienne en 5 GHz (338 s).
