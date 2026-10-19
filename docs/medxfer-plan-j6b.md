# medXfer — Plan du jalon J6b : application Flutter MVP

> **Source :** feuille de route, J6b ; bible chapitre 10 (`API-01` à `API-08`, règles B1 à B11), `DISC-03`, `DISC-10`, `DISC-12`, `STO-15`, `STO-16`, chapitre 17 (`DEV-13`, `DEV-17`, `DEV-19-R`) ; rapport du prototype [g2a-report.md](g2a-report.md) (§7) ; branche `spike/g2a` (prototype Flutter jetable, `spikes/g2a/app`).
> **Objectif :** une vraie application medXfer pour **Android, Windows et Linux**, au-dessus du cœur en-processus (`libmedxfer`). Elle couvre la liste des appareils, l'appairage par PIN et par QR, l'envoi, la réception, la progression, un historique minimal et le panneau développeur.
>
> **Critère de sortie :** scénario complet Android ↔ Windows et Android ↔ Linux au Wi-Fi réel, depuis l'application :
> - appairage par QR et par PIN ;
> - confiance mutuelle ;
> - envoi de fichiers et d'un dossier dans les deux sens ;
> - reprise après coupure ;
> - aucun fichier copié côté émetteur (`STO-16`).
>
> Les gates **G2a** (réserves restantes) et **G7** (retrait du tableau de bord web) sont évaluées.
>
> **Durée estimée :** 8 à 12 jours. Une PR par lot, CI verte sur les trois systèmes avant fusion.

## 0. État de départ (constaté sur `main` le 19 octobre 2026)

| Élément | État |
|---|---|
| Cœur en-processus | ✅ `pkg/core`, `cmd/libmedxfer` (5 fonctions C), schéma **v3**, règles B1 à B7, B9 et B11 vérifiées par `smoke.c`. |
| Prototype Flutter | Branche `spike/g2a`, jamais fusionnée : lecteur d'événements par isolat, chargement de la bibliothèque sur les trois systèmes, empaquetage Windows. Code jetable, mais il sert de référence. |
| Ce qui manque au cœur pour l'application | Envoi par **descripteur** Android (`STO-16`) ; liste des appareils de confiance (lister, oublier, renommer) ; nom de l'appareil ; export du paquet de diagnostic (`DEV-19-R`). |
| Outillage | Flutter 3.47.6 et Dart 3.13 sur le PC et sur Zorin ; téléphone Android 16 (Termux, adb sans fil). C: n'a que 19 Go libres : caches Gradle et émulateur sur M:. |

## 1. Architecture de l'application

```
app/                         projet Flutter (Android, Windows, Linux)
  lib/core/                  la frontière : une interface, deux adaptateurs
    core.dart                  interface Core : call(action, payload), events (Stream), start, stop
    ffi_core.dart              adaptateur réel : dart:ffi + isolat lecteur (B8, B9, B10)
    fake_core.dart             adaptateur factice : rejoue les scénarios « golden » (DEV-17)
  lib/platform/              ce que fait la plateforme, derrière une interface par sujet
    picker.dart                choisir des fichiers ou un dossier, sans copie (STO-16)
    network.dart               interfaces et adresses (DISC-10), MulticastLock (DISC-03)
    background.dart            service de premier plan (API-08)
    hotspot.dart               point d'accès local (DISC-12), si l'évaluation le retient
  lib/features/              un dossier par écran : devices, pairing, send, receive, history, settings, developer
  android/app/src/main/kotlin/ le code Kotlin des adaptateurs Android (aucun plugin qui copie)
```

Les choix suivent la règle des implémentations modulables : une bibliothèque = un adaptateur, derrière une interface.

- **L'interface ne parle qu'au `Core`** : jamais à `dart:ffi`, jamais à un plugin directement.
- **Le cœur ne connaît pas Flutter** : il reçoit des chemins, ou des descripteurs sous Android, et `set_network_info`.
- **Les tests de l'interface tournent sans la bibliothèque**, contre `FakeCore` et les scénarios de `cmd/xfer/testdata/scenarios` (`DEV-17`).

## 2. Ordre d'exécution

```
WP-B.0 cœur, schéma v4 ──► WP-B.1 squelette et frontière ──► WP-B.2 appareils et appairage ──► WP-B.3 envoi, réception, historique
                                                         └─► WP-B.4 Android (en parallèle de B.2 et B.3)
WP-B.3 ──► WP-B.5 panneau développeur, gate G7 ──► WP-B.6 empaquetage ──► WP-B.8 essais réels, guide, rapport
WP-B.7 évaluation du point d'accès (indépendant, avant WP-B.8)
```

## 3. Les lots

### WP-B.0 — Le cœur pour l'application, schéma v4 · S

- `send` accepte des **descripteurs** à la place de chemins : `items: [{fd, name, size, mtime}]`.
  - Le cœur lit par `pread`, n'ouvre rien lui-même et ferme chaque descripteur à la fin de son élément.
  - Un descripteur invalide fait échouer cet élément, pas les autres.
- Actions `trust_list`, `trust_forget` et `trust_rename` ; `set_config` porte le nom de l'appareil (`CleanDeviceName`).
- Action `export_bundle` : le paquet de diagnostic, déjà masqué, écrit dans un dossier choisi par l'appelant.
- Schéma **v4** et fichier « golden » ; scénarios `xfer ctl` pour chaque nouvelle action.

### WP-B.1 — Squelette et frontière · M

- Projet `app/` : Material 3, thème clair et sombre, langues **français et anglais** (`gen-l10n`).
- `FfiCore` :
  - chargement de `medxfer.dll` ou `libmedxfer.so` ;
  - isolat lecteur avec `medxfer_next_event(-1)`, lots de 256 événements (B10), arrêt par la sentinelle (B9) ;
  - aucun pointeur Dart vers le cœur (B8).
- `FakeCore`, et un test de chaque scénario « golden » contre l'interface.
- Construction : `scripts/build-lib.*` produit la bibliothèque ; Gradle (`jniLibs`) et CMake (Windows, Linux) l'embarquent.
- **CI** : nouveau travail Flutter sous Ubuntu, avec `flutter analyze` et `flutter test` contre `FakeCore`.

### WP-B.2 — Appareils et appairage · M

- **Liste des appareils** (`scan`) : un appareil de confiance mutuelle apparaît sous son nom, un inconnu sous `medXfer-xxxx`.
- **Appairage**, dans les deux rôles pour chaque méthode :
  - PIN montré et PIN tapé ;
  - QR montré et QR scanné à la caméra sous Android ; sur le bureau, l'URI se colle.
- **Confiance** : la proposition et la réponse, avec `trust_offer` et `trust_answer`.
- **Écran « Appareils de confiance »** : renommer, oublier.
- Alerte ID-05 (`REFUSED`) affichée en clair.

### WP-B.3 — Envoi, réception, historique · M

- **Envoi** :
  - sur le bureau, fichiers et dossiers par leur chemin ;
  - sous Android, par descripteur (WP-B.4) ;
  - glisser-déposer sur le bureau.
- **Réception** : une carte « X veut vous envoyer N fichiers (taille) » avec Accepter et Refuser ; l'espace disque manquant est signalé avant d'accepter.
- **Progression** : vitesse, temps restant ; pause, reprise et annulation par élément et pour tout le transfert ; reconnexion.
- **Historique minimal** : les 100 derniers transferts (date, appareil, nombre de fichiers, taille, résultat), gardés par l'application dans son dossier de données. Le bouton « Ouvrir le dossier » ne figure que dans le rôle de réception.

### WP-B.4 — Android · M

- **Service de premier plan** (`API-08`) pendant l'écoute et les transferts, avec sa notification.
- **`MulticastLock`** pendant la découverte (`DISC-03`).
- **`set_network_info`** depuis `ConnectivityManager` (`DISC-10`), renvoyé à chaque changement de réseau.
- **Réception** dans `Download/medXfer/` par chemin (`STO-15`).
- **Choix des fichiers** par `ACTION_OPEN_DOCUMENT` et des dossiers par `ACTION_OPEN_DOCUMENT_TREE`. Les descripteurs sont passés au cœur (décision J6).
- **Manifeste** : `INTERNET`, `FOREGROUND_SERVICE_DATA_SYNC`, `POST_NOTIFICATIONS`, `CHANGE_WIFI_MULTICAST_STATE`. Version du NDK fixée.

### WP-B.5 — Panneau développeur et gate G7 · M

- Le mode développeur (`DEV-13`) est un réglage persistant, désactivé par défaut, avec une bannière visible.
- Le panneau couvre les critères de `DEV-19-R` :
  - télémétrie en direct (vitesse, étapes, goulot) ;
  - `bench disk` et `bench net` ;
  - rapports ;
  - paquet de diagnostic ;
  - appairage, envoi et réception.
- **Vos idées pour le tableau de bord** (décision J10) y trouvent leur place.
- Fin du lot : **gate G7** évaluée. Si les critères sont remplis, une PR séparée retire le tableau de bord web (`devui`).

### WP-B.6 — Empaquetage · S

- **Windows** : un dossier portable zippé, avec le runtime Visual C++ à côté de l'exécutable, comme pour G2a ; sans installation ni droits administrateur.
- **Linux** : une archive `tar.gz` avec ses dépendances listées ; essai sur une distribution fraîche (réserve 2 de G2a).
- **Android** : APK signé avec une clé de développement. La clé de publication et le Play Store relèvent de J8.
- Les règles du pare-feu Windows restent pour l'installateur de J8 ; d'ici là, le guide explique comment les poser.

### WP-B.7 — Évaluation du point d'accès local (`DISC-12`) · S

- Prototype Kotlin `WifiManager.startLocalOnlyHotspot`, derrière l'interface `Hotspot`.
- Questions à trancher :
  - un PC ou un autre téléphone peut-il s'y joindre avec le nom et le mot de passe affichés en QR Wi-Fi ?
  - le transfert y passe-t-il, et à quel débit (bande 2,4 ou 5 GHz, choisie par le système) ?
  - la découverte y fonctionne-t-elle ?
- Le résultat va dans le rapport de J6b. L'intégration à l'interface ne se fait que si l'évaluation est concluante (décision J9).

### WP-B.8 — Essais réels, guide, rapport · S

- **Essais réels**, Android ↔ Windows et Android ↔ Linux :
  - tout le critère de sortie ;
  - la vérification qu'aucun fichier n'est copié côté émetteur (taille du cache et des dossiers de l'application avant et après) ;
  - les à-coups de l'interface sous Windows (réserve 4 de G2a).
- **Guide d'utilisation de l'application** et **rapport de sortie** du jalon.

## 4. Hors périmètre

- **macOS et iOS** : J9, après les gates G2b et G8.
- **Web Share v2** : J7. L'application n'expose pas le Web Share actuel.
- **Installateurs, signature de publication, règles du pare-feu posées automatiquement, Play Store** : J8.
- **Android 10 et antérieurs** : voir la décision J2.

## 5. Décisions à valider

| # | Question | Proposition | Autres choix |
|---|---|---|---|
| J1 | Où vit l'application ? | Dans ce dépôt, dossier `app/`. Un seul dépôt, une seule CI : le cœur et l'interface changent ensemble (schéma v4). | Un dépôt séparé. |
| J2 | Version minimale d'Android | **Android 11 (API 30)** : réception directe dans `Download/medXfer/` (`STO-15`), sans repli MediaStore à écrire ni à tester. Android 10 et antérieurs ne représentent plus qu'une petite part des téléphones. | API 26 (Android 8) avec repli MediaStore : plus de téléphones, un lot de plus, et pas d'appareil d'essai. |
| J3 | Identifiant de l'application Android | **`dev.medxfer.app`**. Il ne pourra plus changer une fois publié : en changer donne une autre application pour les utilisateurs. | Un identifiant lié au futur nom de l'organisation GitHub. |
| J4 | Gestion de l'état | **Aucune bibliothèque** : `ChangeNotifier` et `ListenableBuilder` de Flutter. Peu d'écrans, et une dépendance de moins à suivre. | Riverpod, Bloc. |
| J5 | Lecture des QR à la caméra (Android) | **`flutter_zxing`** (zxing-cpp, entièrement libre), derrière l'interface `QrScanner`. | `mobile_scanner` : plus répandu, mais il embarque ML Kit, un binaire Google non libre, ce qui gêne une publication sur F-Droid. |
| J6 | Envoyer un **dossier** sous Android | **B** : le Kotlin parcourt le dossier choisi (`ACTION_OPEN_DOCUMENT_TREE`) et ouvre un descripteur par fichier, jusqu'à 10 000 fichiers ; au-delà, un message le dit. Le cœur ferme chaque descripteur une fois l'élément envoyé. | **A** : fichiers seulement sous Android pour le MVP, dossiers plus tard. **C** : le cœur demande à l'interface d'ouvrir chaque fichier au moment de l'envoyer (aucune limite, mais un aller-retour de plus dans la frontière, à concevoir). |
| J7 | Langues de l'interface | **Français et anglais dès le départ** (`gen-l10n`), selon la langue du système. | Anglais seulement pour le MVP. |
| J8 | Historique | Gardé **par l'application** (100 derniers transferts), pas par le cœur. | Une action d'historique dans le cœur. |
| J9 | Point d'accès local (`DISC-12`) | **Évaluer seulement** dans J6b (WP-B.7), puis intégrer si c'est concluant, dans J6b s'il reste du temps, sinon en J8. | L'intégrer d'office. |
| J10 | Le panneau développeur | Les critères de `DEV-19-R`, **plus vos idées et les défauts que vous avez repérés** sur le tableau de bord web : merci de les lister, je les range dans WP-B.5. | — |
| J11 | Empaquetage du MVP | Windows en dossier portable zippé, Linux en `tar.gz`, Android en APK de développement. | Installateurs dès J6b (prévus en J8). |
| J12 | Maquettes avant le code | **Oui** : une page de maquettes des écrans principaux (liste, appairage, envoi, réception, progression) à valider avant WP-B.2. | Coder directement. |
