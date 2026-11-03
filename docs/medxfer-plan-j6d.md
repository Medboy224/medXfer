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

**Résultats de la sonde** (2 novembre ; `ble-probe.exe` en C++/WinRT sur les deux PC, `BleProbe.kt` dans une version de débogage installée à côté de la version normale, commandes `ble-…` par adb ; un UUID de service de 128 bits tiré pour la sonde ; un message de 200 octets écrit puis lu) :

| Appareil | BLE | Annonce | Remarques |
|---|---|---|---|
| PC Windows 11 (Intel AC 8265) | central et périphérique | 31 octets, pas d'annonce étendue | — |
| Second PC, Windows 10 1909 (Intel AC 8260) | central et périphérique | 31 octets | La sonde y tourne aussi par SSH, sans session ouverte. |
| Galaxy A57 (Android 16) | central et périphérique | **annonce étendue, 1 650 octets**, plusieurs annonces à la fois, PHY 2M et Coded (longue portée) | — |
| TECNO BF6 (Android 12, 32 bits) | central et périphérique | 31 octets, plusieurs annonces à la fois | Annonce et répond écran éteint. |
| Zorin (HP EliteBook 8460p) | ❌ **pas de BLE** : Broadcom 2070, Bluetooth 2.1 | — | Décision D4. |

| # | Question | Résultat |
|---|---|---|
| 1 | Les deux rôles à la fois | ✅ Les deux PC et le Galaxy A57 annoncent et balayent en même temps, et chacun voit les deux autres, en 0,1 à 3,9 s. |
| 2 | La place dans l'annonce | **Un PC Windows ne peut pas porter d'étiquette dans son annonce connectable.** Celle du service GATT, Windows la remplit lui-même : les drapeaux, le service « Device Information » (`0x180A`) et l'UUID de 128 bits ; les 10 octets de données de service n'y tiennent plus, sur les deux PC (« commencée sans toutes les données »). Une seconde annonce, non connectable, n'accepte que des données de service ou de fabricant (les drapeaux, la liste d'UUID et le nom sont refusés) et **attend tant que le service GATT est annoncé** : les cartes Intel n'annoncent qu'une chose à la fois. Android porte l'UUID et 10 octets dans ses données de service (31 octets tout juste), la liste d'UUID dans la réponse au balayage. |
| — | Vie privée de l'annonce | Annoncé « découvrable », Windows ajoute **le nom du PC** ; en mode caché, plus de nom, et l'échange marche toujours. Un appareil non appairé voit le PC sous une **adresse aléatoire qui change** (le second PC) ; le Galaxy A57 voit l'adresse réelle parce qu'il est appairé en Bluetooth classique avec ce PC. Android change d'adresse à chaque annonce. |
| 3 | Délais d'un échange de 200 octets, en un seul envoi (MTU de 517 à 527 octets) | Voir le tableau suivant. |
| 4 | Portée | **Pas encore mesurée** : l'utilisateur, au travail, n'a pas pu se déplacer ; à faire en WP-D.6. À la place, 6 min de stabilité à environ 1 m (le Galaxy A57 annonce, ce PC balaye et se connecte toutes les 6 s) : annonce vue 47 fois sur 48, signal de -62 à -70 dBm, **40 échanges réussis sur 47 (85 %)**, 2 s en moyenne. Six des sept échecs tombent dans la même minute et demie, signal stable (« injoignable », ou rien en 20 s) : cause non établie, peut-être le changement d'adresse du téléphone. L'adaptateur réessaie (WP-D.3). |
| 5 | Cohabitation avec le Wi-Fi | Sur le point d'accès d'un téléphone à 2,4 GHz, entre les deux PC, aucun effet mesurable : 1,2 à 1,4 MB/s dans un sens, 2,1 à 2,5 dans l'autre, avec ou sans annonce et balayage BLE sur ce PC. Ce lien est trop lent pour conclure : **à refaire sur le groupe à 5 GHz** (WP-D.4). |
| 6 | Permissions et accès | Windows : rien à déclarer pour une application non empaquetée. Android 12 et plus : les trois permissions, accordées par adb pour la sonde ; leur demande à l'utilisateur est pour WP-D.3. Android 11 : pas d'appareil d'essai. |
| 7 | Bluetooth éteint | Lu sur chaque système (l'état de la radio sous Windows, `isEnabled` sous Android). Au début de la sonde, il était **éteint sur ce PC et les deux téléphones** : proposer de l'allumer est indispensable (WP-D.5). |
| — | Écran éteint | Le TECNO annonce et répond écran éteint, mais un balayage sans filtre n'y voit plus rien, pas même les appareils autres que medXfer : Android coupe les balayages sans filtre écran éteint. L'adaptateur filtrera sur le service de medXfer (WP-D.3). |

| Échange (qui se connecte → qui annonce) | Vu en | Premier échange | Échanges suivants |
|---|---|---|---|
| PC → second PC (Windows → Windows) | 0,3 s | 3,0 s (découverte des services : 2,5 s) | 0,6 à 1,0 s |
| PC → Galaxy A57 | 0,3 s | 6,9 s (découverte de ses 8 services, ceux du son compris : 6,3 s) | 1,3 à 1,9 s |
| Galaxy A57 → PC | 0,03 à 0,07 s | 4,4 s (négociation du MTU : 3,7 s) | 0,44 à 0,79 s |
| PC → TECNO BF6 | 1,0 s | 3,1 s | 0,72 s |
| TECNO BF6 → PC (écran allumé) | 0,4 à 3,1 s | 9,3 s | 4,3 à 5,2 s, dont **3,3 à 3,5 s de négociation du MTU à chaque fois** |

La négociation du MTU depuis Android vers Windows prend 3,3 à 3,7 s, sauf parfois sur le Galaxy A57 une fois le premier échange passé. L'adaptateur Android essaiera sans la demander : 200 octets passent aussi en écriture longue, par morceaux (WP-D.3).

Sous Windows, chercher directement le service de medXfer chez le Galaxy A57, sans avoir jamais découvert ses services, échoue (« le périphérique ne reconnaît pas la commande ») ; une découverte complète d'abord, et tout marche. À traiter dans l'adaptateur Windows (WP-D.3).

### WP-D.1 — Conception, pour la revue G4 · M

Écrite dans la bible (nouvelles exigences `BLE-xx`, §6 complété), sans nouvelle cryptographie : CPace, HMAC-SHA-256, HKDF et AES-GCM, déjà là ou dans la bibliothèque standard de Go.

- **L'annonce** : l'UUID de service de medXfer, et ce que la sonde laisse de place. Ni nom, ni identifiant stable. L'étiquette tournante de `DISC-11` dit à un appareil de confiance « c'est moi » ; un inconnu ne voit que des valeurs aléatoires.
- **Les messages GATT** : demande de réseau, réponse (nom, mot de passe et sécurité du réseau, comme `ws`, `wp`, `wt` du QR, et les adresses), refus neutre.
- **Appareil de confiance** : clé tirée de la clé de la paire (`DISC-11`) par HKDF, propre à BLE ; un défi frais de chaque côté, pour qu'un message enregistré ne puisse pas être rejoué.
- **Appareil inconnu** : CPace avec le PIN, sur BLE. Il n'y a pas de TLS sous BLE, donc pas d'`ekm` : le sid est fait des deux nonces échangés, le CI porte les deux empreintes. La clé obtenue chiffre le réseau. Sur le Wi-Fi, la connexion TLS épingle ensuite les empreintes que le PIN a authentifiées, comme le QR (P1) : **un seul PIN, un seul compteur d'essais** pour le réseau et l'appairage (`PAIR-04`).
- **L'appareil qui crée le réseau** accepte la demande d'un inconnu (Accepter, Refuser).
- **Qui crée le réseau** : celui qui peut le créer à 5 GHz, c'est-à-dire le téléphone (règle du rapport J6c). Le code de pays peut circuler pour choisir, jamais pour imposer : le système et le pilote appliquent la réglementation.
- Le mot de passe du réseau ne passe **jamais en clair** par BLE, et n'est jamais journalisé.

**Fait (2 novembre)** : bible §6.6 (exigences `BLE-01` à `BLE-12`), avec l'adversaire A7 (§2), `CHAN-04`, les journaux (§13), les tests (§14.1) et les sources (annexe B) ; dossier G4, §3.7 et questions 12 à 17. Trois écarts avec ce qui précède :
- **Les empreintes ne sont pas dans le CI.** Il aurait fallu les envoyer en clair avant CPace, et une empreinte ne change jamais : n'importe qui à portée radio aurait pu suivre un appareil en demandant un PIN. Elles passent chiffrées juste après la confirmation, sous une clé tirée de l'ISK (question 12 du dossier G4).
- **Le code de pays ne circule pas.** Chaque côté dit seulement les bandes qu'il peut créer et rejoindre ; le système du créateur applique la réglementation.
- **La demande d'un inconnu s'affiche avec le PIN et « Refuser »**, sans « Accepter » à part (décision D6, proposée).

### WP-D.2 — Le cœur · M

- Les messages de WP-D.1, chiffrés et déchiffrés dans le cœur : la plateforme ne fait que les porter. Actions et événements dans `internal/cabi` et l'API.
- CPace sur un transport sans TLS ; le compteur d'essais partagé avec l'appairage sur TLS.
- Épinglage, sur le Wi-Fi, des empreintes authentifiées par BLE.
- Tests : vecteurs, rejeu refusé, mauvais PIN, essais épuisés, message tronqué ou modifié.

En deux PR, pour que la partie cryptographique se relise seule :
1. **Le protocole** (2 novembre) : `pkg/pairing/ble.go`, sans radio ni API. Il contient le format, les clés, le chiffrement, les étiquettes, CPace sans TLS sur le même `PIN` que TLS, la règle de qui crée le réseau et les clés authentifiées pour le Wi-Fi. `pkg/pake` gagne seulement `Session.Key`. 16 tests et un fuzzing (1,5 million d'exécutions, sans défaut).
2. **L'API** (2 novembre) : deux actions, `ble_server` et `ble_client`, une étape par appel sur une connexion GATT nommée par la plateforme, 4 de chaque côté au plus. `pair_ble` ouvre la session sur le réseau, la clé authentifiée par BLE épinglée, acceptée une fois. La demande de PIN passe par le PIN à la demande (`pin_requested` avec le nom générique du demandeur, `pin_refuse`), et le mot de passe du réseau est masqué dans le journal. Le suffixe est en minuscules, comme le nom générique, pour que la liste réunisse un appareil vu par BLE et sur le réseau.

### WP-D.3 — Adaptateurs BLE · L

- Une interface dans `app/lib/platform/` (annoncer, balayer, porter un message), un adaptateur par système et par API, selon la sonde (décision D5), et un adaptateur vide quand BLE manque.
- Permissions demandées au moment où BLE sert, avec leur raison ; Bluetooth éteint signalé.
- Balayage et annonce **seulement quand l'application est ouverte** (décision D3), arrêtés pendant les transferts si WP-D.0 le demande.

**Android, fait (2 novembre)** :
- `Ble.kt` : l'annonce porte l'UUID seul ; un serveur GATT garde chaque réponse jusqu'à ce que l'autre la lise ; la recherche est filtrée sur le service ; le client écrit puis lit, sans demander de MTU (écritures et lectures longues).
- `platform/ble.dart` : l'interface, l'adaptateur Android, et aucun ailleurs pour l'instant. Windows suit dans sa propre PR.
- `BleLink`, entre la radio et le cœur. Touché, l'appareil répond seul et crée le réseau quand c'est à lui ; son utilisateur touchant un appareil, il mène l'échange jusqu'à la session.
- Une section BLE du panneau développeur, en attendant les écrans (WP-D.5).

**Essai réel du 2 novembre**, Galaxy A57 et TECNO BF6, par le chemin de confiance (les deux appareils se faisaient déjà confiance) :

| Mesure | Résultat |
|---|---|
| Se trouver par BLE sans Wi-Fi commun, se reconnaître comme appareils de confiance | ✅ noms affichés, sans PIN |
| L'un crée le réseau, l'autre le rejoint, la session s'ouvre | ✅ 7 à 10 s du réseau créé à la session ouverte |
| Session perdue : le réseau du créateur gardé, la session revient seule | ✅ en 25 à 35 s, après correction |
| TECNO au Wi-Fi éteint ; adresse de son réseau | ✅ après correction |
| **Tenue de la session, écran du TECNO éteint** | ❌ quand l'A57 cherche des réseaux, son point d'accès lâche le TECNO, 1,5 à 5 s après (4 fois sur 4) ; écran allumé, aucune coupure sur 3 recherches |
| Après une coupure, sur le TECNO (Android 12) | Android redemande d'accepter le réseau |

Corrigé pendant l'essai :
- **Le réseau du créateur était arrêté 15 s après la perte de la session**, alors que l'autre le rejoignait 8 s plus tard. Il est maintenant gardé `rejoinFor` (10 min), comme du côté qui le rejoint. Cela vaut aussi pour le QR.
- **BLE est mis en pause pendant une session** (décision D3).
- **Le TECNO fait tourner son point d'accès local sur `wlan0`**, que la recherche de son adresse manquait.
- **Wi-Fi éteint, un téléphone ne dit plus pouvoir rejoindre un réseau** : il crée alors le sien, et l'autre le rejoint.

Essayé, sans effet : un verrou Wi-Fi « haute performance » sur le TECNO. Le journal montre que c'est le point d'accès de l'A57 qui le lâche ; le TECNO ne s'en aperçoit que 20 s plus tard. Retiré.

**Reste ouvert** :
- la coupure, écran éteint. Elle ne tient pas à BLE : le réseau est le même après un QR. Pistes : le groupe Wi-Fi Direct à 5 GHz de l'A57 au lieu de son point d'accès local, garder l'écran allumé pendant un transfert, un autre téléphone ;
- l'arrêt d'un réseau créé pour BLE que personne n'a rejoint en 2 minutes (`BLE-09`).

**Windows, fait (3 novembre)** :
- `ble.cpp`, dans le runner, en C++/WinRT : le même canal que `Ble.kt`. `GattServiceProvider` en mode caché, une réponse gardée jusqu'à ce que l'autre la lise (25 s au plus), un balayage filtré sur le service, un client qui fait une découverte complète puis écrit et lit. Une erreur du client dit l'étape où elle survient.
- `wifi_join.cpp` : **le PC rejoint seul le réseau d'un autre appareil**, par l'API Wi-Fi de Windows (`wlanapi`), avec un profil de l'utilisateur, sans droits d'administrateur. Le profil est supprimé au départ et le PC revient au réseau où il était. Il sert aussi au QR : le PC n'envoie plus l'utilisateur dans les réglages de Windows.
- Bandes annoncées : création à 2,4 GHz (sonde WP-C.1) ; rejoindre à 2,4 GHz, et à 5 GHz si la carte le fait ; rien Wi-Fi éteint.
- `platform/ble.dart` : un seul adaptateur à canal, `ChannelBle`, pour Android et Windows.

**Essai réel du 3 novembre**, PC Windows 11 (Intel AC 8265), Galaxy A57, TECNO BF6 ; le PC gardait Internet par le partage de connexion USB du TECNO :

| Mesure | Résultat |
|---|---|
| PC et TECNO (inconnus l'un de l'autre) : se trouver par BLE | ✅ dans les deux sens |
| **Chemin du PIN**, premier essai réel : PIN montré par le TECNO, tapé sur le PC | ✅ |
| Le TECNO crée son réseau, le PC le rejoint seul | ✅ point d'accès local à 2,4 GHz, canal 6 ; session ouverte, un PDF passe |
| PC et Galaxy A57 **appairés en Bluetooth** dans les réglages | ❌ le PC voit l'A57, mais la découverte de ses services échoue à chaque fois (`E_UNEXPECTED`, ou « injoignable »), la sonde de WP-D.0 aussi |
| Le même A57, l'appairage Bluetooth supprimé des deux côtés | ✅ reconnu ; l'A57 crée son groupe à **5 GHz** (canal 36, 866,7 Mbit/s), le PC le rejoint seul, session de confiance sans PIN |
| **1 Gio du PC vers l'A57**, écran allumé | ✅ **42,4 Mo/s** en moyenne, 62,8 Mo/s en pointe, 25,3 s ; empreinte SHA-256 identique |
| Fermeture de l'application sur le PC | ✅ réseau quitté, profil supprimé |
| 1 Gio du PC vers le TECNO, écran du TECNO éteint | ❌ coupé vers 44 % ; l'application affiche pourtant « le transfert continue » et sa barre avance |

**Reste ouvert** :
- un téléphone appairé en Bluetooth avec le PC : le PC n'en lit pas le service. Les utilisateurs appairent souvent leur téléphone ; à corriger avant WP-D.6 ;
- l'A57 ne voit pas l'annonce de l'application Windows, alors qu'il voit celle de la sonde et que le TECNO voit celle de l'application. Le PC touchant l'A57 suffit pour l'instant ;
- un transfert mort affiché comme en cours, après une coupure ;
- la coupure écran éteint (#256), cette fois le TECNO créant le réseau.

### WP-D.4 — Le groupe Wi-Fi Direct à 5 GHz derrière `Hotspot` · M (avancé de J8, décision D1)

- `WifiDirectGroup.kt` devient un adaptateur de `Hotspot`, à côté de `LocalHotspot.kt` : bande 5 GHz demandée, nom et mot de passe tirés à chaque démarrage.
- Choisi par téléphone selon la mesure (décision C7) : le groupe s'il obtient le 5 GHz, sinon `LocalOnlyHotspot`. La bande obtenue est montrée.
- Le téléphone ne doit être connecté à aucun autre réseau pendant le groupe (WP-C.0 : 215 ms de ping sinon) : l'application le dit et propose de le déconnecter.
- Un second téléphone le rejoint par `WifiNetworkSpecifier` (réserve 2 du rapport J6c).
- Valable aussi pour le QR : le QR porte le réseau du groupe.

**Essai réel du 2 novembre 2026** (Galaxy A57 sous Android 16, PC Windows 11 avec Intel AC 8265, TECNO BF6 sous Android 12 ; le PC gardait Internet par le partage de connexion USB du TECNO) :

| Mesure | Résultat |
|---|---|
| Groupe créé par l'application | ✅ À 5 GHz à chaque essai : canaux 48, 157 et 161, choisis par Android. Un groupe laissé par une application tuée est retiré au démarrage suivant. |
| 1 Gio du PC vers le téléphone, téléphone connecté à aucun autre Wi-Fi | ✅ **18,8 s, 54,4 MB/s** (pic 56,8, minimum 50,2), aucune reprise. Côté téléphone, l'attente du réseau occupe tout le transfert : la limite est le lien à 5 GHz, ni le disque ni le hachage. |
| Le même, téléphone aussi connecté au Wi-Fi du TECNO (2,4 GHz, canal 1) | ⚠️ **54,9 s, 18,6 MB/s** (minimum 2,5) : le groupe reste à 5 GHz, mais la radio alterne entre les deux canaux. L'avertissement est justifié. |
| L'avertissement « aussi connecté à un Wi-Fi » | ❌ puis ✅ Android 16 compte le groupe lui-même parmi les réseaux Wi-Fi (`p2p-wlan0-0`) : l'avertissement ne partait jamais. Corrigé (les interfaces Wi-Fi Direct sont écartées). Il se voyait aussi seulement sur l'écran du réseau, fermé une fois l'appairage fait : un message le dit maintenant pendant la session. |
| Se déconnecter du Wi-Fi tout seul | Impossible : depuis Android 10, une application ne peut ni déconnecter ni couper le Wi-Fi. L'application ouvre les réglages. |
| TECNO BF6 face au QR à 5 GHz | ❌ puis ✅ Sa puce ne capte que le 2,4 GHz. Android disait seulement « impossible de détecter le réseau ». Le QR porte maintenant la bande (`wb`) : le TECNO dit aussitôt qu'il ne capte pas le 5 GHz et ce qu'il faut faire (vu sur l'appareil). |
| Réseau recréé à 2,4 GHz pour le TECNO | ✅ Point d'accès local du Galaxy A57, rejoint par le QR (802.11n, 52 à 72 Mbit/s). |
| Second téléphone dans le groupe à 5 GHz (réserve 2 du rapport J6c) | Reportée : aucun second téléphone à 5 GHz disponible. |

Décision du 2 novembre : **5 GHz par défaut**, avec un interrupteur « Haute vitesse (5 GHz) » pour un appareil qui ne capte que le 2,4 GHz.

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
| D2 | Le format de l'annonce, dans 31 octets | ✅ Tranché par la sonde (question 2) : **l'annonce ne dit que « medXfer est là »**, par l'UUID du service, puisqu'un PC Windows ne peut rien y mettre d'autre. La reconnaissance passe par un court échange GATT après la connexion : celui qui balaye envoie un défi frais, l'autre répond par ses étiquettes pour ce défi (`DISC-11`, adapté) et le suffixe de son nom générique (`DISC-07`). Un échange de 0,4 à 2 s par appareil medXfer à portée, conçu en WP-D.1. | Les étiquettes dans l'annonce : impossible depuis Windows. L'annonce étendue : absente des deux PC et du TECNO. |
| D3 | Quand annoncer et balayer | Proposé : **seulement quand l'application est ouverte**. Moins de batterie, pas de service en arrière-plan à justifier auprès d'Android, rien d'annoncé quand l'utilisateur ne s'en sert pas. | En arrière-plan aussi : plus tard, si l'usage le demande. |
| D4 | Un appareil sans BLE (Zorin : Bluetooth 2.1) | ✅ **Le QR et le PIN, comme aujourd'hui** (2 novembre) : les essais couvrent ce cas. Le BLE sous Linux sera essayé quand l'utilisateur aura installé Zorin sur le second PC (Intel AC 8260, BLE) ; d'ici là, des tests automatiques. | Un adaptateur USB Bluetooth 5 : introuvable là où se trouve l'utilisateur. |
| D5 | Les API BLE de chaque système | ✅ Les API du système, une par adaptateur, sans dépendance. **Android** : `android.bluetooth.le`, balayage filtré sur le service. **Windows** (C++/WinRT, dans le runner) : `GattServiceProvider` en mode caché, `BluetoothLEAdvertisementWatcher`, `BluetoothLEDevice` et `GattSession`. **Linux** : BlueZ par D-Bus, sondé plus tard (D4). | — |
| D6 | Comment l'appareil qui crée le réseau accepte un inconnu | Proposé : **la demande s'affiche avec le PIN et un bouton « Refuser »**. Taper le bon PIN de l'autre côté vaut accord, puisque seul celui qui voit cet écran le connaît. Quand c'est l'initiateur qui crée le réseau, son toucher vaut accord. Une étape de moins, et aucun réseau n'est créé avant que le PIN soit vérifié. | « Accepter » puis le PIN : deux étapes chez le répondant pour la même garantie. |
