# medXfer — Rapport de sortie du lot « Câble Ethernet entre deux PC »

**Date :** 1er novembre 2026 · **Plan :** [medxfer-plan-cable.md](medxfer-plan-cable.md) · **Bible :** `DISC-13` (nouvelle), `STO-04` (complétée) · **PR :** #233 (plan), #234 (sonde, décisions), #235 (WP-E.1), #236 (WP-E.2), #237 (WP-E.3), #239 (WP-E.5), et celle de ce rapport (WP-E.4).

Ce rapport répond au critère de sortie du plan : *Windows ↔ Linux, sans réglage, se voient, s'appairent et transfèrent 1 Go dans chaque sens, avec le débit mesuré ; Windows ↔ Windows avec le second PC Windows de l'utilisateur ; Linux ↔ Linux par les tests automatiques.*

---

## 1. Verdict

**Le lot est terminé.** Deux PC reliés par un câble Ethernet direct se trouvent, s'appairent et transfèrent sans aucun réglage, sous Windows comme sous Linux. Entre deux PC récents, le transfert tient **101 à 106 MB/s**, 91 à 96 % des 110 MB/s bruts du câble. C'est le lien le plus rapide de medXfer, devant le Wi-Fi Direct à 5 GHz (53 MB/s, J6c).

| Avant le lot | Après |
|---|---|
| Un Linux réglé par défaut n'avait aucune adresse sur un câble direct | L'application Linux lui en donne une, sans toucher aux profils de l'utilisateur |
| Câble et Wi-Fi vers le même PC : le chemin retenu changeait d'un balayage à l'autre | Un chemin par réseau, câble d'abord, et l'utilisateur choisit |
| L'adresse montrée sous le PIN pouvait être injoignable (le Wi-Fi, un pont LXC éteint) | Une adresse par réseau ; interfaces sans lien et ponts virtuels écartés |
| Vers un disque dur, le transfert tombait à 40 MB/s | 57,7 MB/s : le commit groupé tourne en arrière-plan |

## 2. Critère de sortie

| Partie | Résultat | Preuve |
|---|---|---|
| Windows ↔ Linux sans réglage, 1 Go dans chaque sens | ✅ | PC Windows ↔ Zorin, Zorin avec les réglages par défaut de NetworkManager : adresse donnée par l'application en 65 s, appairage par « Câble » et PIN. PC → Zorin 55,2 MB/s, Zorin → PC 70,1 MB/s, sans reprise, empreintes identiques. Les deux débits sont limités par le disque dur de Zorin (2011, plein à 98 %), d'après les rapports des deux côtés. |
| Windows ↔ Windows | ✅ | Second PC de l'utilisateur, paquet portable : 101,3 et 106,1 MB/s, sans reprise, empreinte identique. La limite est le câble. |
| Linux ↔ Linux | ✅ (tests) | Pas de second PC Linux (décision E3) : `TestBrowseAllKeepsAPathPerNetwork`, `TestOSKindFromSysfs` et les tests de `LinuxCableLink`. |
| Les configurations du plan | ⚠️ | Câble seul (1), adresses réglées à la main (3), câble et Wi-Fi (4) : essayés. Partage de connexion (2) : pas en direct (réserve 1). |

## 3. Les décisions

| # | Décision (30 octobre, après la sonde) |
|---|---|
| E1 | Un Linux sans IPv4 sur le câble : l'application donne l'adresse, par un profil NetworkManager temporaire `link-local`. L'IPv6 seul était exclu : il est désactivé sur les cartes du PC des essais. |
| E2 | Câble et Wi-Fi vers le même PC : les deux chemins sont montrés, l'utilisateur choisit. |
| E3 | Windows ↔ Windows avec un second PC ; Linux ↔ Linux par les tests automatiques. |

## 4. Ce qui a été livré

| PR | Lot | Contenu |
|---|---|---|
| #233, #234 | Plan, WP-E.0 | Sonde sur le vrai câble : 110 MB/s bruts ; aucune IPv4 sous Linux par défaut ; IPv6 désactivé sur le PC ; chemin instable ; adresse montrée fausse. Décisions E1 à E3. |
| #235 | WP-E.1 | Un chemin par réseau (`Path`, nature `ethernet`, `wifi`, `other` : type de carte sous Windows, `/sys/class/net` sous Linux) ; interfaces sans lien et ponts virtuels écartés ; `peers_list.paths`, `pair_pin_shown.addresses`. Bible `DISC-13`. |
| #236 | WP-E.2 | `LinuxCableLink` : après l'échec du DHCP (au plus 50 s), un profil `medxfer-cable-<carte>` en `link-local`, retiré au débranchement, à la fermeture (fenêtre ou signal) et au démarrage s'il reste d'une fermeture brutale. |
| #237 | WP-E.3 | L'écran : « par câble », un bouton par chemin, le PIN par réseau, les avis (adresse donnée sous Linux, adresse `192.168.137.1` d'un partage restée sous Windows). |
| #239 | WP-E.5 | Le commit groupé en arrière-plan (`STO-04`) : vers un disque dur, 40 → 57,7 MB/s, 90 % de ce que le disque écrit. |
| celle-ci | WP-E.4 | Essais réels et ce rapport. |

## 5. Ce que les essais ont trouvé, et sa correction

| Constat | Cause | PR |
|---|---|---|
| Linux par défaut sans adresse sur le câble | NetworkManager abandonne le DHCP à 45 s, sans repli | #236 |
| Le chemin retenu changeait d'un balayage à l'autre (110 ou 2,4 MB/s) | La découverte gardait la dernière réponse | #235 |
| Zorin montrait `10.0.3.1` sous son PIN | Un pont LXC éteint, ni reconnu virtuel ni écarté faute de lien | #235 |
| Le PC montrait son Wi-Fi, injoignable par le câble seul | Une seule adresse montrée | #235 |
| Arrêtée par un signal, l'application Linux laissait son profil sur la carte | Seule la fermeture de la fenêtre passait par l'arrêt propre | #236 |
| Le câble du PC sans adresse `169.254` | L'adresse `192.168.137.1` d'un partage arrêté, restée sur la carte | #237 (l'avis) ; retirée par l'utilisateur |
| Vers un disque dur, 40 MB/s pour un disque qui écrit 64 | Réception et `fsync` alternaient : le commit tournait dans l'écrivain | #239 |

## 6. Réserves et suite

| # | Réserve | Suite |
|---|---|---|
| 1 | Un PC qui partage sa connexion sur le câble (configuration 2) n'a pas été essayé en direct : le PC des essais risquait de perdre Internet | Avec le MikroTik de l'utilisateur, déjà prévu (rapport J6c) |
| 2 | Sous Linux par défaut, la première adresse arrive en 50 à 65 s : l'application attend l'échec du DHCP pour ne jamais prendre la place d'un vrai réseau | Plus tôt quand NetworkManager a déjà abandonné avant le lancement, à étudier |
| 3 | Sur Zorin, le SHA-256 générique de Go (processeur sans AVX2) et le disque dur limitent le débit | Matériel. Une autre fonction de hachage serait un changement de protocole (revue G4) |
| 4 | Le pare-feu Windows : le second PC a dû autoriser medXfer sur les réseaux publics | L'installateur de J8 |
| 5 | Tests intermittents sur macOS : #184, #240 | À reproduire en boucle et corriger |
| 6 | L'IPv6 link-local n'est pas pris en charge | Inutile avec E1 ; à revoir si un système n'a plus d'IPv4 |
| 7 | Guide d'utilisation de l'application | **Reporté à la première version** : l'application change encore |

**Suite** (feuille de route) : J6d, se connecter au réseau par BLE.

## 7. Environnement

- **PC Windows 11** : Intel I219-LM (1 Gbit/s), IPv6 désactivé sur ses cartes ; application construite depuis `main`.
- **Second PC Windows** (`DESKTOP-OSRR12L`) : paquet portable `medxfer-0.1.0-windows-x64.zip`.
- **Zorin** : HP EliteBook 8460p, i5-2520M (2011), `enp0s25` (1 Gbit/s), disque dur Hitachi 7 200 tr/min plein à 98 %, NetworkManager 1.36 ; paquet Linux de la CI.
- **Câble** : RJ45 direct entre les deux PC ; les PC aussi sur le même Wi-Fi, le point d'accès d'un téléphone à 2,4 GHz.
