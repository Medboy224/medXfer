# medXfer — Rapport de sortie du jalon J6d (se connecter au réseau par BLE, groupe Wi-Fi Direct à 5 GHz)

**Date :** 8 novembre 2026 · **Plan :** [medxfer-plan-j6d.md](medxfer-plan-j6d.md) · **Bible :** §6.6, `BLE-01` à `BLE-12`, `DISC-12`, `PAIR-12` · **PR :** #248 à #262, et celle de ce rapport.

Ce rapport répond au critère de sortie du plan :
- les quatre cas du tableau sur les vrais appareils, le QR toujours disponible en repli ;
- le groupe Wi-Fi Direct à 5 GHz créé par l'application, rejoint par un PC et par un second téléphone, avec le débit mesuré ;
- la conception écrite dans la bible et prête pour la revue G4.

---

## 1. Verdict

**J6d est terminé, avec deux réserves non bloquantes** (§6) :
- aucun second téléphone n'a pu rejoindre le groupe à 5 GHz, faute d'appareil qui capte le 5 GHz ;
- la portée tient à travers deux murs, mais le toucher y échoue parfois, sans message clair.

Deux appareils qui n'ont aucun réseau en commun se trouvent maintenant par Bluetooth Low Energy, dans la même liste « À proximité » que ceux du Wi-Fi. Toucher l'un suffit :
- l'un des deux crée le réseau, selon une règle fixe (5 GHz d'abord, §5.1) ;
- l'autre le rejoint **seul**, y compris un PC Windows, qui ne demande plus à l'utilisateur ni nom de réseau ni mot de passe ;
- la session s'ouvre sans code entre appareils de confiance, avec le PIN de l'autre sinon.

| Avant J6d | Après J6d |
|---|---|
| Sans réseau commun : un QR, ou les réglages Wi-Fi et un PIN | On touche l'appareil dans la liste : réseau et session suivent, en 2 à 7 s |
| Un PC ne rejoignait jamais seul le réseau d'un autre | Windows le rejoint seul, avec un profil Wi-Fi temporaire de l'utilisateur, puis revient à son Wi-Fi |
| Le téléphone créait son réseau à 2,4 GHz (4 à 12 MB/s) | Son groupe Wi-Fi Direct à 5 GHz, **53,7 MB/s** vers le PC |
| Le QR ne disait pas la bande : un téléphone sans 5 GHz cherchait en vain | Le QR porte la bande ; l'interrupteur « Haute vitesse (5 GHz) » recrée le réseau à 2,4 GHz |

---

## 2. Critère de sortie (plan, en-tête)

Essais réels du 2 au 8 novembre : Galaxy A57 (Android 16), TECNO BF6 (Android 12, 32 bits), PC Windows 11 (Intel AC 8265).

| # | Critère | Résultat | Preuve |
|---|---|---|---|
| 1a | Même Wi-Fi : on touche l'appareil (inchangé) | ✅ | PC → A57, 7 novembre. Dans l'autre sens, l'échec venait du partage USB du téléphone (#263, §6) |
| 1b | Appareil de confiance, autre réseau : BLE, réseau, session sans code | ✅ | A57 → PC en **4,3 s**, PC → A57 en **6,7 s** ; l'A57 crée son groupe à 5 GHz, le PC le rejoint seul (§5.2) |
| 1c | Appareil inconnu, autre réseau : BLE, réseau, PIN | ✅ | TECNO → PC, 3 et 7 novembre : le PC montre le PIN, le TECNO le tape ; le PC crée le réseau, le TECNO le rejoint |
| 1d | Le QR en repli | ✅ | L'A57 lit le QR du PC, 7 novembre. Zorin (sans BLE, décision D4) garde le QR et le PIN de J6c, inchangés |
| 2a | Groupe à 5 GHz rejoint par un PC, débit mesuré | ✅ | 1 Gio PC → A57 : **53,7 MB/s** à côté, **21,3 MB/s** à travers deux murs (§5.3) |
| 2b | Groupe à 5 GHz rejoint par un second téléphone | ❌ réserve | Aucun second téléphone ne capte le 5 GHz : le TECNO ne capte que le 2,4 GHz (WP-D.4) |
| 3 | Conception dans la bible, prête pour G4 | ✅ | §6.6 et `BLE-01` à `BLE-12` (#251) ; questions 12 à 17 du dossier G4 |

---

## 3. Gates

**G4 (revue externe de la cryptographie)** : le chemin par BLE s'y ajoute. Il n'a aucune primitive nouvelle : CPace du PIN (§6.2), HKDF et AES-256-GCM de la bibliothèque standard de Go, et la clé de la paire (`DISC-11`). Les questions 12 à 17 du dossier ([medxfer-g4-dossier-revue.md](medxfer-g4-dossier-revue.md)) le couvrent. La revue reste à organiser par l'utilisateur ; la bible sort en RC avec l'avertissement sur la cryptographie non revue.

Les autres gates ne changent pas.

---

## 4. Ce qui a été livré

| PR | Lot | Contenu |
|---|---|---|
| #248 | Plan | Plan J6d |
| #249, #250 | WP-D.0 | Sonde BLE sur les deux PC Windows et les deux téléphones |
| #251 | WP-D.1 | Conception : bible §6.6, `BLE-01` à `BLE-12`, dossier G4 |
| #252 | WP-D.4 | Le groupe Wi-Fi Direct à 5 GHz derrière `Hotspot`, la bande dans le QR, l'interrupteur 5 GHz |
| #253, #255 | WP-D.2 | Le protocole BLE dans le cœur (`pkg/pairing/ble.go`), puis dans l'API (`ble_server`, `ble_client`, `pair_ble`) |
| #254 | — | Un test instable de la découverte rendu stable |
| #257 | WP-D.3 | BLE sous Android, `BleLink` entre la radio et le cœur |
| #259, #260 | WP-D.3 | BLE sous Windows ; le PC qui rejoint seul un réseau (`wifi_join.cpp`) ; l'octet de donnée de service |
| #261, #262 | WP-D.5 | Les appareils BLE dans la liste « À proximité », sur le téléphone et le PC ; une liste qui oublie les appareils partis |
| ce rapport | WP-D.6 | Essais réels, le temps de chaque étape, et les corrections du §5.4 |

---

## 5. Les essais réels (WP-D.6)

### 5.1 Qui crée le réseau

La règle est celle du cœur (`Creator`, bible §6.6) :
1. L'appareil qu'on touche reçoit, chiffrées, les bandes sur lesquelles l'autre peut créer un réseau et celles sur lesquelles il peut en rejoindre un.
2. Il regarde d'abord le 5 GHz : lui-même s'il peut créer à 5 GHz et que l'autre peut y rejoindre, sinon l'autre si l'inverse est vrai.
3. Puis le 2,4 GHz, de la même façon. Sans aucune possibilité : pas de réseau, et l'écran le dit.

Le PC crée à 2,4 GHz seulement (WP-C.1) ; le téléphone crée à 5 GHz quand son Wi-Fi est allumé. D'où, dans les essais : l'A57 crée à 5 GHz face au PC, dans les deux sens ; le PC crée face au TECNO, qui ne rejoint qu'à 2,4 GHz.

### 5.2 Délais, du toucher à la session

| Cas | Réseau | Délai | Détail |
|---|---|---|---|
| A57 → PC (confiance), même pièce | Groupe de l'A57, 5 GHz | **4,3 s** | Vérification 0,13 s ; réseau créé 0,2 → 1,95 s ; session 4,3 s |
| PC → A57 (confiance), même pièce | Groupe de l'A57, 5 GHz | **6,7 s** | Vérification 1,4 s ; réseau donné 3,4 s ; PC sur le réseau 6,5 s |
| A57 → PC, pièce voisine | Groupe de l'A57, 5 GHz | 5,1 s | Mesuré côté PC depuis la demande |
| A57 → PC, deux murs | Groupe de l'A57, 5 GHz | 2,4 s | Côté PC depuis la demande, au troisième toucher |
| TECNO → PC (inconnu) | Groupe du PC, 2,4 GHz | 20 s | Du PIN affiché à la session, sa saisie comprise |

**Découverte.** Un appareil apparaît environ 1 s après avoir été capté : sa reconnaissance (un échange Bluetooth chiffré) dure 0,6 à 2,7 s. Il quitte la liste 6 s après avoir cessé d'être capté. Exception : au premier contact après le démarrage de l'application, un PC face à un téléphone appairé en Bluetooth met environ 30 s (§5.4).

### 5.3 Débits mesurés

| Lien | Bande | Débit (1 Gio, PC → A57) |
|---|---|---|
| Groupe de l'A57, même pièce, par BLE | 5 GHz (canal 48) | **53,7 MB/s**, 19,1 s, aucune reprise (rapport de l'A57) |
| Groupe de l'A57, deux murs, par BLE | 5 GHz (canal 149), signal 86 % | **21,3 MB/s**, 48,1 s, aucune reprise |
| Groupe de l'A57 par le QR (WP-D.4, 2 novembre) | 5 GHz | 54,4 MB/s |
| Le même, l'A57 aussi connecté à un autre Wi-Fi | 5 GHz | 18,6 MB/s (avertissement justifié) |

Chaque fois, l'empreinte SHA-256 du fichier reçu est celle de l'original, et la limite est le réseau, ni le disque ni le hachage.

**Portée du BLE** (A57, PC) : -46 à -67 dBm dans la même pièce ; la pièce voisine, porte ouverte, sans difficulté ; à travers deux murs, -82 à -96 dBm, l'A57 entrant et sortant de la liste toutes les quelques secondes. Le Wi-Fi à 5 GHz, lui, y passe encore bien.

### 5.4 Ce que les essais ont trouvé, et sa correction

| Constat | Cause | Correction |
|---|---|---|
| Le PC mettait jusqu'à 45 s à montrer l'A57 | Une reconnaissance à la fois : une ancienne adresse de l'A57, plus annoncée, retenait la file 26 s ; puis le suivant attendait 5 s | Reconnaissance abandonnée à 8 s, la suivante tout de suite, l'appareil vu le dernier d'abord, celle qui échoue attend 5 s |
| La liste gardait des appareils partis | Un appareil BLE n'en sortait jamais ; signalé toutes les 5 s, gardé 12 s | Signalé toutes les 2 s (Android, Windows), retiré après 6 s ; la liste du réseau refaite toutes les 5 s |
| L'un voyait l'autre sans réciproque | Deux reconnaissances croisées ; l'A57 gardait ensuite sa connexion au PC ouverte, et celle du PC restait sans réponse par-dessus | Une reconnaissance ferme sa connexion ; l'appareil reconnu attend 3 s avant la sienne |
| Après chaque session, ~30 s sans reconnaissance du PC | Chaque démarrage de BLE recréait le service GATT : Windows et Android avertissaient l'appareil appairé que ses services avaient changé, et le lien restait bloqué le temps d'une transaction (30 s) | Le service est gardé d'un démarrage à l'autre ; seuls l'annonce et la recherche s'arrêtent. Reste le premier contact après le démarrage (§6) |
| Second toucher refusé aussitôt après un échec | La connexion de l'échange échoué restait ouverte | Un échange échoué ferme sa connexion |
| « Aucun réseau » sans dire quoi faire | Wi-Fi éteint des deux côtés | Le message dit d'allumer le Wi-Fi de l'un des deux ; Windows, Wi-Fi éteint, ne se dit plus capable de créer |
| Une reconnaissance relancée BLE arrêté | Un dernier signalement de la radio, arrivé après l'arrêt | Ignoré |
| Un PC appairé en Bluetooth jamais reconnu (#258) | Un appairage abîmé : refait, l'échange marche (4 sur 4) | Issue gardée ouverte, à revoir avec d'autres appareils |

Chaque étape d'un échange est maintenant journalisée avec son temps, côté qui touche et côté touché : c'est la source des délais du §5.2.

---

## 6. Réserves et suite

| # | Réserve | Suite |
|---|---|---|
| 1 | Un second téléphone dans le groupe à 5 GHz (critère 2b) | Dès qu'un second téléphone qui capte le 5 GHz est disponible |
| 2 | Au premier contact après le démarrage, un PC face à un téléphone appairé en Bluetooth met ~30 s à le reconnaître (l'avis « services changés ») | Toucher depuis le téléphone, qui reconnaît le PC en 1 s, marche dès le départ ; une parade côté PC est à chercher (#258) |
| 3 | À la limite de portée, un toucher échoue sans message clair, et l'appareil clignote dans la liste | Réessayer seul, dire « trop loin, rapprochez-vous », garder plus longtemps un appareil au signal faible |
| 4 | Un téléphone qui partage sa connexion par USB : le toucher d'un PC vu sur ce câble échoue (#263) | Essayer le chemin suivant, ou écarter les interfaces du partage |
| 5 | Wi-Fi du téléphone éteint : il crée à 2,4 GHz sans le dire | L'écran pourrait proposer d'allumer le Wi-Fi pour le 5 GHz |
| 6 | La coupure écran éteint (#256) | Toujours ouverte |
| 7 | Un transfert mort affiché « en cours » après une coupure (3 novembre) | À corriger |
| 8 | BLE sous Linux (décision D4) | Sur le second PC, quand Zorin y sera installé |
| 9 | Revue G4 de la cryptographie BLE | L'utilisateur l'organise |
| 10 | Guide d'utilisation de l'application | **Reporté à la première version** : l'application change encore |

**Suite** (feuille de route) : J7, Web Share v2.

---

## 7. Environnement

- **Android** : Galaxy A57 (SM-A576B), Android 16, arm64 ; TECNO BF6, Android 12, Unisoc SC9863A, 32 bits (2,4 GHz seulement). APK de débogage (`dev.medxfer.app.debug`) installés par adb.
- **Windows** : PC Windows 11, Intel Dual Band Wireless-AC 8265 (Bluetooth 4.2, BLE central et périphérique) ; application de débogage.
- **Réseaux** : le groupe Wi-Fi Direct de l'A57 (5 GHz, canaux 36 à 149) et son point d'accès local (2,4 GHz) ; le groupe Wi-Fi Direct du PC (2,4 GHz). Internet du PC par le partage de connexion USB d'un des téléphones, pour suivre les essais.
