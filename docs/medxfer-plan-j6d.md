# medXfer — Plan du jalon J6d : se connecter au réseau par BLE, groupe Wi-Fi Direct à 5 GHz

> **Source :** feuille de route, J6d ; rapport [j6c-report.md](j6c-report.md) (groupe Wi-Fi Direct à 5 GHz sondé, décision C7) ; bible `PAIR-11`, `PAIR-12`, `DISC-11`, `DISC-12`, §6.2.
>
> **Objectif :** deux appareils qui ne sont pas sur le même réseau se trouvent par **Bluetooth Low Energy**. L'un crée le réseau, l'autre le **rejoint seul**. Personne ne tape de nom de réseau ni de mot de passe, en particulier sur un PC, qui ne peut pas scanner de QR. Quand le téléphone crée le réseau, c'est son **groupe Wi-Fi Direct à 5 GHz** (53 MB/s en J6c), avancé de J8 (décision D1).
>
> BLE ne fait que **mettre les appareils sur le même réseau**. La connexion medXfer suit ses règles habituelles (`PAIR-12`) :
>
> | Cas | Ce que fait l'utilisateur |
> |---|---|
> | Même Wi-Fi | Il touche l'appareil (inchangé) |
> | Appareil de confiance, autre réseau | Il touche l'appareil : BLE, réseau, session sans code (P3) |
> | Appareil inconnu, autre réseau | Il touche l'appareil et tape le PIN affiché par l'autre : BLE, réseau, appairage |
> | Téléphone avec caméra, ou appareil sans BLE | Le QR (inchangé : il porte le réseau et l'appairage) ; sans caméra ni BLE, réglages et PIN |
>
> **Critère de sortie :**
> - les quatre cas du tableau sur les vrais appareils, le QR toujours disponible en repli ;
> - le groupe Wi-Fi Direct à 5 GHz créé par l'application, rejoint par un PC et par un second téléphone, avec le débit mesuré ;
> - la conception (format de l'annonce, messages, cryptographie) écrite dans la bible et prête pour la revue G4.
>
> **Durée estimée :** 10 à 14 jours (7 à 10, plus le groupe à 5 GHz avancé de J8). Une PR par lot, CI verte sur les trois systèmes avant fusion.

## 0. État de départ (constaté sur `main` le 2 novembre 2026)

| Élément | État |
|---|---|
| Bluetooth dans l'application | ❌ Aucun code, aucune permission. |
| Interface `Hotspot` (`app/lib/platform/hotspot.dart`) | ✅ `start`, `stop`, `join`, `leave`. Android : `LocalHotspot.kt` (2,4 GHz). PC : le réseau du PC (J6c). |
| Groupe Wi-Fi Direct | ⚠️ Adaptateur de sonde `WifiDirectGroup.kt`, piloté par adb (`p2p-start`, `p2p-stop`) ; pas derrière `Hotspot`, pas dans l'interface. |
| Appairage par PIN | ✅ CPace sur TLS, lié au canal par `ekm` (§6.2) ; PIN à la demande (`PAIR-11`) ; 3 essais (`PAIR-04`). |
| Étiquettes de reconnaissance | ✅ `DISC-11` : 8 octets par appareil de confiance (16 au plus), période de 10 minutes, dans le TXT mDNS et la balise. |
| Cryptographie disponible | ristretto255 (CPace), HMAC-SHA-256 ; la bibliothèque standard de Go (1.25) offre `crypto/hkdf` et AES-GCM, sans dépendance de plus. |
| Matériel BLE | PC Windows 11 (Intel AC 8265) : BLE, rôles central et périphérique, annonce de 31 octets, **pas d'annonce étendue** (Bluetooth 4.2). Second PC Windows (Intel AC 8260) : BLE, central et périphérique. Galaxy A57 (Android 16), TECNO BF6 (Android 12) et Zorin : à relever par la sonde. |

## 1. Ordre d'exécution

```
WP-D.0 sonde BLE ──► WP-D.1 conception (bible, G4) ──► WP-D.2 cœur ──► WP-D.3 adaptateurs BLE ──► WP-D.5 écran et parcours ──► WP-D.6 essais réels, rapport
WP-D.4 groupe Wi-Fi Direct à 5 GHz derrière Hotspot ─────────────────────────────────────────────┘
```

La sonde passe d'abord : elle décide des API à garder et de la place disponible dans l'annonce, dont dépend la conception. Le groupe à 5 GHz (WP-D.4) ne dépend pas de BLE et avance en parallèle.

## 2. Les lots

### WP-D.0 — Sonde BLE · M

Un adaptateur d'essai par système, avec les API du système, sans dépendance :
- **Android** (Kotlin) : `BluetoothLeAdvertiser`, `BluetoothGattServer`, `BluetoothLeScanner`, `BluetoothGatt` ; piloté par adb, comme `WifiDirectGroup.kt` en J6c.
- **Windows** (C++/WinRT, dans le runner, comme `firewall.cpp`) : `BluetoothLEAdvertisementPublisher` ou `GattServiceProvider` pour l'annonce et le serveur, `BluetoothLEAdvertisementWatcher` et `BluetoothLEDevice` pour le balayage et le client.
- **Linux** : BlueZ par D-Bus (`LEAdvertisingManager1`, `GattManager1`, `Adapter1`) ; depuis Dart par un paquet libre (`bluez` ou `dbus`, licence vérifiée), ou depuis le cœur.

Questions, sur le Galaxy A57, les deux PC Windows, Zorin et le TECNO BF6 :

| # | Question |
|---|---|
| 1 | Chaque appareil tient-il **les deux rôles à la fois** : annonce connectable avec serveur GATT, et balayage avec client ? |
| 2 | **La place dans l'annonce.** Une annonce classique a 31 octets, la réponse au balayage 31 de plus. L'UUID de service de medXfer fait 128 bits : les UUID de 16 bits et les identifiants de fabricant sont attribués par le Bluetooth SIG, medXfer n'en a pas. Que laisse chaque système à l'application (Windows réserve certains champs) ? L'annonce étendue (Bluetooth 5) est-elle disponible quelque part ? Les 16 étiquettes de `DISC-11` (128 octets) ne tiennent pas dans 31 : la mesure décide du format (décision D2). |
| 3 | **Délais** : de l'ouverture de l'écran à l'appareil vu ; puis connexion GATT, échange d'un message chiffré d'environ 200 octets dans chaque sens, déconnexion. Taille du MTU obtenue sur chaque paire (23 octets par défaut). |
| 4 | **Portée** : même pièce, pièce voisine, à travers deux murs. L'utilisateur déplace l'appareil, la sonde mesure. |
| 5 | **Cohabitation avec le Wi-Fi** : les cartes Intel et la plupart des téléphones partagent l'antenne et la bande des 2,4 GHz entre Bluetooth et Wi-Fi. Débit d'un transfert Wi-Fi avec et sans balayage BLE en cours. Si le balayage freine le transfert, l'application l'arrête pendant les transferts. |
| 6 | **Permissions et accès** : Android 12 et plus (`BLUETOOTH_SCAN` sans localisation, `BLUETOOTH_ADVERTISE`, `BLUETOOTH_CONNECT`) ; Android 11 et moins ; une application Windows non empaquetée ; BlueZ sans droits administrateur. |
| 7 | **Bluetooth éteint** : le détecter et proposer de l'allumer, sur chaque système. |

Résultat : un rapport de sonde dans ce plan, et les décisions D2, D4 et D5.

### WP-D.1 — Conception, pour la revue G4 · M

Écrite dans la bible (nouvelles exigences `BLE-xx`, §6 complété), sans nouvelle cryptographie : CPace, HMAC-SHA-256, HKDF et AES-GCM, déjà là ou dans la bibliothèque standard de Go.

- **L'annonce** : l'UUID de service de medXfer, et ce que la sonde laisse de place. Ni nom, ni identifiant stable. L'étiquette tournante de `DISC-11` dit à un appareil de confiance « c'est moi » ; un inconnu ne voit que des valeurs aléatoires.
- **Les messages GATT** : demande de réseau, réponse (nom, mot de passe et sécurité du réseau, comme `ws`, `wp`, `wt` du QR, et les adresses), refus neutre.
- **Appareil de confiance** : clé tirée de la clé de la paire (`DISC-11`) par HKDF, propre à BLE ; un défi frais de chaque côté, pour qu'un message enregistré ne puisse pas être rejoué.
- **Appareil inconnu** : CPace avec le PIN, sur BLE. Il n'y a pas de TLS sous BLE, donc pas d'`ekm` : le sid est fait des deux nonces échangés, le CI porte les deux empreintes. La clé obtenue chiffre le réseau. Sur le Wi-Fi, la connexion TLS épingle ensuite les empreintes que le PIN a authentifiées, comme le QR (P1) : **un seul PIN, un seul compteur d'essais** pour le réseau et l'appairage (`PAIR-04`).
- **L'appareil qui crée le réseau** accepte la demande d'un inconnu (Accepter, Refuser).
- **Qui crée le réseau** : celui qui peut le créer à 5 GHz, c'est-à-dire le téléphone (règle du rapport J6c). Le code de pays peut circuler pour choisir, jamais pour imposer : le système et le pilote appliquent la réglementation.
- Le mot de passe du réseau ne passe **jamais en clair** par BLE, et n'est jamais journalisé.

### WP-D.2 — Le cœur · M

- Les messages de WP-D.1, chiffrés et déchiffrés dans le cœur : la plateforme ne fait que les porter. Actions et événements dans `internal/cabi` et l'API.
- CPace sur un transport sans TLS ; le compteur d'essais partagé avec l'appairage sur TLS.
- Épinglage, sur le Wi-Fi, des empreintes authentifiées par BLE.
- Tests : vecteurs, rejeu refusé, mauvais PIN, essais épuisés, message tronqué ou modifié.

### WP-D.3 — Adaptateurs BLE · L

- Une interface dans `app/lib/platform/` (annoncer, balayer, porter un message), un adaptateur par système et par API, selon la sonde (décision D5), et un adaptateur vide quand BLE manque.
- Permissions demandées au moment où BLE sert, avec leur raison ; Bluetooth éteint signalé.
- Balayage et annonce **seulement quand l'application est ouverte** (décision D3), arrêtés pendant les transferts si WP-D.0 le demande.

### WP-D.4 — Le groupe Wi-Fi Direct à 5 GHz derrière `Hotspot` · M (avancé de J8, décision D1)

- `WifiDirectGroup.kt` devient un adaptateur de `Hotspot`, à côté de `LocalHotspot.kt` : bande 5 GHz demandée, nom et mot de passe tirés à chaque démarrage.
- Choisi par téléphone selon la mesure (décision C7) : le groupe s'il obtient le 5 GHz, sinon `LocalOnlyHotspot`. La bande obtenue est montrée.
- Le téléphone ne doit être connecté à aucun autre réseau pendant le groupe (WP-C.0 : 215 ms de ping sinon) : l'application le dit et propose de le déconnecter.
- Un second téléphone le rejoint par `WifiNetworkSpecifier` (réserve 2 du rapport J6c).
- Valable aussi pour le QR : le QR porte le réseau du groupe.

### WP-D.5 — Écran et parcours · M

- La liste « À proximité » réunit les appareils du même réseau (inchangé) et ceux trouvés par BLE, avec le chemin.
- Toucher un appareil trouvé par BLE : création ou jonction du réseau, puis session (confiance) ou PIN (inconnu), dans le même parcours qu'aujourd'hui.
- Chez l'appareil qui crée le réseau : la demande d'un inconnu, Accepter ou Refuser.
- Messages clairs à chaque étape qui peut échouer (Bluetooth éteint, permission refusée, réseau non rejoint, refus).

### WP-D.6 — Essais réels et rapport · S

- Les quatre cas du tableau, entre le Galaxy A57, les deux PC Windows, Zorin et le TECNO BF6 selon la sonde.
- Le groupe à 5 GHz : un PC et un second téléphone le rejoignent ; 1 Gio, débit et mesure par étape.
- Délais mesurés, de « toucher l'appareil » à « session ouverte ».
- Rapport `docs/j6d-report.md`. Guide d'utilisation : reporté à la première version.

## 3. Hors périmètre

- BLE en arrière-plan, application fermée (décision D3).
- Le transfert de fichiers par BLE : trop lent, BLE ne sert qu'à se retrouver.
- iPhone et Mac : J9.
- Le partage de connexion d'un PC sur le câble : avec le MikroTik de l'utilisateur, déjà prévu.

## 4. Décisions

| # | Question | Décision | Écarté |
|---|---|---|---|
| D1 | Le groupe Wi-Fi Direct à 5 GHz, prévu en J8 | ✅ **Avancé en J6d** (2 novembre) : le réseau créé par BLE va à environ 53 MB/s dès le départ quand le téléphone le crée. J6d passe à 10 à 14 jours, J8 raccourcit d'autant. | Le garder en J8 : J6d plus court, mais le réseau créé par BLE à 2,4 GHz (4 à 10 MB/s) jusque-là. |
| D2 | Le format de l'annonce, dans 31 octets | Après la sonde (question 2). | — |
| D3 | Quand annoncer et balayer | Proposé : **seulement quand l'application est ouverte**. Moins de batterie, pas de service en arrière-plan à justifier auprès d'Android, rien d'annoncé quand l'utilisateur ne s'en sert pas. | En arrière-plan aussi : plus tard, si l'usage le demande. |
| D4 | Un appareil sans BLE (Zorin, si la sonde le confirme) | Après la sonde : le QR et le PIN restent, ou un adaptateur USB. | — |
| D5 | Les API BLE de chaque système | Après la sonde : les API du système, une par adaptateur. | — |
