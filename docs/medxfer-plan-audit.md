# medXfer — Plan du lot « Audit » (avant J6b)

> **Issues :** #156 (ce lot), #157 (constats sur Web Share, critères de sortie de J7).
> **Source :** audit externe complet du 18 octobre 2026 (`audit_complet_medxfer_18_10.md`, transmis par l'utilisateur le 19 octobre) ; décisions de l'utilisateur du 19 octobre : retirer le point d'accès du cœur, traiter Web Share en J7.
> **Objectif :** corriger, avant que l'application Flutter ne s'appuie sur le cœur, les défauts confirmés de l'audit. Chaque point est **vérifié par un test qui le reproduit** avant d'être corrigé. Un point non reproduit est noté comme tel, pas « corrigé » à l'aveugle.
>
> **Critère de sortie :** chaque point de ce plan a un statut final (corrigé avec son test, non reproduit, reporté avec sa raison et son jalon) ; les deux failles critiques ont disparu ; la bible ne marque plus ✅ une exigence que le code viole.
>
> **Durée estimée :** 2 à 3 jours. Une PR par lot, CI verte sur les trois systèmes avant fusion.

## 0. Lecture de l'audit

L'audit est sérieux et globalement juste. Ses points les plus graves ont été vérifiés le 19 octobre dans le code de `main` (✅ ci-dessous). Quelques constats sont dépassés : il a été écrit le 18, avant les lots « Modèle de confiance » et « Noms des appareils ».

Statuts de vérification :
- ✅ confirmé dans le code ;
- ◐ confirmé, mais moins grave que décrit ;
- ✗ non fondé ou dépassé ;
- 🔎 à vérifier par un test au moment de la correction.

## 1. Décisions

| # | Décision | Raison |
|---|---|---|
| A1 | **Le cœur ne crée plus de point d'accès.** `pkg/hotspot`, `xfer send --create-network` et les actions `hotspot_start`, `hotspot_status`, `hotspot_stop`, `open_hotspot_settings` sont retirés ; schéma du cœur **v3**. Nouvelle exigence de la bible : le point d'accès est l'affaire de la plateforme (Android : `LocalOnlyHotspot`, à évaluer en J6b ; Windows : API système depuis un module natif), qui signale ensuite le réseau au cœur avec `set_network_info`. | La bible ne prévoyait pas de point d'accès : `pkg/hotspot` est un reste de l'ancienne conception, qui lance un script PowerShell. Le retirer fait disparaître les deux failles critiques (injection, blocage). Une plateforme = un adaptateur. |
| A2 | **Web Share est corrigé en J7, pas maintenant.** Tous les constats de l'audit sur `share.go` deviennent des critères de sortie de J7 (#157). Les statuts de la bible qui ne sont pas tenus repassent à 📏. | J7 réécrit Web Share selon le chapitre 11 ; corriger le code de transition serait du travail jeté. J7 passe avant la RC. En attendant : ne pas utiliser `xfer share` sur un réseau public. |
| A3 | **La cryptographie de `pkg/pake` ne change pas.** Les remarques de l'audit sur ce paquet (champ `finished` sans verrou, ISK gardée pour les tests, fonctions de l'ordre des messages compilées en production) vont au dossier G4, pour les relecteurs. Seule la variable inutilisée `errInvalidElement` est retirée. | Le code relu par G4 doit rester celui du dossier. |

## 2. Les lots

### Lot A-1 — Retrait du point d'accès du cœur · S · ✅

| Audit | Constat | Vérification | Action |
|---|---|---|---|
| Top 1 | Injection PowerShell par le SSID ou le mot de passe (`hotspot_windows.go`) | 🔎 (le script est bâti avec `Sprintf`) | Retrait (A1) |
| Top 2 | `Start()` tient `w.mu` et appelle `Stop()`, qui le reprend : blocage définitif au premier échec | ✅ | Retrait (A1) |
| §3.7 `handler.go` | `open_hotspot_settings` lance `cmd /c start` sans vérifier le système | ✅ | Retrait (A1) |

Aussi : bible (nouvelle exigence), schéma v3 et fichier « golden », tableau de bord (retrait des boutons), guide.

### Lot A-2 — Canal sécurisé et appairage · M · ✅

| Audit | Constat | Vérification | Action |
|---|---|---|---|
| Top 5a | `Gate.Admit` ne compte une adresse qu'après la poignée de main (`Charge`) : une seule adresse peut prendre les 16 places | ✅ | Compter aussi les poignées de main en cours par adresse |
| Top 5b | `recentLocked` ne supprime jamais une adresse dont la liste est vide | ✅ | `delete` quand la liste est vide |
| Top 6 | Une page `offer_items` vide est acceptée | ◐ (la session attend un message à chaque tour : le pair fait traîner, il ne fait pas tourner le processeur) | Refuser une page vide ; vérifier `Count` dès la première trame |
| Top 7 | `Channel.Send` efface le délai d'écriture posé par l'appelant | ✅ | Rétablir le délai précédent au lieu de l'effacer |
| §3.5 `securechan.go` | Les délais posés par les appelants remplacent `UnauthTimeout` | 🔎 | Avec Top 7 : le délai non authentifié reste une borne |
| Top 10 | Dans `Serve`, la négociation de confiance (jusqu'à 60 s) peut dépasser l'expiration du QR ou du PIN ; `awaitAccept` passe `context.Background()` à `negotiate` | ✅ (contexte) ; 🔎 (expiration) | Arrêter l'expiration une fois `pair_accept` envoyé ; passer le contexte de l'appelant |
| Top 11 | `PIN.show` après relâchement de `p.mu` : deux affichages peuvent arriver dans le désordre | 🔎 | Ordonner l'affichage (numéro de génération) |
| §3.5 `ondemand.go` | `answerPINWanted` ne vérifie pas `Live()` | 🔎 | Vérifier, sinon refus neutre |
| §3.6 `session.go` | `SetMaxControl` sans synchronisation | 🔎 | Atomique |

### Lot A-3 — Cœur, frontière C, moteur · M · ✅

| Audit | Constat | Vérification | Action |
|---|---|---|---|
| Top 12 | `cabi.Stop` relâche le verrou avant `c.Stop()` : un `Start` peut démarrer pendant l'arrêt ; `NextEvent`, `Stop`, `Stats` sans `recoverJSON` (règle B5) | 🔎 | Garder le verrou pendant l'arrêt ; `recoverJSON` partout |
| §3.3 `main.go` (libmedxfer) | `medxfer_next_event` avec un délai négatif : boucle à 100 % du processeur | 🔎 | Délai négatif = attente sans limite, documenté |
| §3.3 `smoke.c` | `argv[1]` non échappé dans le JSON ; `strstr` sans test de `NULL` | 🔎 | Échapper, tester |
| §3.7 `core.go` | Le limiteur de 100 ms jette l'événement final à 100 % | ✅ | Laisser passer l'événement final |
| Top 14 | `reconnects` jamais remis à zéro : après 16 micro-coupures sur un long transfert, le flux s'arrête | 🔎 | Remettre à zéro après des morceaux reçus |
| §3.6 `store.go` | `verify()` alloue un morceau entier avant de savoir s'il y a quelque chose à vérifier ; `Suspend()` lit `f.journal` sans `commitMu` | 🔎 | Retour anticipé ; verrou |
| §3.6 `manifest.go` | Deux chemins de même nom (`/a/doc.pdf`, `/b/doc.pdf`) font échouer l'offre (`duplicate path`) | 🔎 | Nommer le second (`doc (1).pdf`), comme à la réception |
| Top 9 | `getStatus()` lance `arp -a`, sans délai, sous `s.mu.RLock()` | 🔎 | Délai et cache pour `arp -a` ; rien de lent sous le verrou |
| §3.7 `server.go` | `sessionGraceTimer` non arrêté dans `Stop()` ; serveur HTTP sans `ReadHeaderTimeout` | ✅ (`ReadHeaderTimeout`) ; 🔎 (minuteur) | Arrêter le minuteur ; délais du serveur (la partie Web Share est J7) |
| §3.7 `trust.go` | Remplacer la session active n'arrête pas ses transferts | 🔎 | `dropTransfers` |
| §3.7 `discoverable.go` | `instance()` lit `s.discSrv` sans verrou | 🔎 | Verrou |
| Top 15b | `DiscoveryServer.Start` ferme son écoute TCP 19998 de façon asynchrone : désactiver puis réactiver la visibilité aussitôt échoue | 🔎 | Fermeture synchrone, ou attente |
| Top 8 | `xfer node` : `activeSession` partagée sans verrou entre la goroutine d'acceptation et la boucle ; `disconnectChan` ne dit pas quelle session s'est fermée | 🔎 | Une seule goroutine propriétaire ; message de fin portant sa session |
| §3.4 `main.go` | `selectOneShotSender` boucle sans fin si l'entrée standard est fermée | 🔎 | Fin sur EOF |
| §3.5 `trust.go` (identity) | `ByAddress` peut rendre au hasard l'un de deux appareils qui ont eu la même adresse (DHCP) : fausse alerte `REFUSED` possible (ID-05) | 🔎 | Une adresse n'appartient qu'au dernier vu ; message qui évoque un changement d'adresse |

### Lot A-4 — Le reste · S

| Audit | Constat | Vérification | Action |
|---|---|---|---|
| Top 13 | `redact` avale la fin de ligne après un chemin Windows ; remplace le nom d'utilisateur sans frontière de mot | 🔎 | Expressions plus étroites, frontières de mot |
| Top 15a | Couche 2A de `ResolvePairingCode` morte depuis la nouvelle balise | 🔎 | Retirer ou adapter |
| §3.6 `collision.go` | `.gitignore` devient `" (1).gitignore"` | 🔎 | Fichiers cachés : `.gitignore (1)` |
| §3.6 `safepath.go` | Caractère DEL (0x7f) accepté | 🔎 | Refuser |
| §3.6 `manifest.go` | `GenerateFileIDFromInfo` (MD5) inutilisé | 🔎 | Retirer |
| §3.6 `diagnostics.go` | `BenchmarkDisk` chronomètre aussi le tirage aléatoire | 🔎 | Sortir de la boucle chronométrée |
| §3.7 `beacon.go` | Goroutine qui attend `ctx` après la fin normale de `Browse` | 🔎 | Arrêt à la fin |
| §3.7 `self.go` | Noms propres globaux au processus | 🔎 | Documenter ou rattacher au serveur |
| §3.7 `qr.go` | QR Wi-Fi sans échappement de `;` `:` `\` `,` | 🔎 | Échapper |
| §3.7 `handler.go` | `handleSend` coupe une IPv6 au premier `:` ; `handleTestNetwork` ne vide pas le canal des réponses | 🔎 | `net.SplitHostPort` ; vider |
| §3.7 `ui/progress.go` | Pourcentage non borné | 🔎 | Borner à [0, 100] |
| §3.4 `offer.go` (CLI) | `OnItem` et `OnDiskFull` écrivent sans `e.mu` | 🔎 | Verrou |
| §3.4 `oneshot.go` | « Waiting for receiver » peut s'afficher avant le QR | 🔎 | Prêt une fois QR et PIN affichés |
| §3.4 `bench.go` | `parseSize` cite la taille sans son unité dans l'erreur | 🔎 | Citer l'entrée |
| §3.4 tests | `bundle_test.go` et `ctl.go` ne remettent pas le dossier de configuration | 🔎 | `t.Cleanup` ; remise à l'état |
| §3.4 `main.go` | Messages d'erreur en français dans une interface en anglais | 🔎 | Anglais |
| §3.5 `identity` | Renommage sous Windows sans nouvelles tentatives ; certificat non revérifié si l'horloge recule | 🔎 | Tentatives comme `pkg/store` ; `NotBefore` |
| §3.5 `uri.go` | Une IPv6 est acceptée, mais la connexion est forcée en IPv4 | 🔎 | Refuser l'IPv6 dans l'URI (IPv4 seulement, comme le reste) |
| §3.8 `netbench.go`, `tracker.go` | Délai non renouvelé entre les phases ; texte de diagnostic en français | 🔎 | Renouveler ; anglais |
| §3.1 `build.bat` | Parenthèse en trop après `exit /b 1` | ✅ (inoffensive) | Retirer |
| §3.5 `group.go` | `errInvalidElement` inutilisé | ✅ | Retirer (A3) |

### Reportés, avec leur jalon

| Audit | Constat | Jalon |
|---|---|---|
| Top 3 | `GET /share?pin=` : comparaison `==`, sans verrouillage ni comptage, cookie posé | **J7** |
| Top 4 | `handleShareUploadChunk` : `O_TRUNC` sans collision, fichiers hors du ticket, ticket supprimé au premier fichier | **J7** (`WEB-07`) |
| §3.7 `share.go` | XSS via `innerHTML` dans le portail | **J7** (`WEB-05`, CSP) |
| §3.7 `server.go` | `ipLockouts` et `lastAuthFail` jamais purgés | **J7** |
| §3.7 `transfers.go` | `FileBytes` porte les totaux du lot ; `Bottleneck` jamais rempli | **J6b** (selon les besoins de l'application) |
| §3.6 `sender.go` | Les empreintes des éléments restent en mémoire jusqu'à la fin | **J8** (mesure d'abord) |
| §3.6 `offer.go` | `Split` sérialise chaque élément pour mesurer sa taille | **J8** (mesure d'abord) |
| §3.1 `README.md` | Une seule ligne | **J8** (déjà prévu) |
| §3.5 `pkg/pake` | `finished` sans verrou, ISK gardée, fonctions de test compilées | **G4** (A3) |

### Non fondés ou dépassés

| Audit | Constat | Pourquoi |
|---|---|---|
| §3.1 `.gitignore` | « `xfer.exe`, `deploy.bat`, `bin/` présents malgré `.gitignore` » | Ils ne sont pas suivis par git : seulement présents sur la machine. |
| §3.1 `deploy.bat` | Accent grave ligne 33, adresses incohérentes | Fichier local, hors dépôt ; corrigé par l'utilisateur. |
| §3.4 `nodetrust.go` | `trustedAt` reconnaît par l'adresse | Remplacé par la reconnaissance par étiquette (#154). |
| §3.6 `speed.go` | « fenêtre de 3 s » | 2 s au receveur, 5 s à l'émetteur (#140). |

## 3. Bible

- `WEB-02` (pas de cookie), `WEB-03` (comparaison en temps constant, 5 essais) et `WEB-08` (`ReadHeaderTimeout`) étaient marquées ✅, alors que le code de transition ne les tient pas : elles repassent à 📏 J7 (fait avec ce plan).
- Nouvelle exigence pour le point d'accès : `DISC-12` (A1, fait avec A-1).
- Le dossier G4 reçoit les remarques sur `pkg/pake` (A3).

## 4. Hors périmètre

- La réécriture de Web Share : J7.
- Les optimisations de performance que l'audit suggère sans mesure : J8, après mesure.
