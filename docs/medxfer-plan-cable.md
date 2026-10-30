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
> - Windows ↔ Windows et Linux ↔ Linux si le matériel le permet.
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
                                                  └─► WP-E.2 Linux sans IPv4 (si la sonde le montre) ──┘
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

### WP-E.1 — Le cœur : les adresses du câble · M

- **IPv6 link-local**, si la sonde montre qu'il le faut :
  - découverte, annonce et connexions sur `fe80::…`, avec la zone (`%enp0s25`, `%12`) ;
  - la zone ne vaut que sur un PC : l'adresse montrée à l'autre (QR, PIN, annonce) n'en porte pas. Le PC qui se connecte y ajoute celle de son interface où l'appareil a été vu, ou essaie ses interfaces link-local une à une.
- **Le câble l'emporte** quand deux chemins mènent au même appareil (configuration 4) :
  - un appareil vu sur plusieurs interfaces garde ses adresses ;
  - la session passe par le lien le plus rapide (le câble, d'après la vitesse de la carte), et se replie sur l'autre s'il tombe.
- **L'adresse montrée** sous le PIN : l'IPv4 du câble quand elle existe, plus courte à taper qu'une adresse `fe80::…`.
- `DISC-10` mis à jour dans la bible, ou une nouvelle exigence pour le câble.

### WP-E.2 — Linux sans IPv4 sur le câble · S (selon la sonde)

Si la sonde montre que NetworkManager 1.36 ne laisse qu'une adresse IPv6, ou rien (décision E1) :

| | A. IPv6 link-local seulement | B. Profil NetworkManager link-local | C. Les deux |
|---|---|---|---|
| Principe | Le cœur passe par `fe80::…` (WP-E.1) | L'application crée, pour le câble, un profil `ipv4.method link-local` (`nmcli`, sans droits administrateur, comme le point d'accès en J6c) quand le DHCP échoue, et le retire ensuite | IPv6 d'abord, IPv4 link-local en plus |
| Pour | Ne touche pas aux réglages du système | Une adresse IPv4 courte à taper, comme sous Windows | Le plus robuste |
| Contre | Une adresse longue sous le PIN | Modifie les réglages réseau du système (réversible) | Le plus de code |

### WP-E.3 — Écran · S

- Dans « Connecter », un appareil vu par le câble le dit (« par câble »).
- Rien à régler : brancher le câble suffit.
- Sous Windows, un câble sans DHCP est en profil Public. Si les règles du pare-feu manquent, l'application le dit, comme prévu en J8.

### WP-E.4 — Essais réels et rapport · S

- PC Windows ↔ Zorin, dans les configurations 1 à 4 : découverte, appairage par PIN, 1 Go dans chaque sens, avec le débit et la mesure par étape.
- Windows ↔ Windows et Linux ↔ Linux si un second PC est disponible.
- Rapport du lot.

## 3. Hors périmètre

- Les autres câbles : USB (partage de connexion d'un téléphone), Thunderbolt et USB-C entre deux PC.
- Les PC reliés par un switch ou un routeur : c'est déjà « le même réseau ».
- Les règles du pare-feu posées automatiquement : l'installateur de J8.

## 4. Décisions

| # | Question | Recommandation | Autre choix |
|---|---|---|---|
| E1 | Linux sans IPv4 sur le câble | Après la sonde : **C** si NetworkManager ne garde que l'IPv6, sinon rien à faire | A ou B seuls (WP-E.2) |
| E2 | Deux chemins vers le même appareil (câble et Wi-Fi) | **Le câble, automatiquement**, avec repli sur le Wi-Fi | Demander à l'utilisateur |
| E3 | Windows ↔ Windows et Linux ↔ Linux | Si un second PC est disponible | Windows ↔ Linux seulement |
