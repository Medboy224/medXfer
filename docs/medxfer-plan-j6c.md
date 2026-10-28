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

### WP-C.2 — Adaptateurs du PC · M

- **Windows** : un module natif en C++/WinRT dans `app/windows/runner`, avec l'API retenue par WP-C.1, derrière `Hotspot` (canal de plateforme, comme sous Android).
- **Linux** : NetworkManager par D-Bus, derrière `Hotspot` (un paquet Dart de D-Bus, ou `nmcli`, d'après WP-C.1 ; une bibliothèque = un adaptateur).
- Tant que le point d'accès tourne, le cœur ne reçoit que son interface (`set_network_info`, `DISC-10`), comme sous Android : le QR, le PIN et l'annonce portent son adresse.
- Il s'arrête quand on ferme sa fenêtre si personne ne l'a rejoint, sinon à la fin de la session. Le Wi-Fi d'origine revient.
- `DISC-12` mis à jour dans la bible.

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
| C4 | L'API du point d'accès sous Windows | ⏳ Tranchée par la sonde WP-C.1. | « Point d'accès mobile » ou Wi-Fi Direct sans client Wi-Fi Direct. |
| C5 | Le PC à 5 GHz | ✅ **Sondé aussi** (28 octobre) : le PC crée-t-il son réseau à 5 GHz, par chaque API, dans le respect de la réglementation (§ WP-C.0) ? | Le 5 GHz du téléphone seul. |
