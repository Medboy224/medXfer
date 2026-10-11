# medXfer — Rapport du prototype G2a

**Date :** 11 octobre 2026 · **Plan :** [medxfer-plan-prototype-g2a.md](medxfer-plan-prototype-g2a.md) · **Code et mesures brutes :** branche `spike/g2a` (jamais fusionnée), dossier `spikes/g2a/`, commit `75741e2`.

Ce rapport répond à la question du plan (§1) : *le cœur Go peut-il être chargé comme bibliothèque dans une application Flutter sur Windows, Linux et Android, sans instabilité ni perte de performance notable ?*

---

## 1. Verdict

**Oui.** La gate **G2a est fermée** pour Windows, Linux et Android avec l'approche A : bibliothèque Go `c-shared` chargée par `dart:ffi`. L'approche B (`gomobile`, G2A-14) n'a pas été nécessaire.

| Décision | Contenu |
|---|---|
| Frontière | `c-shared` + `dart:ffi` sur les trois plateformes, avec une API C de 5 fonctions (§6). |
| Modèle d'événements | **Lecture** (*poll*) : un isolat Dart de fond lit la file d'événements du cœur par lots. Le **rappel** (`NativeCallable.listener`) est **écarté** : il fait planter l'application lors d'un redémarrage à chaud sous Windows (§4.1). Conformément à l'arbre de décision (§7 du plan), on retient le modèle qui passe partout. |
| Daemon et WebSocket | Peuvent être retirés en phase 4a (J6a). Le moteur réel fonctionne tel quel dans la bibliothèque. |

**Quatre réserves**, qui n'invalident pas la décision :

1. **G2A-06 sous Linux : 88 à 89 % du débit de `xfer`**, sous le seuil de 95 %. Mesuré sur un portable de 2011 (i5-2520M, 2 cœurs). L'enquête montre que le pont n'y est pour rien : la même bibliothèque appelée par la frontière C depuis un programme C fait 101,8 %. L'écart vient du processus hôte Flutter sur cette machine faible. Sous Windows (100,4 %) et sur le téléphone (142 %), aucun écart. À remesurer sur un PC Linux récent pendant J6a (§4.3).
2. **G2A-08 : l'essai sur une machine vraiment propre reste à faire.** Les dépendances sont inventoriées. Le paquet Windows embarque maintenant le runtime Visual C++. Les paquets se lancent hors de l'arbre de compilation avec un environnement vide. À faire avant le premier paquet public (J6b), dans Windows Sandbox ou sur un second PC, et sur une distribution Linux fraîche (§4.6).
3. **Émulateur Android : un passage complet sur trois** a montré un pic de lenteur isolé et une baisse de mémoire juste au-delà de ±5 %. Le vrai téléphone n'a montré aucun écart (§4.2).
4. **Windows : à-coups isolés de 230 à 240 ms** dans l'interface, environ une fois toutes les 3 à 4 minutes. Ils apparaissent avec les deux modèles d'événements **et aussi sans aucun événement**. Ils viennent de l'application Flutter sous Windows sur le PC de développement, pas du pont. Aucun événement n'est perdu. À surveiller en J6b avec la vraie interface (§4.2).

---

## 2. Réponses aux huit questions

| Id | Réponse |
|---|---|
| **Q1** Compilation reproductible depuis Windows | **Oui pour Windows et Android** : `spikes/g2a/build.ps1` produit `medxfer.dll` (llvm-mingw) et `libmedxfer.so` arm64 et x86_64 (NDK). Depuis un clone neuf, une commande suffit (22 s, cache Go chaud ; environ 20 s par cible à froid). **Linux** se compile sur un hôte Linux (`build.sh`) ; la compilation croisée depuis Windows (WSL2 ou `zig cc`) n'a pas été essayée. La CI Ubuntu le fera en phase 4. |
| **Q2** Chargement et messages JSON | **Oui, fiables.** 1 000 appels sans erreur, latence médiane d'un aller-retour de 3 à 9 µs, mémoire stable sur 100 000 appels. |
| **Q3** Modèle d'événements | **Lecture.** Les deux modèles livrent 70 000 événements par essai sans perte, sans désordre et sans doublon, sur toutes les plateformes. Mais le rappel plante au redémarrage à chaud sous Windows. |
| **Q4** Cycle de vie | **Maîtrisé** avec le modèle de lecture : `Start` → `Stop` → `Start` ; 20 redémarrages à chaud sur chaque plateforme ; paniques contenues ; arrière-plan Android de 5 min puis retour ; application tuée puis relancée sans processus orphelin. |
| **Q5** Moteur réel | **Oui, tel quel.** 1 Gio en boucle locale, hash identique partout ; Windows 100,4 % et téléphone 142 % du débit de `xfer`, Linux 88 à 89 % (réserve 1). Téléphone ↔ PC, 1 Gio dans chaque sens, au plafond du réseau. Le prototype a révélé un vrai défaut de `xfer send`, corrigé dans `main` (#44, #45). |
| **Q6** Réseau sous Android | L'**énumération des interfaces est refusée** (`netlinkrib: permission denied`), y compris pour Termux. Les replis de `discovery` ne trouvent aucune cible ; seule l'IP de la route par défaut est connue. Écoute TCP et UDP, multicast et diffusion fonctionnent, en envoi comme en réception. **En arrière-plan, le réseau de l'application est coupé.** Il faut donc une action `set_network_info` et un service de premier plan. |
| **Q7** Stockage sous Android | Sous Android 16, le cœur **écrit directement dans `Download/`**, sans permission : création de sous-dossier, `.part`, `fsync`, renommage. Le stockage actuel y fonctionne donc. Copier 1 Gio d'un dossier privé vers `Download` par MediaStore coûte 2,9 à 3,7 s. |
| **Q8** Taille et démarrage | Bibliothèque de 3,4 à 3,6 Mo par ABI une fois réduite (seuil d'alerte : 20 Mo). Du lancement de `main()` à la première réponse du cœur : 5 à 10 ms sur matériel réel, 52 à 83 ms sur l'émulateur (seuil d'alerte : 300 ms). |

---

## 3. Critères bloquants (plan §6.1)

✅ réussi · ⚠️ réussi avec réserve · ❌ échec · — non applicable

| Critère | Windows 11 | Linux (Zorin OS 17) | Android 16 (téléphone) | Détails |
|---|---|---|---|---|
| **G2A-01** Compilation reproductible | ✅ | ✅ | ✅ arm64 et x86_64 | §2 Q1 |
| **G2A-02** Aller-retour fiable | ✅ médiane 9 µs, dérive +0,29 % | ✅ médiane 6 µs, dérive −0,29 % | ✅ médiane 3 µs, dérive 0,0 % | §4.2 |
| **G2A-03** Événements sans perte | ⚠️ 0 perdu ; à-coups isolés de 230 à 240 ms, aussi sans aucun événement | ✅ lenteur max 2,2 à 13,6 ms | ✅ lenteur max 1,8 à 7,3 ms | §4.2 |
| **G2A-04** Panique contenue | ✅ | ✅ | ✅ | — |
| **G2A-05** Cycle de vie | ✅ (lecture) · ❌ (rappel) | ✅ | ✅ y compris arrière-plan et arrêt forcé | §4.1, §4.4 |
| **G2A-06** Moteur intact, débit ≥ 95 % | ✅ 100,4 % | ⚠️ 88 à 89 % | ✅ 142 % | §4.3 |
| **G2A-07** Transfert entre appareils | ✅ Windows ↔ Android, 1 Gio | non fait (« si possible ») | ✅ | §4.5 |
| **G2A-08** Empaquetage sans outils | ⚠️ | ⚠️ | — | §4.6 |

---

## 4. Résultats détaillés

### 4.1 Modèle d'événements et redémarrage à chaud (G2A-03, G2A-05)

Banc d'essai : `hot-restart.py` pilote `flutter run --machine` et fait 20 redémarrages à chaud (*hot restart*). Entre deux, le cœur émet 200 événements par seconde pendant 5 s, si bien que des événements sont toujours en vol au moment du redémarrage. À chaque génération, le script vérifie quatre choses : `already_started` (règle B4), le compteur `start_calls`, la réception d'événements, et l'absence de croissance des goroutines.

| Plateforme | Lecture | Rappel |
|---|---|---|
| Windows | ✅ 20/20 ; `start_calls` 1 → 21 ; goroutines 3 ; mémoire Go 10,8 → 11,6 Mo | ❌ **plantage au premier redémarrage** (2 essais sur 2) |
| Linux | ✅ 20/20 | ✅ 20/20 |
| Android (debug, par le débogage sans fil) | ✅ 20/20 | ✅ 20/20 |

Message du plantage : `runtime_entry.cc: error: Callback invoked after it has been deleted.` Au redémarrage, Dart détruit l'isolat et son `NativeCallable`, mais des goroutines du cœur gardent son adresse et l'appellent. Le plantage dépend du minutage : systématique sous Windows, jamais vu sous Linux et Android. Le même risque existe en production si Dart ferme le rappel pendant que Go l'appelle. Avec la lecture, Go ne garde **aucun pointeur vers du code Dart** : la question disparaît.

Deux règles sont nées de la mise au point du modèle de lecture (§6) :
- **Arrêt coordonné.** Tuer l'isolat lecteur n'interrompt pas un appel natif en cours : le lecteur bloqué dans `medxfer_next_event` volait le premier événement de l'essai suivant (1 événement perdu sur 70 000 lors du premier essai). Le cœur réveille désormais le lecteur avec une sentinelle (`poll_close`) et l'isolat sort de lui-même.
- **Lots.** Un message inter-isolat par événement faisait monter la lenteur de l'interface à 237 ms en rafale de 1 000 événements/s. En lisant jusqu'à 256 événements par message (`medxfer_next_event(0)` après le premier), la lenteur maximale tombe à 3 à 5 ms.

La mémoire du processus monte d'environ 1 Mo par redémarrage à chaud, côté Dart. Le redémarrage à chaud n'existe qu'en développement.

### 4.2 Aller-retour, événements, paniques (G2A-02 à G2A-04)

Suite automatique de l'application (`AUTORUN=full`) :
- `ping` : 1 000 appels chronométrés, puis 10 000 appels de chauffe et des points de mesure à +10 000, +60 000 et +160 000 appels.
- Événements : 10 000 à 100/s, puis 60 000 à 1 000/s, pour chaque modèle.
- Lenteur de l'interface : une minuterie de 10 ms dont on mesure le retard.

| | Windows | Linux | Téléphone | Émulateur (x86_64) |
|---|---|---|---|---|
| Latence `ping` médiane / p95 / max | 9 / 19 / 125 µs | 6 / 14 / 54 µs | 3 / 6 / 78 µs | 4 / 18 / 676 µs |
| Dérive mémoire, dernier segment | +0,29 % | −0,29 % | 0,0 % | 0,0 % (un passage sur trois : −5,02 %) |
| Événements perdus / hors ordre / doublons | 0 / 0 / 0 | 0 / 0 / 0 | 0 / 0 / 0 | 0 / 0 / 0 |
| Lenteur max de l'interface (lecture, rafale) | 14,6 à 15,8 ms | 10,0 ms | 7,3 ms | 20,2 ms (un passage sur trois : 109 ms) |
| Lenteur max sans aucun événement (référence) | 3,8 à 16,4 ms (un relevé : 241 ms) | 1,9 ms | 5,1 ms | 20,1 ms |

- Windows, à-coups de l'interface. Sur cinq séries de mesures des événements (S1, une reprise de S1, la série finale et deux reprises), on relève 5 pics au-delà de 100 ms : 121, 230, 232, 237 et 241 ms, chacun isolé.
  - Il n'y en a eu aucun pendant les 600 s de rafales.
  - Quatre sont tombés pendant les 1 000 s de flux à 100/s, avec les deux modèles.
  - Un est tombé pendant les 240 s de référence **sans aucun événement**.

  Le 99ᵉ centile reste sous 11 ms. Ces pics ne dépendent donc ni du modèle ni du débit d'événements : ils viennent de l'application Flutter sous Windows sur ce PC de développement, très occupé.
- La mémoire du processus monte pendant la chauffe, puis se stabilise : +63 Mo sur le téléphone, entièrement côté Dart. Le runtime Go reste entre 6 et 16 Mo (`memstats`). La stabilité est donc jugée sur le dernier segment : sous Linux, la chauffe de Dart dépasse 10 000 appels.
- Sur l'émulateur, un passage sur trois a montré deux écarts :
  - une baisse de mémoire de 5,02 %, qui n'est pas une fuite ;
  - un pic isolé de 109 à 113 ms en rafale, alors que la référence sans événement atteignait déjà 45 ms.

  L'émulateur tourne sans fenêtre, sur le PC de développement chargé. Le téléphone n'a montré aucun écart.
- Paniques : une panique dans un gestionnaire devient une erreur JSON (`panic: …`). Une panique dans une goroutine du cœur devient un événement `core_error`, avec sa pile. L'application continue.

### 4.3 Moteur réel et débit (G2A-06)

Boucle locale de 1 Gio dans l'application (`send_file` et `receive_file` du cœur), comparée à `xfer send` et `xfer recv` sur la même machine. On mesure la **durée du moteur** rapportée par le récepteur : du démarrage des workers au dernier fragment, finalisation exclue. On prend la médiane de 7 essais alternés.

| Machine | `xfer` | Application | Rapport |
|---|---|---|---|
| Windows 11, i5-8350U, SSD NVMe | 2 034 ms (503 Mio/s) | 2 025 ms (506 Mio/s) | **100,4 %** |
| Téléphone SM-A576B | 3 476 ms (295 Mio/s), Termux en arrière-plan | 2 446 ms (419 Mio/s) | **142 %** |
| Linux, i5-2520M 2 cœurs, disque mécanique, 256 Mio en cache | 509 ms | 588 à 599 ms (départ après 3 s) ; 671 à 695 ms (départ immédiat) | **88 à 89 %** (départ immédiat : 73 à 76 %) |

Hash SHA-256 identique dans tous les essais. Sous Linux, l'application est comparée à `cprobe`, mesuré dans la même série que l'application ; `cprobe` fait lui-même 101,8 % de `xfer`.

Enquête sur l'écart Linux. Toutes les variantes utilisent le même fichier source, avec un `sync` avant chaque essai :

| Variante | Moteur | / `xfer` |
|---|---|---|
| `oneproc` : émetteur et récepteur dans un seul processus Go, sans C ni Flutter | 499 ms | 102 % |
| `cprobe` : `libmedxfer.so` chargée par un programme C, frontière C, lecture des événements | 500 ms | **101,8 %** |
| Application, modèle rappel ou lecture | 670 à 700 ms / 560 à 690 ms | sans différence |
| Application, départ après 3 s, avec ou sans redessin de l'écran | 588 à 599 ms | 88 à 89 % |

Conclusion : ni la bibliothèque `c-shared`, ni la frontière C, ni le modèle d'événements, ni GOMAXPROCS (4 dans les deux cas), ni l'affinité CPU, ni le redessin n'expliquent l'écart. Il vient du processus hôte Flutter sur cette machine faible :
- environ 15 points au démarrage, quand Flutter finit son propre travail en parallèle ;
- environ 11 points ensuite, cause non identifiée sans profileur.

Ce coût porte sur le processeur, autour de 500 Mio/s. Un vrai transfert est limité par le réseau : 3 Mio/s en Wi-Fi ici, 110 Mo/s en gigabit. À ces débits, il ne se voit pas. Comme le prévoit l'arbre de décision, la cause a été cherchée. Elle n'est pas dans la frontière, et la frontière ne porte déjà que commandes et progression. **Recommandation :** accepter, et remesurer sur un PC Linux récent pendant J6a.

**Fuites.** Contrôle sous Windows : 20 transferts de 64 Mio, hash identiques. Les goroutines passent de 1 à 1. Le tas Go en usage passe de 5,9 Mo à 207 Mo juste après, puis retombe à 2,0 Mo après un passage forcé du ramasse-miettes. Il n'y a donc **pas de fuite**, mais 200 Mo de déchets attendent le prochain GC (5 GC seulement pour 1,28 Gio). C'est à surveiller sur mobile (§7).

### 4.4 Android : arrière-plan et arrêt forcé (G2A-05)

Script `g2a05-android.ps1`, sur le téléphone.

| Étape | Observé |
|---|---|
| L'application sert un fichier sur le port 19990, puis passe en arrière-plan (touche Accueil) pendant 5 min 29 s | Le processus reste vivant (même pid). **Le port est injoignable depuis le PC pendant toute la durée** : Android coupe le réseau de l'application. Cycle de vie : `inactive` → `hidden` → `paused`. |
| Retour au premier plan | `hidden` → `inactive` → `resumed`. Le PC télécharge les 64 Mio en 33,6 s, hash identique : l'état est cohérent. |
| Pendant une autre écoute, arrêt forcé (`am force-stop`) | Plus aucun processus, port fermé. |
| Relance | L'application écoute de nouveau sur le même port ; téléchargement et hash corrects. |

Un essai précédent l'a confirmé : un transfert en cours se fige dès que l'application passe en arrière-plan (figé à 3,7 % pendant que l'utilisateur ouvrait une autre application). **Les transferts sous Android exigent un service de premier plan.**

### 4.5 Transfert entre appareils (G2A-07)

Téléphone SM-A576B ↔ PC Windows. Les deux appareils étaient clients du point d'accès d'un autre téléphone, en 2,4 GHz.

| Sens | Durée | Débit | SHA-256 |
|---|---|---|---|
| Application → `xfer recv` (PC) | 5 min 42 s | 3,0 Mio/s | identique |
| `xfer send` (PC) → application | 7 min 08 s | 2,4 Mio/s | identique |

`xfer bench net` sur le même réseau : 2,7 Mo/s en montée, 2,3 Mo/s en descente. Le moteur est au plafond du lien.

Le premier essai a échoué : le fichier reçu était tronqué à 91 %. Je l'ai reproduit **sans l'application**, entre `xfer` dans Termux et `xfer` sur le PC. Cause : `xfer send` s'arrêtait dès que les octets étaient écrits dans le socket, sans attendre qu'ils soient reçus. C'est corrigé dans `main` (#44, PR #45) ; le cœur du prototype applique la même règle.

### 4.6 Empaquetage (G2A-08)

| | Windows | Linux |
|---|---|---|
| Cœur Go | `medxfer.dll` n'importe que `KERNEL32` et l'UCRT (`api-ms-win-crt-*`), intégrés à Windows 10 et 11 | `libmedxfer.so` ne dépend que de la libc ; glibc ≥ 2.34 (compilée sur Ubuntu 22.04) |
| Lanceur Flutter | `medxfer_g2a.exe` (MSVC) importe `MSVCP140`, `VCRUNTIME140`, `VCRUNTIME140_1`, absents d'un Windows neuf. **Corrigé dans le prototype** : `InstallRequiredSystemLibraries` copie ces DLL à côté de l'exécutable | Pile GTK3 d'un bureau standard ; le lanceur demande aussi glibc ≥ 2.34 |
| Lancement hors de l'arbre | Paquet copié ailleurs, `PATH` réduit à `System32` : suite rapide réussie | Paquet copié, `env -i` (aucune variable de développement) : suite rapide réussie |
| Machine propre | **À faire** (Windows Sandbox n'est pas activé sur le PC) | **À faire** (distribution fraîche) |

Pour viser Ubuntu 20.04 (glibc 2.31) ou plus ancien, il faudra compiler sur une base plus ancienne, par exemple un conteneur.

---

## 5. Critères informatifs (plan §6.2)

### G2A-09 — Réseau sous Android

Sonde `net_probe` dans l'application, téléphone sous Android 16. Le PC envoie en même temps des paquets UDP vers le groupe et en diffusion.

| Test | Résultat |
|---|---|
| `net.Interfaces()`, `net.InterfaceAddrs()` | **Refusés** : `route ip+net: netlinkrib: permission denied`. Même résultat dans Termux. |
| `discovery.GetActiveNetworkTargets()` | **Vide** : les replis actuels ne trouvent rien. |
| IP par la route par défaut (`GetPrimaryLocalIP`) | ✅ 192.168.21.123 |
| Écoute TCP sur `0.0.0.0`, écoute UDP sur 19998 | ✅ |
| Adhésion au groupe 239.255.255.250, envoi multicast, réception de son propre paquet | ✅ |
| Envoi en diffusion (255.255.255.255) | ✅ |
| Réception depuis le PC, sans puis avec `MulticastLock` | ✅ 68 à 72 paquets multicast et 134 à 144 de diffusion en 8 s, dans les deux cas (écran allumé) |
| Incident | Lors du premier passage sur cinq, l'envoi multicast et l'envoi en diffusion ont échoué avec `operation not permitted`. Non reproduit en quatre passages : cible API 34 et 36, APK universel, installation neuve. |
| En arrière-plan | Réseau de l'application coupé (§4.4). |

Conséquences :
- **`set_network_info` est nécessaire**. La plateforme (Kotlin, `ConnectivityManager`) fournit au cœur les adresses, masques et noms d'interfaces.
- La découverte doit tolérer des erreurs d'envoi UDP passagères.
- `DISC-03` (`MulticastLock`) est maintenu, parce que la réception sans verrou n'a été vérifiée qu'écran allumé.

### G2A-10 — Stockage sous Android

Sonde `storage_probe` : dans chaque dossier, elle refait les gestes du récepteur actuel. Elle crée un sous-dossier, écrit 8 Mio en `.part`, fait un `fsync`, renomme vers le nom final, relit, puis supprime.

| Dossier | Résultat |
|---|---|
| Dossiers privés (`files`, `cache`, dossier temporaire de Dart) | ✅ toutes les étapes |
| `Android/data/<paquet>/files` | ✅ |
| `/storage/emulated/0/Download` et `/sdcard/Download`, en chemin direct | ✅ **toutes les étapes, renommage compris, sans aucune permission** |

Coût de l'approche « recevoir dans le dossier privé, puis publier », pour 1 Gio :

| Étape | Durée |
|---|---|
| Écriture dans le dossier privé | 1,4 à 1,5 s (un essai à 6,1 s) |
| Copie vers `Download/medxfer_g2a/` par MediaStore (`IS_PENDING`) | 2,9 à 3,7 s |
| Copie directe par chemin | 2,7 à 5,5 s |

**Recommandation pour J4b et J6b.** À partir d'Android 11 (API 30), recevoir directement dans `Download/medXfer/` par chemin. La conception actuelle (`.part` puis renommage, `STO-01`, `STO-02`) y fonctionne sans double écriture. MediaStore reste le repli pour Android 10 et les versions antérieures, non testés ici, et pour un dossier choisi par l'utilisateur via le sélecteur de dossiers (SAF), non testé non plus.

### G2A-11 — Taille

| Cible | Bibliothèque réduite (`-s -w -trimpath`) |
|---|---|
| Windows amd64 | 3,4 Mo |
| Linux amd64 | 3,6 Mo |
| Android arm64 | 3,4 Mo |
| Android x86_64 | 3,6 Mo |

Ces tailles incluent le moteur, la découverte et le code de test du prototype. Par défaut (API minimale ≥ 23), AGP range les bibliothèques natives non compressées : le supplément de l'APK vaut à peu près la taille de la bibliothèque, par ABI. Seuil d'alerte : 20 Mo.

### G2A-12 — Démarrage à froid

| | De `main()` à la première réponse du cœur | Lancement de l'activité (`am start -W`) |
|---|---|---|
| Téléphone | 9 à 18 ms | 0,76 s |
| Émulateur | 52 à 83 ms | 3,4 à 8,4 s |
| Windows | 5 à 8 ms (mode release) ; 267 ms au premier lancement après une compilation, sans doute l'analyse antivirus de la nouvelle DLL ; 88 à 94 ms (mode debug) | — |
| Linux | 4 à 7 ms | — |

Seuil d'alerte : 300 ms sur le téléphone.

### G2A-13 — Coût de la refonte des gestionnaires (pour J6a)

Dans `pkg/api` :
- 27 fonctions reçoivent un `*websocket.Conn`, soit environ 1 080 lignes : 26 dans `handler.go`, 1 dans `server.go`.
- Elles ne s'en servent que pour **38 réponses directes** (`sendTo(conn, …)`).
- **85 diffusions** (`Broadcast`) sont déjà indépendantes de la connexion.
- 4 autres usages concernent la mise en place de la connexion.

La refonte est mécanique : une petite interface « répondeur » remplace la connexion dans les signatures, puis on réécrit les 38 appels. Coût estimé : une demi-journée à une journée.

---

## 6. Contrat de la frontière retenu

Fonctions exportées par la bibliothèque. Toutes les chaînes sont du JSON en UTF-8.

| Fonction | Rôle |
|---|---|
| `char* medxfer_start(const char* config)` | Démarre le cœur. Idempotente : `{"ok", "already_started", "start_calls"}`. |
| `char* medxfer_call(const char* request)` | Requête `{id, action, payload}`, réponse synchrone `{id, ok, …}`. |
| `char* medxfer_next_event(int timeout_ms)` | Renvoie un événement, ou `NULL` après `timeout_ms`. `0` = sans attente, pour vider un lot. |
| `void medxfer_stop(void)` | Arrête le cœur. |
| `void medxfer_free(char* p)` | Libère une chaîne rendue par la bibliothèque. |

`medxfer_set_event_callback` a été essayée, puis écartée (§4.1).

Règles B1 à B7 du plan, toutes vérifiées :

| Règle | Contenu |
|---|---|
| B1 | Chaque `char*` rendu est libéré par `medxfer_free`, jamais par l'allocateur de l'appelant. |
| B2 | Aucun pointeur Go ne traverse la frontière (copie par `C.CString`). |
| B3 | Messages en JSON UTF-8. |
| B4 | `medxfer_start` est idempotente. Un redémarrage à chaud de Flutter relance Dart, pas le processus. |
| B5 | Chaque goroutine du cœur récupère ses paniques en événement `core_error`. Une panique dans `medxfer_call` devient une erreur JSON. |
| B6 | Les chemins viennent de l'appelant (`data_dir`, `download_dir`). |
| B7 | Événements de progression limités à 10 par seconde et par transfert. |

Règles nouvelles, issues des mesures :

| Règle | Contenu |
|---|---|
| B8 | Le cœur ne garde **aucun pointeur vers du code Dart**. Les événements passent par la file du cœur, lue par `medxfer_next_event`. |
| B9 | Le lecteur d'événements s'arrête de façon coordonnée : le cœur le réveille par une sentinelle, et l'isolat sort de lui-même. On ne tue jamais un isolat bloqué dans un appel natif. |
| B10 | Le lecteur transmet les événements par lots, jusqu'à 256 par message à l'isolat de l'interface. |
| B11 | La file du cœur est bornée (65 536). Un événement qui ne trouve pas de place est compté (`drops`) ; le moteur n'attend jamais l'interface. |
| B12 | Sous Android, le cœur reçoit les informations réseau de la plateforme (`set_network_info`) et tolère des erreurs d'envoi UDP passagères. |
| B13 | Sous Android, transferts et écoute tournent sous un service de premier plan. |

Dans la bible, B1 à B11 forment le §10.4 (`API-07`). B12 devient `DISC-10`, B13 devient `API-08`.

---

## 7. Changements à prévoir pour la phase 4a (J6a, J6b)

1. **Gestionnaires sans WebSocket** (G2A-13) : interface « répondeur » dans les 27 fonctions ; `Broadcast` alimente la file d'événements du cœur. Une demi-journée à une journée.
2. **Paquet de frontière** : les 5 fonctions et les règles B1 à B13. Tests « golden » des messages (`API-06`). Compteur `drops` dans les statistiques.
3. **Lecteur Dart** : isolat de fond, lots, arrêt par sentinelle. Pas de `NativeCallable`.
4. **`set_network_info`** et découverte qui accepte les adresses fournies par la plateforme (`DISC-10`).
5. **Android** :
   - service de premier plan pendant les transferts et l'écoute ;
   - `MulticastLock` pendant la découverte ;
   - destination `Download/medXfer/` par chemin à partir de l'API 30, repli MediaStore ;
   - permission `INTERNET` dans le manifeste principal (le gabarit Flutter ne la donne qu'aux versions debug et profile) ;
   - version du NDK fixée dans `build.gradle.kts`.
6. **Moteur** : délai maximal sur les connexions de `Receiver.Pull` (#48). La fin d'envoi qui attend le récepteur (#44) est déjà corrigée. Le message de fin du protocole v1 (J4a) remplacera cette détection.
7. **Mémoire sur mobile** : envisager `debug.SetMemoryLimit` ou une libération après chaque transfert, puisque 200 Mo de déchets s'accumulent entre deux GC (§4.3). 📏
8. **Empaquetage** :
   - Windows : runtime Visual C++ à côté de l'exécutable (déjà dans le prototype) ;
   - Linux : base de compilation à choisir selon les distributions visées (glibc) ;
   - essai sur machine propre avant J6b (réserve 2).
9. **Remesures** : G2A-06 sur un PC Linux récent ; G2A-07 Linux ↔ Android ; G2A-09 et G2A-10 sur une version d'Android plus ancienne (10 ou 11) ; à-coups de l'interface sous Windows avec la vraie interface, sur un autre PC si possible.

---

## 8. Environnement

| | Version |
|---|---|
| Go | 1.24.9 (`go.mod` : 1.24) |
| Flutter / Dart | 3.47.6 / 3.13.5 (canal stable) |
| Windows | Windows 11 Pro 10.0.26200, i5-8350U (4 cœurs, 8 threads), 16 Go, SSD NVMe |
| Compilateurs Windows | llvm-mingw 20260922 (clang 23.1.2) pour la DLL ; MSVC 14.51 (Build Tools 2026) pour le lanceur Flutter ; Windows SDK 10.0.26100 |
| Android | SDK 36.1, NDK 30.0.16248370, API 24 pour la compilation de la bibliothèque, cible API 36 ; Gradle 9.3.1 |
| Linux | Zorin OS 17 (Ubuntu 22.04, noyau 6.2, glibc 2.35), gcc 11.4, i5-2520M (2 cœurs, 4 threads), 3,8 Go, disque mécanique |
| Téléphone | Samsung SM-A576B, Android 16, arm64 |
| Émulateur | AVD API 36, x86_64 |

---

## 9. Reproduire les mesures

Tout est sur la branche `spike/g2a`, dans `spikes/g2a/` :

| Fichier | Rôle |
|---|---|
| `build.ps1`, `build.sh` | Bibliothèque par plateforme |
| `app/` | Application Flutter de test. Modes `AUTORUN` : `quick`, `full`, `loopback`, `g2a07`, `probes`, `listen`, `restart` |
| `android-run.ps1` | Installe l'APK, lance l'application et rassemble le JSON des résultats depuis logcat |
| `cli-loopback.sh` | Référence `xfer send` / `xfer recv` pour G2A-06 |
| `oneproc/`, `cprobe/` | Témoins de l'enquête sur G2A-06 |
| `g2a07.ps1` | G2A-07 |
| `hot-restart.py` | 20 redémarrages à chaud |
| `g2a05-android.ps1` | Arrière-plan et arrêt forcé sous Android |
| `android-probes.ps1` | G2A-09 et G2A-10 |
| `results-*.json`, `results-g2a06.md` | Résultats bruts |
