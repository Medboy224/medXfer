# Mesures archivées (`bench-results/`)

Les mesures sur appareils réels (bible §17.4, DEV-21) sont gardées ici pour comparer les appareils et les versions. Chaque fichier `matrix-<appareil>-<système>-<date>.json` est la sortie de `xfer bench matrix` côté **receveur**.

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
