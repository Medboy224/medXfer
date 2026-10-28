# medXfer — Plan du jalon J6c : point d'accès à 5 GHz, point d'accès du PC, QR de bout en bout

> **Source :** feuille de route, J6c ; décision J17 du plan J6b ; rapport [j6b-report.md](j6b-report.md), §6 (réserves 1 et 2) ; bible `DISC-10`, `DISC-12`, URI d'appairage (champs `ws`, `wp`, `wt`).
>
> **Objectif :** trois sujets laissés ouverts par J6b.
> - **Le point d'accès à 5 GHz.** Celui du téléphone (`LocalOnlyHotspot`) reste sur la bande choisie par Android : 2,4 GHz sur le téléphone d'essai, loin de l'objectif de vitesse. La sonde vérifie si Wi-Fi Direct permet de demander le 5 GHz.
> - **Le point d'accès du PC.** Quand aucun réseau n'est disponible, le PC crée le réseau, sous Windows et sous Linux, et montre son QR. Le téléphone le scanne, rejoint le réseau et s'appaire : c'est l'**appairage par QR de bout en bout**, la réserve du critère de sortie de J6b. Décidé le 26 octobre, avancé de J8.
> - **Les à-coups de l'interface sous Windows** (réserve 4 de G2a) : mesurés, plus seulement observés.
>
> **Critère de sortie :**
> - un rapport de sonde Wi-Fi Direct et la décision d'intégrer en J8 ou non ;
> - l'appairage par QR de bout en bout, sur les vrais appareils :
>   - un téléphone scanne le QR du PC, sous Windows et sous Linux ;
>   - un second téléphone scanne celui du Galaxy A57 (#169) ;
> - les à-coups sous Windows mesurés.
>
> **Durée estimée :** 4 à 6 jours. Une PR par lot, CI verte sur les trois systèmes avant fusion.

## 0. État de départ (constaté sur `main` le 28 octobre 2026)

| Élément | État |
|---|---|
| Interface `Hotspot` (`app/lib/platform/hotspot.dart`) | ✅ `start`, `stop`, `join`, `leave`, derrière une interface. Seul l'adaptateur Android existe : `LocalHotspot.kt` (créer) et `WifiJoin.kt` (rejoindre). Ailleurs, `supported` vaut faux. |
| QR d'appairage avec le réseau | ✅ L'URI porte le nom, le mot de passe et la sécurité du réseau (`ws`, `wp`, `wt`) ; le téléphone qui scanne rejoint le réseau puis s'appaire (WP-B.2b). Jamais essayé de bout en bout. |
| Écran du point d'accès | ✅ `HotspotScreen` (Android) : QR, nom et mot de passe, PIN. Le PC n'a pas d'équivalent ; sa colonne « Connexion » propose « Créer une connexion (PIN ou lien) », sans réseau. |
| Sonde WP-B.7 | Point d'accès local à **2,4 GHz, canal 1**. Une seule radio (`mIsDbs: false`) : le point d'accès suit le canal du Wi-Fi du téléphone. Débit : 4,2 à 9,8 MB/s. |
| Matériel | Galaxy A57 (Android 16) ; PC Windows 11 (Intel AC 8265, 2,4 et 5 GHz) ; Zorin (Linux) ; un second téléphone Android. |

## 1. Ordre d'exécution

```
WP-C.0 sonde Wi-Fi Direct à 5 GHz (téléphone) ─────────────────────────────┐
WP-C.1 sonde du point d'accès du PC (Windows, Linux) ──► WP-C.2 adaptateurs ──► WP-C.3 écran du PC ──►├─► WP-C.5 essais réels, rapport
WP-C.4 mesure des à-coups sous Windows ─────────────────────────────────────┘
```

Les deux sondes passent d'abord : elles décident des API à garder.

## 2. Les lots

### WP-C.0 — Sonde : Wi-Fi Direct à 5 GHz · S

- Adaptateur Kotlin d'essai :
  - `WifiP2pManager.createGroup` avec `WifiP2pConfig.Builder`, sous Android 10 et plus : nom (`DIRECT-xx-…`), mot de passe, `setGroupOperatingBand(GROUP_OWNER_BAND_5GHZ)` ;
  - derrière l'interface `Hotspot`, à côté de `LocalHotspot`, sans le remplacer.
- Questions, sur le Galaxy A57 :
  - le groupe se crée-t-il à 5 GHz, Wi-Fi du téléphone allumé, puis coupé ? La puce n'a qu'une radio.
  - le PC et Zorin le rejoignent-ils comme un Wi-Fi ordinaire (nom et mot de passe), sans Wi-Fi Direct de leur côté ?
  - un second téléphone le rejoint-il par `WifiNetworkSpecifier`, comme le point d'accès actuel ?
  - la découverte y passe-t-elle (mDNS, balise) ?
  - quel débit (`xfer bench net`, puis un transfert de 1 Go), comparé au point d'accès actuel ?
- Résultat : un rapport de sonde. Si la réponse est oui, l'intégration se fait en J8 (décision J17).

**Résultats de la sonde** (28 octobre, Galaxy A57 sous Android 16 ; `WifiDirectGroup.kt`, commandes `p2p-start` et `p2p-stop` par adb ; débit brut : 3 envois de 300 Mo par `nc` vers le téléphone) :

| Question | Résultat |
|---|---|
| Groupe à 5 GHz | ✅ Demandé par `GROUP_OWNER_BAND_5GHZ`, obtenu à chaque essai : canaux 36, 40, 44 et 157, choisis par Android. |
| Wi-Fi du téléphone connecté ailleurs en même temps | ⚠️ Possible (Wi-Fi à 2,4 GHz canal 1, groupe à 5 GHz), mais la radio unique alterne entre les deux canaux : ping moyen **215 ms** (max 953), débit 5,4 à 6,7 MB/s, aucun gain. **Le téléphone ne doit être connecté à aucun autre réseau.** |
| Un PC le rejoint comme un Wi-Fi ordinaire | ✅ PC Windows (Intel AC 8265, 802.11ac) et Zorin (Intel 6205, 802.11n), par nom et mot de passe ; le PC en 1 s. |
| Découverte et appairage dessus | ✅ Le PC rejoint le groupe ; 14 s plus tard le téléphone le voit dans « Connecter », session ouverte 2 s après (appareils de confiance). |
| Débit brut (`nc`), téléphone sans autre réseau | PC : **23,4 / 49,6 / 21,6 MB/s**, ping 3,6 ms ; Zorin : 12,1 à 13,3 MB/s, ping 3,2 ms. |
| Débit brut, point d'accès actuel (2,4 GHz, canal 1), mêmes conditions | Zorin : 6,1 à 7,0 MB/s, ping 2,9 ms. |
| **Transfert medXfer réel** | **1 Gio du PC vers le téléphone en 19,2 s : 53,4 MB/s en moyenne** (pic 68,1, minimum 52,1), aucune reprise. Étape limitante côté PC : le hachage (10,0 s de travail sur 4 connexions, autant que l'écriture réseau), confiance faible. |
| Un second téléphone le rejoint par `WifiNetworkSpecifier` | À faire (WP-C.5, avec #169). |

**Conclusion** : un groupe Wi-Fi Direct à 5 GHz, téléphone sans autre réseau, porte le transfert de **~7 MB/s à ~53 MB/s**, au niveau d'un bon routeur à 5 GHz. L'intégration en J8 est confirmée (décision J17), avec une règle : quand le téléphone crée le groupe, il quitte son autre réseau, ou l'application le propose.

**La réglementation du 5 GHz** (vaut pour WP-C.0 et WP-C.1) :
- les canaux 36 à 48 sont les plus ouverts (en Europe : en intérieur, sans détection de radar) ;
- les canaux 52 à 144 partagent la bande avec des radars : l'appareil qui crée le réseau doit les détecter et leur céder la place (DFS), et réduire sa puissance (TPC) ;
- les canaux 149 à 165 dépendent du pays.

C'est le système et le pilote Wi-Fi qui appliquent ces règles, d'après le pays (carte SIM, réseaux voisins, réglages). medXfer passe par les API officielles : il **demande** une bande, le système la donne sur un canal permis ou reste à 2,4 GHz. L'application ne force jamais le code pays, ne contourne pas le pilote (`hostapd` sur mesure, `iw reg set`) et ne choisit pas de canal radar. Elle montre la bande obtenue.

### WP-C.1 — Sonde : le point d'accès du PC · S

**Windows**, deux API à essayer :

| | « Point d'accès mobile » (`NetworkOperatorTetheringManager`) | Wi-Fi Direct sans client Wi-Fi Direct (`WiFiDirectAdvertisementPublisher`, réglages *legacy*) |
|---|---|---|
| Principe | Partage une connexion existante, comme le réglage du système | Crée un réseau avec un nom et un mot de passe choisis par l'application |
| À vérifier | Démarre-t-il sans connexion Internet à partager ? Bande choisie ? Droits administrateur ? | Le téléphone le rejoint-il comme un Wi-Fi ordinaire ? Bande ? Tient-il application en arrière-plan ? |

**Linux**, NetworkManager (`nmcli device wifi hotspot`, puis son API D-Bus) sur Zorin :
- faut-il des droits (polkit) pour un utilisateur ordinaire ?
- le Wi-Fi d'origine revient-il à l'arrêt ?

Pour chaque système : le téléphone rejoint-il le réseau par le QR, le transfert y passe-t-il, à quel débit ?

**Le PC à 5 GHz** (ajouté le 28 octobre, décision C5) : avec chaque API, le PC peut-il créer son réseau à 5 GHz ?
- la bande demandée, par le réglage officiel : « Point d'accès mobile » (bande du réglage du système), Wi-Fi Direct sous Windows, `802-11-wireless.band a` sous NetworkManager ;
- l'essai se fait PC connecté à un réseau à 5 GHz, puis à un réseau à 2,4 GHz, puis sans réseau. Certaines cartes, dont l'Intel AC 8265, n'émettent pas d'elles-mêmes à 5 GHz sans un réseau voisin qui confirme le pays (« No Initiate Radiation ») ;
- la bande et le canal obtenus sont relevés, ainsi que le débit avec le téléphone.

Un « non, le système reste à 2,4 GHz » est un résultat valable.

**Résultats de la sonde** (28 octobre ; PowerShell par les API WinRT sous Windows, `nmcli` sous Linux ; bande et canal relevés par un balayage du téléphone, `cmd wifi list-scan-results`) :

| | Windows 11, Intel AC 8265 | Zorin, Intel Centrino 6205 |
|---|---|---|
| « Point d'accès mobile » (`NetworkOperatorTetheringManager`) | ✅ démarre à 2,4 GHz en partageant le Wi-Fi connecté, qui reste connecté. ❌ **Sans réseau, impossible** : aucune connexion à partager. | — |
| Réseau créé sans connexion Internet | ✅ Wi-Fi Direct, réglages *legacy* (`WiFiDirectAdvertisementPublisher`) : nom et mot de passe choisis par l'application, rejoint comme un Wi-Fi ordinaire | ✅ `nmcli device wifi hotspot` |
| Droits administrateur | Aucun | Aucun (session de l'utilisateur) |
| 2,4 GHz | ✅ canal 6 sans autre réseau ; canal 1 connecté au Wi-Fi (il suit son canal) | ✅ canal 1 |
| **5 GHz** | ❌ L'API l'accepte (`IsBandSupported`), mais le démarrage échoue (`WiFiDeviceOff`) ; Wi-Fi Direct n'a pas de réglage de bande, le pilote prend 2,4 GHz | ❌ « Hotspot network creation took too long » : la carte ne démarre pas à 5 GHz |

**Conclusion** : ces deux PC ne créent pas de réseau à 5 GHz d'eux-mêmes. Leurs cartes Intel n'émettent pas les premières à 5 GHz sans un réseau voisin qui confirme le pays (« No Initiate Radiation ») : c'est la réglementation, appliquée par le pilote. Seul le téléphone crée le 5 GHz (WP-C.0, 53 MB/s). Règle pour l'application : **le téléphone crée le réseau quand il le peut** ; le PC le crée en repli (entre deux PC, ou face à un téléphone qui ne le peut pas), à 2,4 GHz. En J6d, BLE fera ce choix sans l'utilisateur.

Reste, avec WP-C.5 : le téléphone qui rejoint le réseau du PC par le QR, et le débit dessus.

### WP-C.2 — Adaptateurs du PC · M

- **Windows** : un module natif en C++/WinRT dans `app/windows/runner`, avec `WiFiDirectAdvertisementPublisher` en réglages *legacy* (décision C4), derrière `Hotspot` (canal de plateforme, comme sous Android). Le publieur vit tant que l'application tourne.
- **Linux** : NetworkManager, derrière `Hotspot` (son API D-Bus par un paquet Dart, ou `nmcli` ; une bibliothèque = un adaptateur). À 2,4 GHz : la sonde WP-C.1 montre que 5 GHz échoue après 25 s.
- Tant que le point d'accès tourne, le cœur ne reçoit que son interface (`set_network_info`, `DISC-10`), comme sous Android : le QR, le PIN et l'annonce portent son adresse.
- Il s'arrête quand on ferme sa fenêtre si personne ne l'a rejoint, sinon à la fin de la session. Le Wi-Fi d'origine revient.
- `DISC-12` mis à jour dans la bible.

**Fait** (28 octobre, avec WP-C.3) :
- **Windows** : `hotspot.cpp` (C++/WinRT), un groupe Wi-Fi Direct *legacy* dont l'application tire le nom et le mot de passe ; il rend aussi l'interface du groupe.
  - L'essai réel a montré que Windows n'y distribue **aucune adresse** : Zorin y restait sans adresse (`ip-config-unavailable`).
  - « Point d'accès mobile » n'en donnait pas non plus sur ce PC : son partage de connexion (ICS) est réglé sur la carte Ethernet.
  - D'où `lib/platform/dhcp_server.dart` : un serveur DHCP minimal (DISCOVER → OFFER, REQUEST → ACK ou NAK, sans routeur ni DNS), lié à la seule adresse du groupe. Plage : le sous-réseau du groupe, 169.254.0.0/16, sans droits administrateur.
- **Linux** : `nmcli`, réseau précédent rétabli à l'arrêt.
- **Le cœur** :
  - `set_network_info` prend `{"enumerate": true}`, qui rend l'énumération au cœur une fois le réseau du PC arrêté ;
  - `network`, sous Android, porte le réseau rejoint.
- **Android, l'appareil qui rejoint** : les sockets Go ne voient pas `bindProcessToNetwork` ; elles partaient par le réseau par défaut (« network is unreachable »). `pkg/netbind` les lie au réseau rejoint par `android_setsocknetwork` (NDK).
- **Confiance** : une session ouverte par un QR entre appareils de confiance mutuelle est mutuelle ; la confiance n'est plus reproposée à chaque scan.
- **Essai réel** : le Galaxy A57 scanne le QR du PC Windows, rejoint son réseau, obtient 169.254.121.149, s'appaire, et un transfert passe.

**Réserve** : le PC Windows coupe le Galaxy A57 toutes les 2 min exactement (« Sécurité sans fil interrompue » côté Windows), pas Zorin (5 min sans coupure, avec ou sans DHCP). Cause probable : la sécurité du lien entre Windows et Android (trames de gestion protégées, ou renouvellement des clés). Palliatif : le téléphone rejoint seul le réseau perdu (Android ne redemande pas l'accord), la session revient seule (#202) : environ 3 s de coupure toutes les 2 min. La cause est à chercher avec le second téléphone (WP-C.5).

### WP-C.3 — Écran du PC · S

- Dans la colonne « Connexion », la partie « Pas sur le même réseau » propose **« Créer un point d'accès »**, quand l'adaptateur le permet.
- Une fenêtre montre le QR, le nom et le mot de passe du réseau, et le PIN, comme `HotspotScreen` sur le téléphone, réutilisé ou partagé.
- Rejoindre le point d'accès d'un téléphone depuis le PC : par les réglages Wi-Fi du système, puis le PIN, comme aujourd'hui (le PC n'a pas de caméra).

### WP-C.4 — Mesure des à-coups sous Windows · S

- L'application en version profile, `lib/perf.dart` (journal `perf:`), sur le PC de développement :
  - 10 minutes au repos ;
  - un transfert de 1 Go ;
  - la navigation entre les pages.
- Mesures : images au-delà de 16,7 ms, les plus longues, leur fréquence.
- La réserve 4 de G2a (230 à 240 ms, environ une fois toutes les 3 à 4 minutes) est levée ou confirmée, avec les chiffres.

### WP-C.5 — Essais réels et rapport · S

- Appairage par QR de bout en bout :
  - Galaxy A57 → PC Windows ;
  - Galaxy A57 → Zorin ;
  - second téléphone → Galaxy A57 (#169).
- Un transfert de 1 Go par chacun de ces réseaux, avec son débit.
- Rapport de sortie, et décision sur Wi-Fi Direct pour J8.

## 3. Hors périmètre

- L'intégration de Wi-Fi Direct dans l'application : J8, si la sonde le confirme.
- Les règles du pare-feu Windows posées automatiquement : l'installateur de J8.
- Le débit sous Linux (réserve 1 de G2a) : il faut un PC Linux récent.

## 4. Décisions

Réponses de l'utilisateur du 28 octobre 2026.

| # | Question | Décision | Conséquence ou autre choix |
|---|---|---|---|
| C1 | Le point d'accès du PC, sur quels systèmes ? | ✅ **Windows et Linux** en J6c. | Windows seul, Linux en J8 : J6c plus court. |
| C2 | L'essai #169 (un second téléphone rejoint par QR) | ✅ **En J6c** : un second téléphone Android est disponible. | — |
| C3 | Clients de la sonde à 5 GHz | ✅ **Le PC et Zorin**, s'il capte le 5 GHz. | Le PC seul. |
| C4 | L'API du point d'accès sous Windows | ✅ **Wi-Fi Direct, réglages *legacy*** (`WiFiDirectAdvertisementPublisher`), tranché par la sonde WP-C.1 : il crée un réseau sans connexion Internet, sans droits administrateur. Windows n'y distribuant pas d'adresses, **l'application y sert le DHCP** (décision du 28 octobre, plutôt que de réparer le partage ICS ou de reporter Windows à J8). | « Point d'accès mobile » : impossible sans connexion à partager, c'est-à-dire justement quand aucun réseau n'est disponible. |
| C5 | Le PC à 5 GHz | ✅ **Sondé aussi** (28 octobre) : le PC crée-t-il son réseau à 5 GHz, par chaque API, dans le respect de la réglementation (§ WP-C.0) ? | Le 5 GHz du téléphone seul. |
