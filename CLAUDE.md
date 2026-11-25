# medXfer : consignes pour l'IA

Ce fichier est lu au début de chaque session. Il résume la méthode du projet, mais **les documents de `docs/` font foi** : en cas d'écart, suis-les et signale l'écart.

Le développeur travaille seul et **décide**. L'IA propose, écrit, teste et mesure ; elle demande quand une décision lui revient (tables « Décisions » des plans, gates de la bible).

## Le projet

Transfert de fichiers chiffré sur le réseau local, sans serveur.
- **Le cœur, en Go** (`pkg/`, `cmd/`). Il est chargé par l'application comme bibliothèque C (`cmd/libmedxfer`, `dart:ffi`).
- **L'application, en Flutter** (`app/`, avec son propre `app/CLAUDE.md`), pour Android, Windows et Linux.
- **La ligne de commande `xfer`**, pour Termux et les machines sans écran.

## Documents de référence

| Document | Contenu |
|---|---|
| `docs/medxfer-bible-technique-v8.md` | Les exigences, chacune avec son identifiant (`CHAN-`, `ID-`, `PAIR-`, `DISC-`, `BLE-`, `XFER-`, `STO-`, `API-`, `WEB-`, `LIM-`, `DEV-`) et son statut (✅ 📏 ⏸ ❓) ; les règles R1 à R5 ; les gates G1 à G8 |
| `docs/medxfer-feuille-de-route-execution.md` | Les jalons J0 à J9 ; **les règles de travail (§5)** |
| `docs/medxfer-plan-j8.md` | Le jalon en cours : ses lots (WP-8.x), ses décisions, ses risques |
| `docs/medxfer-plan-*.md`, `docs/*report*.md` | Un plan au début de chaque jalon ou lot, un rapport à la fin |
| `docs/medxfer-g4-dossier-revue.md` | Le dossier de la revue cryptographique externe (G4) |

Avant de toucher un domaine, lis le chapitre de la bible qui le couvre et le lot du plan qui le demande.

## Carte du code

| Paquet | Rôle |
|---|---|
| `pkg/core` | Le cœur dans le processus de l'appelant : file d'événements bornée, schéma versionné (`SchemaVersion`, golden `testdata/schema-v5.json`) |
| `internal/cabi`, `cmd/libmedxfer` | La frontière C : 5 fonctions, règles B1 à B11 (bible §10.4) |
| `pkg/api` | Sessions, transferts, confiance, découverte, Web Share : les actions et événements du vocabulaire de contrôle |
| `pkg/securechan` | TLS 1.3 mutuel à empreinte épinglée, porte d'admission (`LIM-01`), liaison au canal |
| `pkg/identity` | Clé de l'appareil, liste de confiance (`trusted.json`) |
| `pkg/pairing` | QR (P1), PIN (P2), confiance mutuelle (P3), étiquettes (`DISC-11`), BLE (§6.6, `ble.go`) |
| `pkg/pake` | CPace. **Gelé jusqu'à la revue G4** (voir plus bas) |
| `pkg/transfer`, `pkg/protocol`, `pkg/session` | Protocole de transfert (offre, `DATA_JOIN`, chunks SHA-256, digest), trames bornées, canal de contrôle |
| `pkg/store` | Écriture côté receveur : `.part`, journal `MXST`, commit groupé, `os.Root` |
| `pkg/engine`, `pkg/manifest` | `SafeRelPath`, collisions, espace libre ; construction de l'offre |
| `pkg/discovery`, `pkg/netbind` | mDNS, balise de repli, balayage ; sockets liées au réseau choisi |
| `pkg/webshare` | Web Share v2 (chapitre 11) |
| `pkg/diag`, `pkg/bench` | Rapports par étape, injection de fautes (tag `diag`) ; échelle R1 à R6, `bench matrix` |
| `pkg/testkit`, `pkg/wsclient` | Deux cœurs ou démons pour les tests ; client du vocabulaire |
| `cmd/xfer` | La ligne de commande : `send`, `recv`, `pair`, `node`, `ctl`, `trust`, `name`, `share`, `bench`, `report`, `bundle`, `id` |

## Commandes

```sh
go build ./...
gofmt -l .                       # doit être vide (LF imposé par .gitattributes)
go vet ./... && go vet -tags diag ./...
go test ./...                    # le binaire de production
go test -tags diag ./...         # avec faultconn, faultfs, le chaos
go test -race -tags diag ./...   # Linux
GOARCH=386 CGO_ENABLED=0 go test ./...   # 32 bits, comme les téléphones armeabi-v7a
bash scripts/build-lib.sh --smoke        # la bibliothèque C, appelée par smoke.c
bash scripts/check-prod-binary.sh        # rien de diag, pprof ou testkit dans le binaire publié
```

- **Golden du schéma :** `MEDXFER_UPDATE_GOLDEN=1 go test ./pkg/core`, seulement pour un changement voulu du schéma.
- **Scénarios de `xfer ctl`** (`cmd/xfer/testdata/scenarios/*.jsonl`) : ils sont rejoués par le Go et par le faux cœur de l'application (`DEV-17`).
- **Chaos :** `MEDXFER_CHAOS_ITER=1000` en local, et `MEDXFER_CHAOS_SEED` rejoue une graine en échec.
- **La CI** (`.github/workflows/ci.yml`) lance tout cela sur Windows, Linux et macOS, plus l'application et le paquet Linux.

## Règles d'architecture (bible, chapitre 1)

- **R1 :** une couche ne connaît que l'interface de la couche du dessous.
- **R2 :** la sécurité ne dépend d'aucune optimisation, et aucune optimisation ne l'affaiblit.
- **R3 :** les modules optionnels (Web Share, diag) se retirent sans toucher au cœur.
- **R4 :** une valeur non prouvée est un paramètre, pas une constante.
- **R5 :** chaque lot livre quelque chose qui marche et se teste seul.

Une plateforme égale un adaptateur. Une décision (quel réseau, quelle bande, quel chemin) vit dans le Go ou le Dart, où elle se teste, et pas dans le Kotlin ou le C++.

## Flux de travail (feuille de route §5)

1. **Un lot, une branche, une PR.**
   - Branches : `feat/…`, `fix/…`, `docs/…`, `test/…`, `ci/…`.
   - Une PR reste **sous 600 lignes modifiées**, hors tests et golden. Au-delà, découpe le lot en plusieurs PR et propose le découpage avant d'écrire.
2. **Les commits** suivent la forme `type(portée) : WP-x.y, ce qui change`, en français, avec le WP quand il y en a un.
   - Types : `feat`, `fix`, `perf`, `test`, `docs`, `ci`, `build`, `refactor`.
   - Le corps, en puces, dit le pourquoi, la mesure et l'essai réel. **C'est là que va l'histoire**, pas dans le code.
3. **Pas de correction en passant.** Un constat hors du lot devient une issue, que tu signales.
4. **Une correction commence par un test qui échoue.** La PR montre ce test en échec sur l'ancien code, puis en succès. Un défaut non reproduit est dit tel quel (« préventif »), jamais présenté comme corrigé.
5. **La performance se mesure avant de se toucher.** Mesure avant et après, sur les mêmes appareils, et archive les chiffres dans `bench-results/`.
6. **Un `main` rouge arrête tout.** Un test instable n'est jamais relancé pour passer : il reçoit une issue `test-instable` et une quarantaine (`t.Skip("#NNN")`), puis il est stabilisé. Un vrai bug, lui, se corrige.
7. **La bible suit le code dans la même PR.** Une exigence n'est ✅ que si un test la vérifie ; cite ce test. Une décision changée met à jour la bible ou le plan.
8. **Les essais réels sont faits par le développeur** sur ses appareils. Écris-lui le protocole : étapes, quoi relever, ce qui ferait échouer. N'invente jamais un résultat.

**« Terminé »** veut dire : code fusionné, tests de l'exigence verts sur les trois systèmes, bible ou plan à jour, issue fermée avec le lien de la PR.

## Langues

- **En français :** les documents, plans, rapports, issues, messages de commit et descriptions de PR.
- **En anglais :** les identifiants, les commentaires du code, les messages de la ligne de commande et les journaux.
- **Textes de l'application :** `app/lib/l10n/app_fr.arb` et `app_en.arb`, toujours tous les deux.

## Commentaires dans le code

Un commentaire dit **ce qui est vrai maintenant** : l'invariant, la raison, le piège.
- Il tient en 1 à 5 lignes. Un identifiant (`STO-12`) ou un numéro d'issue (`#267`) remplace une longue explication.
- **Aucune histoire :** ni date, ni appareil, ni « essai réel du… », ni « avant, le code faisait… ». Tout cela va dans le commit, le rapport ou la bible.
- Il ne paraphrase pas le code.

À éviter :
```go
// The TECNO BF6 runs its hotspot on wlan0, its one Wi-Fi interface, which the names alone
// missed (real test of 2 November 2026). Before, only ap0 and swlan0 were looked at, and the
// Galaxy A57 ...
```
À préférer :
```go
// Some phones run their hotspot on their only Wi-Fi interface (wlan0): match by address, not by name.
```

Les commentaires existants ne se réécrivent pas en passant. On les raccourcit quand on touche la fonction, ou dans un lot prévu pour.

## Zones sensibles

- **`pkg/pake` est gelé jusqu'à G4** (audit, décision A3) : le code relu doit être celui du dossier. Toute modification demande l'accord du développeur et une mise à jour du dossier G4.
- **Revue de sécurité obligatoire** (`/security-review`) quand une PR touche `pkg/securechan`, `pkg/pairing`, `pkg/identity`, `pkg/webshare`, `pkg/engine/safepath.go` ou `pkg/store`. Les tests de la bible §14.1 qui s'y rapportent doivent passer.
- **Les secrets du chapitre 13** (PIN, jetons, clés, `ekm`, mot de passe du réseau créé, `K_AB`) ne vont jamais dans un journal, un événement ou une erreur.
- **Aucune suppression d'un fichier de l'utilisateur** (`STO-09` à `STO-14`). Un nouveau `os.Remove` ou `Root.Remove` ne vise que les fichiers que medXfer a créés lui-même.
- **Rien de dangereux dans le binaire publié :** l'injection de fautes et le profilage restent derrière le tag `diag` (`DEV-10`, `DEV-12`).

## Ce que l'IA ne fait pas sans demander

- Changer une décision écrite, l'état d'une gate, le format réseau, ou le schéma du cœur (`SchemaVersion` et son golden).
- Ajouter une dépendance, Go ou Dart.
- Toucher `pkg/pake`.
- Élargir le lot en cours.
- Fusionner, pousser sur `main`, publier une version.

## Avant de dire « fini »

Montre les sorties, pas un résumé :
- `gofmt -l .` vide ;
- `go vet ./...` ;
- `go test ./...`, et `-tags diag` si le chemin est touché ;
- pour l'application : `flutter analyze` et `flutter test` (voir `app/CLAUDE.md`).

Dis ce qui n'a pas été vérifié, et pourquoi.
