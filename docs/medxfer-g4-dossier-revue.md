# medXfer — Dossier pour la revue cryptographique externe (gate G4)

> Version du 13 octobre 2026, à la fin du jalon J5.
> Destinataire : la personne ou le prestataire chargé de la revue externe de l'appairage par PIN. Une version anglaise peut être fournie dès que le prestataire est choisi.
> Périmètre de la revue (décision D8 du plan J5) :
> - `pkg/pake` ;
> - le chemin P2 dans `pkg/pairing` ;
> - la liaison au canal dans `pkg/securechan` ;
> - les chapitres 6 et 7 de la bible technique.
>
> La revue conditionne la **publication**, pas la fusion du code.

## 1. Ce que fait le système

medXfer transfère des fichiers entre appareils d'un même réseau local. Avant le premier transfert, deux appareils s'appairent : cela ouvre une **session**. Ils ne gardent l'empreinte de la clé de l'autre que si les deux utilisateurs le demandent (confiance mutuelle, bible §6.5). Les connexions suivantes l'épinglent alors.

Chemins d'appairage (bible §6) :

| Chemin | Principe | Revue |
|---|---|---|
| P1, QR | Le QR porte l'empreinte du montreur et un secret de 128 bits. La caméra sert de canal authentifié. | Hors périmètre : pas de PAKE |
| **P2, PIN** | Le répondant affiche un PIN de 6 chiffres, que l'initiateur saisit. Un PAKE lié au canal TLS prouve la connaissance commune du PIN. Variante « PIN à la demande » : l'appareil choisi dans une liste affiche le PIN quand on le lui demande (§3.6). | **Objet de la revue** |
| P3, confiance mutuelle | Les deux appareils ont déjà la clé de l'autre, et chacun le déclare dans le canal. | Hors périmètre : pas de PAKE |

La **comparaison d'empreintes à l'œil**, qui existait aussi, est retirée (lot « Modèle de confiance », `PAIR-12`) : un premier contact passe toujours par P1 ou P2.

## 2. Modèle de menace

| Attaquant | Capacités | Ce qui doit tenir |
|---|---|---|
| A2, homme du milieu sur le LAN | Intercepte, relaie, modifie et injecte du trafic ; ouvre ses propres sessions TLS avec chaque côté | Il ne peut pas faire appairer les deux appareils honnêtes l'un avec l'autre à travers lui, même en relayant les messages tels quels. Sans le PIN, il ne peut s'appairer avec personne. |
| A3, devineur de PIN | Se connecte autant de fois qu'il veut au répondant | Un seul essai de PIN par connexion, aucun essai hors ligne, et un nombre d'essais par heure borné (§4.4) |
| Observateur des journaux | Lit les journaux et le paquet de diagnostic | Aucun PIN, ISK, `ekm` ou partage n'y apparaît |

**Hors modèle :**
- un attaquant qui **connaît le PIN** : il s'appaire avec chaque côté en son propre nom, avec sa propre clé. C'est inhérent à tout PAKE ;
- un appareil compromis ;
- la sécurité du chemin P1.

## 3. Construction

### 3.1 Canal

- **TLS 1.3 uniquement**, authentification mutuelle :
  - certificats auto-signés, clé ECDSA P-256 par appareil ;
  - empreinte = SHA-256 du `SubjectPublicKeyInfo` ;
  - ALPN `medxfer/1` ;
  - tickets de session désactivés (`SessionTicketsDisabled`), donc pas de reprise de session.
- **Clé inconnue :** la connexion est acceptée en état `UNAUTH`, avec 4 Kio lus au plus et une durée de vie de 30 s. Seuls les messages `pair_*` y passent.
- **Liaison au canal :** `ekm = ExportKeyingMaterial("EXPORTER-medxfer-v1-pair", nil, 32)` (RFC 8446 §7.5), calculé par chaque côté sur sa propre session TLS. Code : `securechan.Conn.ChannelBinding`.

### 3.2 PAKE : CPace `CPACE-RISTR255-SHA512` (draft-irtf-cfrg-cpace-21)

Mode initiateur-répondant. A est l'initiateur, qui saisit le PIN ; B est le répondant, qui l'affiche.

| Entrée | Valeur dans medXfer |
|---|---|
| `PRS` | Le PIN en chiffres ASCII, après normalisation (espaces et tirets retirés), 6 à 8 chiffres |
| `CI` | `lv_cat("medXfer-pair-v1", fp_A, fp_B)` : empreintes de 32 octets lues dans la session TLS, initiateur en premier (brouillon §10.1) |
| `sid` | `ekm` (32 octets), propre à la session TLS |
| `ADa`, `ADb` | Vides |
| Groupe | ristretto255 (RFC 9496), via `github.com/gtank/ristretto255` v0.2.0, au-dessus de `filippo.io/edwards25519` v1.1.0 |

**Confirmation de clé** (brouillon §10.4) :
- `mac_key = SHA-512("CPaceMac" ‖ sid ‖ ISK)` ;
- `Ta = HMAC-SHA-512(mac_key, lv_cat(Ya, ADa))` et `Tb = HMAC-SHA-512(mac_key, lv_cat(Yb, ADb))` ;
- chaque tag est comparé en temps constant (`hmac.Equal`).

**Usage de l'ISK :** il ne sert qu'à la confirmation. Il n'est ni exporté ni utilisé pour chiffrer, car le transport reste la session TLS. Le PAKE n'apporte que l'authentification mutuelle des clés TLS par le PIN.

### 3.3 Messages (`pkg/pairing`)

Tous circulent dans la session TLS, en état `UNAUTH` :

1. A → B : `pair_pin {suite: "CPACE-RISTR255-SHA512", share: Ya}`
2. B → A : `pair_pin_reply {share: Yb, confirm: Tb}`
3. A → B : `pair_pin_confirm {confirm: Ta, device_name}`, envoyé **seulement si `Tb` est bon**
4. B → A : `pair_accept {device_name, …}`, sinon `pair_reject` avec un motif neutre

Ensuite :
- les deux côtés passent en état `AUTH` ;
- la session est ouverte ; rien n'est enregistré (`PAIR-08`). Une confiance durable passe par la demande de confiance (§3.6).

Les noms des appareils ne circulent qu'après la confirmation : A envoie le sien au message 3, B le sien au message 4.

**Ordre des confirmations (décision D3) :** le répondant confirme le premier. Un initiateur malveillant obtient donc `Tb`, qui lui permet de tester **un** PIN, celui dont il a dérivé `Ya`. La connaissance de `Ya = ya·g'` ne vaut que pour un seul générateur `g'`. C'est la propriété de CPace : une connexion, un essai.

### 3.4 Gestion du PIN (`pairing.PIN`)

- **Tirage :** `crypto/rand.Int(10^n)`, uniforme, sans biais modulo (`PAIR-01`).
- **Validité :** 120 s, pour un seul appairage réussi (`PAIR-02`).
- **Comptage des essais :** un essai est **réservé avant la réponse** du répondant et compte dès l'envoi de `pair_pin_reply`, que l'initiateur confirme ensuite ou non. Des connexions simultanées ne peuvent pas obtenir plus de 3 essais sur un même PIN.
- **Destruction :** après 3 essais ratés, le PIN est détruit et un nouveau est affiché.
- **Délai :** après une série de 3 PIN détruits d'affilée, un délai de 30 s s'applique, doublé à chaque série et plafonné à 10 min. Un appairage réussi le remet à zéro (`PAIR-04`, D5).
- **Arrêt :** après **2 séries** de 3 PIN détruits d'affilée, le PIN est verrouillé et l'attente du répondant s'arrête (`ErrTooManyPINs`), y compris pour un QR offert en même temps. L'utilisateur doit relancer l'appairage (#105).
- **Aucun essai disponible** (PIN épuisé, délai en cours, PIN déjà utilisé, PIN expiré) : le répondant **joue l'échange sur un PIN factice** tiré au hasard. L'initiateur reçoit `pair_pin_reply` puis `pair_reject`, exactement comme pour un mauvais PIN (`PAIR-07`).
- **Refus immédiats, sans essai compté :** une suite inconnue, ou un PIN qui n'est pas offert sur cette écoute. Ils ne révèlent rien du PIN.

### 3.5 Borne sur la devinette en ligne

Chaque essai porte sur un PIN tiré à neuf, avec une probabilité de succès de 10⁻⁶ pour 6 chiffres.

Une attente du répondant (un `xfer pair --pin`, un `xfer send --pin`, ou un « Show a PIN ») admet au plus 2 séries × 3 PIN × 3 essais = **18 essais**, soit une probabilité de succès de **1,8 × 10⁻⁵ au plus**. Au-delà, l'attaquant a besoin que l'utilisateur relance l'appairage, ce que les messages d'arrêt découragent de faire sans réfléchir.

### 3.6 PIN à la demande et confiance mutuelle (lot « Modèle de confiance »)

Ces ajouts n'apportent **aucune cryptographie** : ce sont des messages dans un canal TLS déjà établi, avant ou après le PAKE inchangé.

- **PIN à la demande** (`PAIR-11`) : A envoie `pair_pin_wanted` ; B affiche un PIN et répond `pair_pin_ready` ; l'échange P2 suit sur la **même** connexion, à partir de `pair_pin`. Tant que le PIN est valable, chaque nouvelle demande reçoit le même PIN, avec ses essais et ses délais : la borne du §3.5 ne change pas. Après les 2 séries de PIN détruits, aucune demande ne fait plus tirer de PIN tant que l'utilisateur ne relance pas.
- **Confiance mutuelle** (`PAIR-09`, `PAIR-10`) : dans une session authentifiée par P1, P2 ou P3, `trust_offer` → `trust_accept` (ou `trust_decline`) → `trust_commit`. Chaque côté n'écrit la clé de l'autre qu'après son propre oui : l'offrant à l'envoi du commit, l'autre à sa réception. Un `trust_accept` ou un `trust_commit` non sollicité est ignoré. Le chemin P3 exige en plus que chaque côté déclare, à chaque session, avoir l'autre dans sa liste (`trusts_you`).

## 4. Correspondance avec le brouillon 21

| Brouillon | Code |
|---|---|
| A.1.1 `prepend_len` (LEB128), A.1.3 `lv_cat` | `pkg/pake/cpace.go` : `prependLen`, `lvCat`, `LVCat` |
| A.2 `generator_string`, avec un bourrage de zéros pour `s_in_bytes = 128` | `generatorString` |
| §8.3 `calculate_generator` : `element_derivation(SHA-512(gen_str))` | `calculateGenerator`, puis `SetUniformBytes` (RFC 9496 §4.3.4), dans `group.go` |
| §8.3 tirage du scalaire : 32 octets, bits au-delà du 252ᵉ mis à zéro, scalaire nul refusé | `sampleScalar` (`group.go`) |
| §7.2 `scalar_mult_vfy`, refus si `K = G.I` ; part de longueur ≠ 32 refusée | `scalarMultVfy`, `cpace.Finish` |
| A.3.4 `transcript_ir`, §7.2 `ISK = H(lv_cat(DSI‖"_ISK", sid, K) ‖ transcript)` | `transcriptIR`, `iskOf` |
| §10.4 confirmation | `cpace.Finish` (`mac_key`), `tag`, `Session.Verify` |

**Écarts connus avec le brouillon :** aucun.
- Le `sid` vient du protocole englobant (exporteur TLS) au lieu d'être tiré par l'un des côtés. Le brouillon le permet ; le `sid` est ici unique par session TLS.
- Les AD sont vides. Les identités passent par le CI.

**Seul fichier dépendant de la bibliothèque de groupe :** `pkg/pake/group.go` (décision D9). Toute la logique du protocole est écrite dans `cpace.go`, sans arithmétique de corps ni de groupe (`PAIR-06`, reformulée à la gate G1).

## 5. Tests

| Fichier | Vérifie |
|---|---|
| `pkg/pake/pake_test.go` : `TestDraftAppendixA` | Annexe A : fonctions de chaîne et transcriptions |
| `pkg/pake/pake_test.go` : `TestDraftAppendixB3`, `TestDraftScalarMultVfy` | Annexe B.3 **octet pour octet** : générateur, Ya, Yb, K, ISK (IR et OC), `sid_output` ; entrées invalides de B.3.11. Vecteurs extraits automatiquement du texte du brouillon (`vectors_b3_test.go`). |
| `pkg/pake/xcheck_test.go` | **Comparaison avec une autre implémentation**, `the-sarge/cpace` v0.1.3, calculée une seule fois sur 32 entrées aléatoires (longueurs variées, AD vides) : générateur, parts, K, ISK, **Ta et Tb** identiques. Plus les tags des entrées B.3, que le brouillon ne donne pas. Aucune dépendance (D7). |
| `pkg/pake/pake_test.go` : `TestConfirmation`, `TestBadShares`, `TestErrorsHoldNoSecret`, `FuzzFinish` | Mauvais PIN, autre `ekm`, autres identités, même rôle deux fois, parts altérées ou trop courtes, erreurs sans secret, fuzzing des parts |
| `pkg/securechan/channelbinding_test.go` | Même `ekm` aux deux bouts ; deux connexions, deux valeurs ; à travers un relais, deux valeurs différentes |
| `pkg/pairing/pin_test.go` | Appairage complet ; mauvais PIN ; destruction après 3 échecs (4ᵉ essai refusé, même avec l'ancien PIN exact) ; PIN épuisé indiscernable d'un mauvais PIN ; essai abandonné compté ; **relais MITM** (refusé des deux côtés, essai compté) ; suite inconnue ; expiration ; délai doublé et plafonné ; essais réservés ; tirage et saisie |
| `pkg/pairing/pin_fuzz_test.go` : `FuzzRespondPIN` | Suite, part et trame de confirmation quelconques : jamais de panique, jamais d'appairage. Environ 100 000 exécutions en 90 s, sans défaut trouvé. |
| `pkg/api/pin_test.go`, `pkg/api/pin_secrets_test.go` | Appairage entre deux daemons ; échec sans détail ; **ni PIN ni blob de taille de clé** dans le journal et les événements, après un essai faux puis un bon (vérifié par mutation) |
| `cmd/xfer/pair_test.go`, `cmd/xfer/oneshot_test.go` | Ligne de commande et envoi ponctuel par PIN |

**Essai réel du 13 octobre 2026**, entre un PC Windows et un téléphone Android (Termux), en Wi-Fi :
- appairage dans les deux sens ;
- 3 PIN faux, puis un nouveau PIN, l'ancien PIN exact étant refusé ;
- envoi ponctuel par PIN dans les deux sens, fichiers identiques au SHA-256 près.

**Reproduire :** `go test ./pkg/pake ./pkg/pairing ./pkg/securechan ./pkg/api` ; `go test -run XXX -fuzz FuzzRespondPIN ./pkg/pairing`.

## 6. Limites connues et questions pour la revue

1. **Fenêtre d'appairage bornée.** Chaque PIN tiré à nouveau repousse la fin de l'attente, mais l'attente s'arrête après 2 séries de PIN détruits (#105, §3.4). **Ce plafond de 18 essais par attente vous paraît-il suffisant ?**
2. **Ordre des confirmations.** Le répondant confirme le premier (§3.3). **Confirmez-vous qu'il ne donne rien de plus qu'un essai en ligne par connexion ?**
3. **PIN factice.** Sans essai disponible, le calcul est le même (CPace complet sur un PIN aléatoire). Seules quelques différences de temps restent possibles, liées à l'état interne : un verrou et l'accès au PIN. **Une différence de temps mesurable révélerait-elle l'état du compteur ?**
4. **Effacement des secrets.** Le scalaire, l'ISK et `mac_key` ne sont pas effacés explicitement : Go ne le garantit pas, à cause du ramasse-miettes et des copies. **Est-ce acceptable pour des secrets éphémères de ce type ?**
5. **Noms d'appareils.** Ils ne sont pas authentifiés au-delà de la clé. Ce sont des libellés, que l'appareil distant pourrait fixer à n'importe quelle valeur, y compris le nom d'un autre appareil. Seule l'empreinte affichée identifie l'appareil.
6. **Limitation des connexions non authentifiées (LIM-01)** : 16 simultanées, 5 nouvelles par minute et par IP. Question ouverte côté produit : issue #93.
7. **Brouillon, pas encore RFC.** Le brouillon 21 attend l'éditeur RFC. Une révision des vecteurs à la publication serait vérifiée par les mêmes tests.
8. **Bibliothèque de groupe.** `gtank/ristretto255` est épinglée dans `go.sum`. Sa revue est hors périmètre, mais **un avis sur ce choix est bienvenu**.
9. **PIN à la demande** (§3.6). N'importe quel appareil du réseau peut faire afficher un PIN sur un appareil qui écoute. Il n'obtient pas d'essai de plus (même PIN tant qu'il est valable, borne du §3.5), mais il peut le faire apparaître à l'écran sans y avoir été invité. **Ce comportement, inspiré de l'association par saisie de code du Bluetooth, vous paraît-il acceptable ?**

## 7. Références

- [draft-irtf-cfrg-cpace-21](https://datatracker.ietf.org/doc/draft-irtf-cfrg-cpace/)
- [RFC 9496](https://www.rfc-editor.org/rfc/rfc9496) (ristretto255) ; [RFC 8446 §7.5](https://www.rfc-editor.org/rfc/rfc8446#section-7.5) (exporteurs TLS)
- Bible technique, chapitres 6 (appairage) et 7 (canal) : [medxfer-bible-technique-v8.md](medxfer-bible-technique-v8.md)
- Veille de la gate G1 : [medxfer-g1-pake.md](medxfer-g1-pake.md)
- Plan du jalon J5 : [medxfer-plan-j5.md](medxfer-plan-j5.md)
