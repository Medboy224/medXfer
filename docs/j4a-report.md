# medXfer — Rapport de sortie du jalon J4a (protocole v1)

**Date :** 12 octobre 2026 · **Plan :** [medxfer-plan-j4a.md](medxfer-plan-j4a.md) · **Bible :** chapitres 8, 9 (en partie), 12 et 17 · **PR :** #67 à #81.

Ce rapport répond au critère de sortie du plan (§3) : *un seul chemin de transfert, essai réel PC ↔ téléphone, mesures par étape, performance, issues et gate.*

---

## 1. Verdict

**Le protocole v1 est livré** : une offre, un port, `DATA_JOIN`, SHA-256 par chunk, digest de fin, source modifiée détectée, mesures par étape. **Quatre critères sur cinq sont remplis**, le cinquième l'étant à la fusion de #81. **Le critère 4 (performance en boucle locale) ne l'est pas**, pour une raison connue et chiffrée (§2, §6) : il te revient de trancher.

| Avant J4a | Après J4a |
|---|---|
| Un port par transfert, plus un port d'appairage | Un seul port par appareil, une seule règle de pare-feu (`CHAN-01`) ; chaque connexion de données nomme son transfert (`DATA_JOIN`) et n'est admise que pour la clé appairée (`CHAN-06`) |
| Un dossier = un transfert par fichier (file de lot côté daemon, course sur macOS #54) | Un dossier = **une seule offre** ; le receveur demande le plus petit chunk manquant de toute l'offre, plusieurs fichiers à la fois, mémoire bornée à N × 2 chunks |
| CRC32 par chunk, `FileID` = MD5(nom, taille, date), pas de hash de bout en bout | **SHA-256 par chunk** vérifié avant écriture (`XFER-03`, `XFER-04`), **digest de fin** (§8.3), **source modifiée** détectée (`XFER-07`) |
| Contrôle en JSON non borné | Trames bornées pour tout (`XFER-01`, `LIM-04`) ; offres paginées (D2) et validées (`LIM-05`) |
| Diagnostic par latences approximatives | **Mesures par étape** (`DEV-03`), **étape limitante** validée à 100 % (`DEV-04`), offre `bench` et `xfer bench ladder` R1 à R4 (`DEV-07` à `DEV-09`) |

**Réserves** (détail au §8) :
1. **Critère 4 :** en boucle locale, J4a est environ 39 % moins rapide que J3. C'est le coût du SHA-256 haché deux fois sur le même processeur. Entre deux appareils réels, J4a n'est pas plus lent (§6).
2. **Pas de journal ni de `fsync` groupé nouveau :** la reprise repose encore sur les fichiers `.medxfer` actuels, comme prévu (J4b).
3. **Fichier de 4 Gio et chaos 1 000 itérations :** non faits, hors du périmètre de J4a (J4b, WP-4.9).

---

## 2. Critères de sortie (plan §3)

| # | Critère | Résultat | Preuve |
|---|---|---|---|
| 1 | Un seul chemin de transfert : offre, port unique, `DATA_JOIN`, SHA-256 par chunk, `digest` ; l'ancienne API n'existe plus | ✅ | `xfer send` / `recv`, `xfer node` (#72) et le daemon (#73) passent par `pkg/transfer` ; `engine.Sender` / `Receiver`, les trames v0 et le CRC32 supprimés (#74, −2 877 lignes) ; digest (#77) |
| 2 | Essai réel PC ↔ téléphone : dossier de nombreux petits fichiers et gros fichier, hash identiques, une seule règle de pare-feu | ✅ | §5 : **2 000 fichiers de 4 Kio et un fichier de 512 Mio dans une seule offre**, PC → téléphone, 1 min 24 s, 2 001 SHA-256 identiques, un seul port (18888) |
| 3 | Mesures par étape dans le rapport ; étape limitante retrouvée dans 100 % des scénarios synthétiques | ✅ | #78 : rapport en schéma 2 (`stages`, `bottleneck`) ; `TestLimitingStageIsFound` : disque lent, réseau bridé, hash ralenti, 3 passages chacun, 9/9 avec une confiance haute |
| 4 | Débit en boucle locale (512 Mio, TLS) pas plus de 10 % sous J3 | ❌ | §6 : J3 1,44 s, J4a 2,36 s en moyenne (5 passages alternés). L'écart vient de la couche SHA-256 (gate G6) ; entre appareils, pas de baisse |
| 5 | #33, #48 et #54 fermées ; G6 fermée dans la bible | ✅ à la fusion de #81 | #48 (#70), #54 (#73) ; G6 (#67) ; #33 par #81 |

---

## 3. Ce qui a été livré

| Lot | PR | Contenu |
|---|---|---|
| Plan | #67 | Plan J4a, décisions D1 à D6 ; gate G6 fermée : SHA-256 (téléphone 536 Mo/s, BLAKE3 en Go 60 Mo/s) |
| WP-4.1 | #68 | Trames bornées (`ReadFrame`, limites par type) ; le contrôle passe en trames `CONTROL` ≤ 1 Mio ; `error{code: unsupported}` |
| WP-4.2a | #69 | `transfer.Hub` : un port, aiguillage par la première trame ; `DATA_JOIN` admis pour la seule clé du transfert ; délai de connexion (#48) |
| WP-4.2b | #70 | Daemon, `xfer node` et envoi ponctuel sur un seul port ; `StreamTar` retiré (#9) |
| WP-4.3a | #71 | Moteur v1 : une offre pour tous les fichiers, SHA-256 par chunk, ordonnanceur du receveur, progression sur les chunks uniques |
| WP-4.3b | #72 | `xfer send` / `recv` / `node` sur le moteur v1 ; offres paginées (D2) |
| WP-4.3c | #73 | Daemon sur le moteur v1 ; actions de l'interface pilotant l'ordonnanceur, `skip` (D6) ; `LIM-08` ; #54 réécrit |
| WP-4.3c-2 | #74 | Ancien moteur retiré, ses garanties portées sur `pkg/transfer` (chemins, clés, reconnexion, collisions…) |
| Correctif | #76 | #75 : rien n'est créé avant le premier chunk vérifié (le mode « écraser » vidait le fichier de l'utilisateur) ; annulation immédiate ; doublons d'offre refusés |
| WP-4.4 | #77 | Digest de fin, reprise à zéro sur écart ; source modifiée (`SOURCE_CHANGED`) ; D7 |
| WP-4.5a | #78 | Mesures par étape, étape limitante, rapport v2 |
| WP-4.5b | #79 | Offre `bench`, sources et puits ; `xfer bench ladder` R1 à R4 |
| Correctif | #81 | #33 : l'émetteur ne restait jamais « fini » après une reprise ; trame `DONE` (D8). #80 : tests de reprise rendus déterministes. Deux courses `-race` corrigées |

---

## 4. Exigences et tests

| Exigence | Tests (fichiers) | État |
|---|---|---|
| `XFER-01`, `LIM-04` | `pkg/protocol/protocol_test.go` (`TestOversizedFrameIsRefusedBeforeAllocating`, `TestBadFramesAreRefused`, `FuzzReadFrame`) | ✅ |
| `XFER-02` | `TestOfferValidate` (bornes du chunk), `TestOfferPastTheLimitsIsRefused` | ✅ |
| `XFER-03`, `XFER-04` | `TestBadHashIsNeverWritten` (2 mensonges : reçu ; 3 : échec, rien d'écrit) | ✅ |
| `XFER-05` | `TestReceiverRejoinsARestartedSender`, `faultconn` (coupure en plein chunk), `TestUnreachableSenderIsGivenUp` (#48) | ✅ |
| `XFER-07` | `TestSourceChangedBeforeSending`, `TestSourceChangedDuringSending` | ✅ |
| Digest (§8.3) | `TestTamperedPartialFileStartsOver`, `TestDigestMismatchTwiceFails`, `TestDigestFrames` ; contrôle par mutation (sans digest, 3 tests échouent) | ✅ |
| `CHAN-01`, `CHAN-06` | `pkg/transfer/hub_test.go`, `TestDataConnectionsArePinned`, `TestOneShotSendAdmitsOnlyThePairedReceiver` | ✅ |
| `LIM-05`, `LIM-06` | `TestOfferValidate` (100 001 éléments, manifeste > 16 Mio, chemins en double, fichier qui est un dossier), `TestPagedOffer*` | ✅ |
| `LIM-07`, `LIM-08` | bornes des workers ; `TestOneTransferAtATime` | ✅ |
| #75 | `TestNothingIsWrittenWithoutVerifiedData`, `TestOverwriteKeepsTheFileUntilDataArrives`, `TestOfferPathsCannotEscape` | ✅ |
| #33, #44 (D8) | `TestSenderLearnsTheEnd`, `TestSenderLearnsTheEndAfterAResume`, `TestSenderWaitsForReceiverBeforeCompleting` | ✅ (#81) |
| `DEV-03`, `DEV-04` | `TestLimitingStageIsFound` (diag), `pkg/diag/stages_test.go`, `TestTransferReportHasStages` | ✅ |
| `DEV-07` à `DEV-09` | `TestBenchOfferToNullSink`, `TestBenchOfferToDiskWithoutSync`, `pkg/bench/ladder_test.go`, `TestBenchLadderPeer` | ✅ (R1 à R4 ; R5, R6, `matrix` en J4b) |

CI : ubuntu (avec `-race`), windows et macos verte sur chaque PR, avec et sans `-tags "diag devui"`.

---

## 5. Essais sur appareils réels

**Environnement :** PC Windows 11 Pro et Samsung SM-A576B (Android 16, Termux). Jusqu'à #72 : point d'accès d'un autre téléphone (environ 3 Mo/s ce jour-là). Ensuite : **hotspot direct du téléphone** (6 à 12 Mo/s, ping 3 à 9 ms).

| Essai | PR | Résultat |
|---|---|---|
| `xfer send` / `recv`, 32 Mio dans les deux sens ; dossier de 41 fichiers | #72 | ✅ identiques |
| `xfer node` appairé par QR, dans les deux sens ; refus d'une offre puis nouvel envoi ; clé changée refusée (`ID-05`) | #72 | ✅ |
| Deux daemons (`xfer ctl`) : dossier de 41 fichiers ; 512 Mio avec **pause et reprise** demandées par l'émetteur, aucun octet écrit pendant la pause | #73 | ✅ identiques |
| Dossier de 43 fichiers dont 2 vides (l'un seul dans son sous-dossier) | #76 | ✅ identiques |
| 512 Mio ; **`.part` altéré entre deux sessions**, repris à zéro par le digest | #77 | ✅ identique (sans digest, le fichier aurait contenu les octets altérés) |
| Étape limitante affichée des deux côtés (`net_wait` / `net_write`, confiance haute) | #78 | ✅ |
| `xfer bench ladder` R1 à R4, 256 Mio, 4 flux | #79 | ✅ toutes les marches ; ratios dominés par le bruit du Wi-Fi |
| **Critère 2 :** 2 000 × 4 Kio et 512 Mio dans une seule offre, PC → téléphone | clôture | ✅ 2 001 fichiers identiques, 1 min 24 s, un seul port |

**Débit entre appareils, ancien contre nouveau moteur** (#73, 512 Mio PC → téléphone, trois paires alternées) : ancien 90,0 s en moyenne (81,4 / 107,6 / 81,1), nouveau **84,2 s** (78,8 / 92,8 / 81,0).

---

## 6. Performance en boucle locale (critère 4)

**Mesure** (même PC, i5-8350U sans SHA-NI, 512 Mio, `xfer send` / `xfer recv` en boucle locale, passages alternés, binaire de fin de J3 contre `main` de J4a) :

| | Passages | Moyenne |
|---|---|---|
| J3 (CRC32) | 1,14 / 1,36 / 1,60 / 1,61 / 1,48 s | **1,44 s** |
| J4a (SHA-256, digest) | 2,46 / 2,49 / 2,27 / 2,11 / 2,48 s | **2,36 s** |

J4a met 64 % de temps de plus, soit environ 39 % de débit en moins. **Pas de régression entre les PR de J4a** : les binaires de #77, #78, #79 et #81, mesurés dans les mêmes conditions, sont tous entre 2,1 et 2,5 s.

**Cause, chiffrée par l'échelle** (`xfer bench ladder` en boucle locale, 512 Mio) :

| Marche | Débit |
|---|---|
| R2 : TCP + TLS 1.3 | 683 / 899 Mo/s |
| R3 : + trames et SHA-256, puits nul | 326 / 343 Mo/s |

La couche TLS ne coûte rien de plus qu'en J3 (914 ms pour 512 Mio en J3, #58). **Toute la perte vient du SHA-256**, que la gate G6 a choisi à la place du CRC32. En boucle locale, chaque chunk est haché deux fois sur le même processeur (émetteur et receveur), et ce PC n'a pas d'accélération SHA. Le profil de #71 l'attribuait déjà à 63 % du CPU. Entre deux appareils, chacun ne hache qu'une fois : le SHA-256 n'y est jamais l'étape limitante (§5 : `net_wait` / `net_write`), et le débit ne baisse pas.

**Le critère, tel qu'il est écrit, ne peut pas être tenu sans revenir sur G6.** Trois voies :

| Option | Effet |
|---|---|
| **A. Recalibrer le critère** (proposition) : mesurer la non-régression **entre deux appareils** (gros fichier, passages alternés) et, en boucle locale, **à la marche R2** (TLS, sans la couche choisie par G6) | Les deux sont tenus aujourd'hui (84,2 contre 90,0 s ; R2 sans perte). Le coût du SHA-256 en boucle locale est accepté, consigné, et suivi par `bench ladder` |
| B. Garder le critère et chercher du débit en boucle locale | Peu de marge : le SHA-256 de Go utilise déjà les instructions disponibles ; ne hacher qu'une fois romprait la vérification de bout en bout (`XFER-03`) |
| C. Revenir sur G6 (BLAKE3) | Plus rapide sur le PC, mais 60 Mo/s sur le téléphone en Go contre 536 Mo/s pour SHA-256 : pire là où ça compte |

---

## 7. Décisions prises pendant J4a (en plus de D1 à D6)

- **D7 : le digest passe par les connexions de données** (`DIGEST_REQ` 0x24, `DIGEST` 0x25), car `pkg/transfer` ne dépend pas de la session (D1). Un fichier d'un seul chunk reçu dans la session s'en passe : son digest découle du hash vérifié.
- **D8 : la fin passe aussi par les connexions de données** (`DONE` 0x26, statut complet ou partiel). L'émetteur s'arrête sur ce signal, pas sur le compte des octets servis, qu'une reprise, un saut ou un fichier déjà présent ne permettent jamais d'atteindre (#33).
- **Création différée (#75) :** rien n'est créé sur disque avant le premier chunk vérifié ; le nom est résolu une dernière fois à ce moment. Un fichier vide se demande comme un chunk de longueur nulle.
- **Mesure de l'attente réseau** à partir du moment où le worker a un tampon libre : sinon un disque lent passerait pour du réseau (`DEV-03`, bible).
- **`pkg/bench`** porte l'échelle, pour que `diag` reste indépendant du moteur.
- **`NewSender` copie les éléments de l'offre** : un appelant peut la réutiliser sans course (#81).

---

## 8. Réserves et suites

| Sujet | Suite |
|---|---|
| Critère 4 en boucle locale (§6) | **Ta décision** (option A proposée) |
| Reprise sur les fichiers `.medxfer` actuels ; pas de journal ni de commit groupé nouveau ; relecture des chunks repris pour le digest, des deux côtés | J4b : journal, clé de reprise, hash conservés |
| Un partiel périmé (source changée) reste sur disque, la nouvelle version part vers « nom (1) » | J4b (gestion des partiels) |
| Variantes de casse (`A.txt` / `a.txt`) sous Windows et macOS ; normalisation Unicode | J4b |
| `bench ladder` R5, R6, `bench matrix` ; fichier de 4 Gio ; chaos 1 000 itérations | J4b (WP-4.9) |
| Racine d'une offre de plusieurs chemins nommée `batch_N_files` | Cosmétique, à revoir avec l'interface (J6a) |
| Issues ouvertes hors J4a : #36 (sonde disque du daemon), #65 (nom d'hôte dans la découverte, `DISC-06`) | J4b / J6a |

---

## 9. Suite

Après la fusion de #81 et ta décision sur le critère 4, **J4b** : journal et `fsync` groupé (§9 de la bible), clé de reprise, gestion des partiels, R5, R6 et `bench matrix`, chaos.

Les décisions qui dépendent d'une personne restent celles du rapport de J3 : **G1** (PAKE) au début de J5, et **G4**, la revue cryptographique externe, à contractualiser ou dater à la sortie de J5.
