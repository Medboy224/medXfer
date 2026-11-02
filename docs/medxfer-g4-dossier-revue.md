# medXfer — Dossier pour la revue cryptographique externe (gate G4)

> Version du 13 octobre 2026, à la fin du jalon J5. Complétée le 2 novembre 2026 (jalon J6d) par le chemin par BLE (§3.7) : protocole codé dans le cœur, pas encore relié à l'API ni aux radios.
> Destinataire : la personne ou le prestataire chargé de la revue externe de l'appairage par PIN. Une version anglaise peut être fournie dès que le prestataire est choisi.
> Périmètre de la revue (décision D8 du plan J5) :
> - `pkg/pake` ;
> - le chemin P2 dans `pkg/pairing` ;
> - la liaison au canal dans `pkg/securechan` ;
> - les chapitres 6 et 7 de la bible technique ;
> - le chemin par BLE (bible §6.6) : `pkg/pairing/ble.go`, et `Session.Key` dans `pkg/pake`.
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
| **P2 et P3 par BLE** | Deux appareils qui ne sont pas sur le même réseau se trouvent par BLE. L'un crée un réseau, l'autre en reçoit le nom et le mot de passe, chiffrés sous une clé tirée du PIN (CPace sans TLS) ou de la clé de la paire. | **Objet de la revue** (§3.7) |

La **comparaison d'empreintes à l'œil**, qui existait aussi, est retirée (lot « Modèle de confiance », `PAIR-12`) : un premier contact passe toujours par P1 ou P2.

## 2. Modèle de menace

| Attaquant | Capacités | Ce qui doit tenir |
|---|---|---|
| A2, homme du milieu sur le LAN | Intercepte, relaie, modifie et injecte du trafic ; ouvre ses propres sessions TLS avec chaque côté | Il ne peut pas faire appairer les deux appareils honnêtes l'un avec l'autre à travers lui, même en relayant les messages tels quels. Sans le PIN, il ne peut s'appairer avec personne. |
| A3, devineur de PIN | Se connecte autant de fois qu'il veut au répondant | Un seul essai de PIN par connexion, aucun essai hors ligne, et un nombre d'essais par heure borné (§4.4) |
| Observateur des journaux | Lit les journaux et le paquet de diagnostic | Aucun PIN, ISK, `ekm` ou partage n'y apparaît |
| A7, appareil à portée radio (BLE) | Écoute, se connecte, relaie et rejoue des messages BLE, hors du réseau local | Il n'obtient pas le mot de passe du réseau, ne s'appaire pas sans le PIN, et ne peut pas suivre un appareil d'un lieu à l'autre (§3.7) |

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

**Usage de l'ISK :** sur TLS, il ne sert qu'à la confirmation. Il n'est ni exporté ni utilisé pour chiffrer, car le transport reste la session TLS. Le PAKE n'apporte que l'authentification mutuelle des clés TLS par le PIN. Sur BLE (§3.7), où il n'y a pas de TLS, il sert aussi à tirer par HKDF les clés qui chiffrent le réseau et les empreintes : c'est son seul nouvel usage.

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

### 3.7 Chemin par BLE (jalon J6d, bible §6.6)

Deux appareils qui ne sont pas sur le même réseau se trouvent par BLE. L'un crée un réseau Wi-Fi (un groupe Wi-Fi Direct, un point d'accès local) ; l'autre en reçoit par BLE le nom et le mot de passe, chiffrés, et le rejoint. La session s'ouvre ensuite en TLS, sur ce réseau. Il n'y a **pas de TLS sous BLE** : c'est la différence avec le reste du dossier.

La spécification complète est au §6.6 de la bible : format, tableau des messages, déroulement, exigences `BLE-01` à `BLE-12`. Sont résumés ici les choix cryptographiques.

**Primitives.** Rien de nouveau : CPace tel que ci-dessus, HMAC-SHA-256, HKDF-SHA-256 (`crypto/hkdf`) et AES-256-GCM (`crypto/cipher`), de la bibliothèque standard de Go. Notations :
- `I` : l'initiateur, client GATT, celui qui touche l'autre appareil ;
- `R` : le répondant, serveur GATT ;
- `K_AB` : la clé de reconnaissance de 32 octets de la paire, échangée dans `trust_commit` (question 10) ;
- `c_I`, `c_R`, `n_I`, `n_R` : 16 octets aléatoires, neufs à chaque échange.

**Reconnaissance.** L'annonce ne porte que l'UUID du service : un PC Windows ne peut rien y mettre d'autre (sonde WP-D.0).
- I envoie un défi `c_I`.
- R répond par 16 étiquettes `HMAC-SHA-256(K_tag, fp_R ‖ c_I)[:8]`, une par appareil de confiance (`K_tag = HKDF(K_AB, "medXfer-ble-v1 tag")`), complétées par des octets aléatoires, dans un ordre aléatoire. Il y joint le suffixe du nom générique, tiré à chaque lancement, et son propre défi `c_R`.

**Appareil de confiance mutuelle** (P3 par BLE) :
- `K_i2r ‖ K_r2i = HKDF-SHA-256(K_AB, sel = c_I ‖ c_R, "medXfer-ble-v1 trusted" ‖ fp_I ‖ fp_R, 64)` ;
- I envoie `HMAC-SHA-256(K_tag, fp_I ‖ c_R)[:8]`, qui ne fait que désigner la paire, et sa demande chiffrée sous `K_i2r` ;
- R répond, chiffré sous `K_r2i`, par le réseau.

Le déchiffrement authentifie chaque côté, sur un défi qu'il a lui-même tiré.

**Appareil inconnu** (P2 par BLE) : CPace en mode initiateur-répondant, avec :
- `PRS` : le PIN ;
- `CI` : `lv_cat("medXfer-ble-pair-v1")`, **sans les empreintes** ;
- `sid` : `n_I ‖ n_R`, échangés juste avant (brouillon §10.9) ;
- AD vides ;
- même confirmation (§3.2), et R confirme le premier.

Puis `K_i2r ‖ K_r2i = HKDF-SHA-256(ISK, sel vide, "medXfer-ble-v1 pin", 64)`. Avec `Ta`, I envoie chiffrés son empreinte et sa demande ; R répond, chiffrés, par son empreinte et le réseau.

**Chiffrement.** AES-256-GCM. Le nonce est le compteur des messages chiffrés sous cette clé, à partir de 0, et ne circule pas ; l'AD est `version ‖ type`. Chaque clé est neuve à chaque échange et ne sert que dans un sens, pour un ou deux messages.

**Sur le Wi-Fi.** Celui qui rejoint le réseau épingle l'empreinte du créateur, comme le QR (P1). Sur le chemin du PIN, il envoie `pair_ble`. Le créateur n'accepte que l'empreinte que BLE vient d'authentifier, une fois, dans les 2 minutes. **Un seul PIN** sert au réseau et à l'appairage, avec **un seul compteur d'essais**, partagé avec le PIN sur TLS : la borne du §3.5 ne change pas.

**Pourquoi les empreintes ne sont pas dans le CI.** Le brouillon préfère les identités dans le CI (§10.1). Mais elles devraient alors circuler en clair avant l'échange, et une empreinte ne change jamais : n'importe qui à portée radio pourrait suivre un appareil d'un lieu à l'autre en demandant un PIN. Elles circulent donc chiffrées, juste après la confirmation, sous une clé que seul un côté qui avait le PIN peut tirer de l'ISK.

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

**Seule modification de `pkg/pake` depuis la version relue (J6d) :** la méthode `Session.Key(info, n)`, soit `HKDF-SHA-256(ISK, sel vide, info, n)` (`crypto/hkdf`), pour le chemin par BLE (§3.7). Le reste du paquet est inchangé, remarques du point 11 comprises.

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

**Chemin par BLE (§3.7)**, `pkg/pairing/ble_test.go` et `pkg/pake/pake_test.go` :

| Test | Vérifie |
|---|---|
| `TestKey` | Les deux côtés tirent la même clé de l'ISK ; une autre pour un autre usage, une autre exécution, un mauvais PIN |
| `TestBLETrusted`, `TestBLETrustOneSided` | Reconnaissance et demande entre appareils de confiance mutuelle ; refus quand un côté a oublié l'autre (`PAIR-09`) |
| `TestBLEHelloTags` | Toujours 16 étiquettes, neuves à chaque défi, avec 0, 1 ou 17 appareils de confiance ; un inconnu n'y reconnaît rien |
| `TestBLEReplay` | Une demande enregistrée refusée dans un autre échange, et après la minute du défi |
| `TestBLEPIN`, `TestBLEWrongPIN`, `TestBLEDecoy` | Appairage par le PIN, PIN consommé ; 3 mauvais PIN détruisent le PIN ; PIN expiré joué sur un PIN factice, indiscernable |
| `TestBLEAndTLSShareAttempts` | **Un seul compteur** : 2 mauvais PIN sur TLS et 1 par BLE détruisent le PIN |
| `TestBLERelayChangesNonce`, `TestBLETampered` | Un relais qui change un nonce casse la confirmation ; un message modifié, coupé, allongé ou d'un autre type est refusé |
| `TestBLENothingStableInClear` | Aucune empreinte, aucun nom ni mot de passe de réseau en clair dans les messages |
| `TestBLEOnePINRequest`, `TestBLEStalePINRequest` | Une seule demande de PIN à la fois ; « Refuser » ; une connexion perdue ne garde pas l'écran |
| `FuzzBLEReceive` | Messages quelconques, dans n'importe quel ordre : jamais de panique, jamais de demande authentifiée. Environ 1,5 million d'exécutions en 60 s, sans défaut trouvé. |

Restent à écrire avec l'API : `pair_ble` sur TLS, et l'absence de secret BLE dans les journaux.

**Reproduire :** `go test ./pkg/pake ./pkg/pairing ./pkg/securechan ./pkg/api` ; `go test -run XXX -fuzz FuzzRespondPIN ./pkg/pairing` ; `go test -run XXX -fuzz FuzzBLEReceive ./pkg/pairing`.

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
10. **Étiquettes de reconnaissance** (bible `DISC-11`). Une fois la confiance mutuelle établie, chaque appareil annonce `HMAC-SHA-256(K_AB, label ‖ fp ‖ période de 10 min)[:8]`, avec une clé de 32 octets échangée dans `trust_commit`, donc dans un canal déjà authentifié. HMAC vient de la bibliothèque standard de Go. Le but : qu'un appareil de confiance reconnaisse l'autre dans la découverte, sans qu'un tiers puisse relier deux annonces. **8 octets et 10 minutes vous paraissent-ils suffisants contre le suivi ?**
11. **Remarques de l'audit externe du 18 octobre 2026** (#156). Elles portent sur `pkg/pake`, que nous n'avons pas modifié, pour que le code relu soit celui de ce dossier. Seule la variable inutilisée `errInvalidElement` a été retirée.
    - Le champ `finished` de `cpace` n'est pas protégé par un verrou : un échange est utilisé par une seule goroutine, mais rien ne l'impose.
    - `Session` garde l'ISK (champ `isk`) après le calcul de `mac_key` ; seuls les tests le lisent.
    - Les fonctions du mode « initiateur-répondeur » et du mode « ordonné » (`transcriptOC`, `oCat`, `lexLarger`) sont compilées dans le binaire, alors que medXfer n'utilise que le premier ; les autres ne servent qu'aux vecteurs de test.

    **Faut-il corriger l'un de ces points avant la publication ?**
12. **CPace sans TLS, empreintes hors du CI** (§3.7). Le CI ne porte qu'une étiquette, le `sid` est fait des deux nonces, et les empreintes passent ensuite chiffrées sous une clé tirée de l'ISK. **Cela donne-t-il la même garantie que des empreintes dans le CI, contre un homme du milieu et contre un partage de clé avec un tiers ?**
13. **Clés tirées de l'ISK.** `mac_key = SHA-512("CPaceMac" ‖ sid ‖ ISK)` pour la confirmation, et `HKDF-SHA-256(ISK, "medXfer-ble-v1 pin")` pour le chiffrement. **Cette séparation des clés vous paraît-elle suffisante ?**
14. **Chemin de confiance sans confidentialité persistante.** Les clés sont tirées de `K_AB`, statique, et des deux défis. Qui obtiendrait `K_AB` plus tard lirait le mot de passe d'un réseau enregistré, déjà arrêté et jamais réutilisé. **Faut-il ajouter un échange X25519 (`crypto/ecdh`, bibliothèque standard) ?**
15. **AES-GCM à nonce compté et implicite**, sous des clés neuves à chaque échange, un sens par clé. **Une objection ?**
16. **Suivi par BLE.** Un inconnu voit l'UUID tant que l'application cherche ou attend un appareil, et le suffixe tiré à chaque lancement : il peut relier les annonces d'un appareil pendant un lancement, pas au-delà. Les étiquettes sont toujours 16, tronquées à 8 octets, et ne servent qu'à désigner une clé. **Ce niveau vous paraît-il acceptable ?**
17. **Demande de PIN par radio.** La question 9 s'étend à toute personne à portée Bluetooth, hors du réseau. L'appareil montre la demande avec le suffixe du demandeur et un bouton « Refuser », une demande à la fois. **Faut-il une limite de plus ?**

## 7. Références

- [draft-irtf-cfrg-cpace-21](https://datatracker.ietf.org/doc/draft-irtf-cfrg-cpace/)
- [RFC 9496](https://www.rfc-editor.org/rfc/rfc9496) (ristretto255) ; [RFC 8446 §7.5](https://www.rfc-editor.org/rfc/rfc8446#section-7.5) (exporteurs TLS) ; [RFC 5869](https://www.rfc-editor.org/rfc/rfc5869) (HKDF, chemin par BLE)
- Plan du jalon J6d, avec la sonde BLE : [medxfer-plan-j6d.md](medxfer-plan-j6d.md)
- Bible technique, chapitres 6 (appairage) et 7 (canal) : [medxfer-bible-technique-v8.md](medxfer-bible-technique-v8.md)
- Veille de la gate G1 : [medxfer-g1-pake.md](medxfer-g1-pake.md)
- Plan du jalon J5 : [medxfer-plan-j5.md](medxfer-plan-j5.md)
