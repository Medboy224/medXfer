# medXfer — Rapport de sortie du jalon J5 (appairage par PIN)

**Date :** 14 octobre 2026 · **Plan :** [medxfer-plan-j5.md](medxfer-plan-j5.md) · **Bible :** §6.2, §6.4, chapitre 7, §14.1 · **PR :** #98 à #111, et celle de ce rapport.

Ce rapport répond au critère de sortie du plan (§3) et de la feuille de route : *appairage par PIN réussi ; revue cryptographique des chapitres 6 et 7 contractualisée ou datée (gate G4).*

---

## 1. Verdict

**J5 est terminé sur le plan technique : 4 critères sur 5 sont remplis** (§2). L'appairage par PIN fonctionne entre le PC et le téléphone, dans les deux sens, envoi ponctuel compris. Il repose sur CPace lié à la session TLS, avec confirmation de clé dans les deux sens. Il est vérifié contre la spécification, contre une autre implémentation et par des tests d'attaque.

**Le jalon ne sera clos qu'avec la gate G4.** La revue externe doit être contractualisée ou datée : prestataire, budget, date. Cette décision t'appartient. Le dossier de revue est prêt : [medxfer-g4-dossier-revue.md](medxfer-g4-dossier-revue.md). La revue conditionne la **publication**, pas la suite du travail (D8) : J6a peut commencer en parallèle.

| Avant J5 | Après J5 |
|---|---|
| Deux façons d'être admis sans caméra : la comparaison des empreintes, ou rien | Le **PIN de 6 chiffres** (8 en option) : `xfer pair --pin`, `xfer send --pin` / `xfer recv --pin`, actions du daemon, boutons du tableau de bord |
| Gate G1 ouverte ; `PAIR-06` impossible à tenir (aucune bibliothèque Go conforme) | G1 fermée : **CPace `CPACE-RISTR255-SHA512`**, assemblé dans `pkg/pake` sur `gtank/ristretto255`, avec `PAIR-06` reformulée |
| `securechan` sans liaison au canal | `ChannelBinding()` : exporteur TLS 1.3, propre à chaque session |
| Rien contre la devinette en ligne d'un code court | 3 essais par PIN, comptés dès la réponse ; PIN factice quand plus aucun essai ne reste ; délai, puis **arrêt après 2 séries** (#105) : au plus 18 essais par attente |

**Réserves** (détail au §7) :
1. G4 : la revue reste à contractualiser.
2. CPace est encore un brouillon CFRG (le 21ᵉ), pas une RFC.
3. Les secrets éphémères ne sont pas effacés de la mémoire.
4. La découverte (mDNS, `DISC-05`) reste pour J6a.

---

## 2. Critères de sortie (plan §3)

| # | Critère | Résultat | Preuve |
|---|---|---|---|
| 1 | Appairage par PIN réussi entre le PC et le téléphone, dans les deux sens, y compris un envoi ponctuel | ✅ | §5 : essai réel du 13 octobre |
| 2 | Annexes A et B.3 du brouillon 21 vérifiées octet pour octet, dans la CI | ✅ | `TestDraftAppendixA`, `TestDraftAppendixB3`, `TestDraftScalarMultVfy` (#100) ; en plus, une autre implémentation sur 32 entrées, tags de confirmation compris (#106) |
| 3 | MITM, 4ᵉ tentative et délai testés dans la CI ; rien de secret dans les journaux | ✅ | `TestPINThroughARelayFails`, `TestThreeFailuresDestroyThePIN`, `TestPINDelayDoublesAndResets`, `TestPINStopsAfterMaxSeries` ; `TestPINPairingLeavesNoSecret`, vérifié par mutation (#103, #106, #107) |
| 4 | Gate G4 : revue des chapitres 6 et 7 contractualisée ou datée | ⏳ | **Ta décision** : prestataire, budget, date. Dossier prêt (#106) |
| 5 | Bible à jour : `PAIR-06` reformulée, G1 fermée, Annexe B | ✅ | #100, puis #102, #103, #107, #108 |

---

## 3. Ce qui a été livré

| PR | Lot | Contenu |
|---|---|---|
| #98 | Plan | Plan J5, décisions D1 à D10, dont D9 (modularité) et D10 (pas de bibliothèque de groupe réécrite) |
| #99, #101 | Test | `TestLimitingStageIsFound` rendu déterministe sous Windows (étape ralentie de 100 ms) |
| #100 | WP-5.0, WP-5.1 | G1 consignée dans la bible ; `pkg/pake` : CPace, API par identifiant de suite, un seul fichier adaptateur, vecteurs du brouillon |
| #102 | WP-5.2 | `securechan.Conn.ChannelBinding()` (`EXPORTER-medxfer-v1-pair`) |
| #103 | WP-5.3 | Chemin P2 dans `pkg/pairing` : messages `pair_pin*`, gestion du PIN, PIN factice, `Serve` qui accepte QR et PIN |
| #104 | WP-5.4 | Ligne de commande (`pair`, `send`, `recv --pin`), daemon (`pair_pin_show`, `pair_pin`), tableau de bord ; `reorderArgs` piloté par le jeu de drapeaux de chaque commande |
| #106 | WP-5.5 | Journaux sans secret, fuzzing de `RespondPIN`, comparaison avec une autre implémentation, dossier G4 |
| #107 | #105 | Arrêt de l'attente après 2 séries de PIN détruits |
| #108 | #93 | Quota élargi pour une adresse authentifiée récemment (LIM-01) |
| #110 | #109 | Adresse Wi-Fi plutôt que celle des données mobiles |
| #111 | #65 | L'annonce de découverte ne porte plus le nom de l'appareil (`DISC-06`) |

---

## 4. Exigences et tests

| Exigence | Tests | Statut |
|---|---|---|
| `PAIR-01` PIN uniforme, sans biais | `TestDrawPIN` | ✅ |
| `PAIR-02` 120 s, une seule session | `TestServePINExpires`, `TestPINDelayDoublesAndResets` (PIN déjà utilisé) | ✅ |
| `PAIR-03` liaison au canal (`fp_i`, `fp_r`, `ekm`) | `TestChannelBinding*` (dont le relais), `TestConfirmation` (autre `ekm`, autres identités), `TestPINThroughARelayFails` | ✅ |
| `PAIR-04` 3 essais, délai, arrêt | `TestThreeFailuresDestroyThePIN`, `TestSilentAttemptCounts`, `TestPINAttemptsAreReserved`, `TestPINDelayDoublesAndResets`, `TestPINStopsAfterMaxSeries`, `TestServeStopsAfterTooManyPINs` | ✅ |
| `PAIR-05` confirmation dans les deux sens | `TestConfirmation`, `TestCrossCheck*` (valeurs de Ta et Tb) | ✅ |
| `PAIR-06` reformulée : pas de primitive écrite, vecteurs, revue | `TestDraftAppendixA`, `TestDraftAppendixB3`, `TestCrossCheckTheSargeCPace` ; revue : G4 | ✅, revue ⏳ |
| `PAIR-07` échec neutre, ni compteur ni alias | `TestNoAttemptLeftLooksLikeAWrongPIN`, `TestPairPINWrongFails` | ✅ |
| `PAIR-08` « un seul envoi » sans écriture | Chemin commun avec le QR (`TestOnceWritesNothing`) ; `xfer send --pin` et `xfer recv --pin` en `Once` | ✅ |
| Chapitre 13 : rien de secret dans les journaux | `TestPINPairingLeavesNoSecret` (vérifié par mutation), `TestErrorsHoldNoSecret` | ✅ |
| `LIM-01` (#93) | `TestGateKnownAddressGetsLargerQuota`, `TestServerGateRemembersPairedAddress` (vérifié par mutation) | ✅ |
| `DISC-06` (#65) | `TestAnnouncementNamesNoDevice`, `TestDaemonAnnouncesNoName` | ✅ |

**Fuzzing :**
- `FuzzFinish`, sur les partages : 60 s sans défaut ;
- `FuzzRespondPIN`, sur les messages `pair_pin*` : environ 100 000 exécutions en 90 s, sans défaut.

---

## 5. Essais sur appareils réels (13 et 14 octobre)

PC Windows (`10.86.69.174`) et téléphone Android sous Termux (`10.86.69.197`), en Wi-Fi. Configurations isolées (`MEDXFER_CONFIG_DIR`) : les vrais appareils de confiance n'ont pas été touchés.

| Essai | Résultat |
|---|---|
| Le téléphone affiche le PIN, le PC le saisit | ✅ appairés en 0,15 s ; confiance enregistrée des deux côtés |
| Le PC affiche le PIN ; le téléphone tente 3 PIN faux | ✅ 3 refus, un nouveau PIN s'affiche ; l'ancien PIN exact est refusé ; le nouveau appaire |
| Envoi ponctuel par PIN, téléphone → PC, 50 Mo | ✅ 7,2 s, SHA-256 identique |
| Envoi ponctuel par PIN, PC → téléphone, 20 Mo | ✅ 3,3 s, SHA-256 identique |
| Adresse annoncée par le téléphone, données mobiles actives (#109) | ❌ le 13 : l'adresse du modem. ✅ le 14, avec le correctif de #110 : l'adresse Wi-Fi, constatée par une sonde compilée pour Android sur le téléphone |

Les débits de ces essais (environ 7 MB/s) sont ceux du réseau ce jour-là (`net_wait`, confiance haute). Ce ne sont pas des mesures de performance : celles-ci restent celles de J4b.

---

## 6. Décisions prises pendant J5 (en plus de D1 à D10)

| Décision | Raison |
|---|---|
| **Un essai compte dès l'envoi de `pair_pin_reply`** (#103) | L'initiateur a alors testé son PIN : partir sans confirmer ne doit pas rendre l'essai |
| **PIN factice** quand aucun essai ne reste (#103) | Un attaquant ne peut pas distinguer « plus d'essai » de « mauvais PIN » (`PAIR-07`) |
| **`Options.PIN` du `Serve` existant**, au lieu d'un `ServePIN` séparé (#103) | QR et PIN acceptés ensemble (D6), sans dupliquer la boucle d'acceptation |
| **Le PIN n'est jamais un argument** de la ligne de commande (#104) | Il resterait dans l'historique et la liste des processus |
| **`pair_pin_shown` part au seul client demandeur**, et le PIN est enregistré auprès du masqueur (#104) | `Broadcast` écrit dans le journal |
| **Arrêt après 2 séries de PIN détruits** (#105, ta décision du 14 octobre) | Au-delà de 6 PIN détruits, ce n'est plus une faute de frappe ; au plus 18 essais par attente |
| **Quota LIM-01 élargi pour une adresse authentifiée récemment** (#93) : 60 par minute pendant 10 min | Un appareil appairé dont le Wi-Fi coupe n'est plus bloqué ; un inconnu reste à 5 |
| **Nom de découverte générique** `medXfer-xxxx` (#65) | `DISC-06` ; l'alias ne circule qu'après l'appairage |
| **Comparaison figée** avec `the-sarge/cpace`, sans dépendance (D7, #106) | Vérifie aussi les tags de confirmation, que le brouillon ne donne pas |

---

## 7. Réserves et suites

1. **G4, la revue externe.** C'est le dernier critère de J5. Le dossier liste 8 questions pour le relecteur. Il est en français ; une version anglaise sera probablement nécessaire selon le prestataire.
2. **CPace n'est pas encore une RFC.** Le brouillon 21 attend l'éditeur RFC. Si les vecteurs changent à la publication, les mêmes tests le montreront.
3. **Effacement des secrets.** Le scalaire, l'ISK et `mac_key` ne sont pas effacés explicitement, Go ne le garantissant pas. Question 4 du dossier G4.
4. **Noms affichés pendant la découverte.** Depuis #111, les listes affichent `medXfer-xxxx` et l'adresse IP. Un appareil de confiance pourrait y être reconnu localement, par son adresse, avant même l'appairage : à faire en **J6a** ou **J6b**, avec mDNS (`DISC-01`) et « n'annoncer que sur demande » (`DISC-05`).
5. **Quota LIM-01 par adresse IP** (#93). Un appareil qui partagerait l'IP d'un appareil appairé (NAT) profiterait aussi du quota élargi. Le gain est borné : 60 par minute, et 16 connexions simultanées au plus.
6. **Écrans Flutter du PIN** : J6b. J5 fournit la ligne de commande, le daemon et les événements.

---

## 8. Suite

**J6a, cœur en-processus et découverte** (feuille de route) :
- une interface de réponse à la place du WebSocket ;
- `pkg/core` ;
- une bibliothèque `c-shared` ;
- mDNS avec repli UDP.

Je prépare son plan dès que tu le souhaites. La revue G4 peut avancer en parallèle.
