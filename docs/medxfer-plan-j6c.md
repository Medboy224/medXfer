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
| Un second téléphone le rejoint par `WifiNetworkSpecifier` | Reporté à J8, avec l'intégration du groupe : en WP-C.5, le TECNO BF6 a rejoint le point d'accès local du Galaxy A57, pas le groupe. |

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

**Réserve** : le PC Windows coupe le Galaxy A57 toutes les 2 min exactement (« Sécurité sans fil interrompue » côté Windows), pas Zorin (5 min sans coupure, avec ou sans DHCP). Palliatif : le téléphone rejoint seul le réseau perdu (Android ne redemande pas l'accord), la session revient seule (#202) : environ 3 s de coupure toutes les 2 min. **Cause trouvée et corrigée en WP-C.5** (ci-dessous).

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

**Résultats** (28 et 29 octobre ; version profile, Impeller sur OpenGL ES, PC de développement). Un écran immobile ne dessine rien : avec `--dart-define=MEDXFER_PERF_TICK=true`, `perf.dart` demande une image à chaque vsync, et le journal donne aussi l'image la plus longue et le plus grand écart entre deux images.

| Phase | Durée | Images | Au-delà de 16,7 ms | Au-delà de 100 ms | Plus longue |
|---|---|---|---|---|---|
| Repos | 10,4 min | 37 366 (60/s) | 0,55 % | **0** | 76 ms (écart max 83 ms) |
| Navigation entre les pages seule | 3,5 min | 12 612 (60/s) | 12,4 % | 1 | 112 ms |
| Clics, avec « Choisir des fichiers » | 6,8 min | — | — | 17, dans 9 fenêtres de 2 s | 604 ms |

- **Au repos, aucun à-coup** : la réserve 4 de G2a ne se reproduit pas en 10 minutes, où elle aurait dû apparaître 2 ou 3 fois.
- **Les arrêts de 400 à 600 ms** de la phase de clics viennent de la fenêtre « Choisir des fichiers » du système : elle est modale et bloque la fenêtre de l'application tant qu'elle est ouverte. Sans elle, la navigation seule en donne un seul, de 112 ms, et aucun au-delà.
- **Les changements de page perdent des images** : 12 % passent 16,7 ms, jusqu'à 67 ms, soit des passages à 30 images par seconde. C'est le dessin (raster p90 de 12 à 19 ms, contre 4 à 8 ms au repos), pas le code Dart (build p90 de 0,2 à 5 ms). C'est à creuser avant la v1, sans bloquer J6c.
- **Pendant un transfert** (29 octobre, WP-C.5) : deux transferts de 1 Go reçus par le PC, depuis le Galaxy A57. **Aucune image au-delà de 100 ms.** La seule de la séance, 333 ms, tombe à la création du réseau : le démarrage du groupe Wi-Fi Direct attend sur le fil de la fenêtre.

**Conclusion** : la réserve 4 de G2a est **levée**, au repos, pendant la navigation et pendant un transfert. Il reste la perte d'images aux changements de page, et l'attente à la création du réseau.

### WP-C.5 — Essais réels et rapport · S

- Appairage par QR de bout en bout :
  - Galaxy A57 → PC Windows ;
  - Galaxy A57 → Zorin ;
  - second téléphone → Galaxy A57 (#169).
- Un transfert de 1 Go par chacun de ces réseaux, avec son débit.
- Sous Windows, le transfert se fait avec la version profile, et le journal `perf:` (WP-C.4, la mesure pendant un transfert).
- Rapport de sortie, et décision sur Wi-Fi Direct pour J8.

**Résultats** (29 octobre) :

| Essai | Résultat |
|---|---|
| Galaxy A57 → Zorin par QR | ✅ Réseau de Zorin (2,4 GHz), appairage 14 s après la lecture, 1 Go à environ 7,5 MB/s. Zorin quitte son Wi-Fi le temps du partage (carte unique, NetworkManager), et l'application le dit désormais (#214). |
| Lecture du QR | 24 s au premier essai. Après #214 (lecteur sans pause entre deux images, 1080p, QR de 340 px sur PC) : **1,1 à 2,3 s** dès que le QR est dans le cadre, mesuré image par image. |
| TECNO BF6 → Galaxy A57 par QR (#169) | ✅ Lecture rapide (3 essais sur 4), réseau rejoint, appairage et confiance, transferts à environ **4 MB/s**. C'est la limite de la puce du TECNO (Unisoc SC9863A, 2,4 GHz, une antenne). Le TECNO est en Android 32 bits : d'où la décision C6. |
| Bande du point d'accès du Galaxy A57, son Wi-Fi allumé mais connecté à aucun réseau | **2,4 GHz** (canal 1, 2412 MHz) : Android ne passe pas à 5 GHz de lui-même. Le 5 GHz du téléphone ne vient que de Wi-Fi Direct (WP-C.0). |
| PC Windows → Galaxy A57 par les réglages Wi-Fi de Windows, puis le PIN (#169, 30 octobre) | ✅ Réseau du Galaxy A57 rejoint (2,4 GHz, canal 11, lien à 144 Mbit/s), appairage par PIN et confiance, 57 Mo du PC vers le téléphone en 5,4 s : **10,1 MB/s** en moyenne (pic 12). Le premier essai a échoué (« failed to pair with … ») sans laisser de trace de sa cause (journal absent, ci-dessous) ; le second, avec les événements journalisés, a réussi du premier coup. |

**La coupure de Windows toutes les 2 minutes** (29 octobre, cause trouvée et corrigée) :

| Essai | Résultat |
|---|---|
| TECNO BF6 sur le réseau du PC | Coupé aussi, exactement 120 s après chaque connexion (`reason=0`, côté PC) : la cause est Windows, pas le Galaxy A57. |
| L'application écoute les demandes de connexion (`WiFiDirectConnectionListener`) | Windows lui demande d'accepter chaque téléphone Android (`WiFiDirect#…_LegacyPendingRequest`). Sans réponse, il le coupe à 2 min. L'accepter (`WiFiDirectDevice.FromIdAsync`, sur le fil de la fenêtre comme l'exemple de Microsoft) échoue après 30 s, et Windows le coupe alors à 30 s. L'accès au périphérique est pourtant accordé (`DeviceAccessStatus` 1). |
| Cause | Pour accepter, Windows doit distribuer lui-même les adresses du réseau, par son partage de connexion (ICS, `192.168.137.1`). Sur ce PC, la carte Ethernet gardait `192.168.137.1`, fixée à la main, reste d'un ancien partage : Windows n'en distribuait aucune, d'où aussi le serveur DHCP de l'application en WP-C.2. |
| Adresse retirée (avec l'accord de l'utilisateur) | Windows distribue les adresses (`192.168.137.177`), acceptation en **93 ms**, **plus aucune coupure** (plus de 6 min, deux fois), 1 Go Galaxy A57 → PC à environ 12 MB/s. |

Correction (PR de WP-C.5) :
- le PC accepte les téléphones, sur le fil de la fenêtre, sans appairage Windows ;
- notre serveur DHCP ne démarre que si Windows ne distribue aucune adresse. Deux serveurs sur un réseau se répondraient l'un à l'autre ;
- dans ce cas, l'application cesse d'écouter : Windows coupe à 2 min et non à 30 s, et le téléphone revient seul. L'écran du réseau le dit, nomme la carte qui détient l'adresse du partage, et propose la voie stable : retirer l'adresse ou arrêter le partage, ou créer le réseau sur le téléphone (règle de WP-C.1).

Défauts relevés pendant ces essais, corrigés :
- **adresse réattribuée prise pour une attaque** (#217) : un appareil de confiance garde sa dernière adresse ; le suivant qui la reçoit était refusé (« device key changed », règle `ID-05`), même avec le secret du QR ou le bon PIN. Désormais, le secret du QR ou un PIN vérifié prouvent l'autorisation de l'utilisateur, et l'appareil reprend l'adresse ; sans preuve, le refus reste (`ID-05` reformulée, validée par l'utilisateur le 30 octobre) ;
- **Wi-Fi du téléphone coupé** (#218) : après le scan, Android refusait la connexion, sans message clair ; une fenêtre le dit et ouvre le panneau Wi-Fi, et la jonction reprend seule ;
- **réseau du PC perdu sans le savoir** (#219) : quand le Wi-Fi du PC change de réseau, Windows arrête le groupe Wi-Fi Direct sans prévenir l'application ; elle le voit désormais, le recrée, et dit de rallumer le Wi-Fi s'il est coupé ;
- **journal absent de l'application** (#169) : seule la ligne de commande ouvrait le journal de diagnostic. Le paquet de diagnostic de l'application (`DEV-11`) n'en contenait donc aucun, et l'échec d'appairage du 30 octobre, montré quelques secondes dans un message, n'a laissé aucune trace. Le cœur de l'application l'ouvre désormais dans son dossier de données, et un appairage interrompu y note sa cause.

Reste : un vrai partage de connexion vers Ethernet (le MikroTik de l'utilisateur), pour l'avertissement de l'écran du réseau en conditions réelles.

**Rapport de sortie** : [j6c-report.md](j6c-report.md) (30 octobre).

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
| C6 | Les téléphones Android 32 bits (`armeabi-v7a`), comme le TECNO BF6 de l'essai #169 | ✅ **Pris en charge dès J6c** (29 octobre) : la cible `android-arm` dans `build-lib.ps1`, un APK par architecture (`package-android.ps1`, `--split-per-abi`), les tests du cœur en 32 bits dans la CI (`GOARCH=386`). | Plus tard, en J8 ; ou 64 bits seulement. |
| C7 | Comment un téléphone Android crée le réseau : `LocalOnlyHotspot` ou un groupe Wi-Fi Direct (recherches de l'utilisateur, 29 octobre) | ✅ **Les deux, choisis par téléphone selon la mesure** (29 octobre) : le moyen qui crée à 5 GHz, sinon `LocalOnlyHotspot`. Les deux restent derrière l'interface `Hotspot` ; le groupe Wi-Fi Direct est intégré en J8. La bande de `LocalOnlyHotspot` sera mesurée sur chaque nouveau téléphone d'essai. | `LocalOnlyHotspot` seul : sur le Galaxy A57, il est toujours à 2,4 GHz (canal 1, mesuré Wi-Fi coupé, allumé sans réseau et connecté), 4 à 10 MB/s, contre 53 MB/s pour le groupe Wi-Fi Direct à 5 GHz (WP-C.0). Sa bande est choisie par Android et le fabricant, pas par l'application. |
| C8 | Le lecteur de QR (#227 : « il déraille parfois », jusqu'à une minute sans lecture) | ✅ **zxing-cpp 3.1.1 (Apache 2.0) par sa propre bibliothèque Android, sur un écran caméra de l'application** (CameraX, 1080p, l'image entière ; zoom 1×, 2×, 3× et pincement, mise au point au toucher et recentrée toutes les 2 s), et le QR affiché en correction M (30 octobre). Mesures sur le Galaxy A57 : 0,4 à 1 s de près comme de loin, au niveau d'ML Kit. **La cause était la chaîne caméra de `flutter_zxing`** : le même zxing-cpp y trouvait le code mais le lisait mal (2 261 images de suite en erreur de somme de contrôle) ; rejouées sur PC, les images de l'écran natif sont lues par zxing-cpp sans erreur. Rien à signaler au projet zxing-cpp. | ML Kit : le meilleur mesuré, mais propriétaire, seulement étalon (medXfer reste 100 % libre). BoofCV (Apache 2.0) : régulier mais plus lent, et n'a rien lu que zxing-cpp ne lise (136 images rejouées) ; écarté. |
