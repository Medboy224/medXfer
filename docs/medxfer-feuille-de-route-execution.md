# medXfer — Feuille de route d'exécution (prête à coder)

**Statut :** remplace l'*ordre* du §15 de la bible v8. Le *contenu* des phases (ce qui est construit, avec quelles exigences) reste celui de la bible ; ce document fixe **dans quel ordre on le construit**, en lots (« WP ») assez petits pour être faits, testés et fusionnés un par un.
**Références :** [bible v8](medxfer-bible-technique-v8.md) · [plan de la phase 0](medxfer-plan-phase-0.md) · [plan du prototype G2a](medxfer-plan-prototype-g2a.md).
**Conventions :** *Jalon* = `J0` à `J9`. *Phase* = le nom thématique de la bible (0, 0b, 1, 2a…). *Lot* = `WP-<jalon>.<n>`.

---

## 1. Ce qui change par rapport au §15 de la bible, et pourquoi

| # | Changement | Raison |
|---|---|---|
| 1 | **Nouveau jalon J0 « Mise en route »** (CI, état de référence, version de Go) avant tout correctif. | Sans intégration continue ni état de référence, on ne sait pas si une modification casse quelque chose. La CI donne aussi la couverture macOS sans Mac. |
| 2 | **Les phases 1 et 2a sont fusionnées en un seul jalon « Confiance » (J3).** | Un canal à empreintes épinglées est inutilisable tant qu'aucun appairage n'enregistre ces empreintes. Les séparer obligerait à écrire deux fois un mécanisme provisoire. |
| 3 | **Le port unique et `DATA_JOIN` (`CHAN-01`, `CHAN-06`) passent de la phase 1 à la phase 3 (J4a).** En J3, les connexions de données gardent un port par transfert mais passent en **TLS mutuel à empreinte épinglée**, et le cœur refuse toute connexion de données dont l'empreinte n'est pas celle du pair du transfert. | Le port unique oblige à changer l'API du moteur (`Sender` n'écoute plus lui-même). C'est un gros changement de structure, indépendant du chiffrement. Le chiffrement et l'authentification des données (fautes 1 et 3) sont obtenus tout de suite sans lui. |
| 4 | **L'appairage par PIN (phase 2b) passe après le transfert v1.** | Le QR suffit pour livrer un cœur sûr. Le PIN dépend de la gate G1 (choix du PAKE) et d'une revue externe ; son délai ne doit pas bloquer l'intégrité et la durabilité (J4), plus utiles à l'utilisateur. La veille sur G1 démarre en parallèle dès J3. |
| 5 | **L'outillage de base (0b) vient juste après la phase 0**, avant « Confiance ». | `testkit` et `xfer ctl` raccourcissent tous les tests des jalons suivants. |
| 6 | **Le prototype G2a est une piste parallèle à créneau fixe** (après J1), pas un travail intercalé chaque jour. | Un prototype à plafond de 8 jours perd son sens s'il est découpé en heures éparses. |
| 7 | **La phase 3 est coupée en deux** : J4a (protocole) et J4b (stockage). | C'est le plus gros chantier. Chaque moitié est livrable seule. |
| 8 | **La phase 4a est coupée en deux** : J6a (API du cœur en-processus, mDNS) et J6b (application Flutter MVP). | Le cœur peut être prêt et testé en ligne de commande avant qu'une interface existe. |
| 9 | **Les détails des jalons lointains ne sont pas écrits maintenant.** Le plan détaillé d'un jalon est produit à son entrée. | Un plan détaillé de J6 écrit aujourd'hui serait périmé après J3 (le vocabulaire des messages aura changé). |

### 1.1 Correspondance anciennes phases → jalons
| Ancienne phase | Jalon |
|---|---|
| (nouveau) | **J0** Mise en route |
| 0 | **J1** Sécurité immédiate |
| 0b | **J2** Outils de base |
| (prototype G2a) | **P** Piste parallèle |
| 1 + 2a | **J3** Confiance |
| 3 | **J4a** Protocole + **J4b** Stockage |
| 2b | **J5** Appairage par PIN |
| 4a | **J6a** Cœur en-processus + mDNS · **J6b** Application MVP |
| 5 | **J7** Web Share v2 |
| 6 | **J8** Mesure, optimisation, finitions v1 |
| 4b | **J9** macOS et iOS (dépend de la gate G8) |

### 1.2 Graphe de dépendances
```
J0 ─▶ J1 ─▶ J2 ─▶ J3 ─▶ J4a ─▶ J4b ─┬─▶ J5 ─┐
       │            │                │       ├─▶ J8 ─▶ (v1 Windows/Linux/Android)
       └─▶ P (G2a) ─┴───────────────▶ J6a ─▶ J6b ─▶ J7 ─┘
                                      J9 (macOS/iOS) : après G8, en parallèle de J8
Veille G1 (PAKE) : démarre pendant J3, alimente J5
```

---

## 2. Pistes de travail

| Piste | Contenu | Jalons |
|---|---|---|
| **C — Cœur** | Sécurité, protocole, stockage. C'est le chemin critique. | J1, J3, J4, J5 |
| **O — Outils** | CI, diagnostics, tests, benchmarks. | J0, J2, puis en continu |
| **P — Plateformes** | Prototype G2a, puis application Flutter, mDNS, empaquetage. | P, J6, J9 |
| **V — Veille** | Choix du PAKE (G1), plan de revue externe (G4), contraintes Android/iOS. | Lecture et décisions écrites, pas de code |

Une personne seule suit l'ordre des jalons ; la piste P n'intervient que dans son créneau (§3, P). Avec une deuxième personne : C pour l'une, O et P pour l'autre à partir de J2.

---

## 3. Les jalons

Les durées sont des **ordres de grandeur pour une personne à temps plein qui connaît le code** ; elles sont à réestimer à la fin de J1 d'après la vitesse réelle. Hypothèse : une semaine = 5 jours.

### J0 — Mise en route · 1 à 1,5 jour
**Objectif :** un dépôt où chaque changement est compilé, vérifié et testé sur trois systèmes, avec un état de référence connu.

| Lot | Tâche | Détail |
|---|---|---|
| WP-0.1 | Branche et version de Go | `git checkout -b chore/j0-setup` ; `go.mod` passe à `go 1.24` ; `go mod tidy`. **Constat :** `go.mod` déclare `gorilla/websocket` en `// indirect` alors que `pkg/api` l'importe directement ; `go mod tidy` corrige cela. |
| WP-0.2 | Isoler le test matériel | Déplacer `TestHotspotWebSocketCommands` dans `pkg/api/hotspot_hw_test.go` avec l'étiquette `//go:build windows && hardware`. Ce test démarre un vrai Wi-Fi Direct sous Windows et ne peut pas réussir ailleurs (le contrôleur non-Windows renvoie « Windows-only »). |
| WP-0.3 | Intégration continue | Fichier `.github/workflows/ci.yml` (ci-dessous). |
| WP-0.4 | État de référence | Lancer la CI et `go test` en local ; noter chaque échec préexistant dans une issue ; ne rien « corriger en passant ». |
| WP-0.5 | Documents dans le dépôt | Créer `docs/` ; y placer la bible v8, les trois plans et ce document ; déplacer la v7 dans `docs/archive/`. Le `README.md` reste vide (volontaire). |
| WP-0.6 | Suivi | Créer les jalons GitHub `J0` à `J9` et une issue par lot du jalon courant (modèle de titre : `[J1][WP-1.2] SafeRelPath et fin des suppressions`). |

**Workflow de CI à coller dans `.github/workflows/ci.yml`** (versions des actions à vérifier au moment de la mise en place) :
```yaml
name: ci
on:
  push:
    branches: [main]
  pull_request:

jobs:
  test:
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, windows-latest, macos-latest]
    runs-on: ${{ matrix.os }}
    # Temporaire : le temps de connaître l'état de référence sur Windows et macOS.
    # À supprimer à la fin de WP-0.4 quand les trois systèmes sont verts.
    continue-on-error: ${{ matrix.os != 'ubuntu-latest' }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go build ./...
      - run: go vet ./...
      - run: go test ./pkg/...
      - if: matrix.os == 'ubuntu-latest'
        run: go test -race ./pkg/...
```
**Critère de sortie :** la CI s'exécute sur les trois systèmes ; la liste des échecs préexistants est publiée ; `docs/` est en place.

---

### J1 — Sécurité immédiate (ancienne phase 0) · ≈ 5,5 jours
Le détail complet de chaque tâche est dans [medxfer-plan-phase-0.md](medxfer-plan-phase-0.md). Ordre d'exécution :

| Lot | PR | Contenu | Taille |
|---|---|---|---|
| WP-1.1 | PR 0 | **`/status` refusé hors loopback** (faille 9). À faire dès que la CI est verte, le jour même. | S |
| WP-1.2 | PR 2 | Tâche C : `SafeRelPath`, fin des suppressions par un pair. | M |
| WP-1.3 | PR 3 | Tâche D : `fsync` avant la progression, fenêtre bornée (tas-minimum), métadonnées validées, taille de chunk bornée. | L |
| WP-1.4 | PR 4 | Tâche B : PIN en `crypto/rand`, plus de secret dans les journaux, comparaisons en temps constant, en-têtes. | S |
| WP-1.5 | PR 5 | Tâche A : jeton, `Origin`, `Host`, tableau de bord, migration des 23 connexions de test. | L |

(La « PR 1 » du plan de la phase 0 — version de Go, étiquette `hardware`, état de référence — est absorbée par J0.)
**Critère de sortie :** tous les tests du §14.1 de la bible qui concernent ces tâches sont verts ; les trois `curl` du plan sont conformes depuis un autre appareil ; la CI est verte sur les trois systèmes.

---

### J2 — Outils de base (ancienne phase 0b) · ≈ 4 à 5 jours
**Objectif :** pouvoir écrire, lire et rejouer des scénarios sans écrire de code d'interface.
Les lots sont indépendants entre eux sauf mention.

| Lot | Contenu | Remarques |
|---|---|---|
| WP-2.1 | `pkg/diag` : déplacer `TransferSessionTracker` et le rapport depuis `pkg/api/telemetry.go` (`DEV-01`) | Alias de types dans `pkg/api` pour ne rien casser ; les tests suivent. |
| WP-2.2 | Rapport JSON versionné et `xfer report show` / `compare` (`DEV-05`, `DEV-06`) | Ne contient ni noms de fichiers ni IP par défaut. |
| WP-2.3 | `pkg/testkit` v0 : `NewPair`, `WaitEvent`, `AssertTreesEqual`, bâtis sur le WebSocket durci de J1 (`DEV-15`) | Remplace les aides de test de J1 ; sera ré-implémenté en appels directs en J6a sans changer les tests. |
| WP-2.4 | `xfer ctl` v0 : démarre un daemon intégré sur un port aléatoire avec jeton et relaie en NDJSON (`DEV-16`) | Version directe (sans WebSocket) en J6a. |
| WP-2.5 | `faultconn` et `faultfs` sous l'étiquette `diag` (`DEV-10`) | Servira aux tests de chaos de J4. |
| WP-2.6 | `xfer bench disk` et `xfer bench net` (`DEV-07`, version de base) | Le réseau n'est pas encore chiffré : la marche R2 viendra avec J3. |
| WP-2.7 | `xfer bundle` et tampon de journaux avec masquage (`DEV-11`) + test « aucun secret dans les journaux » | |
| WP-2.8 | `xfer dev-ui` : étiquette `devui`, absent des binaires publiés (`DEV-19`) + test de CI qui le vérifie | |

**Critère de sortie :** `go test ./...` passe sans aucun ancien assistant de test WebSocket dupliqué ; un scénario NDJSON enregistré se rejoue avec `xfer ctl` ; la CI vérifie l'absence de `faultconn`, `pprof` et `devui` dans le binaire de production.

---

### P — Prototype G2a (piste parallèle) · 5 à 8 jours, créneau fixe après J1
Détail complet dans [medxfer-plan-prototype-g2a.md](medxfer-plan-prototype-g2a.md). **Créneau :** juste après J1, avant J3, en une seule séquence. Raison : son résultat conditionne J6, pas J3 ; mais il vaut mieux le connaître avant d'écrire l'API du cœur. **L'étape S0 (installation des outils) peut se faire pendant les attentes de la CI de J0 et J1.**
**Critère de sortie :** rapport `g2a-report.md` ; gate G2a fermée ou plan B décidé.

---

### J3 — Confiance (anciennes phases 1 et 2a) · ≈ 10 à 14 jours
**Objectif :** plus aucun octet en clair, plus de connexion sans identité vérifiée, appairage par QR. Bible : chapitres 4, 6.1 et 7.

| Lot | Contenu | Exigences |
|---|---|---|
| WP-3.1 | `pkg/identity` : clé P-256 persistante, certificat, empreinte (SPKI), magasin de confiance en écriture atomique | `ID-01` à `ID-06` |
| WP-3.2 | `pkg/securechan` : configuration TLS 1.3 + mTLS, `VerifyConnection` à épinglage, ALPN `medxfer/1`, tickets désactivés, états `UNAUTH`/`AUTH`. **Suppression** de `UpgradeToTLSIfClientHello` et du repli de `DialTLSPeer`. | `CHAN-02` à `CHAN-05`, `CHAN-07` |
| WP-3.3 | **Confiance provisoire en ligne de commande** : à la première connexion, afficher l'empreinte courte du pair et demander confirmation (comme SSH, jamais silencieux) ; refuser tout changement d'empreinte d'un appareil connu. Disparaît du chemin normal avec WP-3.5. | `ID-05` |
| WP-3.4 | **Données en TLS mutuel épinglé**, ports par transfert conservés : `Sender` et `Receiver` reçoivent le listener / le dialer sécurisé ; le listener de données refuse toute connexion dont l'empreinte n'est pas celle du pair du transfert. | Objectif de `CHAN-06` sans `DATA_JOIN` |
| WP-3.5 | Appairage QR (P1) : secret 128 bits à usage unique, URI `medxfer://v1/pair?...`, TTL 120 s, commandes `xfer pair --show` et `xfer pair <uri>` | `PAIR-08`, §6.1 |
| WP-3.6 | Retrait de l'ancien code d'appairage `XXX-YYY` du chemin d'authentification ; limites de connexions non authentifiées | `LIM-01` à `LIM-03` |
| WP-3.7 | Tests de sécurité : repli interdit, MITM par proxy, connexion de données non autorisée, changement d'empreinte | §14.1 |

**Veille G1 pendant J3 (2 à 3 jours cumulés, sans code de production) :** comparer SPAKE2 (RFC 9382), CPace et `schollz/pake` d'après les critères de la gate ; écrire la décision recommandée.
**Critère de sortie :** une capture réseau d'un transfert complet ne montre aucun contenu lisible ; les tests MITM et de repli passent ; deux appareils s'appairent par QR puis transfèrent.

---

### J4a — Protocole de transfert v1 · ≈ 6 à 9 jours
Bible chapitre 8. **Décision G6 (SHA-256 ou BLAKE3) prise en tête de jalon**, par un petit benchmark de hachage sur le téléphone.

| Lot | Contenu |
|---|---|
| WP-4.1 | Trames bornées pour le contrôle (`CONTROL`, JSON ≤ 1 Mio) et messages typés (`pkg/protocol`) ; suppression du CRC32 |
| WP-4.2 | Écouteur unique par appareil et `DATA_JOIN` : **`Sender` ne crée plus de listener** ; fin des ports par transfert. C'est le lot qui change l'API du moteur. |
| WP-4.3 | Offre unique pour un fichier, un lot ou un dossier ; acceptation ; ordonnancement piloté par le receveur |
| WP-4.4 | SHA-256 par chunk ; `digest` de fin ; détection de modification de la source |
| WP-4.5 | Mesures par étape dans `pkg/diag` ; offre de type `bench` ; échelle R1 à R4 (`DEV-03`, `DEV-04`, `DEV-07`, `DEV-08`) |

### J4b — Stockage v1 · ≈ 6 à 9 jours
Bible chapitre 9.

| Lot | Contenu |
|---|---|
| WP-4.6 | `.part`, journal de 40 octets par chunk, commit groupé, reprise après coupure, finalisation et renommage |
| WP-4.7 | Dossier racine par transfert, collisions au niveau racine, doublons à la casse près, adoption de `os.Root` |
| WP-4.8 | Disque plein : pause propre, fichiers conservés (`STO-07`) |
| WP-4.9 | Échelle R5 et R6, `bench matrix`, choix des valeurs par défaut (flux, chunk) |
| WP-4.10 | Chaos : 1 000 itérations d'arrêt brutal / coupure / disque plein ; fuzzing de l'analyseur de trames, du journal et de `SafeRelPath` |
| WP-4.11 | Décision sur `StreamTar` d'après le benchmark de 100 000 petits fichiers (gardé ou retiré) |

**Critère de sortie de J4 :** le test de chaos passe ; un transfert complet est mesuré sur l'échelle de benchmark ; la gate G6 est fermée.

---

### J5 — Appairage par PIN · ≈ 5 à 8 jours (+ revue externe à planifier)
Bible §6.2. Dépend de la gate G1. Lots : `Pairer.ByPIN` derrière l'interface, liaison au canal (`ekm`), confirmation de clé, 3 essais, vecteurs de test de la spécification, tests MITM sur l'appairage. **Critère de sortie :** appairage par PIN réussi ; revue cryptographique des chapitres 6 et 7 contractualisée ou datée (gate G4).

### J6a — Cœur en-processus et découverte · ≈ 7 à 10 jours
Remplacer `*websocket.Conn` par une interface de réponse dans les gestionnaires ; `pkg/core` avec les 4 fonctions ; `xfer ctl` et `testkit` en appels directs ; bibliothèque `c-shared` issue du prototype ; mDNS (Go ou plateforme, selon le prototype) avec balise UDP de repli ; adaptation au réseau Android (`set_network_info` si G2A-09 l'exige). **Critère de sortie :** tous les tests passent par l'API en-processus ; le daemon et son WebSocket ne servent plus qu'à `devui`.

### J6b — Application Flutter MVP · ≈ 8 à 12 jours
Écrans : liste des appareils, appairage par QR (caméra) et par PIN, envoi (choix de fichiers/dossiers), réception (acceptation avec taille et expéditeur), progression, historique minimal, panneau développeur (`DEV-13`). Android : service de premier plan, destination des fichiers (décision issue de G2A-10). Windows, Linux : empaquetage. **Critère de sortie :** les gates G2a et G7 évaluées ; scénario complet Android ↔ Windows/Linux au Wi-Fi réel.

### J7 — Web Share v2 · ≈ 5 à 7 jours
Bible chapitre 11 : listener séparé, jeton porteur sans cookie (fragment d'URL), HTTPS auto-signé, CSP (JavaScript externalisé), PIN de repli à 5 essais par session, retrait du bouton « 4 chiffres », collision gérée pour le dépôt par morceaux (`WEB-07`). Gate G5 (test utilisateur de l'avertissement de certificat).

### J8 — Mesure, optimisation, finitions v1 · ≈ 5 à 8 jours
Matrice d'appareils réels (§14.3), gate G3, optimisations ciblées d'après le rapport par étape, benchmark nocturne en CI (`DEV-21`), suppression de `devui` si G7 est remplie, ingénierie de livraison (signature, paquets, `SECURITY.md`, documentation d'utilisation, premier `README`). **Critère de sortie :** objectif de 80 % d'iperf3 atteint ou plan écrit ; toutes les exigences ont leur test vert.

### J9 — macOS et iOS · à planifier quand la gate G8 est levée
Prototype G2b, Bonjour, permission « réseau local », comportement en arrière-plan sur iPhone, empaquetage et distribution. Pas de plan détaillé avant que l'environnement macOS existe.

---

## 4. Calendrier indicatif (une personne, temps plein)

| Jalon | Durée | Cumul |
|---|---|---|
| J0 | 1 à 1,5 j | ≈ 1,5 j |
| J1 | ≈ 5,5 j | ≈ 7 j |
| J2 | 4 à 5 j | ≈ 12 j |
| P (créneau) | 5 à 8 j | ≈ 20 j |
| J3 | 10 à 14 j | ≈ 34 j |
| J4a + J4b | 12 à 18 j | ≈ 52 j |
| J5 | 5 à 8 j | ≈ 60 j |
| J6a + J6b | 15 à 22 j | ≈ 82 j |
| J7 | 5 à 7 j | ≈ 89 j |
| J8 | 5 à 8 j | ≈ 97 j |

Soit environ **20 semaines** en fourchette basse, pour une première version sur Windows, Linux et Android. Ces chiffres sont des hypothèses et non des engagements ; les estimations de ce type dérapent souvent, un tampon de 30 à 50 % est raisonnable (**≈ 6 mois**). La seule vraie mesure sera la vitesse constatée en J1 ; on recalcule alors tout le tableau.
J9 s'ajoute ensuite et ne figure pas dans ce total.

---

## 5. Règles de travail

**Branches et fusion.** `main` est protégée : on y fusionne uniquement par PR dont la CI est verte. Une branche par lot, nommée `feat/j1-wp-1-2-safepath` ; une PR par lot, courte (idéalement moins de 600 lignes modifiées hors tests).
**Commits.** Un message qui commence par l'identifiant du lot : `[WP-1.2] Rejeter les chemins piégés dans SafeRelPath`.
**Modèle de PR.** Objectif ; exigences de la bible couvertes (identifiants) ; tests ajoutés ; mesure de performance si le chemin chaud est touché ; risques ; cases : CI verte, aucun secret journalisé, aucun nouveau `os.Remove` hors du périmètre autorisé.
**Définition de « terminé » d'un lot.** Code fusionné ; tests de l'exigence présents et verts sur les trois systèmes ; documentation (bible ou plan) mise à jour si la décision a changé ; issue fermée avec lien vers la PR.
**Règle de périmètre.** Pas de correction « en passant » : tout constat hors lot devient une issue.
**Revue du plan.** À la fin de chaque jalon : une demi-journée pour relire ce document, recalculer les durées, écrire le plan détaillé du jalon suivant.

---

## 6. Par où commencer concrètement (aujourd'hui)

1. **Dans votre copie du dépôt** : `git checkout -b chore/j0-setup`.
2. **WP-0.1** : passer `go.mod` à `go 1.24`, lancer `go mod tidy`, `go build ./...`.
3. **WP-0.2** : déplacer le test de hotspot derrière l'étiquette `hardware`.
4. **WP-0.3** : ajouter `.github/workflows/ci.yml` (§3, J0), pousser, ouvrir une PR vers `main`, vérifier les trois systèmes.
5. **WP-0.4** : noter l'état de référence en issues.
6. **WP-1.1** (PR 0, `/status`) : à enchaîner immédiatement, c'est la seule modification urgente de sécurité qui tient en quelques lignes.
7. En attendant la CI ou à la fin de la journée : **étape S0 du prototype** (installer Visual Studio C++, MinGW-w64, Flutter, Android Studio + NDK, WSL2).

Quand le jalon J0 est terminé, relisez avec moi les durées et passons au plan détaillé du lot suivant.
