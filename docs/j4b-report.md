# medXfer — Rapport de sortie du jalon J4b (stockage v1)

**Date :** 13 octobre 2026 · **Plan :** [medxfer-plan-j4b.md](medxfer-plan-j4b.md) · **Bible :** chapitre 9, §8.3 (petits fichiers), §14.2 et §17.4 · **PR :** #84 à #96, et celle de ce rapport.

Ce rapport répond au critère de sortie du plan (§3) : *chaos, échelle et valeurs par défaut, essai réel de reprise et de disque plein, performance, issues et gate.* Avec J4a, il clôt **J4**.

---

## 1. Verdict

**J4b est terminé, et avec lui J4.** Le stockage v1 est livré : fichiers `.part` et journal, reprise vérifiée, dossier racine par transfert, disque plein géré, valeurs par défaut mesurées, petits fichiers rendus durables par lots. **Les cinq critères de sortie sont remplis** (§2), ainsi que celui de J4 dans la feuille de route : le chaos passe, un transfert complet est mesuré sur l'échelle, la gate G6 est fermée.

| Avant J4b | Après J4b |
|---|---|
| Reprise sur les fichiers d'état `.medxfer` ; un partiel périmé restait sous le nom final | `.part` et journal de 40 octets par chunk (`MXST`), invariant J, commit groupé, reprise qui revérifie les 8 derniers chunks, clé de reprise (émetteur, chemin, taille, date) ; nom final seulement une fois complet et vérifié |
| Un dossier reçu se mêlait à l'existant ; « écraser » pouvait vider des fichiers de l'utilisateur | Un dossier racine neuf par transfert (`STO-10`), créé d'un seul coup avec son marqueur, retrouvé à la reprise ; tout passe par `os.Root` ; chemins identiques à la casse près refusés |
| Disque plein : l'élément échouait ; une sonde écrivait 8 Mio dans le dossier de réception (#36) | Espace vérifié avant d'accepter (`NO_SPACE`) ; disque plein : pause, fichiers gardés, reprise ; plus de sonde |
| Échelle R1 à R4 ; valeurs par défaut jamais mesurées (4 × 4 Mio) | R5, R6 et `bench matrix` aux passages alternés ; **4 × 2 Mio** mesurés entre le PC et le téléphone |
| Ni chaos ni fuzzing du stockage | Chaos de 1 000 itérations sous Windows et sous Linux, avec 5 fautes dont l'arrêt brutal d'un vrai processus ; fuzzing du journal, de `SafeRelPath`, d'`Offer.Validate` |
| Petits fichiers : deux `fsync` par fichier ; `StreamTar` en suspens | Durabilité par lots (`syncfs`) et 8 connexions : **100 000 fichiers 3,9 fois plus vite** ; `StreamTar` retiré pour de bon |

**Réserves** (détail au §8) :
1. Normalisation Unicode des noms (NFC et NFD) : pas traitée.
2. Sous Windows, un petit fichier coûte environ 2 ms à créer (NTFS, antivirus), même sans `fsync`.
3. Le commit groupé tourne dans le flux qui écrit, et non en arrière-plan comme dans J4a. Son coût n'est pas mesurable à 40-50 MB/s ; il est à remesurer à plus haut débit.
4. iperf3 n'a pas encore été mesuré : c'est la référence de la cible de 80 % (gate G3, J8).

---

## 2. Critères de sortie (plan §3)

| # | Critère | Résultat | Preuve |
|---|---|---|---|
| 1 | Chaos : 1 000 itérations sans corruption, sans fichier hors de la destination, sans fichier préexistant modifié ; 50 itérations vertes en CI | ✅ | **Windows** : 1 000 itérations (#94), où les lots ne s'appliquent pas. **Linux, lots actifs** (13 octobre, PC Zorin) : 1 000 itérations, soit disque plein 209, arrêt brutal 202, coupure réseau 211, coupure de courant 193, écriture déchirée 185 ; et 200 sous `-race`. 50 itérations en CI à chaque PR, sous `-race` sur ubuntu |
| 2 | Échelle R1 à R6 mesurée entre le PC et le téléphone ; valeurs par défaut choisies par `bench matrix` | ✅ | #92 (R5, R6, `matrix`), mesures en 2,4 et 5 GHz (`bench-results/`) ; D8 : 4 × 2 Mio (#95), mesure ciblée en passages alternés |
| 3 | Réception tuée (`kill -9`) en plein gros fichier puis reprise : seuls les chunks manquants sont redemandés, hash identique. Disque plein : pause puis reprise | ✅ | §5 : 512 Mio tués à 73 chunks durables sur 256, reprise à **28,5 %**, SHA-256 identique. Disque plein réel sous Linux (tmpfs) et sous Windows (clé USB FAT32, `ERROR_DISK_FULL` reconnu) : pause, reprise, SHA-256 identique (#89, #90) |
| 4 | Pas plus de 10 % de baisse entre deux appareils par rapport à J4a ; coût de R5 mesuré et consigné | ✅ | 512 Mio du PC vers le téléphone, binaires J4a et J4b en alternance, 6 passages de chaque : **J4a 65,7 s (médiane 64,2), J4b 63,5 s (médiane 61,7)**. R5 (journal et `fsync`) contre R4 en 5 GHz : 42,4 contre 43,0 MB/s du téléphone vers le PC, 35,8 contre 35,1 MB/s dans l'autre sens. Le coût n'est pas mesurable |
| 5 | #36 fermée ; G6 fermée | ✅ | #36 par #89 ; G6 en J4a (#67) |

---

## 3. Ce qui a été livré

| PR | Lot | Contenu |
|---|---|---|
| #84 | Plan | Plan J4b, décisions D1 à D10 (D11 ajoutée en WP-4.7) |
| #85 | WP-4.6a | Go 1.25 ; `pkg/store` : `.part`, journal `MXST`, invariant J, commit groupé, reprise vérifiée |
| #86 | WP-4.6b | Le receveur passe sur `pkg/store` : clé de reprise, journal paresseux, un seul `fsync` à la finalisation (36 % plus rapide sur les petits fichiers) |
| #87 | Test | `TestLimitingStageIsFound` : les scénarios réseau et hash sans `fsync` |
| #88 | WP-4.7 | Dossier racine neuf par transfert, marqueur et reprise (D11), D5 et D6, `PATH_CONFLICT` |
| #89 | WP-4.8 | Espace vérifié avant d'accepter, disque plein mis en pause, sonde supprimée (#36) |
| #90 | Correctif | Progression pendant une pause sur disque plein ; test instable #91 |
| #92 | WP-4.9 | R5, R6, `bench matrix`, dispersion et fiabilité de la recommandation |
| #94 | WP-4.10 | Chaos et fuzzing ; deux défauts de reprise et un débordement corrigés |
| #95 | D8 | Chunks de 2 Mio par défaut ; `bench matrix` alterne ses passages |
| #96 | WP-4.11 | Durabilité par lots, 8 connexions pour les petits fichiers, `StreamTar` retiré |
| ce rapport | Sortie | Vitesse affichée calculée sur les octets reçus dans la session (voir §7) |

---

## 4. Exigences et tests

| Exigence | Tests | Statut |
|---|---|---|
| `STO-01`, `STO-02` : nom final seulement complet et vérifié | `TestNoFinalNameBeforeTheEnd`, `TestFinalizeRules`, `TestBatchFlush`, `TestTamperedPartialFileIsRepaired` | ✅ |
| `STO-03` : invariant J | `TestPowerLossNeverLies` (coupure simulée après chaque opération) ; chaos, faute `powercut` | ✅ |
| `STO-04` : commit groupé (64 Mio ou 2 s) | `TestGroupCommit`, `TestNoSync` | ✅ |
| `STO-05` : queue du journal déchirée | `TestTornTailIsCut`, `TestJournalCutAtEveryOffset` (chaque offset), `FuzzJournal` | ✅ |
| `STO-06` : 8 derniers chunks revérifiés, `verify=full` | `TestLastChunksAreVerified`, `TestTamperedPartialFileIsRepaired` | ✅ |
| `STO-07` : espace avant d'accepter, disque plein | `TestCheckSpaceCountsWhatIsLeft`, `TestDiskFullPausesThenResumes`, `TestDiskFullBeforeTheJournal`, `TestOfferTooBigIsRefusedBeforeAnyByte`, `TestDiskFullPausesTheTransfer` | ✅ |
| `STO-08` : clé de reprise | `TestResumeKey`, `TestOtherSenderDoesNotResume`, `TestChangedSourceStartsOver` | ✅ |
| `STO-09` : ne jamais supprimer ce qui n'est pas du transfert | `TestForeignPartialIsLeftAlone`, `TestDiscardRemovesOnlyItsFiles`, `TestStopBeforeFirstCommitSingleFile` | ✅ |
| `STO-10` : dossier racine | `TestFolderGoesToAFreshRoot`, `TestResumeFindsItsRootAgain`, `TestMarkRootIsAtomic`, `TestStopBeforeFirstCommitInOwnFolder` | ✅ |
| `STO-11` : `PATH_CONFLICT`, rien de supprimé | `TestFileWhereAFolderIsNeeded`, `TestResolve` | ✅ |
| `STO-12`, `STO-13` : `SafeRelPath`, `os.Root` | `TestOfferPathsCannotEscape`, `TestLinks`, `FuzzSafeRelPath` | ✅ |
| `STO-14` : doublons à la casse près | `TestOfferValidate`, `FuzzOfferValidate` | ✅ |
| `XFER-02` : chunk 2 Mio par défaut, 256 Kio à 8 Mio | `TestOfferValidate` ; mesures D8 (`bench-results/`) | ✅ |
| `DEV-07` : R5, R6, `bench matrix` | `TestLadderRunsEveryStep`, `TestMatrixRunsEachCell`, `TestMatrixRunsAlternate`, `TestRecommend`, `TestBenchMatrixPeer` | ✅ |
| Petits fichiers (WP-4.11) | `TestBatchFlush`, `TestBatchNameTaken`, `TestBatchSyncFSFails`, `TestBatchTakesOnlyCompleteFiles`, `TestChooseWorkers` ; profil `TestSmallFilesProfile` | ✅ |
| Chaos et fuzzing (§14.2) | `TestChaos` (5 fautes, graine rejouable), `FuzzJournal`, `FuzzSafeRelPath`, `FuzzOfferValidate`, `FuzzReadFrame` | ✅ |

---

## 5. Essais sur appareils réels

**Environnement :**
- un PC Windows 11 Pro (Intel AC 8265, 802.11ac 2×2) ;
- un Samsung Galaxy A57 (SM-A576B, Android 16, Termux) ;
- un PC sous Zorin OS (Linux) pour `-race` et le chaos ;
- deux liens : le hotspot 2,4 GHz du téléphone (802.11n, environ 10 à 12 MB/s), et un groupe Wi-Fi Direct en 5 GHz créé sur le téléphone (802.11ac, 650 à 867 Mbit/s).

| Essai | PR | Résultat |
|---|---|---|
| Dossier de 2 001 fichiers dans une offre ; `.part` altéré, repris de zéro par le digest | #86 | ✅ identiques |
| Réception tuée (`SIGKILL`) puis reprise : seule la partie manquante est reçue | #86 | ✅ journal de 44 chunks, hash identique |
| Dossier envoyé à un téléphone qui a déjà un dossier de ce nom ; tué puis repris | #88 | ✅ reçu dans « bigdir (1) », repris au même endroit, dossier de l'utilisateur intact |
| Offre de 900 Go vers un disque de 114 Go | #89 | ✅ refusée avant toute confirmation, rien d'écrit |
| Disque plein réel : Linux (tmpfs de 382 Mo), Windows (clé USB FAT32 remplie) | #89, #90 | ✅ pause, `.part` gardé, reprise, SHA-256 identique |
| Échelle et matrices, 2,4 et 5 GHz ; mesure ciblée 4 × 2 contre 4 × 4 Mio | #92, #95 | ✅ (`bench-results/`) |
| 100 000 fichiers de 1 à 16 Kio, PC → téléphone, 2,4 GHz : 9 min 58 s, puis 2 min 34 s | #96 | ✅ |
| **Sortie :** 2 000 × 4 Kio et 512 Mio dans une offre (le critère 2 de J4a) | ce rapport | ✅ **2 001 SHA-256 identiques**, 1 min 25 s (J4a : 1 min 24 s) |
| **Sortie :** `kill -9` à 20 s dans un fichier de 512 Mio, puis reprise | ce rapport | ✅ 73 chunks durables sur 256, reprise affichée à 28,5 %, SHA-256 identique |
| **Sortie :** fichier de **4,5 Gio** (au-delà de 2³² octets) | ce rapport | ✅ 4 831 838 208 octets en 8 min 38 s, SHA-256 identique |

---

## 6. Performance

**Gros fichiers.**
- En 5 GHz, medXfer va aussi vite que TCP brut sur le même lien : R6 entre 36 et 52 MB/s, R1 entre 36 et 43 MB/s. TLS, SHA-256, journal et `fsync` ne coûtent rien de mesurable à ces vitesses.
- En 2,4 GHz, le lien plafonne à 10-12 MB/s.
- Avant ces travaux, l'ancien moteur faisait en 5 GHz, sur 11,88 Go : 52,3 MB/s du PC vers le téléphone, 42,4 MB/s dans l'autre sens. La version actuelle fait environ 52 et 38 à 40 MB/s sur des passages de 256 Mio, où le coût fixe d'un transfert pèse davantage.

**Non-régression face à J4a** (512 Mio du PC vers le téléphone, en 2,4 GHz, binaires en alternance) :

| Passage | J4a | J4b |
|---|---|---|
| 1 à 3 | 52,3 / 67,4 / 46,7 s | 75,5 / 57,7 / 65,7 s |
| 4 à 6 | 89,0 / 77,6 / 61,0 s | 43,4 / 93,5 / 45,0 s |
| Moyenne (médiane) | 65,7 s (64,2) | **63,5 s (61,7)** |

Le lien varie du simple au double pour un même binaire : seule une alternance sur plusieurs passages permet de conclure. J4b avec des chunks de 4 Mio fait 43,2 / 53,7 / 57,1 s.

**Petits fichiers** (`bench-results/README.md`) : 100 000 fichiers du PC vers le téléphone, en 2,4 GHz.

| Version | Durée | Fichiers/s |
|---|---|---|
| `fsync` par fichier (avant WP-4.11) | 9 min 58 s | 167 |
| Lots, 4 connexions | 3 min 26 s | 486 |
| **Lots et 8 connexions** | **2 min 34 s** | **649** |

---

## 7. Décisions prises pendant J4b (en plus de D1 à D11)

- **Journal :** il reste paresseux seulement dans le dossier propre au transfert, où un `.part` sans journal lisible est forcément le sien. Pour un fichier seul, l'en-tête du journal est écrit avec le fichier (WP-4.10).
- **Marqueur du dossier racine :** il apparaît d'un seul coup. Le dossier est créé sous un nom temporaire `.medxfer-<clé>`, le marqueur y est écrit, puis le dossier est renommé (WP-4.10).
- **`Offer.Validate` :** une offre dont les tailles débordent un `int64` est refusée (WP-4.10).
- **Taille de chunk :** 2 Mio par défaut (D8). `bench matrix` alterne ses passages et déclare une recommandation non fiable quand la dispersion dépasse 5 %.
- **Petits fichiers :**
  - durabilité par lots, avec un `syncfs` par lot sous Linux et Android ;
  - `Receiver.Workers = 0` laisse le receveur choisir : 4 connexions, ou 8 pour une offre de petits fichiers ;
  - `StreamTar` est retiré pour de bon (WP-4.11).
- **Vitesse affichée :** elle compte les octets reçus dans la session, plus ce qu'une reprise retrouve sur le disque. Au début d'une reprise, elle montrait des centaines de MB/s (`TestResumeIsNoSpeed`, ce rapport).
- **Chaos :** un hub par itération. Le Gate de LIM-01 compte les poignées de main ratées par adresse : partagées entre itérations, elles bloquaient le receveur (#93).

---

## 8. Réserves et suites

| Sujet | Suite |
|---|---|
| Normalisation Unicode des noms (un même nom en NFC et en NFD) : deux fichiers qui n'en font qu'un pour l'œil, et pour macOS | À traiter avant J9 (macOS), ou en J6b si l'appli le révèle |
| Windows : environ 2 ms par petit fichier même sans `fsync` (création NTFS, antivirus) ; les 8 connexions aident, pas les lots | J8 (mesure et optimisation) |
| Commit groupé synchrone, dans le flux qui écrit, alors que J4a le lançait en arrière-plan | À remesurer à plus haut débit (Wi-Fi 6, Ethernet) : J8, gate G3 |
| `fsync` qui ment, sur un fichier déjà renommé : une perte que le receveur ne peut pas voir | Limite connue (bible §14.2) |
| iperf3 non mesuré : c'est la référence de la cible de 80 % | J8 (G3) |
| Le premier lot d'un transfert paie aussi les données en attente des autres programmes (`syncfs`) | Noté (bible §9.6) |
| #93 : LIM-01 et les appareils appairés dont les poignées de main échouent | Décision attendue |
| #65 : nom d'hôte dans l'annonce de découverte (`DISC-06`) | J6a |
| Réserves de J4a réglées en J4b : racine `batch_N_files` (D5 : `medXfer <date>`), casse (`STO-14`), partiel périmé (`.part` et clé de reprise), fichier de 4 Gio (essai de 4,5 Gio) | — |

---

## 9. Suite

**J5 — appairage par PIN.** Il dépend de la gate **G1** (choix du PAKE), à trancher en tête de jalon. La revue cryptographique externe (gate **G4**) est à contractualiser ou à dater à la sortie de J5.

La performance (gate G3, iperf3, matrice d'appareils) reste en J8. Les références mesurées ici (`bench-results/`) servent de point de départ.
