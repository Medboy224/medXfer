# medXfer — Rapport de sortie du jalon J6a (cœur en-processus et découverte)

**Date :** 15 octobre 2026 · **Plan :** [medxfer-plan-j6a.md](medxfer-plan-j6a.md) · **Bible :** chapitre 5 (`DISC-01` à `DISC-10`), chapitre 10 (`API-01` à `API-08`, règles B1 à B11) · **PR :** #114 à #130, et celle de ce rapport.

Ce rapport répond au critère de sortie de la feuille de route : *tous les tests passent par l'API en-processus ; le daemon et son WebSocket ne servent plus qu'à `devui`*. Il couvre aussi les cinq critères du plan (§3).

---

## 1. Verdict

**J6a est terminé. Les cinq critères du plan sont remplis** (§2).

medXfer est désormais une **bibliothèque** :
- l'interface la démarre, l'appelle, lit ses événements et l'arrête, sans socket de contrôle ;
- la bibliothèque C (`libmedxfer`) que chargera l'application Flutter est vérifiée à travers la vraie frontière C sous Windows, Android et Linux ;
- les appareils se trouvent par **mDNS**, seulement quand l'utilisateur le demande.

| Avant J6a | Après J6a |
|---|---|
| Les gestionnaires répondaient sur un `*websocket.Conn` | Une interface `Responder` ; le transport n'est plus qu'un détail |
| Tous les tests d'intégration passaient par le WebSocket du daemon | Tous passent **en-processus** (`testkit`, `xfer ctl`) ; un test l'impose |
| La frontière C n'existait que dans le prototype jetable de G2a | `cmd/libmedxfer`, 5 fonctions sur `pkg/core`, compilées pour Windows, Android et Linux, testées par `smoke.c` |
| Une balise toutes les 600 ms, en permanence, vers `239.255.255.250` (l'adresse de SSDP), avec le nom d'hôte | mDNS `_medxfer._tcp`, plus une balise de repli en diffusion toutes les 2 s, **sur demande**, avec un nom générique |
| Le binaire publié ouvrait une surface de contrôle (`/ws`, `/status`) | Elle n'existe qu'avec `devui`, comme le daemon |

**Réserves** (détail au §7) :
1. L'application Flutter, le lecteur Dart (B8, B10) et le service de premier plan Android (`API-08`) sont pour J6b.
2. Sous Windows, il faut une règle de pare-feu UDP 5353 pour être trouvé (J8).
3. La balise de repli n'a été vérifiée seule qu'en boucle locale ; sur le réseau réel, c'est mDNS qui a été isolé.

---

## 2. Critères de sortie (plan §3)

| # | Critère | Résultat | Preuve |
|---|---|---|---|
| 1 | Tous les tests d'intégration passent par l'API en-processus ; seuls les tests du transport `devui` ouvrent un WebSocket, et un test le vérifie | ✅ | #121 : `testkit` et `xfer ctl` sur `wsclient.Local` ; `TestNoTestOpensAControlWebSocket`, vérifié par mutation |
| 2 | La bibliothèque `c-shared` se compile pour Windows, Linux et Android ; ses 5 fonctions sont testées ; Linux dans la CI | ✅ | #122 : `smoke.c` réussit 7 vérifications sur 7 sous Windows (DLL), Android (téléphone) et Linux (CI et Zorin) |
| 3 | Découverte mDNS entre le PC et le téléphone, dans les deux sens, uniquement sur demande ; repli UDP vérifié | ✅ | #123, #125 et l'essai réel du §5 ; repli en boucle locale (`TestBeaconAnnounceAndBrowse`) |
| 4 | Le binaire de publication n'a plus de socket de contrôle ; le daemon et son WebSocket n'existent qu'avec `devui` | ✅ | #126 : `TestControlSurfaceAbsentWithoutDevUI` ; `check-prod-binary.sh` refuse `handleWebSocket` et `handleHTTPStatus` |
| 5 | Bible à jour : statuts réels de `DISC-01`, `DISC-04`, `DISC-05` et `API-01` à `API-08` | ✅ | #123, #125, puis ce rapport |

---

## 3. Ce qui a été livré

| PR | Lot | Contenu |
|---|---|---|
| #114 | Plan | Plan J6a, décisions D1 à D8 |
| #115 | Doc | Guide d'utilisation de la ligne de commande |
| #116 | WP-6.1 | Interface `Responder` dans les 32 gestionnaires |
| #117, #118 | Test | Seuil de l'offre « instantanée » (test instable) |
| #119 | WP-6.0 | Veille mDNS : `hashicorp/mdns`, interface par index, pare-feu, IANA |
| #120 | WP-6.2 | `pkg/core` : `New`, `Start`, `Call`, `NextEvent`, `Stop` ; file bornée ; schéma v1 figé |
| #121 | WP-6.3 | `testkit` et `xfer ctl` en-processus ; garde-fou contre le WebSocket |
| #122 | WP-6.4 | Bibliothèque `libmedxfer`, scripts de compilation, `smoke.c`, CI |
| #123 | WP-6.5a | mDNS et balise de repli en diffusion |
| #124 | Doc | Rendre un PC Windows visible (pare-feu) |
| #125 | WP-6.5b | Annonce sur demande, `set_network_info`, nom affiché, appareils de confiance reconnus |
| #126 | WP-6.6 | Surface de contrôle et daemon réservés à `devui` |
| #127, #128 | Test | `TestDaemonAnnouncesNoName` attendait le mauvais `status` (test instable) |
| #129, #130 | Correctif | L'émetteur ne recevait presque jamais `transfer_complete` (trouvé par l'essai réel) |

---

## 4. Exigences et tests

| Exigence | Tests | Statut |
|---|---|---|
| `API-01`, `API-03` | `TestTwoCoresPairByPIN` (deux appareils, aucun socket de contrôle) ; `TestControlSurfaceAbsentWithoutDevUI` | ✅ |
| `API-02` | `TestCallReplies`, `TestHandlersAnswerWithoutWebSocket`, `TestLocalClient` | ✅ |
| `API-04` | Les sept contrôles, dans `control_guard_devui_test.go` (inchangés) | ✅ |
| `API-06` | `TestSchemaIsFrozen` : 37 actions et 51 événements, vérifié par mutation | ✅ |
| `API-07` | B4, B5, B9, B11 : `TestStartIsIdempotent`, `TestPanicBecomesAnError`, `TestStopWakesReadersWithSentinel`, `TestQueueBoundAndProgressRate` ; B1 à B3 par `smoke.c` | ✅ (B8, B10 : J6b) |
| `DISC-01`, `DISC-04` | `TestMDNSAnnounceAndBrowse` (réseau réel), `TestBeaconAnnounceAndBrowse`, `TestBrowseAllMerges` | ✅ |
| `DISC-05` | `TestDiscoverableOnDemand` et l'essai réel du §5 | ✅ |
| `DISC-06` | `TestAnnouncementNamesNoDevice`, `TestDaemonAnnouncesNoName` | ✅ |
| `DISC-07` | `instance` dans `status`, `pair_show` et `pair_pin_shown` ; `TestScanRecognizesTrustedDevices`, `TestTrustedAt` | ✅ |
| `DISC-10` | `TestSetNetworkInfo`, `TestSetNetworkInfoAction` | ✅ |

---

## 5. Essais réels (15 octobre 2026)

PC Windows (`10.86.69.174`), téléphone Android sous Termux (`10.86.69.197`, Wi-Fi et données mobiles actifs), PC Zorin sous Linux (`10.86.69.91`).

### 5.1 Découverte

| Essai | Résultat |
|---|---|
| mDNS en-processus sur Zorin, puis sur le téléphone | ✅ |
| Le téléphone lance `xfer send` ; Zorin et le PC le trouvent | ✅ `role=sender`, en 3 s |
| Le PC lance `xfer send` ; Zorin et le téléphone le trouvent (règle UDP 5353 posée) | ✅ ; une recherche mDNS **seule** depuis Zorin le voit aussi |
| **`DISC-05`** : le téléphone démarre silencieux 20 s, puis `set_discoverable` | ✅ le PC voit **0 appareil**, puis **1** (`medXfer-3bf2`, rôle `node`) |
| Le PC se cherche lui-même par mDNS | ❌ multicast en boucle locale sous Windows, sans usage pratique |

### 5.2 Le chemin de la bibliothèque, entre appareils

Une sonde temporaire, non commitée, pilote medXfer par `internal/cabi`, le code exact de `medxfer_start`, `medxfer_call` et `medxfer_next_event`, sans la couche C, vérifiée à part par `smoke.c`.

Le téléphone affiche un PIN, l'autre appareil le saisit, puis ils enchaînent **20 transferts de 64 Mo** dans la même session.

| Sens | Durée par transfert | Mémoire du téléphone (RSS) |
|---|---|---|
| Téléphone → PC | 5,3 à 5,9 s (environ 12 Mo/s) | 16 Mo au départ ; **plafond vers 67 Mo dès le 3ᵉ transfert, sans dérive** |
| Zorin → téléphone | 6,4 à 11,7 s | 16 Mo au départ ; **plafond vers 50 Mo**, sans dérive |

- **Pas de fuite** sur 20 transferts : le tas revient à environ 4 Mo après un GC.
- Sous Android, `debug.FreeOSMemory` ne fait pas baisser la RSS ; sous Linux, si (63 → 12 Mo).
- **Décision (G2A §7.7) :** pas de `debug.SetMemoryLimit` par défaut en J6b. Le prototype de G2a accumulait 200 Mo entre deux GC ; le vrai cœur plafonne à 50-67 Mo. À remesurer avec l'application.
- **Un transfert sur 40 a échoué, par la faute de la sonde** : elle vidait le dossier de réception dès la fin d'un transfert, alors que l'offre suivante était déjà acceptée et en cours d'écriture (« `openat z64.bin.part.state: no such file or directory` »). Il y a une leçon pour l'interface : **une nouvelle offre peut arriver avant l'événement `transfer_complete` du transfert précédent**, et elle doit l'accepter à tout moment.

### 5.3 Défaut trouvé : #129

L'essai a révélé que **l'émetteur ne recevait presque jamais `transfer_complete`**. Son moteur s'arrêtait dès la fermeture des connexions de données, avant l'arrivée du message `complete` du receveur, qui était alors ignoré. Corrigé par #130, avec un test de 10 transferts de suite, vérifié par mutation. Le défaut existait avant J6a ; l'essai réel du côté émetteur l'a fait apparaître.

---

## 6. Décisions prises pendant J6a (en plus de D1 à D8)

| Décision | Raison |
|---|---|
| **D6 révisée (validée le 15 octobre) :** balise de repli en diffusion, sans groupe multicast | Les adresses de `224.0.0.0/24` s'attribuent par l'IANA ; `239.255.255.250` est celle de SSDP |
| **Le TXT mDNS porte le rôle** (`r=sender\|node`) en plus de `v=1` | `xfer recv` ne liste que les émetteurs ; le rôle n'identifie personne |
| **Le multicast sort par l'interface de l'adresse annoncée, désignée par son index** (`SIOCGIFINDEX`, sans netlink) | Sinon, un téléphone avec les données mobiles cherche par le modem (veille §3.2) |
| **La couche C est compilée seulement avec le tag `libmedxfer`** ; la logique est dans `internal/cabi`, en Go pur | `go build ./...` doit marcher sur toute machine, y compris sans compilateur C |
| **Le transport local de `wsclient` traite les requêtes une à une, dans une goroutine** | Même sémantique que le WebSocket : un gestionnaire qui attend ne bloque pas l'appelant |
| **Le cœur démarre silencieux** ; le daemon `devui` s'annonce tant qu'il sert | `DISC-05` pour l'application, sans changer l'outil de développement |
| **La liste d'interfaces de la plateforme fait foi** : une route par défaut absente de cette liste est ignorée | Défaut trouvé par le test de `set_network_info` |

---

## 7. Réserves et suites

1. **J6b** :
   - l'application Flutter ;
   - le lecteur d'événements Dart, en isolat de fond, par lots, avec arrêt par la sentinelle `core_stopped` (B8, B10) ;
   - le service de premier plan Android (`API-08`) ;
   - l'adaptateur réseau Kotlin qui appelle `set_network_info` ;
   - `MulticastLock` pendant la découverte (`DISC-03`).
2. **Pare-feu Windows (J8)** : l'installateur pose les règles (UDP 5353, UDP 19998, TCP 18887 et 18888), et l'application vérifie au lancement qu'elles existent. Le guide d'utilisation l'explique déjà.
3. **Balise de repli** : vérifiée seule en boucle locale. Sur le réseau réel, `DiscoverPeers` a trouvé les appareils, sans isoler le mécanisme, mais mDNS seul a été isolé.
4. **L'application doit accepter une offre à tout moment**, même avant le `transfer_complete` précédent (§5.2).
5. **Mesures à refaire avec l'application** (G2A §7.9) : mémoire et débit, et un Android plus ancien (10 ou 11).

---

## 8. Suite

**J6b, l'application Flutter (MVP)** (feuille de route) : liste des appareils, appairage par QR et par PIN, envoi, réception, progression, panneau développeur, service de premier plan Android, et empaquetage Windows et Linux.
