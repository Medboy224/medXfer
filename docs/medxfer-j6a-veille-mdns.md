# medXfer — Veille mDNS (WP-6.0 du jalon J6a)

> 14 octobre 2026. Prépare WP-6.5 (découverte) et les décisions D5 et D6 du [plan J6a](medxfer-plan-j6a.md).
> **Question :** quelle implémentation de `_medxfer._tcp` (`DISC-01`), et quel repli (`DISC-04`) ?

## 1. Résumé

| Question | Réponse |
|---|---|
| Bibliothèque | **`github.com/hashicorp/mdns` v1.0.7**, derrière l'interface `Discoverer` (D5 confirmée) |
| Android sans énumération des interfaces | ✅ Fonctionne, **à une condition** : les requêtes doivent sortir par l'interface Wi-Fi, désignée par son **index**. Sinon elles partent par les données mobiles (§3.2). |
| Windows | ✅ En recherche. En annonce, il faut une **règle de pare-feu UDP 5353** : à poser par l'installateur (J8) |
| Groupe de la balise de repli (D6) | ❌ **Aucune adresse de `224.0.0.0/24` ne peut être prise d'office** (§4). **D6 est à revoir** : je propose la diffusion sur le sous-réseau, sans groupe |

## 2. Candidats (état au 14 octobre 2026, API GitHub)

| Bibliothèque | Licence | Activité | Version Go | Énumère les interfaces ? | Verdict |
|---|---|---|---|---|---|
| **`hashicorp/mdns`** | MIT | v1.0.7 (juin 2026), dernier push le 12 octobre | v1.0.7 : 1.25 ; branche principale : 1.26 | Non : interface nulle ou fournie | ✅ **Retenue** |
| `brutella/dnssd` | MIT | v1.2.14 (octobre 2024) | — | **Oui** (`net.Interfaces()`, refusé sous Android) | ❌ pour Android |
| `pion/mdns` | MIT | v2.2.2 (octobre 2026) | — | — | ❌ Résolution de noms `.local` seulement, pas de DNS-SD (services) |
| `grandcat/zeroconf` | non reconnue par GitHub | figée depuis décembre 2023 | — | — | ❌ |
| `libp2p/zeroconf` | non reconnue | dernière version en 2022 | — | — | ❌ |

**Dépendances de `hashicorp/mdns` :** `github.com/miekg/dns` (BSD-3) et `golang.org/x/net` (BSD-3). Elles sont compatibles avec Apache 2.0.

**Limites connues :**
- pas d'annonce spontanée ;
- pas de « goodbye » à l'arrêt ;
- pas de vérification des conflits de nom.

Pour medXfer, c'est acceptable :
- nos clients interrogent le réseau à chaque recherche, plutôt que de garder un cache ;
- le nom `medXfer-xxxx` est tiré au hasard (une collision sur 65 536) ;
- les navigateurs de services du système garderont une entrée périmée jusqu'à son TTL au plus.

Le critère 5 (retrait propre) n'est donc qu'à moitié rempli. Si besoin, un adaptateur maison sur `x/net/dns/dnsmessage` pourra s'y substituer, derrière la même interface.

## 3. Essais réels (sonde hors dépôt, `hashicorp/mdns` v1.0.7)

Appareils :
- PC Windows (`10.86.69.174`, Wi-Fi en profil **Privé**, service Bonjour d'Apple présent sur 5353) ;
- téléphone Android sous Termux (`10.86.69.197`, Wi-Fi et données mobiles actifs) ;
- PC Zorin sous Linux (`10.86.69.91`).

### 3.1 Résultats

| Annonce | Recherche | Résultat |
|---|---|---|
| Téléphone | Téléphone | ✅ |
| Téléphone | PC Windows | ✅ |
| Téléphone | Zorin | ✅ |
| Zorin | PC Windows | ✅ |
| PC Windows | Téléphone | ❌ pare-feu (§3.3) |
| Zorin | Téléphone (interface par défaut) | ❌ (§3.2) |
| Zorin | Téléphone, **requêtes sur l'interface Wi-Fi** (index 50) | ✅ |

Chaque fois, l'enregistrement trouvé est complet : `medXfer-xxxx._medxfer._tcp.local.`, adresse, port 18888, TXT `v=1`.

### 3.2 Android : la sortie multicast doit être forcée sur le Wi-Fi

Ce que l'on observe :
- Le téléphone **répond** aux requêtes des autres. Elles viennent d'un port quelconque : c'est une requête « legacy unicast » au sens de la RFC 6762 §6.7, à laquelle il répond en unicast, et cette réponse part par le Wi-Fi.
- Mais **ses propres requêtes multicast** partent par l'interface de la route par défaut, `rmnet0`, c'est-à-dire les données mobiles. Elles n'atteignent jamais le réseau local. C'est la même cause que #109.
- Demander une réponse en unicast (bit QU) ne change rien : c'est la requête elle-même qui n'arrive pas.

La correction :
- donner à la recherche l'interface Wi-Fi **par son index** ;
- sous Linux et Android, Go règle `IP_MULTICAST_IF` et l'adhésion au groupe avec `ip_mreqn`, qui ne demande que l'index : pas de netlink ;
- l'index s'obtient par `ioctl(SIOCGIFINDEX)` à partir du nom de l'interface, autorisé dans Termux (`swlan0` → 50), alors que `/sys/class/net` est refusé ;
- dans l'application, le nom vient de `set_network_info` (`DISC-10`), et la plateforme peut aussi donner l'index directement.

**Pour WP-6.5 :** l'adaptateur mDNS reçoit les interfaces de `set_network_info` (nom, et index ou à défaut `SIOCGIFINDEX`). Il fixe l'interface multicast pour l'annonce (`Config.Iface`) comme pour la recherche (`QueryParam.Interface`). Sur le bureau, l'interface par défaut suffit tant que la route par défaut est locale. Sinon, on applique la même règle que #109 : l'interface locale plutôt que l'interface cellulaire.

### 3.3 Windows : règle de pare-feu UDP 5353

- Le Wi-Fi du PC est en profil **Privé**. Les règles qui autorisent `bin\xfer.exe` ne valent que pour le profil Public, et les seuls ports ouverts sur tous les profils sont 18888 (TCP), 19998 et 18889 (UDP).
- Le PC peut **chercher**, car les réponses à ses propres requêtes passent, mais il ne reçoit pas les requêtes des autres : il ne peut donc pas **être trouvé**.
- **Pour J8, l'installateur :** une règle entrante UDP 5353 pour le programme medXfer, profils Privé et Public. Le guide de développement donnera la commande équivalente.
- La présence du service Bonjour d'Apple sur le même port ne gêne pas : les deux sockets coexistent.

## 4. Le groupe de la balise de repli (D6)

D'après le registre de l'IANA (« IPv4 Multicast Address Space Registry », bloc *Local Network Control Block*, consulté le 14 octobre 2026) :
- les adresses de `224.0.0.0/24` s'attribuent par **revue d'expert, approbation de l'IESG ou action de normalisation**. Il en reste de libres (par exemple `224.0.0.153` à `224.0.0.250`), mais en prendre une sans attribution revient à **squatter** un bloc géré ;
- `224.0.0.254` (expériences selon la RFC 3692) « **MUST NOT be shipped as defaults in implementations** ».

La règle `DISC-04` (« un groupe de la plage non routée `224.0.0.0/24` ») ne peut donc pas être appliquée telle quelle.

**Proposition (D6 révisée) :** la balise de repli part en **diffusion sur le sous-réseau** (adresse de broadcast de chaque interface, port UDP 19998), **sans groupe multicast**.
- Le code le fait déjà : c'est son « vecteur A ». On retire seulement le vecteur multicast vers `239.255.255.250`.
- Aucun registre en jeu, et G2A-09 a vu la diffusion fonctionner sous Android.
- La charge utile reste celle du TXT, au plus une balise toutes les 2 s, et seulement sur demande (`DISC-05`).

**Autres options, moins bonnes :**
- une adresse de `239.255.0.0/16`, à portée administrative (RFC 2365), avec un TTL de 1. Elle est permise sans attribution, mais partage l'espace de SSDP ;
- demander une adresse à l'IANA. C'est la seule façon propre d'avoir un groupe à nous ; les délais sont inconnus, et ça peut se faire plus tard sans rien casser, grâce à l'adaptateur.

`DISC-04` serait reformulée dans la bible en conséquence.

## 5. Ce qui change dans le plan J6a

- **D5** confirmée : `hashicorp/mdns` v1.0.7, avec l'interface multicast fixée par index (§3.2).
- **D6** à revalider : la diffusion sur le sous-réseau, sans groupe (§4).
- **J8** : règle de pare-feu UDP 5353 dans l'installateur Windows.
