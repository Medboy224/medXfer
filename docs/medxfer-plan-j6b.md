# medXfer — Plan du jalon J6b : application Flutter MVP

> **Source :** feuille de route, J6b ; bible chapitre 10 (`API-01` à `API-08`, règles B1 à B11), `DISC-03`, `DISC-10`, `DISC-12`, `STO-15`, `STO-16`, chapitre 17 (`DEV-13`, `DEV-17`, `DEV-19-R`) ; rapport du prototype [g2a-report.md](g2a-report.md) (§7) ; branche `spike/g2a` (prototype Flutter jetable, `spikes/g2a/app`).
> **Objectif :** une vraie application medXfer pour **Android, Windows et Linux**, au-dessus du cœur en-processus (`libmedxfer`). Elle couvre la liste des appareils, l'appairage par PIN et par QR, l'envoi, la réception, la progression, un historique minimal et le panneau développeur.
>
> Sous Android, l'utilisateur choisit ce qu'il envoie **dans l'application**, par des onglets de catégories : applications, photos, vidéos, musique, fichiers. Les fichiers reçus vont dans un dossier `medXfer/` à la racine du stockage, rangés par type.
>
> L'interface est **fluide, animée et personnalisable**, et tourne aussi sur des téléphones anciens (§1.1).
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
> **Durée estimée :** 11 à 16 jours, avec l'explorateur par catégories (WP-B.9) ajouté le 19 octobre. Une PR par lot, CI verte sur les trois systèmes avant fusion.

## 0. État de départ (constaté sur `main` le 19 octobre 2026)

| Élément | État |
|---|---|
| Cœur en-processus | ✅ `pkg/core`, `cmd/libmedxfer` (5 fonctions C), schéma **v3**, règles B1 à B7, B9 et B11 vérifiées par `smoke.c`. |
| Prototype Flutter | Branche `spike/g2a`, jamais fusionnée : lecteur d'événements par isolat, chargement de la bibliothèque sur les trois systèmes, empaquetage Windows. Code jetable, mais il sert de référence. |
| Ce qui manque au cœur pour l'application | Une **progression par fichier** : aujourd'hui, `transfer_progress` remplit les champs du fichier avec les totaux du lot, si bien que les deux barres affichent le même pourcentage (signalé par l'utilisateur, et reporté à J6b par l'audit). Envoi par **descripteur** Android, pour le sélecteur du système (`STO-16`). Liste des appareils de confiance (lister, oublier, renommer). Nom de l'appareil. Export du paquet de diagnostic (`DEV-19-R`). |
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

L'explorateur Android (WP-B.9) passe par l'interface `Library` (`lib/platform/library.dart`) : applications installées, médias par MediaStore, fichiers par chemin.

- **L'interface ne parle qu'au `Core`** : jamais à `dart:ffi`, jamais à un plugin directement.
- **Le cœur ne connaît pas Flutter** : il reçoit des chemins, ou des descripteurs sous Android, et `set_network_info`.
- **Les tests de l'interface tournent sans la bibliothèque**, contre `FakeCore` et les scénarios de `cmd/xfer/testdata/scenarios` (`DEV-17`).

### 1.1 Exigences de l'interface

| Id | Exigence |
|---|---|
| UI-1 | **Fluidité :** 60 images par seconde dans les listes et les animations, y compris sur un téléphone d'entrée de gamme de 2019 (2 à 3 Go de mémoire). Listes paresseuses, vignettes en cache et décodées à leur taille d'affichage, aucun calcul lourd dans l'isolat de l'interface. |
| UI-2 | **Démarrage :** moins de 1,5 s jusqu'au premier écran utile sur ce même téléphone. |
| UI-3 | **Animations** : transitions entre écrans, apparition des appareils, barres de progression. Toutes respectent le réglage système « réduire les animations ». |
| UI-4 | **Personnalisation :** thème clair, sombre et noir (écrans OLED), couleur d'accent au choix, taille du texte du système respectée. |
| UI-5 | **Taille :** APK de moins de 25 Mo par architecture, bibliothèque du cœur comprise. |

Ces exigences sont vérifiées à la fin de chaque lot sur le téléphone de test et, pour UI-1 et UI-2, sur l'appareil le plus ancien disponible.

## 2. Ordre d'exécution

```
WP-B.0 cœur, schéma v4 ──► WP-B.1 squelette et frontière ──► WP-B.2 appareils et appairage ──► WP-B.3 envoi, réception, historique
                                                         └─► WP-B.4 Android ──► WP-B.9 explorateur par catégories
WP-B.3 ──► WP-B.5 panneau développeur, gate G7 ──► WP-B.6 empaquetage ──► WP-B.8 essais réels, guide, rapport
WP-B.7 évaluation du point d'accès (indépendant, avant WP-B.8)
```

## 3. Les lots

### WP-B.0 — Le cœur pour l'application, schéma v4 · S

- **Progression par fichier** : `transfer_progress` donne, à côté des totaux du lot, l'avancement de chaque élément en cours. Plusieurs fichiers avancent en même temps, sur plusieurs connexions : il y a une liste, pas « le » fichier courant. Le test reproduit d'abord le défaut signalé (les deux barres au même pourcentage).
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
- **Stockage** (décision J2) :
  - réception dans `medXfer/` à la racine du stockage partagé, rangée par type (`Apps`, `Photos`, `Vidéos`, `Musique`, `Fichiers`) ;
  - écrire à la racine demande l'**accès à tous les fichiers** (`MANAGE_EXTERNAL_STORAGE`), accordé par l'utilisateur dans les réglages, après un écran qui explique pourquoi ;
  - sans cet accès, repli dans `Download/medXfer/` (`STO-15`, sans permission) et envoi par le sélecteur du système, avec des descripteurs ;
  - une sonde refait d'abord sur le téléphone, à la racine, les gestes du récepteur (`.part`, `fsync`, renommage), comme G2A-10 l'a fait pour `Download`.
- **Manifeste** : `INTERNET`, `FOREGROUND_SERVICE_DATA_SYNC`, `POST_NOTIFICATIONS`, `CHANGE_WIFI_MULTICAST_STATE`, `MANAGE_EXTERNAL_STORAGE`, `READ_MEDIA_IMAGES`, `READ_MEDIA_VIDEO`, `READ_MEDIA_AUDIO` (et `READ_EXTERNAL_STORAGE` jusqu'à Android 12). Version du NDK fixée.

### WP-B.9 — Explorateur par catégories (Android) · M

- Des onglets **Apps**, **Photos**, **Vidéos**, **Musique** et **Fichiers**, avec une sélection multiple qui survit au changement d'onglet et un panier visible (« 12 éléments, 1,4 Go ») avant d'envoyer.
- **Apps** : les applications lancées depuis l'écran d'accueil (requête `<queries>` sur `ACTION_MAIN`/`CATEGORY_LAUNCHER`, sans `QUERY_ALL_PACKAGES`), envoyées par le chemin de leur APK.
- **Photos, Vidéos, Musique** : MediaStore, avec vignettes et regroupement par album ou par dossier.
- **Fichiers** : navigation dans le stockage par chemin, avec l'accès à tous les fichiers. Sans cet accès, le sélecteur du système prend le relais.
- **Envoi par chemin** : avec ces accès, les fichiers sont lisibles là où ils sont (`STO-16`), sans copie ni descripteur.
- Les exigences UI-1 et UI-5 sont vérifiées sur une bibliothèque de plusieurs milliers de photos.

### WP-B.5 — Panneau développeur et gate G7 · M

- Le mode développeur (`DEV-13`) est un réglage persistant, désactivé par défaut, avec une bannière visible.
- Le panneau couvre les critères de `DEV-19-R` :
  - télémétrie en direct (vitesse, étapes, goulot) ;
  - `bench disk` et `bench net` ;
  - rapports ;
  - paquet de diagnostic ;
  - appairage, envoi et réception.
- Les remarques de l'utilisateur sur le tableau de bord web (décision J10) y trouvent leur place. La première, la progression du fichier identique à celle du lot, est corrigée dans le cœur (WP-B.0).
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

## 5. Décisions

Réponses de l'utilisateur du 19 octobre 2026. Les propositions sans objection sont notées « proposée » jusqu'à leur confirmation.

| # | Question | Décision | Conséquence ou autre choix |
|---|---|---|---|
| J1 | Où vit l'application ? | ✅ **Dans ce dépôt, dossier `app/`**. | Un seul dépôt, une seule CI : le cœur et l'interface changent ensemble (schéma v4). |
| J2 | Version minimale d'Android ; où vont les fichiers reçus | ✅ **Android 11 (API 30)**. Les fichiers reçus vont dans un **dossier `medXfer/` à la racine du stockage**, rangé par type. | Écrire à la racine demande l'accès à tous les fichiers (WP-B.4), qu'il faut justifier lors d'une publication sur le Play Store (en J8). Le transfert d'un appareil à l'autre fait partie des usages admis, mais c'est à vérifier à la publication. Sans cet accès, repli dans `Download/medXfer/`. |
| J3 | Identifiant de l'application Android | ✅ **`dev.medxfer.app`, provisoire.** Il sera remplacé par celui du nom définitif **avant toute publication**. | Après une publication, en changer donnerait une autre application pour les utilisateurs. |
| J4 | Gestion de l'état | Proposée : **aucune bibliothèque** (`ChangeNotifier`, `ListenableBuilder`). | Riverpod, Bloc. |
| J5 | Lecture des QR à la caméra (Android) | Proposée : **`flutter_zxing`** (zxing-cpp, libre), derrière l'interface `QrScanner`. | `mobile_scanner`, qui embarque ML Kit, un binaire Google non libre. |
| J6 | Comment choisir ce qu'on envoie sous Android | ✅ **Dans l'application, par onglets de catégories** (WP-B.9), envoi par chemin. Le sélecteur du système reste le repli. | Remplace la question des dossiers par descripteurs : avec l'accès aux fichiers, un dossier s'envoie par son chemin. |
| J7 | Langues de l'interface | Proposée : **français et anglais dès le départ**. | Anglais seulement pour le MVP. |
| J8 | Historique | Proposée : **gardé par l'application** (100 derniers transferts). | Une action d'historique dans le cœur. |
| J9 | Point d'accès local (`DISC-12`) | Proposée : **évaluer seulement** (WP-B.7), puis intégrer si c'est concluant. | L'intégrer d'office. |
| J10 | Le panneau développeur | ✅ Critères de `DEV-19-R`, plus les remarques de l'utilisateur. Première remarque : la progression par fichier (WP-B.0). | D'autres remarques peuvent s'ajouter jusqu'à WP-B.5. |
| J11 | Empaquetage du MVP | Proposée : Windows en dossier portable zippé, Linux en `tar.gz`, Android en APK de développement. | Installateurs dès J6b (prévus en J8). |
| J12 | Maquettes avant le code | ✅ Oui. **Méthode à choisir** : une page de maquettes cliquables, puis le code Flutter lui-même, essayé sur le téléphone avec le cœur factice. | Le détail de la méthode est donné dans la discussion de la PR. |
| J13 | Exigences de l'interface (§1.1) | ✅ **Fluide, animée, personnalisable, et capable de tourner sur des téléphones anciens.** | Vérifiées à chaque lot (UI-1 à UI-5). |
