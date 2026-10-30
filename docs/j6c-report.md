# medXfer — Rapport de sortie du jalon J6c (point d'accès à 5 GHz, point d'accès du PC, QR de bout en bout)

**Date :** 30 octobre 2026 · **Plan :** [medxfer-plan-j6c.md](medxfer-plan-j6c.md) · **Bible :** `DISC-10`, `DISC-12`, `ID-05`, `DEV-11`, URI d'appairage (`ws`, `wp`, `wt`) · **PR :** #208 à #231, et celle de ce rapport.

Ce rapport répond au critère de sortie du plan :
- un rapport de sonde Wi-Fi Direct et la décision pour J8 ;
- l'appairage par QR de bout en bout sur les vrais appareils ;
- les à-coups sous Windows mesurés.

Il lève la réserve 1 de J6b (le QR jamais essayé de bout en bout) et la réserve 4 de G2a.

---

## 1. Verdict

**J6c est terminé.** Les trois critères sont remplis sur les vrais appareils, sans réserve bloquante.

Quand aucun réseau n'est commun, un appareil crée maintenant le réseau, sur chaque système :
- le **téléphone** : son point d'accès local (`LocalOnlyHotspot`). Un groupe Wi-Fi Direct à 5 GHz est sondé, et sera intégré en J8 ;
- le **PC** : sous Windows, un groupe Wi-Fi Direct vu comme un Wi-Fi ordinaire ; sous Linux, NetworkManager.

Les deux montrent un QR qui porte le réseau et l'appairage. Le téléphone qui le scanne rejoint le réseau et s'appaire seul. Un appareil sans caméra rejoint le réseau par ses réglages, puis tape le PIN.

| Avant J6c | Après J6c |
|---|---|
| Seul le téléphone créait un réseau, à 2,4 GHz | Le PC aussi (Windows, Linux) ; le téléphone peut le faire à 5 GHz (sonde, 53 MB/s) |
| QR jamais essayé de bout en bout | Essayé dans les trois sens prévus, plus un PC qui rejoint le téléphone par ses réglages |
| Lecture du QR : jusqu'à une minute | 0,4 à 1 s, de près comme de loin (zxing-cpp sur un écran caméra de l'application) |
| Téléphones Android 64 bits seulement | 32 bits aussi (`armeabi-v7a`) |
| Une adresse réattribuée faisait refuser l'appareil suivant | Le secret du QR ou un PIN vérifié l'autorisent ; sans preuve, le refus reste (`ID-05`) |
| Le paquet de diagnostic de l'application n'avait pas de journal | Le journal est ouvert, et un appairage interrompu y note sa cause |

---

## 2. Critère de sortie (plan, en-tête)

Essais réels du 28 au 30 octobre : Galaxy A57 (Android 16), TECNO BF6 (Android 12, 32 bits), PC Windows 11, Zorin (Linux).

| # | Critère | Résultat | Preuve |
|---|---|---|---|
| 1 | Rapport de sonde Wi-Fi Direct, décision pour J8 | ✅ | WP-C.0 (#210) : groupe à 5 GHz obtenu à chaque essai, **1 Gio en 19,2 s, 53,4 MB/s**. Intégration en J8 confirmée, choisie par téléphone selon la mesure (décision C7) |
| 2a | Un téléphone scanne le QR du PC, sous Windows | ✅ | Galaxy A57 → PC Windows : réseau rejoint, appairage, 1 Go à environ 12 MB/s, plus aucune coupure après #216 |
| 2b | Idem, sous Linux | ✅ | Galaxy A57 → Zorin : appairage 14 s après la lecture, 1 Go à environ 7,5 MB/s |
| 2c | Un second téléphone scanne celui du Galaxy A57 (#169) | ✅ | TECNO BF6 : réseau rejoint, appairage et confiance, environ 4 MB/s (limite de sa puce) |
| 2d | Un PC rejoint le téléphone par ses réglages Wi-Fi, puis le PIN (#169, WP-C.3) | ✅ | PC Windows → Galaxy A57 : appairage par PIN et confiance, **10,1 MB/s** (§5.1) |
| 3 | À-coups sous Windows mesurés | ✅ | WP-C.4 (#213) : aucun au repos en 10 min, aucun au-delà de 100 ms pendant un transfert |

---

## 3. Gates

**G2a, réserves restantes :**

| Réserve | État |
|---|---|
| 1. Débit sous Linux, 88 à 89 % de `xfer` (portable de 2011) | **Ouverte** : pas de PC Linux récent pour la remesurer |
| 4. À-coups de 230 à 240 ms sous Windows | **Levée** (WP-C.4) : au repos, pendant la navigation et pendant un transfert. La création du réseau figeait la fenêtre jusqu'à 1,48 s ; corrigé (#228), 69 ms au plus entre deux images |

Les autres gates ne changent pas : G4 (revue externe de la cryptographie) reste à organiser, G8 (environnement macOS) conditionne J9.

---

## 4. Ce qui a été livré

| PR | Lot | Contenu |
|---|---|---|
| #208, #209 | Plan | Plan J6c ; J6d (BLE) ajouté à la feuille de route |
| #210 | WP-C.0 | Sonde : groupe Wi-Fi Direct à 5 GHz sur le Galaxy A57 |
| #211 | WP-C.1 | Sonde : le point d'accès du PC ; décision C4 |
| #212 | WP-C.2, WP-C.3 | Le PC crée son réseau (Windows, Linux), l'écran du réseau, le QR de bout en bout ; `pkg/netbind` |
| #213 | WP-C.4 | Mesure des à-coups sous Windows |
| #214 à #231 | WP-C.5 | Essais réels et leurs corrections (§5) |
| #220, #225 | Feuille de route | Lot « Câble Ethernet entre deux PC » ; décision C7 et Android ↔ iPhone en J9 |

---

## 5. Les essais réels (WP-C.5)

### 5.1 Débits mesurés

| Lien | Bande | Débit |
|---|---|---|
| Groupe Wi-Fi Direct du Galaxy A57 → PC Windows (sonde) | 5 GHz | **53,4 MB/s** (1 Gio) |
| Réseau du PC Windows, Galaxy A57 → PC | 2,4 GHz | environ 12 MB/s (1 Go) |
| Point d'accès local du Galaxy A57, PC Windows → téléphone | 2,4 GHz (canal 11) | 10,1 MB/s (57 Mo) |
| Réseau de Zorin, Galaxy A57 → Zorin | 2,4 GHz | environ 7,5 MB/s (1 Go) |
| Point d'accès local du Galaxy A57, TECNO BF6 | 2,4 GHz | environ 4 MB/s, limite de la puce du TECNO |

À 2,4 GHz, le débit reste entre 4 et 12 MB/s quel que soit l'appareil qui crée le réseau. Seul le 5 GHz change d'ordre de grandeur, et seul le téléphone le crée : les cartes Intel des deux PC n'émettent pas les premières à 5 GHz (WP-C.1, réglementation appliquée par le pilote). D'où la règle : **le téléphone crée le réseau quand il le peut**.

### 5.2 Ce que les essais ont trouvé, et sa correction

| Constat | Cause | PR |
|---|---|---|
| Lecture du QR du PC en 24 s | Le lecteur ne lisait que 3,7 images par seconde ; QR trop petit sur l'écran du PC ; caméra en 720p | #214 |
| Zorin quitte son Wi-Fi sans le dire | NetworkManager crée le réseau sur la carte Wi-Fi elle-même | #214 (l'application le dit) |
| Le TECNO BF6 ne pouvait pas installer l'application | Android 32 bits | #215 (décision C6) |
| Windows coupait les téléphones toutes les 2 min | Windows demande d'accepter chaque téléphone, ce qui exige qu'il distribue lui-même les adresses ; une adresse de partage oubliée sur la carte Ethernet l'en empêchait | #216 |
| Test intermittent sous macOS dans la CI | Un transfert fermé restait connu du hub | #221 |
| L'application Windows s'arrêtait net (code 139) | L'annonce mDNS arrêtait un serveur absent, quand l'interface du réseau disparaissait | #222 |
| Un essai réel ne laissait pas de trace hors débogage | — | #224 (`MEDXFER_LOG_EVENTS`) |
| Wi-Fi du téléphone coupé : connexion refusée sans message (#218) | Android refuse de rejoindre un réseau Wi-Fi coupé | #226 |
| Le PC gardait à l'écran le QR d'un réseau mort (#219) | Windows arrête le groupe quand son Wi-Fi change de réseau, sans prévenir ; la création du réseau figeait aussi la fenêtre | #228 |
| Lecture du QR jusqu'à une minute, même bien cadré (#227) | La chaîne caméra de `flutter_zxing` : le même zxing-cpp trouvait le code mais le lisait mal | #229 (décision C8) |
| « Device key changed » pour un appareil neuf (#217) | L'adresse d'un appareil de confiance, réattribuée au suivant, était prise pour une attaque | #230 (`ID-05` reformulée) |
| Un échec d'appairage sans trace de sa cause (#169) | Le cœur de l'application n'ouvrait pas son journal de diagnostic | #231 |

### 5.3 La lecture du QR (décision C8)

L'utilisateur a demandé une lecture rapide en toute situation : QR à l'envers, de loin, avec le zoom. Trois lecteurs ont été comparés sur le Galaxy A57 :
- **ML Kit** : le meilleur mesuré, mais propriétaire. Il n'a servi que d'étalon : medXfer reste 100 % libre ;
- **BoofCV** : régulier mais plus lent ; il n'a rien lu que zxing-cpp ne lise ;
- **zxing-cpp** : retenu. Sur un écran caméra de l'application (CameraX, 1080p, zoom 1×, 2×, 3× et pincement), il lit en **0,4 à 1 s**.

---

## 6. Réserves et suite

| # | Réserve | Suite |
|---|---|---|
| 1 | Un vrai partage de connexion vers Ethernet (le MikroTik de l'utilisateur) : l'avertissement de l'écran du réseau en conditions réelles | Dès que l'utilisateur remet ce partage en place |
| 2 | Un second téléphone qui rejoint le **groupe Wi-Fi Direct** (le TECNO a rejoint le point d'accès local, pas le groupe) | J8, avec l'intégration du groupe |
| 3 | Android demande l'accord de l'utilisateur à chaque nouveau QR : le nom du réseau change à chaque création | J6d (BLE) ou J8 : un nom stable par appareil, à étudier |
| 4 | Une panic dans une goroutine du cœur arrête toute l'application (#222) | Un filet de sécurité (`recover` sur les goroutines de longue durée), à examiner à part |
| 5 | Les changements de page perdent des images (raster p90 de 12 à 19 ms) | Avant la v1 |
| 6 | Envoi par le sélecteur du système sans écran ni progression (#223), non reproduit | Le journal de l'application (#231) en gardera la trace s'il revient |
| 7 | Le premier essai PC Windows → Galaxy A57 a échoué une fois, cause inconnue (WP-C.5 du plan) | Idem : le journal note désormais la cause |
| 8 | Débit sous Linux (réserve 1 de G2a) | À remesurer dès qu'un PC Linux récent est disponible |
| 9 | Guide d'utilisation de l'application | **Reporté à la première version** : l'application change encore |

**Suite** (feuille de route) :
1. Le lot « Câble Ethernet entre deux PC » (décidé le 29 octobre).
2. J6d : se connecter au réseau par BLE.
3. J7, puis J8.
4. J9 (macOS et iOS), quand la gate G8 est levée. La note iOS de la feuille de route est complétée : iPhone ↔ iPhone, PC ↔ iPhone, et Wi-Fi Aware depuis iOS 26.

---

## 7. Environnement

- **Android** : Galaxy A57 (SM-A576B), Android 16 ; TECNO BF6, Android 12, Unisoc SC9863A, 32 bits. APK de publication signés avec la clé de développement, installés par adb.
- **Windows** : PC Windows 11, Intel Dual Band Wireless-AC 8265 ; versions de publication et de profil.
- **Linux** : Zorin OS, Intel Centrino 6205.
- **Réseaux** : le point d'accès local du Galaxy A57 (2,4 GHz) et son groupe Wi-Fi Direct (5 GHz, sonde) ; le groupe Wi-Fi Direct du PC Windows et le point d'accès NetworkManager de Zorin (2,4 GHz) ; Internet par le point d'accès d'un autre téléphone, à 2,4 GHz.
