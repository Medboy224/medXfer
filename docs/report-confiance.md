# medXfer — Rapport de sortie du lot « Modèle de confiance »

**Date :** 18 octobre 2026 · **Plan :** [medxfer-plan-confiance.md](medxfer-plan-confiance.md) · **Bible :** chapitre 6 (`PAIR-08` à `PAIR-12`, §6.5) · **Issue :** #143 · **PR :** #144 (plan), #145 (WP-C.1), #146 (WP-C.2), #147 (WP-C.3), #148 (WP-C.4), et celle de ce rapport (WP-C.5).

Ce rapport répond au critère de sortie du plan : *aucun chemin du code n'admet un appareil inconnu sans PIN ni QR ; rien n'est écrit dans `trusted.json` sans le oui des deux côtés ; une entrée présente d'un seul côté ne donne aucun privilège*.

---

## 1. Verdict

**Le lot est terminé. Les trois critères sont remplis** (§2). Ils sont vérifiés par les tests et par des essais réels entre le téléphone (Termux), le PC Zorin et le PC Windows.

Le lot corrige le point faible signalé par l'utilisateur le 18 octobre : la **question de confiance**. Les deux écrans affichaient les empreintes, et l'utilisateur répondait `y`, `a` ou `N`. Ce chemin n'était sûr que si l'on comparait vraiment 2 × 16 caractères. Il n'existe plus.

## 2. Critères de sortie

| Critère | État | Preuve |
|---|---|---|
| Aucun appareil inconnu admis sans PIN ni QR (`PAIR-12`) | ✅ | Le code ne contient plus `trustQuestion`, `askTrust`, `trust_request` ni `Ask`. Un `pair_request` ou un `pair_hello` sans secret est refusé avec `pin_required`, sauf confiance mutuelle (`TestUnknownDeviceNeedsAPINOrAQRCode`, `TestJoinTrustedNeedsMutualTrust`). |
| Rien d'écrit sans les deux oui (`PAIR-08`, `PAIR-10`) | ✅ | Le QR et le PIN n'écrivent plus rien (`TestSessionWritesNothing`, `TestQRPairsForTheSession`, `TestPairPINBetweenDaemons`). Seule `TrustTalk` écrit, après le oui de son côté. L'autre appareil ne peut pas forcer une écriture (`TestTrustCannotBeForced`). |
| Une entrée d'un seul côté ne donne rien (`PAIR-09`) | ✅ | `Mutual` exige la liste de ce côté **et** la déclaration de l'autre (`trusts_you`). Testé : commit perdu, oubli d'un seul côté (`TestLostCommitLeavesAnInertEntry`, `TestForgetOnOneSideEndsBoth`, `TestOnlyMutualTrustIsAdmittedAtOnce`), et en réel (§4). |

## 3. Ce qui a été livré

| Lot | PR | Contenu |
|---|---|---|
| Plan | #144 | Le modèle cible et les décisions D1 à D10, validées le 18 octobre. |
| WP-C.1 | #145 | `pkg/pairing` : le PIN à la demande (`pair_pin_wanted`, `PINOnDemand`, qui garde le PIN valable, `PAIR-04`), `Mutual` et `trusts_you`, la négociation `TrustTalk`. Bible chapitre 6 réécrit, dossier G4 complété (§3.6, question 9). |
| WP-C.2 | #146 | Le cœur : premier contact par PIN ou QR, sessions éphémères, confiance mutuelle. Schéma **v2** (`trust_offer`, `trust_answer`, `pin_needed`, `pin_requested`, `trust_offered`, `trust_established`, `trust_declined`, `trust_incomplete`). Tableau de bord adapté au minimum (D8). |
| WP-C.3 | #147 | `xfer send` affiche QR **et** PIN ; `xfer recv` utilise la confiance mutuelle si elle existe, sinon le PIN à la demande ; `xfer pair` établit la confiance mutuelle ; `--trust`. |
| WP-C.4 | #148 | `xfer node` : `pair <n\|ip\|code>` par PIN à la demande, commande `trust`, plus de question de confiance. |
| WP-C.5 | ce rapport | Guide d'utilisation §1 à §6 réécrit, §8 complété ; essais avec le PC Windows ; un test qui écoutait sur toutes les interfaces écoute maintenant sur la boucle locale ; ce rapport. |

## 4. Essais réels (18 octobre 2026)

Réseau : le point d'accès du téléphone (2,4 GHz). PC Windows (« Naruto »), PC Zorin, téléphone en Termux. Chaque fichier reçu a été comparé par md5. Après chaque série, les listes de confiance ont été remises à vide et les fichiers d'essai supprimés.

| # | Parcours | Résultat |
|---|---|---|
| 1 | Téléphone `xfer send`, Zorin `xfer recv` choisit dans la liste | PIN à la demande tapé sur Zorin ; fichier intact ; **rien d'enregistré** |
| 2 | Même chose avec `--trust` des deux côtés | Confiance mutuelle établie **sans question** ; chacun a l'autre dans `xfer trust list` |
| 3 | Second envoi, sans option | « a device of mutual trust » : **sans PIN** ; fichier intact |
| 4 | Le téléphone oublie Zorin, nouvel envoi | Zorin est prévenu (« … no longer trusts it: that gives nothing »), puis le PIN ; fichier intact |
| 5 | Deux `xfer node`, Zorin `pair` vers le téléphone | Le téléphone affiche « The device at … wants to pair: it must type this PIN » ; Zorin le tape ; « for this session only, nothing saved » |
| 6 | Dans cette session, Zorin `trust`, le téléphone répond `y` | « Mutual trust established, on both sides » des deux côtés |
| 7 | `disconnect`, puis `pair` à nouveau | **Sans PIN** ; puis envoi du téléphone vers Zorin, fichier intact |
| 8 | PC Windows `xfer send --trust`, téléphone `xfer recv --trust` | PIN à la demande ; confiance mutuelle établie ; fichier intact |
| 9 | Deux `xfer node`, Windows `pair` vers le téléphone (port 38415, 18887 étant pris par `xfer dev-ui`) | **Sans PIN** grâce au n° 8 ; envoi du téléphone vers Windows, fichier intact |

Les essais par PIN entre deux `xfer node` (#142) avaient déjà vérifié un mauvais PIN, refusé des deux côtés.

## 5. Décisions prises pendant le lot (en plus de D1 à D10)

| # | Décision | Raison |
|---|---|---|
| E1 | Le cœur demande le PIN sur une première connexion, puis le fait taper sur une **seconde** (`pin_needed`, puis `pair_pin`). La fermeture de la première n'est pas un échec (`ErrNoPINTyped`). | L'interface ne bloque pas une connexion pendant que l'utilisateur tape. La ligne de commande, elle, tape sur la même connexion. |
| E2 | Pendant un envoi ponctuel, la demande de confiance se fait **sur la connexion d'appairage**, juste après `pair_accept`, et seulement si l'un des deux l'a demandée (drapeau `trust_offer`). | La connexion de contrôle d'un envoi ponctuel se ferme ensuite ; le transfert ne dépend pas de l'issue. |
| E3 | Le QR et le PIN de `xfer send` sont **renouvelés ensemble** toutes les 2 min. | Un PIN ne vit pas plus de 2 min (`PAIR-02`) ; un receveur qui choisit l'émetteur dans la liste doit toujours trouver un PIN valable. |
| E4 | `send` et `recv` lisent le terminal par **un seul lecteur**, démarré au premier besoin. | Plusieurs lecteurs bufférisés de l'entrée standard se volaient des lignes. |
| E5 | Un nouveau `pair_pin_show` (cœur) ou `pair --pin` (nœud) relance les PIN à la demande après un arrêt pour trop d'erreurs (#105). | Sans cela, l'arrêt durerait jusqu'au redémarrage. |

## 6. Réserves et suites

1. **Revue G4** : le dossier pose une question de plus. N'importe quel appareil du réseau peut faire apparaître un PIN à l'écran d'un autre, sans essai supplémentaire.
2. **Nom « localhost » sous Android** (#136) : un téléphone se présente encore sous ce nom dans `xfer send` (essai n° 8).
3. **Fenêtres du pare-feu Windows pendant les tests** : chaque test qui écoute sur toutes les interfaces fait apparaître la fenêtre d'autorisation et ajoute une règle « xfer.test.exe ». Le test du port du nœud écoute maintenant sur la boucle locale. Si d'autres fenêtres apparaissent, il faudra trouver le test en cause.
4. **Le tableau de bord** n'a reçu que l'adaptation minimale (D8) : il est gelé et sera retiré à la gate G7.

## 7. Suite

**J6b**, l'application Flutter MVP, se construit sur ce modèle :
- schéma v2 ;
- `pin_needed` et `pin_requested` pour le PIN à la demande ;
- `trust_offer` et `trust_answer` pour la confiance mutuelle.
