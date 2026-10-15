# medXfer — Plan détaillé du jalon J6a (cœur en-processus et découverte)

> **Source :** feuille de route « J6a » ; bible chapitre 5 (`DISC-01` à `DISC-10`), chapitre 10 (`API-01` à `API-08`, règles B1 à B11) ; rapport du prototype [g2a-report.md](g2a-report.md), §6 et §7.
> **Objectif :** medXfer devient une **bibliothèque** que l'interface (Flutter en J6b, la ligne de commande dès maintenant) appelle dans son propre processus, sans socket de contrôle :
> - le cœur s'utilise avec 4 fonctions : démarrer, appeler, lire un événement, arrêter ;
> - la bibliothèque `c-shared` les expose en C avec `medxfer_free` ;
> - les appareils se trouvent par **mDNS** (`_medxfer._tcp`), avec une balise UDP de repli, et seulement quand l'utilisateur l'a demandé.
>
> **Critère de sortie (feuille de route) :** tous les tests passent par l'API en-processus ; le daemon et son WebSocket ne servent plus qu'au tableau de bord de développement (`devui`).
>
> **Durée estimée :** 7 à 10 jours. Une PR par lot, CI verte sur les trois systèmes avant fusion.

## 0. État de départ (constaté sur `main` après J5)

| Constat | Conséquence pour J6a |
|---|---|
| Dans `pkg/api`, **32 fonctions** reçoivent un `*websocket.Conn` (26 dans `handler.go`, 3 dans `trust.go`, 2 dans `pin.go`, 1 dans `server.go`), pour **48 réponses directes** (`sendTo`). Les **97 diffusions** (`Broadcast`) sont déjà indépendantes de la connexion. G2a en comptait 27 et 38 : J5 en a ajouté. | WP-6.1 : interface « répondeur ». |
| `xfer ctl` et `testkit` pilotent un daemon **par WebSocket** (`wsclient`) : tous les tests d'intégration passent par le socket. | WP-6.3 : les mettre sur l'API en-processus. |
| La frontière C (5 fonctions, règles B1 à B11) est **validée par G2a**, mais son code vit sur la branche `spike/g2a`, jamais fusionnée, avec un moteur simplifié. | WP-6.4 : la réécrire au-dessus du vrai cœur, d'après le contrat (pas de copie du prototype). |
| La découverte émet **en permanence** une balise toutes les 600 ms vers `239.255.255.250`, l'adresse de SSDP/UPnP. **Pas de mDNS.** Le nom générique `medXfer-xxxx` est en place depuis #111. | WP-6.5 : `DISC-01`, `DISC-04` et `DISC-05` ne sont pas encore tenus, même si la bible les marque ✅. Le statut sera corrigé. |
| Sous Android, `net.Interfaces` est refusé. Dans Termux, le code se rabat sur la commande `ifconfig`, que le bac à sable d'une application n'autorise pas : dans l'application G2a, `GetActiveNetworkTargets` était **vide**. | WP-6.5 : `set_network_info` (`DISC-10`). |
| Le daemon (`xfer daemon`) sert à la fois de cœur, de surface de contrôle WebSocket et de tableau de bord. | WP-6.6 : il ne reste qu'une enveloppe de développement autour du cœur. |

## 1. Ordre d'exécution et dépendances

```
WP-6.0 veille mDNS ─────────────────────────────────────────────┐
WP-6.1 répondeur ──► WP-6.2 pkg/core ──► WP-6.3 testkit, ctl ──►├─► WP-6.6 daemon = devui ──► WP-6.7 essai réel, rapport
                                    └──► WP-6.4 bibliothèque C ─┤
                                         WP-6.5 découverte ◄────┘ (après WP-6.0)
```

WP-6.0 se fait en parallèle des premiers lots. WP-6.4 et WP-6.5 sont indépendants l'un de l'autre.

## 2. Les lots

### WP-6.0 — Veille mDNS · S

Choisir l'implémentation de `_medxfer._tcp` selon cinq critères :
1. pur Go, sans CGO ;
2. maintenue ;
3. licence compatible avec Apache 2.0 ;
4. fonctionne **sans énumérer les interfaces** : sous Android, avec les adresses fournies par `set_network_info` et une adhésion au groupe sur l'interface par défaut (G2A-09 l'a vue fonctionner) ;
5. annonce et recherche, avec retrait propre (`goodbye`).

**Candidats, à vérifier pendant la veille, sans rien supposer :** `hashicorp/mdns`, `grandcat/zeroconf` et sa reprise `libp2p/zeroconf`, `brutella/dnssd`. Autre option : écrire nous-mêmes la petite partie DNS-SD sur `golang.org/x/net/dns/dnsmessage`. Ce serait le format de messages de la bibliothèque standard étendue, sans crypto (D10 de J5 ne s'applique pas).

**Essais :**
- PC Windows ↔ téléphone Termux ↔ PC Zorin ;
- annonce vue par l'outil du système (`dns-sd` sous Windows, `avahi-browse` sous Linux).

**Sortie :** une note de veille, comme pour G1, et la décision D5 ci-dessous.

### WP-6.1 — Gestionnaires sans WebSocket · S

- **Interface `Responder`** (D1), qui remplace `*websocket.Conn` dans les 32 fonctions. Les 48 `sendTo` deviennent `r.Reply(evt)`. Transformation mécanique, sans changement de comportement.
- **Adaptateur WebSocket** : le daemon actuel enveloppe sa connexion dans un `Responder`. Les tests existants passent sans changement : c'est la preuve qu'il n'y a pas de régression.

### WP-6.2 — `pkg/core` : le cœur en-processus · M

- **API Go** (D2) : `core.New(Config) (*Core, error)`, puis `(*Core).Start`, `Call(req) Response`, `NextEvent(timeout) (Event, bool)` et `Stop`.
  - Plusieurs cœurs dans un même processus sont permis : les tests en lancent deux.
  - La bibliothèque C n'en tient qu'un.
- **Requêtes et réponses :**
  - `Call` prend `{id, action, payload}` et rend une réponse synchrone `{id, ok, …}` (`API-02`) ;
  - les réponses aujourd'hui envoyées à part (`sendTo`) deviennent la réponse de `Call` quand il n'y en a qu'une ; sinon des événements corrélés par `id`.
- **File d'événements** (D3) :
  - `Broadcast` alimente une **file bornée** (65 536, B11), avec les pertes comptées (`drops`) dans les statistiques ;
  - progression limitée à 10 événements par seconde et par transfert (B7) ;
  - chaque goroutine récupère ses paniques en événement `core_error` (B5) ;
  - les chemins viennent de la configuration (B6).
- **Messages versionnés** et fichiers « golden » (`API-06`) : chaque action et chaque événement a un exemple figé dans `testdata/`.
- **Secrets** : le journal et l'événement `pair_pin_shown` gardent leurs règles. Un événement adressé à un seul client (aujourd'hui `sendTo`) devient la réponse à l'appel, et n'est jamais diffusé.

### WP-6.3 — `testkit` et `xfer ctl` en-processus · M

- **`testkit.Node`** pilote un `*core.Core` directement : même vocabulaire (`Request`, `WaitEvent`), sans socket.
- **`xfer ctl`** rejoue ses scénarios sur un cœur en-processus.
- **Garde-fou :** un test vérifie qu'**aucun test d'intégration** n'ouvre de WebSocket, à part les tests propres à `devui`, ceux du transport. C'est le critère de sortie.

### WP-6.4 — Bibliothèque `c-shared` · M

- **`cmd/libmedxfer`** (`package main`) expose les **5 fonctions** du §10.4 au-dessus de `pkg/core`. B1 à B4 sont tenues au plus près de la frontière : `medxfer_free`, copies `C.CString`, JSON UTF-8, `medxfer_start` idempotente.
- **Arrêt coordonné** (B9) : une sentinelle réveille `medxfer_next_event`.
- **Compilation :**
  - Windows : llvm-mingw ;
  - Android : NDK, arm64 et x86_64 ;
  - Linux : sur l'hôte et dans la CI Ubuntu.
  - Les scripts reprennent ceux de G2a.
- **Tests :**
  - un petit programme C, ou un test Go avec cgo sous Linux, appelle les 5 fonctions : démarrage idempotent, appel, événements par lots, arrêt pendant une attente, libération ;
  - la CI Ubuntu compile la bibliothèque à chaque PR.
- **Taille surveillée** (G2A-11) : seuil d'alerte à 20 Mo.

### WP-6.5 — Découverte : mDNS, repli, sur demande · M à L

- **Interface `Discoverer`** (D9 de J5 : modularité), avec trois adaptateurs :
  - mDNS (WP-6.0) ;
  - balise UDP de repli ;
  - plus tard, Bonjour ou NSD fournis par la plateforme (iOS en J9, Android si l'essai l'exige).
- **`DISC-01`** : `_medxfer._tcp` sur le port du listener, TXT réduit à `v=1`, nom `medXfer-xxxx`.
- **`DISC-04`** : balise de repli vers un groupe de `224.0.0.0/24` (D6), charge utile identique au TXT, une toutes les 2 s au plus. **`239.255.255.250` est abandonné.**
- **`DISC-05`** : annonce **seulement sur demande**, c'est-à-dire mode « recevoir » ouvert, envoi en attente ou `xfer node`. Le reste du temps, l'appareil cherche mais ne s'annonce pas.
- **`DISC-07`** : le suffixe du nom (`…-4F2A`) s'affiche à côté du PIN et du QR, pour reconnaître le bon appareil.
- **`DISC-10`** : action `set_network_info` (adresses, masques, noms d'interfaces). Le cœur s'en sert à la place de l'énumération, et tolère les erreurs d'envoi UDP passagères.
- **Appareils de confiance reconnus dans la liste** : par leur adresse, en local, avant même l'appairage (réserve 4 du rapport J5).
- **Tests :**
  - annonce et recherche entre deux cœurs en boucle locale ;
  - pas d'annonce sans demande ;
  - repli UDP seul ;
  - `set_network_info` sans énumération possible, simulé.

### WP-6.6 — Le daemon ne sert plus qu'à `devui` · S

- La surface de contrôle WebSocket (`/ws`, et les routes `/api/*` de développement) n'est compilée qu'avec le tag `devui` (`API-04` : désactivée par défaut). Le binaire de publication n'a plus de socket de contrôle (`API-01`).
- **`xfer daemon`** : voir D7.
- Le Web Share (J7) garde son propre listener, sans changement ici.

### WP-6.7 — Essai réel et rapport · S

- **Découverte mDNS** PC ↔ téléphone (Termux) ↔ PC Zorin, dans les deux sens : l'appareil n'apparaît que quand il est en mode « recevoir ».
- **Bibliothèque Android** chargée par une application minimale. Le lanceur du prototype G2a peut resservir : on vérifie seulement que la vraie bibliothèque démarre, répond, envoie et reçoit.
- **Mémoire sur mobile** (G2A §7.7) : mesure de 20 transferts de suite, et décision sur `debug.SetMemoryLimit`, à appliquer en J6b.
- **Rapport de sortie.**

## 3. Critère de sortie de J6a

1. **Tous les tests d'intégration passent par l'API en-processus** (`pkg/core`). Seuls les tests du transport `devui` ouvrent un WebSocket, et un test le vérifie.
2. **La bibliothèque `c-shared`** se compile pour Windows, Linux et Android, et ses 5 fonctions sont testées. Linux est compilé dans la CI.
3. **Découverte mDNS** entre le PC et le téléphone, dans les deux sens, uniquement sur demande ; repli UDP vérifié.
4. **Le binaire de publication** n'a plus de socket de contrôle ; le daemon et son WebSocket n'existent qu'avec `devui`.
5. **Bible à jour :** statuts réels de `DISC-01`, `DISC-04`, `DISC-05` et `API-01` à `API-08`.

## 4. Hors périmètre

- **L'application Flutter**, ses écrans, le lecteur d'événements Dart (isolat, lots, sentinelle côté Dart) et le service de premier plan Android (`API-08`) : J6b.
- **macOS et iOS**, Bonjour du système (`DISC-02`) : J9, après G2b et G8.
- **Web Share v2** : J7.

## 5. Décisions à valider avec ce plan

| # | Décision | Raison |
|---|---|---|
| D1 | **Interface `Responder { Reply(EventMessage) }`** à la place de `*websocket.Conn` dans les gestionnaires. Le WebSocket et le cœur en-processus en sont deux implémentations. | G2A-13 : refonte mécanique, sans changer le comportement ; le transport devient un détail (modularité). |
| D2 | **`pkg/core` gère des instances** (`core.New`), pas un état global ; la bibliothèque C en tient une seule. | Les tests lancent deux appareils dans un processus ; un état global l'interdirait. |
| D3 | **Une seule file d'événements par cœur**, bornée à 65 536, pertes comptées ; le transport `devui` la lit comme l'interface le ferait. | Règles B8 et B11 ; un seul chemin pour les événements, donc une seule chose à tester. |
| D4 | **La bibliothèque est réécrite d'après le contrat du §10.4**, sans fusionner le code de `spike/g2a`. | Le prototype portait un moteur simplifié et une fonction écartée (`medxfer_set_event_callback`) ; c'était du code jetable. |
| D5 | **mDNS en Go derrière l'interface `Discoverer`**, sur le bureau et sous Android (adresses fournies par `set_network_info`). Bibliothèque choisie par la veille WP-6.0. Si l'essai sous Android échoue, l'adaptateur NSD de la plateforme arrive en J6b. | Un seul code pour le bureau et Android ; la plateforme reste possible sans rien réécrire (D9 de J5). Sur iOS, Bonjour du système est de toute façon obligatoire (`DISC-02`). |
| D6 | **Balise de repli en diffusion sur chaque sous-réseau**, port 19998, **sans groupe multicast** (révisée après la veille WP-6.0 et validée le 15 octobre 2026). | Les adresses de `224.0.0.0/24` s'attribuent par l'IANA et ne se prennent pas d'office ; `239.255.255.250` est celle de SSDP/UPnP. La diffusion ne dépend d'aucun registre, et le téléphone la reçoit (G2A-09). |
| D7 | **`xfer daemon` n'existe plus que dans les builds `devui`** (avec `xfer dev-ui`). Le binaire de publication garde la ligne de commande (`send`, `recv`, `pair`, `node`…), qui appelle le cœur en-processus (`API-03`). | Critère de sortie : le WebSocket ne sert plus qu'à `devui` ; une surface de contrôle réseau n'a pas sa place dans un binaire publié (chapitre 10). **À trancher :** si tu utilises `xfer daemon` sans tableau de bord, par exemple sur Termux, dis-le, et on garde un mode sans écran en plus. |
| D8 | **Les noms d'actions et d'événements ne changent pas** (`API-02`). Seul le transport change, et le schéma est figé par les fichiers « golden » (`API-06`). | Le tableau de bord, `xfer ctl` et la future application parlent le même vocabulaire. |
