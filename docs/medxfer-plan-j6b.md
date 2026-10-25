# medXfer — Plan du jalon J6b : application Flutter MVP

> **Source :** feuille de route, J6b ; bible chapitre 10 (`API-01` à `API-08`, règles B1 à B11), `DISC-03`, `DISC-10`, `DISC-12`, `STO-15`, `STO-16`, chapitre 17 (`DEV-13`, `DEV-17`, `DEV-19-R`) ; rapport du prototype [g2a-report.md](g2a-report.md) (§7) ; branche `spike/g2a` (prototype Flutter jetable, `spikes/g2a/app`).
> **Objectif :** une vraie application medXfer pour **Android, Windows et Linux**, au-dessus du cœur en-processus (`libmedxfer`). Elle couvre la liste des appareils, l'appairage par PIN et par QR, l'envoi, la réception, la progression, un historique minimal et le panneau développeur.
>
> Sous Android, l'utilisateur choisit ce qu'il envoie **dans l'application**, par des onglets de catégories : applications, photos, vidéos, musique, fichiers. Les fichiers reçus vont dans un dossier `medXfer/` à la racine du stockage, rangés par type.
>
> L'interface est **fluide, animée et personnalisable**, et tourne aussi sur des téléphones anciens (§1.1).
>
> **On se connecte d'abord, puis on envoie, dans les deux sens** (§1.2). Quand aucun réseau n'est disponible, le téléphone crée lui-même un **point d'accès** depuis l'application, que l'autre appareil rejoint par QR ou par PIN.
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

### 1.2 Le parcours (maquettes validées le 19 octobre 2026)

Une page de maquettes cliquables (dix écrans) a fixé le parcours avant le code :

- **Accueil** :
  - onglets en haut : Historique, Apps, Photos, Vidéos, Musique, Fichiers ;
  - une ligne « Sur cet appareil » avec un cercle « tout sélectionner » ;
  - applications et photos en grille ;
  - un panier en bas (nombre et taille), et un grand bouton **Connecter**.
- **Se connecter d'abord** :
  - choisir des fichiers ne demande pas d'être connecté, mais l'envoi, si ;
  - une fois connectés, les deux appareils envoient et reçoivent autant qu'ils veulent, et un bandeau rappelle à qui l'on est connecté.
  - Le cœur le permet déjà : la session reste ouverte après l'appairage.
- **Connecter** :
  - **créer un point d'accès** : ce téléphone crée son réseau et affiche un QR, qui porte le réseau et l'appairage. Pour un appareil sans caméra, il affiche plutôt trois étapes : rejoindre le Wi-Fi (nom et mot de passe affichés), revenir dans medXfer, taper le PIN ;
  - **rejoindre** : scanner le QR de l'autre, ou taper son PIN ;
  - les appareils **déjà sur le même Wi-Fi**, ceux de confiance sous leur nom.
- **Transfert** : la barre du lot et celle de chaque fichier, avec **pause et « passer » par fichier** et pour tout le lot.
- **Identité visuelle propre à medXfer** (décision J15) : thème clair par défaut, accent bleu, police Figtree ; sombre, noir et trois autres accents dans les réglages.

## 2. Ordre d'exécution

```
WP-B.0 cœur, schéma v4 ──► WP-B.1 squelette et frontière ──► WP-B.7 sonde du point d'accès (tôt, sur le téléphone)
                                                         ──► WP-B.2 connexion et appairage ──► WP-B.3 envoi, réception, historique
                                                         └─► WP-B.4 Android ──► WP-B.9 explorateur par catégories
WP-B.3 ──► WP-B.5 panneau développeur, gate G7 ──► WP-B.6 empaquetage ──► WP-B.10 dossier de réception ──► WP-B.8 essais réels, guide, rapport
```

La sonde du point d'accès passe tôt : certains fabricants restreignent cette fonction, et le parcours de connexion en dépend.

## 3. Les lots

### WP-B.0 — Le cœur pour l'application, schéma v4 · S

- **Progression par fichier** : `transfer_progress` donne, à côté des totaux du lot, l'avancement de chaque élément en cours. Plusieurs fichiers avancent en même temps, sur plusieurs connexions : il y a une liste, pas « le » fichier courant. Le test reproduit d'abord le défaut signalé (les deux barres au même pourcentage).
- `send` accepte des **descripteurs** à la place de chemins : `items: [{fd, name, size, mtime}]`.
  - Le cœur lit par `pread` et n'ouvre rien lui-même. Les descripteurs restent ouverts jusqu'à la fin du transfert, car le receveur peut redemander un morceau (empreinte fausse, reprise) et un descripteur ne se rouvre pas ; le cœur les ferme ensuite, ou dès que l'offre est retirée, refusée ou que l'envoi échoue.
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

### WP-B.2 — Connexion et appairage · M

- **Écran « Connecter »** (§1.2) : créer un point d'accès (WP-B.7), rejoindre, ou choisir un appareil du même Wi-Fi.
- **Session** : bandeau « Connecté à … », déconnexion ; l'envoi et la réception restent possibles dans les deux sens tant que la session est ouverte.
- **Liste des appareils** (`scan`) : un appareil de confiance mutuelle apparaît sous son nom, un inconnu sous `medXfer-xxxx`.
- **Appairage**, dans les deux rôles pour chaque méthode :
  - PIN montré et PIN tapé ;
  - QR montré et QR scanné à la caméra sous Android ; sur le bureau, l'URI se colle.
- **Confiance** : la proposition et la réponse, avec `trust_offer` et `trust_answer`.
- **Écran « Appareils de confiance »** : renommer, oublier.
- Alerte ID-05 (`REFUSED`) affichée en clair.

**Découpage** (19 octobre) : deux PR.
- **WP-B.2a**, livré : le modèle `Connection` et tout ce qui précède, sauf la caméra et le point d'accès. Sous Android, l'écran « Connecter » ne marche pas sans les interfaces données au cœur : `set_network_info` et `MulticastLock` sont avancés de WP-B.4 (`lib/platform/network.dart`), envoyés au démarrage et à chaque ouverture de l'écran.
- **WP-B.2b**, livré : le scan du QR à la caméra (`QrScanner`, adaptateur `flutter_zxing`, décision J5), et l'intégration du point d'accès (WP-B.7) : le QR porte le réseau, le parcours PIN en trois étapes.

### WP-B.3 — Envoi, réception, historique · M

- **Envoi** :
  - sur le bureau, fichiers et dossiers par leur chemin ;
  - sous Android, par descripteur (WP-B.4) ;
  - glisser-déposer sur le bureau.
- **Réception** : une carte « X veut vous envoyer N fichiers (taille) » avec Accepter et Refuser ; l'espace disque manquant est signalé avant d'accepter.
- **Progression** : vitesse, temps restant ; pause, reprise et « passer » **par fichier** (`pause_file`, `resume_file`, `skip_file`) et pour tout le transfert ; reconnexion.
- **Historique minimal** : les 100 derniers transferts (date, appareil, nombre de fichiers, taille, résultat), gardés par l'application dans son dossier de données. Le bouton « Ouvrir le dossier » ne figure que dans le rôle de réception.

**Fait** (20 octobre) : modèle `Transfers`, offre entrante, écran de transfert, historique (`history.json`, 100 entrées), envoi sur le bureau par `file_selector` (fichiers, dossier) et par dépôt (`desktop_drop`), chacun derrière son interface. Sous Android, la réception marche déjà ; l'envoi attend WP-B.4 (descripteurs) et WP-B.9 (explorateur). Essai réel : un dossier de 5 fichiers reçu de Zorin, offre et progression par fichier correctes ; « passer » pendant la réception bloquait le cœur (#174, corrigé par #175).

### WP-B.4 — Android · M

- **Service de premier plan** (`API-08`) pendant l'écoute et les transferts, avec sa notification.
- **`MulticastLock`** pendant la découverte (`DISC-03`) : fait en WP-B.2a.
- **`set_network_info`** (`DISC-10`) : envoyé depuis WP-B.2a au démarrage et à l'ouverture de « Connecter » ; reste à le renvoyer à chaque changement de réseau (`ConnectivityManager`).
- **Réception** dans `Download/medXfer/` par chemin (`STO-15`).
- **Stockage** (décision J2) :
  - réception dans `medXfer/` à la racine du stockage partagé, rangée par type (`Apps`, `Photos`, `Vidéos`, `Musique`, `Fichiers`) ;
  - écrire à la racine demande l'**accès à tous les fichiers** (`MANAGE_EXTERNAL_STORAGE`), accordé par l'utilisateur dans les réglages, après un écran qui explique pourquoi ;
  - sans cet accès, repli dans `Download/medXfer/` (`STO-15`, sans permission) et envoi par le sélecteur du système, avec des descripteurs ;
  - une sonde refait d'abord sur le téléphone, à la racine, les gestes du récepteur (`.part`, `fsync`, renommage), comme G2A-10 l'a fait pour `Download`.
- **Manifeste** : `INTERNET`, `FOREGROUND_SERVICE_DATA_SYNC`, `POST_NOTIFICATIONS`, `CHANGE_WIFI_MULTICAST_STATE`, `MANAGE_EXTERNAL_STORAGE`, `READ_MEDIA_IMAGES`, `READ_MEDIA_VIDEO`, `READ_MEDIA_AUDIO` (et `READ_EXTERNAL_STORAGE` jusqu'à Android 12). Version du NDK fixée.

**Fait** (20 octobre) : réception dans `medXfer/<type>/` ou `Download/medXfer/`, accès à tous les fichiers demandé dans la fenêtre de l'offre, envoi par le sélecteur du système avec des descripteurs, service de premier plan de type `connectedDevice` (et non `dataSync`, limité à 6 h par jour depuis Android 15), réseau redonné au cœur à chaque changement. La sonde du stockage à la racine (`.part`, `fsync`, renommage) est faite par l'essai réel : réception complète dans `medXfer/Photos/`, empreintes identiques. Reste : « Ouvrir le dossier » à vérifier sur le téléphone.

### WP-B.9 — Explorateur par catégories (Android) · M

- Des onglets **Apps**, **Photos**, **Vidéos**, **Musique** et **Fichiers**, avec une sélection multiple qui survit au changement d'onglet et un panier visible (« 12 éléments, 1,4 Go ») avant d'envoyer.
- **Apps** : les applications lancées depuis l'écran d'accueil (requête `<queries>` sur `ACTION_MAIN`/`CATEGORY_LAUNCHER`, sans `QUERY_ALL_PACKAGES`), envoyées par le chemin de leur APK.
- **Photos, Vidéos, Musique** : MediaStore, avec vignettes et regroupement par album ou par dossier.
- **Fichiers** : navigation dans le stockage par chemin, avec l'accès à tous les fichiers. Sans cet accès, le sélecteur du système prend le relais.
- **Envoi par chemin** : avec ces accès, les fichiers sont lisibles là où ils sont (`STO-16`), sans copie ni descripteur.
- Les exigences UI-1 et UI-5 sont vérifiées sur une bibliothèque de plusieurs milliers de photos.

**Fait** (20 octobre) : onglets Apps, Photos, Vidéos, Musique et Fichiers ; sélection qui survit au changement d'onglet, « tout sélectionner » pour l'onglet affiché, panier (nombre, taille) puis « Envoyer » ou « Connecter ». Envoi : sans dossier, chaque fichier ouvert par l'application et remis par descripteur sous son nom (une application devient « Nom.apk ») ; avec des dossiers, tout par chemin ; applications et dossiers ensemble, à envoyer à part (le cœur prend des chemins ou des descripteurs, pas les deux). Essai réel : deux images choisies dans la grille, reçues sur Zorin avec des empreintes identiques.

Après le premier essai : vignettes demandées à la taille de la case (elles étaient floues à 256 px), chargées six à la fois en commençant par celles à l'écran, et oubliées si la case sort de l'écran avant son tour ; onglet Fichiers classé en Dossiers, Documents, E-books, Fichiers APK, Archives et Gros fichiers (100 Mo et plus, du plus gros au plus petit), par MediaStore ; vignettes des images, vidéos, PDF et icônes des APK dans les listes ; taille d'un dossier calculée quand on le choisit (le panier affichait 0 o). En version profile (`lib/perf.dart`, journal `perf:`), la grille Photos défile à 120 images par seconde, aucune image au-delà de 16,7 ms, p90 total 4,8 ms ; les 20 images par seconde constatées venaient d'une version debug, qui exécute le Dart sans compilation préalable. **Reste** : UI-1 et UI-5 rejouées depuis le panneau développeur de WP-B.5.

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

**Fait** (20 octobre) : écran Réglages (thème, accent, mode développeur), enregistré dans `settings.json` derrière `SettingsStore`. Mode développeur désactivé par défaut ; quand il est actif, un bandeau en bas de chaque écran ouvre le panneau. Le panneau montre le transfert en direct (vitesse, courbe, temps par étape et par connexion, étape limitante et sa netteté), lance les bancs disque et réseau, affiche et copie le rapport du dernier transfert, crée le paquet de diagnostic. Pour la vue en direct, `transfer_progress` donne maintenant les étapes mesurées (`stage_ms`) et l'étape limitante (`bottleneck`, `bottleneck_confidence`) ; jusque-là, elles n'existaient que dans le rapport de fin.

Corrigé en chemin, dans le cœur :
- `xfer node` ne répondait pas au banc réseau, qui affichait alors « 0 o/s, 0 ms » comme une mesure. Les deux partagent maintenant `AnswerBench`, et un banc sans réponse rend une erreur.
- L'aller-retour était mesuré sur l'horloge murale, dont les pas atteignent la milliseconde sous Windows. Il passe sur l'horloge monotone.
- Un transfert arrêté ou en échec laissait ses mesures ouvertes : le transfert suivant repartait de ses étapes, dans la vue en direct comme dans le rapport. Elles sont maintenant fermées, avec un rapport.
- Après un redémarrage, « dernier rapport » relit le plus récent enregistré.

Essai réel (Galaxy A57 et Zorin, Wi-Fi) :
- banc disque : écriture 393 Mo/s ;
- banc réseau : 5,1 Mo/s, aller-retour 4,7 ms ; contre l'ancien `xfer node`, l'erreur claire s'affiche ;
- réception d'un fichier de 800 Mo : courbe et étapes en direct, étape limitante « attente du réseau », nette ;
- paquet de diagnostic : configuration sans secret, rapports sans nom de fichier ;
- réglages relus après un redémarrage.

**Gate G7 : critères remplis** (`DEV-19-R`).

| Critère | Où |
|---|---|
| Télémétrie en direct | Panneau, transfert en direct |
| Lancer les bancs d'essai | Panneau, bancs disque et réseau ; `xfer bench` pour l'échelle complète |
| Exporter rapports et paquet de diagnostic | Panneau ; `xfer report`, `xfer bundle` |
| Appairage, envoi, réception | L'application (WP-B.2, WP-B.3, WP-B.9) |
| Scripts sans écran | `xfer ctl` (`DEV-16`) |

Le sélecteur natif, le dépôt temporaire et la navigation de fichiers du tableau de bord (`DEV-19` e) partent avec lui : l'application a ses propres sélecteurs et son explorateur.

**Tableau de bord retiré** (20 octobre, PR séparée) :
- la page, ses routes (`/api/browse`, `/api/fs/*`), le sélecteur natif, l'étiquette `devui` et la commande `xfer daemon` / `xfer dev-ui` ;
- la surface de contrôle qui ne servait qu'à elle : `/status`, la WebSocket `/ws`, le jeton de contrôle (`API-04`), la dépendance `gorilla/websocket` ;
- `/` mène au portail Web Share, qui reste ; le kit de test et `xfer ctl` pilotent le cœur dans le même processus, comme avant.

`scripts/check-prod-binary.sh` interdit toujours les symboles et le texte du tableau de bord, au cas où ils reviendraient.

**Reste**, hors des critères de G7 : le diagnostic à distance (`DEV-14`, `diag_request`) n'existe pas encore dans le cœur ; le mode développeur n'active pas encore les traces (`DEV-12`).

### WP-B.6 — Empaquetage · S

- **Windows** : un dossier portable zippé, avec le runtime Visual C++ à côté de l'exécutable, comme pour G2a ; sans installation ni droits administrateur.
- **Linux** : une archive `tar.gz` avec ses dépendances listées ; essai sur une distribution fraîche (réserve 2 de G2a).
- **Android** : APK signé avec une clé de développement. La clé de publication et le Play Store relèvent de J8. Les bibliothèques natives sont compressées dans l'APK (WP-B.2b) : 14,0 Mo en arm64 avec le lecteur de QR, contre 30,1 Mo sans compression (UI-5).
- Les règles du pare-feu Windows restent pour l'installateur de J8 ; d'ici là, le guide explique comment les poser.

**Fait** (20 octobre) : trois scripts, un paquet par système, dans `dist/`. Installation et pare-feu : [guide-utilisation-app.md](guide-utilisation-app.md).

| Paquet | Script | Taille | Vérifié |
|---|---|---|---|
| Windows : dossier portable zippé | `package-windows.ps1` | 17,1 Mo | Le cœur, `msvcp140`, `vcruntime140` et `vcruntime140_1` présents ; lancé décompressé hors de l'arbre, toutes ces DLL chargées depuis son dossier |
| Linux : `tar.gz` et `DEPENDANCES.txt` | `package-linux.sh` | — | Dans la CI, sur une Ubuntu 24.04 neuve avec les seuls paquets listés : toutes les bibliothèques trouvées, l'application tourne 15 s (Xvfb) |
| Android : APK arm64 | `package-android.ps1` | 20,4 Mo | Signé avec la clé de développement ; un APK signé avec la clé de débogage est refusé |

Ce que l'essai sur une distribution neuve a trouvé : Flutter charge OpenGL au lancement (`libEGL.so.1`, par `libepoxy`), ce que `ldd` ne voit pas. Sans lui, l'application s'arrête aussitôt. `DEPENDANCES.txt` liste maintenant `libegl1`, `libgl1`, `libgles2` et `libgl1-mesa-dri`. La réserve 2 de G2a (G2A-08 sous Linux) est levée.

La clé de développement : `scripts/android-dev-key.ps1` la crée hors du dépôt (`%USERPROFILE%\.medxfer\medxfer-dev.jks`) et écrit `app/android/key.properties`, jamais versionné. Sans ce fichier, la construction de version reste signée avec la clé de débogage, pour `flutter run --release`.

L'APK pèse 20,4 Mo contre 14,0 Mo à WP-B.2b : l'explorateur et ses bibliothèques s'y sont ajoutés. Il reste sous les 25 Mo d'UI-5.

Le guide signalait les ports TCP 18887 et UDP 5353 ; il manquait le port UDP 19998 de la balise de découverte, ajouté aux deux guides.

### WP-B.7 — Point d'accès local (`DISC-12`) : sonde, puis intégration · M

**La sonde**, tôt, sur le téléphone de test :
- Adaptateur Kotlin `WifiManager.startLocalOnlyHotspot`, derrière l'interface `Hotspot`. Android choisit le nom et le mot de passe du réseau ; l'application les affiche, sans pouvoir les fixer. Ce réseau ne donne pas accès à Internet.
- Questions à trancher :
  - un autre téléphone le rejoint-il depuis l'application, après le scan du QR (`WifiNetworkSpecifier`, avec l'accord de l'utilisateur demandé une fois par Android) ?
  - un PC le rejoint-il par ses réglages Wi-Fi ?
  - le transfert y passe-t-il, et à quel débit (bande 2,4 ou 5 GHz, choisie par le système) ?
  - la découverte y fonctionne-t-elle ?
  - le réseau tient-il pendant un long transfert, écran éteint ?

**Résultats de la sonde** (19 octobre, Galaxy A57 sous Android 16, PC Zorin client ; essais par adb) :

| Question | Résultat |
|---|---|
| Création depuis l'application | ✅ `startLocalOnlyHotspot` réussit avec `NEARBY_WIFI_DEVICES`. Le Wi-Fi du téléphone reste connecté (interface `swlan0`, 10.46.56.232/24), adb aussi. |
| Nom et mot de passe | Tirés au hasard par Android **à chaque démarrage** (`AndroidShare_1732`, puis `AndroidShare_9672`), sécurité WPA2/WPA3 mixte. Le QR les porte donc à chaque fois ; l'adresse du téléphone, elle, est restée la même. |
| Premier plan | Android ne démarre le point d'accès que pour une application au premier plan : appelé avant la reprise de l'activité (`onNewIntent`), il refuse (`ERROR_INCOMPATIBLE_MODE`). Depuis l'activité reprise, trois redémarrages de suite réussissent, moins d'une seconde après l'arrêt. L'adaptateur réessaie quand même pendant 20 s ; ce code veut aussi dire « point d'accès du système allumé ». |
| Un PC le rejoint | ✅ Par NetworkManager, en 1,1 s ; ping moyen 3 ms. |
| Bande | **2,4 GHz, canal 1** (802.11n, 130 Mbit/s). La puce n'a qu'une radio (`mIsDbs: false`) : le point d'accès prend le canal du Wi-Fi auquel le téléphone est connecté (ici, un réseau à 2,4 GHz ; il démarre sur le canal 11, puis passe au canal 1 en moins d'une seconde). Connecté à un réseau à 5 GHz, ou Wi-Fi coupé, il devrait passer à 5 GHz : à vérifier à la main. |
| Débit | `xfer bench net`, 11 essais : envoi du PC 4,2 à 9,8 MB/s, réception 4,9 à 6,1 MB/s. Le même PC passait par un autre point d'accès à 2,4 GHz : 2,0 MB/s. Transfert réel chiffré de 256 Mo par QR : 47,2 s (5,7 MB/s), empreinte identique. |
| Écran éteint | ✅ Huit essais à la suite sur 2 min 30, téléphone en veille et application en arrière-plan : aucun échec. Les durées plus longues relèvent du service de premier plan (WP-B.4). |
| Découverte | Les balises UDP arrivent bien par le point d'accès. **Mais** l'annonce mDNS et l'URI du QR portent l'adresse du Wi-Fi du téléphone (192.168.119.123), pas celle du point d'accès. Le transfert est passé quand même : le PC sortait par le téléphone, qui accepte toutes ses adresses. Un appareil qui sort ailleurs (câble, autre réseau) échouerait. |
| Un téléphone le rejoint par QR | À faire avec un second téléphone (`WifiJoin`, prêt). |

**Conséquences pour l'intégration** :
- Quand le point d'accès tourne, l'application donne au cœur l'interface `swlan0` par `set_network_info` (`DISC-10`), pour que le QR, le PIN et l'annonce portent l'adresse du point d'accès.
- Le débit dépend du canal du Wi-Fi du téléphone. Quand ce Wi-Fi est à 2,4 GHz, l'écran de connexion peut proposer de le couper le temps du transfert, si l'essai à la main le confirme.

**L'intégration** (faite en WP-B.2b) :
- L'URI d'appairage porte en option le nom et le mot de passe du réseau, pour que le QR fasse tout d'un coup. Ces champs sont des secrets du point d'accès : jamais journalisés (chapitre 13).
- Le parcours en trois étapes pour un appareil sans caméra.
- Si un téléphone refuse le point d'accès local : un message clair, et l'usage du point d'accès du système, réglé à la main.
- Tant que le point d'accès tourne, le cœur ne reçoit que son interface (`set_network_info`) : le QR, le PIN et l'annonce portent son adresse. Il s'arrête à la fermeture de « Connecter » si personne ne l'a rejoint, sinon à la fin de la session.
- Essai réel (20 octobre) : Zorin rejoint le point d'accès du Galaxy A57 par ses réglages, puis tape le PIN et l'adresse 10.46.56.232 ; jumelage et confiance mutuelle réussis, puis point d'accès arrêté à la fin de la session. La jonction par un second téléphone qui scanne le QR reste à essayer (#169).

### WP-B.10 — Dossier de réception au choix · S

Ajouté le 25 octobre à la demande de l'utilisateur (décision J16).

- **Réglages** : « Dossier de réception », avec le dossier en cours et un bouton pour en choisir un autre.
- **Windows et Linux** : sélecteur de dossier du système (`file_selector`).
- **Android, avec l'accès à tous les fichiers** :
  - choix d'un dossier du stockage, par l'explorateur de l'application ;
  - option « ranger par type » (`Photos/`, `Vidéos/`, `Fichiers/`…, la décision J2), activée par défaut.
- **Android, sans cet accès** : `Download/medXfer/`, comme aujourd'hui ; le réglage explique pourquoi il n'y a pas de choix.
- **Le cœur est prêt** : son dossier de téléchargement (`set_config`) et le dossier de chaque offre acceptée (`respond_offer`, `save_dir`).
- Un dossier qui n'existe plus ou n'est plus accessible fait revenir au dossier par défaut, avec un message.

**Fait** (25 octobre) : section « Réception » des réglages (dossier en cours, « Choisir un dossier », « Revenir au dossier par défaut », « Ranger par type » sous Android), enregistrée dans `settings.json`. Le dossier choisi doit accepter l'écriture (un fichier d'essai créé puis supprimé). Chaque offre acceptée porte son dossier (`save_dir`) ; un dossier disparu fait recevoir dans le dossier par défaut, avec un message. Sans l'accès à tous les fichiers, le réglage explique le choix bloqué et propose l'accès. Essai réel : dossier `medxfer-essai-b10` choisi sur le Galaxy A57 par l'explorateur, une image envoyée par l'application Windows, reçue dans `medxfer-essai-b10/Photos/`, empreintes identiques.

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
- **Point d'accès à 5 GHz** (Wi-Fi Direct) : sonde juste après J6b (J6c), intégration en J8 (décision J17).

## 5. Décisions

Réponses de l'utilisateur du 19 octobre 2026. Les propositions J4, J5, J7, J8, J9 et J11 ont été acceptées le même jour, avec la méthode des maquettes (J12) et le rangement par type (J2).

| # | Question | Décision | Conséquence ou autre choix |
|---|---|---|---|
| J1 | Où vit l'application ? | ✅ **Dans ce dépôt, dossier `app/`**. | Un seul dépôt, une seule CI : le cœur et l'interface changent ensemble (schéma v4). |
| J2 | Version minimale d'Android ; où vont les fichiers reçus | ✅ **Android 11 (API 30)**. Les fichiers reçus vont dans un **dossier `medXfer/` à la racine du stockage**, rangé par type. | Écrire à la racine demande l'accès à tous les fichiers (WP-B.4), qu'il faut justifier lors d'une publication sur le Play Store (en J8). Le transfert d'un appareil à l'autre fait partie des usages admis, mais c'est à vérifier à la publication. Sans cet accès, repli dans `Download/medXfer/`. |
| J3 | Identifiant de l'application Android | ✅ **`dev.medxfer.app`, provisoire.** Il sera remplacé par celui du nom définitif **avant toute publication**. | Après une publication, en changer donnerait une autre application pour les utilisateurs. |
| J4 | Gestion de l'état | ✅ **aucune bibliothèque** (`ChangeNotifier`, `ListenableBuilder`). | Riverpod, Bloc. |
| J5 | Lecture des QR à la caméra (Android) | ✅ **`flutter_zxing`** (zxing-cpp, libre), derrière l'interface `QrScanner`. | `mobile_scanner`, qui embarque ML Kit, un binaire Google non libre. |
| J6 | Comment choisir ce qu'on envoie sous Android | ✅ **Dans l'application, par onglets de catégories** (WP-B.9), envoi par chemin. Le sélecteur du système reste le repli. | Remplace la question des dossiers par descripteurs : avec l'accès aux fichiers, un dossier s'envoie par son chemin. |
| J7 | Langues de l'interface | ✅ **français et anglais dès le départ**. | Anglais seulement pour le MVP. |
| J8 | Historique | ✅ **gardé par l'application** (100 derniers transferts). | Une action d'historique dans le cœur. |
| J9 | Point d'accès local (`DISC-12`) | ✅ **Au cœur du parcours** (maquettes v2, 19 octobre) : créé depuis l'application, rejoint par QR ou par PIN. Une sonde passe tôt (WP-B.7), avant les écrans. | Si un téléphone le refuse : le point d'accès du système, réglé à la main. |
| J10 | Le panneau développeur | ✅ Critères de `DEV-19-R`, plus les remarques de l'utilisateur. Première remarque : la progression par fichier (WP-B.0). | D'autres remarques peuvent s'ajouter jusqu'à WP-B.5. |
| J11 | Empaquetage du MVP | ✅ Windows en dossier portable zippé, Linux en `tar.gz`, Android en APK de développement. | Installateurs dès J6b (prévus en J8). |
| J12 | Maquettes avant le code | ✅ Une page de maquettes cliquables (deux versions, validée le 19 octobre), puis le code Flutter lui-même, essayé sur le téléphone avec le cœur factice. | — |
| J13 | Exigences de l'interface (§1.1) | ✅ **Fluide, animée, personnalisable, et capable de tourner sur des téléphones anciens.** | Vérifiées à chaque lot (UI-1 à UI-5). |
| J14 | Connexion et envoi | ✅ **Se connecter d'abord**, puis envoyer dans les deux sens (§1.2). | Choisir les fichiers, puis l'appareil (la première maquette). |
| J15 | Identité visuelle | ✅ **Propre à medXfer** : thème clair, accent bleu, Figtree. D'une application prise en référence, on reprend le parcours et la disposition, jamais les couleurs ni l'apparence. | — |
| J16 | Où vont les fichiers reçus (25 octobre) | ✅ **Au choix de l'utilisateur**, dans les réglages (WP-B.10). Par défaut, la décision J2 : `medXfer/` rangé par type. | Un dossier fixe, celui de J2. |
| J17 | Point d'accès à 5 GHz (25 octobre) | ✅ **Par Wi-Fi Direct** (Android 10 et plus : un groupe créé par l'application, dont la bande peut être 5 GHz), derrière l'interface `Hotspot`, le point d'accès actuel restant en repli. Sonde juste après J6b (J6c), intégration en J8, avec la mise à jour de `DISC-12`. | `LocalOnlyHotspot` seul : Android choisit la bande, une application ordinaire ne peut pas demander 5 GHz (sonde WP-B.7 : 2,4 GHz, canal 1). |
