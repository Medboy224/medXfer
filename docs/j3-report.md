# medXfer — Rapport de sortie du jalon J3 (Confiance)

**Date :** 11 octobre 2026 · **Plan :** [medxfer-plan-j3.md](medxfer-plan-j3.md) · **Bible :** chapitres 4, 6.1, 7 et 12 · **PR :** #51 à #64.

Ce rapport répond au critère de sortie du plan (§3) : *plus aucun octet en clair, plus de connexion sans identité vérifiée, appairage par QR.*

---

## 1. Verdict

**J3 est terminé.** Les cinq critères de sortie sont remplis (§2). Le cinquième l'est depuis #64 : avant cette correction, la découverte laissait passer en clair le nom des fichiers sur le chemin par code. Je l'ai trouvé en préparant ce rapport.

| Avant J3 (bible, annexe « failles ») | Après J3 |
|---|---|
| Données des transferts en TCP sans TLS (faille 1) | TLS 1.3 mutuel, chaque connexion de données épinglée à la clé du pair du transfert |
| Repli en clair et `InsecureSkipVerify` (faille 2) | Aucun repli ; vérification par l'empreinte, des deux côtés |
| Port de données ouvert à quiconque devine un `FileID` (faille 3) | Le port de données n'admet que la clé appairée ; avant l'appairage, il refuse tout |
| Code `XXX-YYY` comme seule « authentification » (faille 4, en partie) | Le code ne sert plus qu'à trouver l'appareil ; la confiance vient de la clé (appareil de confiance, QR, ou comparaison des empreintes sur les deux écrans) |

**Réserves**, qui ne remettent pas en cause la sortie (détail au §7) :
1. **Nom d'hôte dans la découverte :** l'annonce contient encore le nom d'hôte de l'appareil (`DISC-06`), traité par #65 en J6a.
2. **Daemon entre deux machines :** il n'a pas été essayé ainsi, car le pare-feu du PC n'accepte les connexions entrantes que vers `bin\xfer.exe`. Il est couvert par des tests entre deux vrais daemons sur la même machine.
3. **Capture réelle :** elle a été faite par un relais enregistreur sur le vrai trajet Wi-Fi, pas par `tcpdump` ni `pktmon`, faute de droits d'administration.
4. **Application Flutter (prototype G2a) :** elle appairait par code. Elle devra utiliser le QR et la confirmation des empreintes en J6a et J6b.

---

## 2. Critères de sortie (plan §3)

| # | Critère | Résultat | Preuve |
|---|---|---|---|
| 1 | Une capture d'un transfert complet ne montre aucun contenu lisible : test automatisé, plus une capture réelle consignée | ✅ | Tests `TestCaptureShowsNothingOfTheTransfer` (chemin QR, #62) et `TestCaptureShowsNothingOfTheCodePath` (chemin par code, #64), avec le témoin `TestCaptureSeesAPlaintextTransfer`. Capture réelle téléphone → PC (#62) : 33 625 621 octets, ni marqueur, ni nom, ni secret ; taux zlib de 1,0. Réponses de découverte réelles (#64) : ni nom de fichier ni empreinte, même avec le bon code. |
| 2 | Tests MITM, repli interdit et connexion de données non autorisée verts sur la CI des trois systèmes | ✅ | §4 ; CI ubuntu (avec `-race`), windows et macos verte sur chaque PR du jalon |
| 3 | Deux appareils réels s'appairent par QR, puis transfèrent ; hash identique | ✅ | §5 : `xfer pair` (#59), `xfer send` / `xfer recv <uri>` (#60), `xfer node` avec `pair --show` / `pair <uri>` puis `send` (#61) |
| 4 | `UpgradeToTLSIfClientHello` et le repli de `DialTLSPeer` n'existent plus ; aucun chemin ne fait passer de données en clair | ✅ | `session/tls.go` supprimé (#53). Hors tests, chaque `engine.NewSender` / `NewReceiver` reçoit une fonction de sécurisation (`SetSecure`), et un émetteur ponctuel refuse toute connexion de données avant l'appairage (#60). Restent en clair par conception : la découverte, qui ne transporte plus rien du transfert (#64), le contrôle local du daemon sur la boucle locale (protégé par jeton depuis J1), et le Web Share (J7). |
| 5 | `docs/medxfer-g1-pake.md` écrit, avec sa recommandation | ✅ | #63 : CPace ristretto255 recommandé ; décision attendue au début de J5 |

---

## 3. Ce qui a été livré

| Lot | PR | Contenu |
|---|---|---|
| Plan | #51 | Plan détaillé, décisions D1 à D5 |
| WP-3.1 | #52 | `pkg/identity` : clé P-256 persistante, certificat, empreinte (SHA-256 du SPKI), forme courte `XXXX-XXXX-XXXX-XXXX`, `trusted.json` en écriture atomique ; `xfer id`, `xfer trust list\|forget` |
| WP-3.2 | #53 | `pkg/securechan` : TLS 1.3 mutuel, ALPN `medxfer/1`, tickets désactivés, `VerifyConnection` à épinglage, états `UNAUTH`/`AUTH` ; suppression de `session/tls.go` et de tout repli |
| — | #55 | Quarantaine de `TestBatchQueueDynamicAdvanceOnPause` (#54), bloqué 10 min sur macOS |
| WP-3.6a | #56 | Limites des connexions non authentifiées : 16 à la fois, 5 par minute et par IP, handshake de 5 s, 4 Kio en `UNAUTH` |
| WP-3.3 | #57 | Confiance provisoire en ligne de commande : les deux empreintes affichées, « non » par défaut, refus sans terminal ; `ID-05` |
| WP-3.4 | #58 | Données en TLS mutuel épinglé, ports par transfert conservés ; le moteur reçoit ses fonctions par injection (D5) |
| WP-3.5a | #59 | `pkg/pairing` : URI `medxfer://v1/pair?...`, secret de 128 bits à usage unique, 120 s ; `xfer pair --show` / `xfer pair <uri>` ; correction de #19 (adresse d'une interface active) |
| WP-3.5b | #60 | Envoi ponctuel authentifié des deux côtés : QR de `xfer send`, `xfer recv <uri>`, confirmation des deux écrans sans QR, appareil de confiance admis d'office |
| WP-3.6b | #61 | Le code sort de l'authentification (daemon, `xfer node`) ; actions daemon `pair_show`, `pair_uri`, `trust_request` / `trust_respond` |
| WP-3.7 | #62 | Proxy MITM refusé ; capture réseau automatisée ; capture réelle consignée |
| WP-3.8 | #63 | Veille G1 (PAKE) |
| Correctif | #64 | Plus rien du transfert dans la découverte ; la clé de l'émetteur est lue dans le TLS |

---

## 4. Exigences et tests

| Exigence | Tests (fichiers) | État |
|---|---|---|
| `ID-01` à `ID-04`, `ID-06` | `pkg/identity/identity_test.go` (identité stable, certificat réémis avec la même empreinte, nouvelle clé = nouvelle identité, formes de l'empreinte, écriture interrompue, oubli) | ✅ |
| `ID-05` | `cmd/xfer/nodetrust_test.go`, `oneshot_test.go`, `pkg/api/trust_test.go` (`TestDaemonRefusesChangedKey`), `pkg/securechan` | ✅, et vu sur appareils réels (§5) |
| `CHAN-02` | `TestTLS12IsRefused`, `TestMissingALPNIsRefused` | ✅ |
| `CHAN-03` | `TestTrustedPeersAuthenticateBothWays`, `TestPinnedKeyMismatchIsRefused`, `TestUnknownPeerIsRefused`, `TestMITMIsRefusedBy*` (×3) | ✅ |
| `CHAN-04` | `TestUnauthLimits` (4 Kio), `TestAuthenticateLiftsUnauthLimit` ; seul `pair_request` / `pair_secret` / `pair_hello` est lu en `UNAUTH` (daemon, `xfer node`, `pkg/pairing`) | ✅ |
| `CHAN-05` | `TestPlaintextClientIsRefused`, `TestCutHandshakeHasNoFallback`, `TestDaemonRefusesPlaintextPairing` | ✅ |
| `CHAN-06` (objectif, sans `DATA_JOIN`) | `pkg/engine/secure_test.go` (autre clé, émetteur inconnu, client en clair, fuites de goroutines), `TestOneShotPeerSecure` | ✅ ; `DATA_JOIN` et l'écouteur unique en J4a |
| `CHAN-07` / `LIM-02` | `TestHandshakeTimeout` (un client qui ne termine jamais le handshake est coupé au délai, réglable, 5 s par défaut) | ✅ |
| `LIM-01` | `TestGateRateLimitPerIP`, `TestGateMaxUnauth`, `TestServerGate`, `TestDaemonLimitsNewConnectionsPerIP` | ✅ |
| `LIM-03` | `TestUnauthLimits` | ✅ |
| §6.1, `PAIR-07`, `PAIR-08` | `pkg/pairing/pairing_test.go` (usage unique, expiration, mauvais secret neutre, clé du QR épinglée, `--once` n'écrit rien, offre portée par `pair_accept`, `pair_hello`), `pkg/api/trust_test.go`, `cmd/xfer/pair_test.go` | ✅ |
| `DISC-06` (en partie) | `TestAnswerRevealsNothingWithTheRightCode`, `TestPublicOfferHidesTheTransfer` | ✅ pour les fichiers et la clé ; nom d'hôte → #65 |
| §14.1, partie J3 | Capture réseau (deux chemins et témoin), MITM, repli, données non autorisées | ✅ |

---

## 5. Essais sur appareils réels

**Environnement :**
- un PC Windows 11 Pro et un Samsung SM-A576B (Android 16, Termux), tous deux sur le point d'accès Wi-Fi 2,4 GHz d'un autre téléphone ;
- fichiers de 32 Mio ;
- binaires compilés depuis chaque branche.

| Essai | PR | Résultat |
|---|---|---|
| Confiance provisoire : les deux écrans montrent les mêmes empreintes ; clé changée refusée avec l'alerte | #57 | ✅ |
| Données en TLS épinglé, téléphone → PC | #58 | ✅ hash identique |
| `xfer pair --show` sur le téléphone, `xfer pair <uri>` sur le PC : chacun fait confiance à l'autre | #59 | ✅ |
| `xfer send` (QR) → `xfer recv <uri>` | #60 | ✅ hash identique |
| Code, refus par l'émetteur ; acceptation des deux côtés ; appareil inconnu sans terminal ; appareil de confiance sans question | #60 | ✅ |
| Deux `xfer node` : appairage par QR puis envoi ; refus `ID-05` ; appareil de confiance par code ; appareil inconnu par code avec les deux questions | #61 | ✅ hash identique |
| Capture du trajet réel par relais enregistreur | #62 | ✅ rien en clair |
| Découverte interrogée avec le bon code, sans code, avec un mauvais ; réception par code (appareils de confiance, puis inconnus des deux côtés) | #64 | ✅ ni nom ni clé ; hash identique |

**Débit :** environ 3 Mo/s sur ce réseau (32 Mio en 10 à 11 s), dans les deux sens. Ce n'est pas un critère de J3. Le coût du TLS mesuré en boucle locale est négligeable : 909 ms en clair contre 914 ms en TLS pour 512 Mio (#58). La mesure de référence est la gate G3, en J8.

---

## 6. Décisions prises pendant J3 (en plus de D1 à D5)

- **Le code `XXX-YYY` localise et n'authentifie rien** (#61, #64). La découverte ne transporte que l'offre publique : « transfert privé », dossier ou non, port d'appairage. Le code, s'il correspond, est seulement renvoyé à celui qui l'a envoyé.
- **Trois façons d'être admis**, partout : un appareil de confiance passe sans question ; un QR (secret à usage unique) fait confiance des deux côtés, sauf `--once` ; sans QR, la comparaison des deux empreintes sur les deux écrans, avec « non » par défaut et un refus sans réponse. `ID-05` s'applique dans tous les cas.
- **Envoi ponctuel :** le QR de `xfer send` vit 10 minutes et se renouvelle, contre 120 s pour `xfer pair`. Avant l'appairage, l'écouteur de données refuse tout ; après, il n'admet que la clé du destinataire appairé. Rien n'est enregistré, sauf une réponse « always ».
- **Daemon :** l'utilisateur répond par l'API, avec l'événement `trust_request` et l'action `trust_respond` ; le tableau de bord de développement affiche une boîte de confirmation. `testkit.StartPair` déclare les deux daemons de confiance l'un pour l'autre : les tests de transfert gardent leur forme.
- **Code supprimé :** `sendOneShot` / `oneShotDataServer` du daemon (jamais appelés) et `securechan.OpenServerFunc`, qui acceptait n'importe quelle clé.

---

## 7. Réserves et suites

| Sujet | Suite |
|---|---|
| Le nom d'hôte est dans les balises et les réponses de découverte (`DISC-06`) ; la découverte émet en permanence (`DISC-05`) | #65, J6a, avec mDNS et un nom générique `medXfer-XXXX` |
| Le daemon n'a pas été essayé entre deux machines | Le daemon disparaît en J6a ; le cœur en processus sera essayé sur appareils en J6a et J6b |
| Capture réelle par relais, pas par le système | La commande `pktmon` (administrateur) est dans #62 si une capture système est voulue |
| L'application G2a appaire par code | J6a et J6b : `pair_show` (afficher le QR), `pair_uri` (lire le QR avec la caméra), réponse à `trust_request` |
| Durée de vie du QR de `xfer send` codée en dur (10 min) | À rendre réglable (règle R4) lors du passage au cœur en processus (J6a) |
| `CHAN-06` complet : `DATA_JOIN` et un seul écouteur | J4a (WP-4.2) |
| Issues rattachées à J4 : #9, #33 (progression > 100 %, vue aussi sans reprise), #36, #46, #48, #54 | J4a et J4b |
| Gate G1 (PAKE) | Décision au début de J5 ; la revue externe (G4) est à anticiper (§8) |

---

## 8. Suite

**J4a** commence par la **gate G6** : un petit benchmark de hachage sur le téléphone pour choisir entre SHA-256 et BLAKE3. Viennent ensuite les trames bornées, l'écouteur unique avec `DATA_JOIN` (fin des ports par transfert), l'offre unique et les mesures par étape.

Seule décision qui dépend d'une personne et non d'une mesure : **trouver qui fera la revue cryptographique externe** (G4). Elle doit être « contractualisée ou datée » à la sortie de J5.
