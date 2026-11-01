# medXfer — Plan du lot « Câble Ethernet entre deux PC »

> **Source :** feuille de route, lot « Câble Ethernet entre deux PC » (demande de l'utilisateur du 29 octobre 2026, placé après J6c et avant J6d) ; rapport [j6c-report.md](j6c-report.md) ; bible `DISC-10`.
>
> **Objectif :** deux PC, sous Windows ou Linux et quelle que soit leur configuration, reliés par un câble Ethernet direct, se trouvent, s'appairent et transfèrent des fichiers, **sans réglage manuel**.
>
> C'est potentiellement le lien le plus rapide entre deux PC : 1 Gbit/s, soit environ 110 MB/s, deux fois le meilleur débit mesuré jusqu'ici (53 MB/s, Wi-Fi Direct à 5 GHz, J6c).
>
> **Critère de sortie :**
> - Windows ↔ Linux, sans réglage : les deux PC se voient, s'appairent et transfèrent 1 Go dans chaque sens, avec le débit mesuré ;
> - dans chacune des configurations du §2 (WP-E.0) ;
> - Windows ↔ Windows avec le second PC Windows de l'utilisateur (décision E3) ; Linux ↔ Linux par les tests automatiques.
>
> **Durée estimée :** 3 à 5 jours. Une PR par lot, CI verte sur les trois systèmes avant fusion.

## 0. État de départ (constaté sur `main` le 30 octobre 2026)

| Élément | État |
|---|---|
| PC Windows | Intel I219-LM (1 Gbit/s), câble débranché. Windows se donne déjà une adresse link-local (`169.254.185.66`). L'adresse fixe `192.168.137.1`, reste d'un ancien partage, a été retirée en J6c. |
| Zorin | HP EliteBook 8460p, `enp0s25` (1 Gbit/s), NetworkManager 1.36.6. Profil « Connexion filaire 1 » en DHCP. Pas de réglage `ipv4.link-local` : il n'existe qu'à partir de NetworkManager 1.40. |
| Le cœur, IPv4 link-local | Pris en charge. `scoreInterface` classe ces adresses à 40, sous le Wi-Fi (80). Sur un PC qui a les deux, l'adresse montrée (QR, PIN, annonce) est celle du Wi-Fi, et le transfert y passe. |
| Le cœur, IPv6 | Ignoré à la découverte et à l'annonce (`scoreInterface` rend -1). Une cible `[fe80::…]` tapée est bien lue (#156), mais rien ne la trouve ni ne la montre. |
| Pare-feu Windows | Un câble sans DHCP donne un « réseau non identifié », profil **Public**. Les règles posées par l'installateur viennent en J8. |

## 1. Ordre d'exécution

```
WP-E.0 sonde (câble, adresses, découverte, débit) ──► WP-E.1 cœur ──► WP-E.3 écran ──► WP-E.4 essais réels, rapport
                                                  ├─► WP-E.2 Linux sans IPv4 (décision E1) ─────────────┤
                                                  └─► WP-E.5 débit sur le câble ────────────────────────┘
```

## 2. Les lots

### WP-E.0 — Sonde : le câble entre le PC Windows et Zorin · S

Un câble RJ45 ordinaire suffit : les cartes à 1 Gbit/s s'adaptent seules (droit ou croisé).

Les deux PC restent sur leur Wi-Fi le temps de la sonde, pour l'accès à Internet et à Zorin par SSH. Pour le cas « câble seul », Zorin est joint **par le câble** (SSH sur son adresse link-local), et son Wi-Fi est coupé par un script qui le rétablit toujours.

Questions, pour chaque configuration :

| # | Configuration | Ce qu'on attend |
|---|---|---|
| 1 | Câble seul, aucun DHCP (le cas principal) | Windows : `169.254.x.x`. Zorin : DHCP en échec après `ipv4.dhcp-timeout` (45 s par défaut). Lui reste-t-il une adresse IPv4 ? IPv6 link-local seulement ? NetworkManager relance-t-il la connexion en boucle ? |
| 2 | Un PC partage sa connexion sur l'Ethernet | Windows (ICS) : `192.168.137.1` et DHCP. NetworkManager (« Partagé avec d'autres ordinateurs ») : `10.42.0.1`. L'autre reçoit une adresse privée |
| 3 | Adresses fixées à la main | Celles de l'utilisateur, quelles qu'elles soient |
| 4 | Les deux PC aussi sur le même Wi-Fi | Deux chemins : le câble doit l'emporter |

Pour chacune :
- les adresses obtenues de chaque côté, et en combien de temps après le branchement ;
- la découverte mutuelle sur le câble : mDNS (`224.0.0.251`, `ff02::fb`) et la balise (UDP 19998) ;
- le débit brut (`nc`, puis `xfer bench net`), puis un transfert medXfer de 1 Go. Le processeur de Zorin date de 2011 : le hachage le limitera peut-être (réserve 1 de G2a), la mesure par étape le dira.

Un « Linux n'a aucune adresse IPv4 sur le câble » est un résultat valable : il décide de WP-E.2.

**Résultats de la sonde** (30 octobre ; PC Windows 11 ↔ Zorin par un câble direct ; `xfer bench net`, `xfer bench ladder` et `xfer ctl` du `main` du jour ; profils NetworkManager temporaires, celui de l'utilisateur rétabli à chaque fois) :

| Question | Résultat |
|---|---|
| Lien | ✅ 1 Gbit/s des deux côtés, ping de 0,4 à 1,1 ms. |
| Adresses, profil de l'utilisateur (Zorin : `ipv4.method link-local`, réglé à la main le 16 septembre ; configuration 3) | ✅ Windows `169.254.185.66`, Zorin `169.254.57.98`, 7 s après le branchement. |
| Adresses, **profil par défaut** de NetworkManager (DHCP, configuration 1) | ❌ **Aucune IPv4.** Seulement une adresse IPv6 link-local pendant 45 s ; à 46 s, « la configuration IP n'a pas pu être réservée » : l'activation échoue, et NetworkManager recommence. |
| IPv6 link-local | ❌ Inutilisable avec ce PC : l'IPv6 est **désactivé** sur ses deux cartes, Ethernet et Wi-Fi (`ms_tcpip6`). Un réglage courant : l'IPv6 seul ne suffit pas. |
| Débit brut | **109,4 et 111,4 MB/s** (latence 0,68 ms). Le même couple par le Wi-Fi, un point d'accès de téléphone à 2,4 GHz : 2,2 et 2,4 MB/s. |
| Échelle, PC → Zorin (512 Mo, 4 connexions) | R1 110,2 · R2 (TLS) 110,5 · R3 (trames, SHA-256) 99,4 · R4 (disque) 87,0 · R5 (journal, `fsync`) 39,4 · **R6 40,6 MB/s**, limité par `fsync` : le disque dur de Zorin (2011, 7 200 tr/min, plein à 98 %). |
| Échelle, Zorin → PC | R1 109,5 · R2 107,1 · R3 95,4 · R4 90,9 · R5 83,5 · **R6 83,3 MB/s**, limité par le réseau. Le processeur de Zorin (i5-2520M) tient le hachage. |
| Découverte, câble seul (Wi-Fi de Zorin coupé ; configuration 1) | ✅ Mutuelle et stable : chacun voit l'autre à son adresse `169.254`, 3 balayages sur 3. |
| Découverte, câble et Wi-Fi (configuration 4) | ⚠️ L'adresse retenue change d'un balayage à l'autre : le câble, puis le Wi-Fi, puis le câble. Un transfert peut partir à 2,4 MB/s au lieu de 110. |
| L'adresse que chaque PC montre de lui-même (QR, PIN, `local_ip`) | ❌ Le PC montre son Wi-Fi (`192.168.42.174`), injoignable par le câble seul. Zorin montre **`10.0.3.1`**, le pont LXC `lxcbr0`, éteint et sans câble : `scoreInterface` ne le reconnaît pas comme virtuel et ne regarde pas si l'interface a un lien. |
| `xfer bench … --listen` | Il ne propose que l'adresse du Wi-Fi : les adresses `169.254` sont écartées de la liste, comme dans le cœur (`server.go`). |
| Un PC partage sa connexion (configuration 2) | Pas essayé en direct : le PC aurait pris une adresse de Zorin avec Zorin pour passerelle, pour un bail d'une heure, et risquait de perdre Internet à la fin de l'essai. C'est un réseau local ordinaire, que medXfer gère déjà. L'essai réel avec le MikroTik de l'utilisateur reste prévu (réserve du rapport J6c). |

**Conclusion** :
- le câble porte le transfert à **83 MB/s** vers un PC rapide, et à 40 MB/s vers le disque dur de Zorin : le premier lien de medXfer au-delà du Wi-Fi Direct à 5 GHz (53 MB/s). Le coût de `fsync` sur un disque dur est un sujet pour J8 ;
- **sans réglage, un Linux n'a pas d'adresse IPv4 sur un câble direct**, et l'IPv6 peut être désactivé sous Windows : l'application Linux doit donner l'adresse elle-même (WP-E.2, décision E1) ;
- l'adresse montrée doit être celle du câble, et l'interface choisie doit avoir un lien et ne pas être virtuelle (WP-E.1) ;
- deux chemins vers un même PC sont montrés, et l'utilisateur choisit (décision E2).

### WP-E.1 — Le cœur : les adresses du câble · M

- **L'interface retenue** (`scoreInterface`, `GetPrimaryLocalIP`) :
  - une interface sans lien n'est pas retenue ;
  - les ponts virtuels sont reconnus comme tels : `lxcbr0`, `lxdbr0`, les `br-…` de Docker, `cni0`, `vboxnet…`.
- **Les adresses montrées** (QR, PIN, `local_ip`) : celles de chaque réseau où l'autre PC peut se trouver, le câble compris. Les adresses `169.254` ne sont plus écartées des listes (`server.go`, `xfer bench`).
- **Deux chemins vers le même appareil** (configuration 4, décision E2) :
  - l'appareil garde une adresse par chemin, au lieu de la dernière vue ;
  - le cœur les donne à l'interface (`peers_list`), avec la nature de chaque chemin : câble ou Wi-Fi ;
  - la session passe par celui que l'utilisateur a choisi.
- **IPv6 link-local** : pas dans ce lot. Avec la décision E1, chaque PC a une IPv4 sur le câble, alors que l'IPv6 peut être désactivé (constaté sous Windows).
- `DISC-10` mis à jour dans la bible, ou une nouvelle exigence pour le câble.

**Fait** (30 octobre) :
- `pkg/discovery` :
  - un chemin par réseau (`Path` : adresse et nature), dans un ordre stable, le câble d'abord ;
  - la nature vient du type de la carte sous Windows (`GetAdaptersAddresses`), de `/sys/class/net` sous Linux, sinon du nom ;
  - l'énumération écarte les interfaces sans lien (`net.FlagRunning`) et les ponts virtuels (`lxcbr0`, `lxdbr0`, `br-…`, `cni0`…).
- `peers_list` porte `paths` ; `pair_pin_shown` porte `addresses`, une par réseau. Les adresses `169.254` ne sont plus écartées (`status`, `xfer bench`).
- Nouvelle exigence `DISC-13` dans la bible.
- **Sur le vrai câble**, PC Windows et Zorin aussi sur le même Wi-Fi :
  - chacun voit l'autre par ses deux chemins, câble d'abord, à l'identique sur 3 balayages sur 3 ;
  - le PIN montre les deux adresses ;
  - Wi-Fi de Zorin coupé, Zorin montre `169.254.57.98`, l'adresse du câble, et non plus `10.0.3.1`.

### WP-E.2 — Linux sans IPv4 sur le câble · S (décision E1 : l'application donne l'adresse)

La sonde le montre : avec le profil par défaut, NetworkManager ne laisse aucune IPv4 sur un câble direct.
- Quand une carte Ethernet a un lien mais pas d'IPv4, l'application Linux active un profil NetworkManager temporaire, `medxfer-cable` :
  - `ipv4.method link-local`, sans démarrage automatique ;
  - par `nmcli`, sans droits administrateur, comme le point d'accès en J6c.
- Elle le retire quand le câble est débranché ou l'application fermée. NetworkManager revient alors au profil de l'utilisateur, qui n'est jamais modifié.
- Quand le déclencher : à l'échec du DHCP (45 s avec les réglages par défaut), ou plus tôt si l'essai montre qu'on peut le savoir sans risque pour un vrai réseau (une box qui répond lentement).
- Derrière une interface, à côté de `LinuxHotspot`. Rien sous Windows, qui se donne déjà son adresse.

**Fait** (1er novembre) :
- `CableLink` (`app/lib/platform/cable.dart`) et `CableWatch` : une vérification toutes les 5 s.
- L'application agit sur une carte filaire avec un lien et sans IPv4 :
  - après 50 s sans interruption ;
  - ou juste après un DHCP abandonné par NetworkManager.
- Elle ne touche ni aux profils de l'utilisateur, ni à un profil connecté sans IPv4.
- Le profil part :
  - au débranchement ;
  - à la fermeture, fenêtre fermée ou signal d'arrêt (SIGTERM, SIGINT : une fin de session laissait le profil sur la carte) ;
  - au démarrage, s'il reste d'une fermeture brutale.
- **Essai réel sur Zorin**, profil par défaut (DHCP, priorité au-dessus de celle du profil de l'utilisateur) :
  - aucune IPv4, puis le profil de l'application à 52 et 60 s (deux essais), et l'adresse `169.254.57.98` 5 s plus tard ;
  - application arrêtée par un signal : son profil supprimé, celui de l'utilisateur revenu seul.

Écartées :
- **l'IPv6 link-local seul** : inutilisable avec un PC dont l'IPv6 est désactivé, comme celui des essais ;
- **le PC Windows qui distribue les adresses** (comme son réseau en J6c) : il perturberait un vrai réseau si le câble menait à une box ou à un switch, et Linux ↔ Linux resterait sans solution.

### WP-E.3 — Écran · S

- Dans « Connecter », un appareil vu par le câble le dit (« par câble »).
- Un appareil vu par deux chemins (décision E2) : les deux sont montrés, câble et Wi-Fi, et l'utilisateur choisit. Si le chemin choisi tombe, l'application le dit, comme pour une session perdue.
- Sous le PIN, l'adresse du câble quand il est branché.
- Sous Linux, quand l'application donne l'adresse au câble (WP-E.2), elle le dit.
- Rien d'autre à régler : brancher le câble suffit.
- Ajouté après l'essai du 1er novembre : sous Windows, l'adresse `192.168.137.1` d'un partage de connexion arrêté depuis reste sur la carte Ethernet, et Windows ne lui donne plus d'adresse `169.254`. L'application le dit et dit comment remettre la carte en automatique ; elle ne change rien elle-même.

**Fait** (1er novembre) :
- La colonne « Connexion » du PC :
  - un appareil vu par le câble seul porte « par câble » ;
  - vu par le câble et par le Wi-Fi, il offre un bouton par chemin, « Câble » et « Wi-Fi », et la connexion passe par celui choisi.
- La fenêtre du PIN montre une ligne par réseau : « Câble : … », « Wi-Fi : … ».
- Les avis :
  - sous Linux, l'adresse donnée à une carte ;
  - sous Windows, l'adresse d'un partage restée sur la carte Ethernet (`WindowsCableLink`).
- Tests de l'écran : les deux chemins et le choix, « par câble », les lignes du PIN, les avis.
- Sous Windows, un câble sans DHCP est en profil Public. Si les règles du pare-feu manquent, l'application le dit, comme prévu en J8.

### WP-E.5 — Le débit sur le câble · M (demande de l'utilisateur, 30 octobre)

Sur le câble, le réseau n'est plus la limite : 110 MB/s bruts, mais 83 à 92 MB/s pour le transfert complet de Zorin vers le PC. L'utilisateur demande l'amélioration si medXfer en est la cause.

**Mesures** (30 octobre, Zorin → PC, `xfer bench ladder` et `matrix`, chunks de 2 Mo). Zorin a un i5-2520M de 2011 : AVX, sans AVX2 ni SHA-NI.

| Étape | 1 connexion | 2 | 4 |
|---|---|---|---|
| R1 TCP brut | 111,0 | 112,0 | 110,7 |
| R2 + TLS | 110,6 | 111,1 | 111,1 |
| R3 + trames et SHA-256 | **45,2** | 61,6 | 50,8 à 91,6 selon le moment |

- Le chiffrement ne coûte rien, même sur une seule connexion : AES-NI.
- **Le SHA-256 de chaque chunk** coûte le plus. Celui de Go fait 115 MB/s sur un cœur de Zorin (212 sur deux, 235 sur quatre), 2,4 fois moins qu'OpenSSL (277) : Go n'a pas de chemin rapide pour un processeur sans AVX2 ni SHA-NI. Hacher un chunk de 2 Mo prend 17 ms, autant que l'envoyer sur le câble.
- **Dans une connexion, le hachage et l'envoi ne se recouvrent pas** comme le prévoit `serveConn` (deux requêtes en vol) : 45 MB/s au lieu d'environ 110. C'est un défaut de medXfer.
- Le processeur de Zorin ne dépasse pas 51 % de ses 4 fils, même avec 8 connexions : une partie du travail reste en série.
- Les mesures varient d'un moment à l'autre : 91,6 puis 52 MB/s pour la même case, Zorin au repos. Chaque conclusion repose sur des mesures répétées (`--repeat 3`).

À faire :
1. trouver, par une trace d'exécution du côté qui envoie, pourquoi le hachage et l'envoi ne se recouvrent pas dans une connexion, et le corriger ;
2. lire le chunk directement dans le tampon de la trame, sans la copie actuelle ;
3. mesurer avant et après, sur Zorin et sur le PC, en répétant. Le but : le débit brut, moins le coût que le matériel impose.

Hors de ce lot : changer de fonction de hachage. C'est un changement de protocole, qui passe par la revue G4.

**Refait le 1er novembre, à la demande de l'utilisateur** : le transfert complet (R6) dans les deux sens, chaque mesure faite 3 fois (médiane), puis chaque perte qui vient de medXfer corrigée, avec la mesure avant et après. Les premières mesures, faites une fois chacune et à l'étape R3 seulement, ne suffisaient pas.

**Avant** (médianes de 3, 512 Mo, 4 connexions, chunks de 2 Mo, MB/s) :

| Sens | R1 | R2 | R3 | R4 | R5 | R6 |
|---|---|---|---|---|---|---|
| Zorin → PC | 111,2 | 111,4 | 104,1 | 103,5 | 90,7 | 100,7 |
| PC → Zorin | 111,0 | 110,9 | 98,7 | 88,6 | **39,4** (`fsync`) | **40,0** (`fsync`) |

Le 83 MB/s du 30 octobre (Zorin → PC) venait d'une mesure unique : répétée, elle donne 100,7.

**La grande perte : `fsync` sur un disque dur.**
- Le disque de Zorin écrit **64 MB/s** avec `fsync` (`xfer bench disk` : 60,2 et 63,8 ; `dd` : 64,6 et 64,9). medXfer n'en tirait que 40 : 62 %.
- Pas le rythme des commits : 256 Mio ou 8 s au lieu de 64 Mio ou 2 s donnent 40,4. Pas les écrivains concurrents : 1, 2 et 4 connexions donnent 30,6, 37,5 et 38,4.
- `vmstat` pendant R5 : le disque écrit **par à-coups**, 65 MB/s une seconde, 8 à 30 la suivante. Le commit tournait dans l'écrivain : le réseau remplissait le cache pendant que le disque attendait, puis le disque vidait 64 Mio pendant que la connexion de cet écrivain s'arrêtait. Réception et `fsync` alternaient.

**Correction** (`pkg/store`) : le commit groupé tourne en arrière-plan.
- `CommitIfDue` le lance et rend la main : la réception continue pendant le `fsync`.
- Quand le suivant est dû et que le précédent n'est pas fini, l'écrivain attend : le disque donne le rythme, et une coupure de courant coûte au plus deux commits à renvoyer.
- L'ordre de l'invariant J ne change pas : le commit ne prend que des chunks déjà écrits, rend les données durables, puis les note au journal (`TestPowerLossNeverLies`).
- L'erreur d'un commit en arrière-plan est rendue à l'appel suivant ; `Close`, `Suspend` et `Finalize` attendent celui qui tourne (`Settle`).
- `STO-04` complétée dans la bible.

**Après** (médianes de 3) :

| Sens | R5 | R6 |
|---|---|---|
| PC → Zorin | 39,4 → **58,8** | 40,0 → **57,7** (+44 %, 90 % de ce que le disque écrit) |
| Zorin → PC | 90,7 → **102,1** | 100,7 → **101,7** (91 % du TCP brut) |

**Ce qui reste vient du matériel :**
- de R2 à R3 (6 à 10 %), le SHA-256 de Zorin. Go y prend sa version générique : ses chemins rapides demandent AVX2 ou SHA-NI. Celui de Go fait 115 MB/s sur un cœur de Zorin, OpenSSL 277 ;
- vers Zorin, son disque.

Écartés, mesures à l'appui :
- **plus de requêtes en vol** par connexion (4 ou 6 au lieu de 2) : sur une seule connexion, l'envoi attend le hachage (trace d'exécution, 80 % du temps), et 4 ou 6 requêtes l'aident (63 → 70 → 77 MB/s). Mais avec les 4 connexions par défaut, R3 donne 98,5, 96,1 et 90,4 : rien à gagner. Les deux requêtes de la bible (§8.3) restent ;
- **la copie du chunk dans la trame** : environ 0,2 ms pour 2 Mo, contre 17 ms de hachage sur Zorin.

### WP-E.4 — Essais réels et rapport · S

- PC Windows ↔ Zorin, dans les configurations 1, 3 et 4, Zorin avec son profil par défaut puis le sien : découverte, appairage par PIN, 1 Go dans chaque sens, avec le débit et la mesure par étape.
- Windows ↔ Windows, avec le second PC Windows de l'utilisateur (décision E3).
- Linux ↔ Linux : pas de second PC Linux ; couvert par les tests automatiques.
- Rapport du lot.

**PC Windows ↔ Zorin** (1er novembre, application construite depuis `main` avec #239, des deux côtés) :
- **Configuration** : Zorin avec les réglages par défaut de NetworkManager (`ipv4.method auto` : l'utilisateur avait réglé son profil en `link-local` à la main, remis par défaut le 1er novembre). Le PC sans partage de connexion, sa carte en automatique. Les deux PC aussi sur le même Wi-Fi (configuration 4).
- **Adresse** : l'application de Zorin donne au câble son adresse seule, 65 s après son lancement (`169.254.57.98`).
- **Connexion** : la colonne « Connexion » du PC offre « Câble » et « Wi-Fi ». Appairage par « Câble » et le PIN de Zorin, confiance établie. Zorin est enregistré à `169.254.57.98` : la session passe par le câble.
- **Transferts de 1 Gio**, sans reprise, empreintes identiques à celles des originaux :

| Sens | Durée | Débit moyen (pic) | Limite, d'après les rapports des deux côtés |
|---|---|---|---|
| PC → Zorin | 18,6 s | **55,2 MB/s** (80) | `fsync` sur le disque dur de Zorin, qui écrit 64 MB/s (WP-E.5 : 57,7 à l'échelle) |
| Zorin → PC | 14,6 s | **70,1 MB/s** (91,5) | la lecture du fichier sur le disque dur de Zorin (`disk_read`, confiance haute) : 1 Gio hors de sa mémoire |

Les deux limites sont le disque de Zorin (2011, plein à 98 %). L'échelle, données en mémoire, donne 101,7 MB/s de Zorin vers le PC : le câble et medXfer vont plus vite que ce disque.

**PC Windows ↔ second PC Windows** (`DESKTOP-OSRR12L`, paquet portable construit depuis `main`, 1er novembre) :
- **Configuration** : réglages par défaut. Le second PC prend seul son adresse `169.254.79.36` ; les deux aussi sur le même Wi-Fi. À son premier lancement, le pare-feu a été autorisé sur les réseaux publics, où Windows classe le câble.
- **Découverte** : le second PC vu par ses deux chemins, câble d'abord, 3 balayages sur 3.
- **Connexion** : appairage par « Câble » et PIN, confiance établie. Le second PC est enregistré à `169.254.79.36` : la session passe par le câble.
- **Transferts de 1 Gio**, sans reprise, empreinte identique à celle de l'original pour le fichier reçu :

| Sens | Durée | Débit moyen (pic) | Limite |
|---|---|---|---|
| PC → second PC | 10,1 s | **101,3 MB/s** (120) | le réseau |
| Second PC → PC | 9,6 s | **106,1 MB/s** (110,5) | le réseau (`net_wait`, confiance haute) |

Entre deux PC récents, medXfer tient 91 à 96 % des 110 MB/s bruts du câble : la limite est le câble lui-même, comme le prévoyait WP-E.5.

**Rapport du lot** : [report-cable.md](report-cable.md).

## 3. Hors périmètre

- Les autres câbles : USB (partage de connexion d'un téléphone), Thunderbolt et USB-C entre deux PC.
- Les PC reliés par un switch ou un routeur : c'est déjà « le même réseau ».
- Les règles du pare-feu posées automatiquement : l'installateur de J8.

## 4. Décisions

Réponses de l'utilisateur du 30 octobre 2026, après la sonde.

| # | Question | Décision | Autre choix |
|---|---|---|---|
| E1 | Linux sans IPv4 sur le câble | ✅ **L'application Linux donne l'adresse** : un profil NetworkManager temporaire `link-local`, retiré quand le câble est débranché ou l'application fermée (WP-E.2). | L'IPv6 seul ; le PC Windows qui distribue les adresses (écartés en WP-E.2). |
| E2 | Deux chemins vers le même appareil (câble et Wi-Fi) | ✅ **Les deux sont montrés, l'utilisateur choisit** (WP-E.1, WP-E.3). | Le câble, automatiquement, avec repli sur le Wi-Fi. |
| E3 | Windows ↔ Windows et Linux ↔ Linux | ✅ **Windows ↔ Windows** avec un second PC Windows de l'utilisateur ; Linux ↔ Linux par les tests automatiques. | Windows ↔ Linux seulement. |
