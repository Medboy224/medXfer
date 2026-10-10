# medXfer — Plan détaillé du jalon J2 (outils de base)

> Source : feuille de route §3 « J2 », bible chapitre 17 (`DEV-01` à `DEV-19`).
> **Objectif :** pouvoir écrire, lire et rejouer des scénarios sans écrire de code d'interface.
> **Durée estimée :** 4 à 5 jours. Une PR par lot, CI verte sur les trois systèmes avant fusion (règle du main vert, §5).

## 0. État de départ (constaté sur `main` après J1)

| Constat | Conséquence pour J2 |
|---|---|
| `TransferSessionTracker` et `TransferSummaryReport` vivent dans `pkg/api` (`telemetry.go`, `models.go`) et dépendent de `engine.TransferStats`. | WP-2.1 les déplace dans `pkg/diag`. |
| Le rapport contient `transfer_name`, c'est-à-dire le **nom du fichier ou du dossier** → contraire à `DEV-05`. Pas de `schema_version`. | WP-2.2 : champ retiré par défaut, version de schéma ajoutée. Le tableau de bord (`web.go`, `renderTransferSummary`) n'utilise que `formatted_report` et les champs numériques. |
| `pkg/api/api_test.go` : 31 tests, **22 connexions WebSocket** ouvertes à la main ; aides de J1 dans `testhelpers_test.go`. | WP-2.3 : `pkg/testkit` remplace ces aides. |
| `engine.BenchmarkDisk` existe, mais n'est accessible que par le tableau de bord. `RunNetworkBurstServer/Client` existent (TCP en clair). | WP-2.6 : sous-commandes CLI qui réutilisent ce code. |
| Aucune étiquette de build `diag` ni `devui`. Seule `hardware` existe (J0). | WP-2.5 et WP-2.8 les créent ; la CI vérifie leur absence en production. |
| Issues rattachées à J2 : #22 et #23 (tests en quarantaine : le fichier finit trop vite), #26 (compteurs de tampons derrière une étiquette). | #26 → WP-2.5 ; #22 et #23 → WP-2.5 + WP-2.3 (un débit bridé par `faultconn` rend ces tests déterministes, sans gros fichier : voir la mémoire « disque presque plein »). |

## 1. Ordre d'exécution et dépendances

```
WP-2.1 diag ──► WP-2.2 rapport ──► WP-2.7 bundle
WP-2.5 faultconn/faultfs ──► WP-2.3 testkit ──► WP-2.4 ctl
WP-2.6 bench (indépendant)        WP-2.8 dev-ui (en dernier)
```

Ordre proposé : **2.1 → 2.2 → 2.5 → 2.3 → 2.4 → 2.6 → 2.7 → 2.8**.

## 2. Les lots

### WP-2.1 — `pkg/diag` (`DEV-01`) · S
- Déplacer `TransferSessionTracker`, `TransferSummaryReport`, `TransferPhaseSample` et `FormatBytes` dans `pkg/diag`.
- Dans `pkg/api` : alias de types (`type TransferSessionTracker = diag.TransferSessionTracker`, etc.), pour ne rien casser.
- `pkg/diag` importe `engine` (pour `TransferStats`) ; **`engine` n'importe jamais `diag`**.
- `telemetry_test.go` est déplacé avec le code.
- **Test `DEV-01`** : un test vérifie avec `go list -deps` qu'aucun paquet de `pkg/engine`, `pkg/protocol` ou `pkg/session` ne dépend de `pkg/diag`.

### WP-2.2 — Rapport JSON versionné, `xfer report show|compare` (`DEV-05`, `DEV-06`) · M
- `diag.Report` : `schema_version: 1`, `generated_at`, `app_version`, `os`, `arch`, durée, octets, fichiers, débits moyen / pic / minimum, étape limitante, phases, retries.
- **Par défaut, ni nom de fichier, ni IP, ni nom d'hôte, ni empreinte.** `ReportOptions{IncludeNames bool}` permet de les ajouter explicitement.
- Le texte « copiable » actuel devient `diag.FormatText(report)`, une vue du même rapport.
- Le daemon écrit le rapport dans `<config>/reports/<horodatage>.json` et n'en garde que les 20 derniers.
- `xfer report show <f.json>` affiche le texte. `xfer report compare a.json b.json` affiche les écarts de débit, d'étape limitante et de retries, en % et en valeur absolue.
- Les deux commandes acceptent `--json`.
- **Tests :**
  - un rapport par défaut ne contient pas le nom du fichier transféré ni `127.0.0.1` (recherche dans le JSON sérialisé) ;
  - aller-retour JSON ;
  - `compare` sur deux rapports fabriqués à la main.

### WP-2.5 — `faultconn` et `faultfs` sous l'étiquette `diag` (`DEV-10`) · M
- `pkg/diag/faultconn` (`//go:build diag`) enveloppe une `net.Conn` avec un plan déterministe à graine fixe : latence, gigue, débit maximal (seau à jetons), coupure après N octets, RST.
- `pkg/diag/faultfs` (`//go:build diag`) fournit trois défauts :
  - `ENOSPC` après N octets ;
  - écriture déchirée ;
  - `fsync` qui ment.
- `faultfs` passe par une petite interface d'écriture côté `engine`. Si elle n'existe pas encore proprement, cette interface est créée ici, sans changer le comportement.
- Branchement dans `engine` par un point d'injection qui n'existe qu'avec l'étiquette :
  - `dialHook` ;
  - `fileOpenHook` dans un fichier `hooks_diag.go` ;
  - le fichier `hooks_prod.go` correspondant contient des fonctions identité.
- **#26** : `buffersInUse` et `buffersPeak` passent aussi sous `diag`.
- **CI :**
  - nouvelle étape `go test -tags diag ./...` sur Ubuntu ;
  - nouvelle étape « binaire de production propre » :
    `go list -deps ./cmd/xfer | grep -E 'faultconn|faultfs|net/http/pprof|devui'` doit être vide.

### WP-2.3 — `pkg/testkit` v0 (`DEV-15`) · L
- Fonctions :
  - `NewPair(t, opts...)` : deux `DaemonServer` sur des ports aléatoires, dossiers temporaires, jeton de contrôle, appairage déjà fait ;
  - `pair.A.Send(...)` et `pair.B.Accept(...)` ;
  - `WaitEvent(t, node, "transfer_complete", timeout)` ;
  - `AssertTreesEqual(t, a, b)` ;
  - `Throttle(bytesPerSec)` : option qui passe par `faultconn`, donc tests sous `-tags diag`.
- Bâti sur le WebSocket durci de J1. L'API publique est pensée pour être ré-implémentée en appels directs en J6a **sans toucher aux tests**.
- Portage des 22 connexions d'`api_test.go` (et de `control_guard_test.go` là où le test ne porte pas *sur* la garde elle-même). L'aide `controlRequest` reste pour les tests HTTP purs.
- **Sortie des quarantaines :**
  - #22 (`TestBatchQueueDynamicAdvanceOnPause`) et #23 (`TestLiveWorkerRejectsDifferentFileOnSenderRestart`) sont réécrits avec `Throttle`, puis réactivés ;
  - chacun doit passer 50 fois de suite (`-count=50`) en local et 3 fois en CI avant de fermer l'issue.
- **Critère :** plus aucun `websocket.DefaultDialer.Dial` dans `pkg/api/*_test.go`, hors tests de la garde de contrôle.

### WP-2.4 — `xfer ctl` v0 (`DEV-16`, `DEV-17`) · M
- `xfer ctl` démarre un daemon intégré, avec :
  - un port aléatoire sur 127.0.0.1 ;
  - un jeton aléatoire ;
  - un dossier de configuration temporaire, sauf avec `--config`.
- Il lit des requêtes NDJSON sur l'entrée standard (même format que `RequestMessage`) et écrit les événements en NDJSON sur la sortie.
- `--script scenario.jsonl` permet de rejouer un scénario. On y trouve :
  - des lignes `{"expect": {"event": "...", ...}}` pour l'attente d'événements ;
  - `{"sleep_ms": N}` pour les pauses ;
  - des champs variables masqués (`"*"`).
- Code de sortie ≠ 0 si un `expect` échoue ou dépasse son délai.
- Un ou deux scénarios « golden » sont fournis dans `testdata/scenarios/` : statut, puis configuration, puis disque.
- **Test :** `go test` rejoue ces scénarios avec `xfer ctl` → critère de sortie de J2.

### WP-2.6 — `xfer bench disk` et `xfer bench net` (`DEV-07`, version de base) · S
- `xfer bench disk [--dir D] [--size 64M] [--json]` : réutilise `engine.BenchmarkDisk` avec des données aléatoires (`DEV-09`) et nettoie toujours ses fichiers.
- **Taille par défaut modeste**, au plus 64 Mio : le disque du PC est presque plein.
- `xfer bench net --listen` d'un côté, `xfer bench net --peer host:port [--size 32M] [--json]` de l'autre : débit et RTT, en réutilisant `RunNetworkBurst*`.
- Message explicite : « non chiffré, à refaire après J3 (marche R2) ».
- **Tests :** sortie `--json` décodable, et nettoyage des fichiers vérifié.

### WP-2.7 — `xfer bundle` et journaux masqués (`DEV-11`) · M
- `pkg/diag/logring` : tampon circulaire de 2 Mio, branché sur `log`. Un masquage s'applique à l'écriture :
  - PIN de 6 chiffres après « PIN » ;
  - jetons hexadécimaux de 32 caractères ou plus ;
  - `Authorization: …` ;
  - `token.<hex>` ;
  - chemins personnels (`C:\Users\<nom>`, `/home/<nom>`).
- `xfer bundle [-o fichier.zip]` produit une archive qui contient :
  - les derniers rapports (WP-2.2) ;
  - les journaux ;
  - la version, l'OS et l'architecture ;
  - la configuration sans secrets ni chemins.
- **Test « aucun secret » :**
  - on lance un daemon avec un PIN et un jeton connus, puis on fait un transfert ;
  - le bundle est décompressé et comparé à une liste de motifs interdits : PIN, jeton, nom de fichier, nom d'utilisateur.

### WP-2.8 — `xfer dev-ui` sous l'étiquette `devui` (`DEV-19`) · M
- Le tableau de bord (`IndexHTML`, `/api/browse`, `/api/upload`, `/api/fs/*`) passe sous `//go:build devui`.
- La commande `xfer dev-ui` n'existe qu'avec cette étiquette.
- Sans l'étiquette, `xfer daemon` garde :
  - `/health` ;
  - `/ws` et `/status` (contrôlés par jeton) ;
  - le Web Share optionnel.
- `build.bat` et `deploy.bat` : les builds de développement ajoutent `-tags devui`. Une cible « release » sans étiquette est ajoutée.
- La CI vérifie que `devui` est absent du binaire de production (même étape que WP-2.5).
- **Point à valider avant ce lot :** aujourd'hui, vous utilisez le tableau de bord au quotidien. Avec ce lot, il ne reste disponible que dans les binaires compilés avec `-tags devui`, ce qui est le cas des scripts de développement. C'est l'exigence de la bible, mais elle change vos habitudes : nous confirmerons au moment d'attaquer le lot.

## 3. Critère de sortie de J2
1. `go test ./...` et `go test -tags diag ./...` passent, sans aucun ancien assistant WebSocket dupliqué dans les tests.
2. Un scénario NDJSON enregistré se rejoue avec `xfer ctl` (test automatisé).
3. La CI vérifie l'absence de `faultconn`, `faultfs`, `pprof` et `devui` dans le binaire de production.
4. #22, #23 et #26 sont fermées, et plus aucun `t.Skip` de quarantaine ne vise un test de J2.
5. Le rapport par défaut ne contient ni nom de fichier ni IP (test).

## 4. Hors périmètre (renvoyé plus loin)
- `bench ladder`, `bench matrix`, `bench transfer` et l'offre `kind: "bench"` (`DEV-07` complet, `DEV-08`) → J4a / J8.
- Mesures par étape et étape limitante (`DEV-03`, `DEV-04`) → J4a.
- `pprof` et `MEDXFER_TRACE` (`DEV-12`) → J8, mais la vérification CI est posée dès WP-2.5.
- Mode développeur et diagnostic à distance (`DEV-13`, `DEV-14`) → J6a.
- Prototype G2a (piste P) : créneau prévu après J1. À caser pendant les attentes de CI de J2 ou juste après J2, avant J3.
