# medXfer — Bible Technique v8

**Statut :** référence de conception, remplace la v7. Aucune partie n'est « gelée » : chaque décision porte un statut (voir §0.2) et une condition de réexamen.
**Périmètre :** transfert de fichiers entre appareils d'un même réseau local (LAN), sans compte, sans cloud, sans relais.
**Comment lire :** les chapitres 4 à 11 décrivent les couches du bas vers le haut. Chaque exigence a un identifiant (ex. `CHAN-03`), une raison et un test d'acceptation (chapitre 14). Un développeur peut implémenter une couche en lisant uniquement son chapitre et l'interface de la couche du dessous. Le chapitre 17 décrit les outils de développement, de mesure et de test.

---

## 0. Mode d'emploi et règles du document

### 0.1 Vocabulaire normatif
- **DOIT / NE DOIT PAS** : obligatoire. Une implémentation qui ne le respecte pas est incorrecte.
- **DEVRAIT** : recommandé ; l'écart doit être justifié par écrit.
- **PEUT** : optionnel.

### 0.2 Statut de chaque décision
| Tag | Signification |
|---|---|
| ✅ FONDÉ | Appuyé par une spécification, une source citée (Annexe B) ou une propriété démontrable. |
| 📏 À MESURER | Une valeur par défaut est donnée, mais elle ne devient définitive qu'après le benchmark indiqué. |
| ⏸ REPORTÉ | Hors périmètre v1. La condition de réintégration est écrite. |
| ❓ OUVERT | Décision à prendre par un prototype (spike). Le point de décision (« gate ») est au chapitre 16. |

### 0.3 Les cinq règles anti-effet-domino
La v7 mélangeait des choix indépendants (transport, crypto, stockage, thermique) : si l'un tombait, les autres tombaient avec lui. La v8 impose :

1. **R1 — Dépendance descendante uniquement.** Une couche ne connaît que l'*interface* de la couche du dessous, jamais son implémentation.
2. **R2 — Sécurité et performance sont découplées.** Aucune exigence de sécurité ne dépend d'une optimisation, et aucune optimisation ne peut affaiblir une exigence de sécurité.
3. **R3 — Les modules optionnels sont débranchables.** Web Share, thermique, historique : on peut les supprimer sans modifier le cœur.
4. **R4 — Une valeur non prouvée est un paramètre, pas une constante de protocole.** Exemples : nombre de flux, taille de chunk, seuils.
5. **R5 — Chaque phase de la feuille de route (chapitre 15) livre un produit qui fonctionne et se teste seul.**

### 0.4 Règle de preuve
Toute exigence DOIT avoir un test d'acceptation. Toute affirmation technique externe (RFC, comportement d'une bibliothèque, politique d'une plateforme) DOIT être sourcée ou marquée « non vérifiée ». L'Annexe B liste ce qui a été vérifié et ce qui ne l'a pas été.

---

## 1. Vision, priorités et critères « classe mondiale »

**Promesse :** envoyer un fichier d'un appareil à un autre sur le même réseau, sans compte ni cloud, à la vitesse du lien, avec une sécurité que l'utilisateur n'a pas à comprendre.

**Ordre de priorité en cas de conflit :** Sécurité > Fiabilité > Simplicité > Vitesse.
La vitesse vient en dernier parce qu'un transfert rapide mais corrompu ou interceptable n'a aucune valeur. Elle reste un objectif mesuré, pas sacrifié.

### 1.1 Critères mesurables
| Axe | Critère | Statut |
|---|---|---|
| Simplicité | Du choix du fichier à l'acceptation côté receveur : au plus 3 actions utilisateur et 10 s sur réseau normal. | 📏 |
| Vitesse | Débit utile ≥ 80 % de celui d'`iperf3` (TCP, 4 flux, 30 s) sur le même lien, chiffrement actif, données incompressibles. | 📏 (procédure §14.3) |
| Sécurité | Aucun octet de fichier ou de métadonnée en clair ; zéro faille connue des classes du chapitre 2 ; revue cryptographique externe des chapitres 6 et 7 avant la version 1.0. | ✅ |
| Fiabilité | 1000 itérations de test de chaos (arrêt brutal, coupure réseau, disque plein) sans fichier corrompu : le hash final est toujours identique. | 📏 |
| Confidentialité | Aucune télémétrie ; aucun nom d'appareil réel diffusé avant que la confiance soit établie. | ✅ |
| Qualité | 100 % des exigences couvertes par un test ; fuzzing des analyseurs de trames ; CI sur Windows, Linux, macOS, Android, iOS. | ✅ |

### 1.2 Non-objectifs de la v1
Relais Internet, comptes utilisateurs, synchronisation de dossiers, anonymat réseau, protection contre un système d'exploitation déjà compromis, transfert hors LAN.

### 1.3 Plateformes visées et ordre de validation
Cinq plateformes : **Windows, Linux, Android, macOS, iOS**. Le poste de développement est Windows ; il n'y a, à ce jour, ni Mac ni iPhone. Ordre de validation : (1) Windows, Linux, Android, validés par le prototype G2a (`medxfer-plan-prototype-g2a.md`, résultats dans `g2a-report.md`, octobre 2026 ; Linux sur un PC Zorin OS 17, sans WSL2) ; (2) macOS et iOS dès qu'un environnement macOS est disponible (gates G2b et G8). Le cœur Go est testé sur macOS dès la phase 0b par l'intégration continue (`DEV-20`).

---

## 2. Modèle de menace

### 2.1 Adversaires considérés
| Id | Adversaire | Exemple concret |
|---|---|---|
| A1 | Écoute passive du réseau | Wi-Fi de café, point d'accès partagé |
| A2 | Attaquant actif sur le LAN | Usurpation ARP, faux serveur mDNS, homme du milieu (MITM) |
| A3 | Appareil du LAN non appairé | Se connecte directement au port de données |
| A4 | Pair malveillant | Envoie un manifeste piégé (chemins `../`, noms réservés, millions de fichiers) |
| A5 | Page web malveillante | Un site ouvert dans le navigateur de l'utilisateur tente de joindre `127.0.0.1` |
| A6 | Invité Web Share hostile | Force brute du PIN, envoi de fichiers piégés |
| A7 | Appareil à portée radio, hors du réseau | Un inconnu dans le même café, à portée Bluetooth (§6.6) |

Hors périmètre : un autre processus malveillant tournant sous le même compte utilisateur (on réduit seulement l'exposition avec des permissions de fichiers strictes).

### 2.2 Menace → contrôle → exigences
| Menace | Contrôle | Exigences |
|---|---|---|
| A1 lit les fichiers | TLS 1.3 sur *toutes* les connexions, aucun texte clair | `CHAN-01`, `CHAN-02`, `CHAN-05` |
| A2 intercepte (MITM) | Épinglage d'empreinte (mTLS) + PAKE lié au canal | `CHAN-03`, `PAIR-03`, `PAIR-05` |
| A2 s'interpose au premier contact | Premier contact par QR ou PIN seulement, jamais sur une comparaison d'empreintes à l'œil | `PAIR-11`, `PAIR-12` |
| A3 profite d'une confiance unilatérale ou oubliée | Confiance mutuelle déclarée à chaque session ; rien d'écrit sans les deux oui | `PAIR-08` à `PAIR-10` |
| A2 force un repli en clair | Aucun chemin de repli ; handshake raté = connexion fermée | `CHAN-05` |
| A3 télécharge sans appairage | Connexion de données acceptée seulement si l'empreinte du pair correspond au transfert | `CHAN-06` |
| A3 devine le code | PAKE, 3 essais, durée de vie limitée | `PAIR-04` |
| A4 écrit hors du dossier / supprime des fichiers | Chemins validés composant par composant, jamais de suppression, dossier racine par transfert | `STO-09` à `STO-14` |
| A4 épuise les ressources | Limites chiffrées du chapitre 12 | `LIM-01` à `LIM-08` |
| A5 pilote l'application | Aucun socket de contrôle ; sinon contrôles Origin, Host, jeton | `API-01` à `API-04` |
| A6 force le PIN | 5 essais par *session*, pas par IP ; jeton porteur sans cookie | `WEB-03`, `WEB-04` |
| A7 suit un appareil par BLE | Annonce sans identifiant ; étiquettes neuves à chaque défi, toujours 16 ; aucune empreinte en clair | `BLE-02`, `BLE-04`, `BLE-06` |
| A7 obtient le réseau, rejoue ou relaie un échange BLE | Clés neuves à chaque échange, tirées de la clé de la paire ou du PIN ; empreinte épinglée ensuite sur le Wi-Fi | `BLE-05` à `BLE-10` |
| A7 devine le PIN par BLE | Le même PIN et le même compteur que sur TLS | `BLE-07`, `PAIR-04` |
| Crash ou disque défaillant | Journal ordonné + hash final | `STO-03` à `STO-08` |

---

## 3. Architecture en couches et registre de décisions

### 3.1 Les couches
```
 G  Surface de contrôle   cœur Go appelé en-processus (FFI) | CLI | loopback optionnel
 F  Stockage              fichier .part + journal + renommage atomique
 E  Protocole de transfert OFFER / CHUNK / DIGEST
 D  Canal sécurisé        TLS 1.3, mTLS, empreintes épinglées, ALPN
 C  Appairage             QR (secret fort)  |  PIN (PAKE)  |  appareil de confiance   (aussi par BLE, §6.6)
 B  Découverte            mDNS | balise UDP | BLE | saisie manuelle   (non fiable par conception)
 A  Identité et confiance clé persistante, empreinte, liste d'appareils de confiance

 H  Web Share (module séparé, ne dépend que de A et F)
 X  Diagnostic et outils (module séparé : observe le cœur par événements, voir chapitre 17)
```
**Principe central :** la découverte (B) est *non authentifiée* ; elle propose seulement une adresse. La confiance vient de A, C et D. Une découverte entièrement compromise ne peut donc pas compromettre la sécurité (règle R2).

### 3.2 Interfaces entre couches
Ces interfaces sont la seule dépendance autorisée entre couches (règle R1). Changer de transport (TCP vers QUIC), de hash ou de bibliothèque PAKE ne touche qu'une seule couche.

```go
// B — Découverte
type Discoverer interface {
    Browse(ctx context.Context) (<-chan Peer, error)   // Peer = {Addr, Port, GenericName}
    Advertise(ctx context.Context, a Advert) error
}

// A — Identité
type Trust interface {
    Self() Identity                       // clé, certificat, empreinte
    Contains(fp Fingerprint) bool
    Add(fp Fingerprint, alias string) error
}

// D — Canal sécurisé : renvoie une connexion DÉJÀ authentifiée ou refusée
type SecureDialer interface {
    Dial(ctx context.Context, addr string, expected *Fingerprint) (SecureConn, error)
}
type SecureConn interface {
    net.Conn
    PeerFingerprint() Fingerprint
    ChannelBinding() ([]byte, error)      // ExportKeyingMaterial, 32 octets ; une erreur arrête l'appairage
}

// C — Appairage (la bibliothèque PAKE est cachée derrière cette interface)
type Pairer interface {
    ByQR(ctx context.Context, c SecureConn, secret []byte) error
    ByPIN(ctx context.Context, c SecureConn, pin string, role Role) error
}

// F — Stockage
type ChunkSink interface {
    Put(item, index uint32, hash [32]byte, data []byte) error
    Have(item, index uint32) bool
    Commit() error                         // fsync des données puis du journal (§9)
}
```

### 3.3 Registre de décisions : v7 → v8
| Sujet v7 | Décision v8 | Raison | Réexamen si |
|---|---|---|---|
| QUIC + BBR | ⏸ REPORTÉ. TCP + TLS 1.3 multi-flux. | Gain non démontré sur LAN ; coût CPU élevé en espace utilisateur ; BBR n'apporte presque rien sur un LAN. | Le benchmark §14.3 reste sous 80 % d'iperf3 après optimisation de TCP+TLS. |
| TLS-PSK dérivé de SPAKE2 | ❌ Remplacé par mTLS + épinglage d'empreinte + PAKE lié au canal. | L'API standard de Go ne documente pas de PSK externe (recherche non concluante, Annexe B). Le remplaçant est un schéma classique et n'exige aucune fonction spéciale. | Jamais nécessaire. |
| SPAKE2 imposé | ✅ **CPace** (`CPACE-RISTR255-SHA512`), gate G1 fermée le 13 octobre 2026. QR sans PAKE dès la phase 2. | Choix du CFRG ; aucune constante publique ; vecteurs de la suite ; aucune arithmétique de corps à écrire (veille G1). | Une RFC ou une implémentation Go maintenue qui passe les mêmes vecteurs. |
| SQLite WAL | ⏸ REPORTÉ. Journal append-only de 40 octets/chunk. | Même garantie de crash pour une charge à écrivain unique, sans CGO ni dépendance. | Historique des transferts, recherche, statistiques. |
| BLAKE3 / SHA-256 adaptatif | ❌ SHA-256 unique (gate G6 pour confirmer). | Bibliothèque standard, accélérée matériellement sur amd64 et arm64 ; moins de code. | Le benchmark G6 montre un hash plus lent que 1,5× le lien sur un téléphone cible. |
| Compression LZ4/ZSTD | ⏸ REPORTÉ. | Les médias sont déjà compressés ; aucun gain ; complexité. | Mesure sur corpus de fichiers réels. |
| Régulation thermique | ⏸ REPORTÉ. | Seuils °C arbitraires ; `/sys/class/thermal` inaccessible sur Android récent ; API officielles à utiliser le moment venu. | Phase 6. |
| Migration de connexion QUIC | ❌ Supprimée. | Le pair doit être sur le même LAN ; la 4G ne le rejoint pas. | — |
| Moniteur 80 %/40 % + P95 | ❌ Supprimé. Fenêtre de requêtes bornée. | Le contrôle de flux de TCP bloque l'émetteur dès que le récepteur cesse de lire. | — |
| En-tête de bloc « 40 octets » | ❌ Redéfini (§8.2). | L'ancien comptait 56 octets, pas 40. | — |
| Taille de bloc 4→1 Mo en cas de chaleur | ❌ Supprimée. Taille fixe par transfert. | Changer la taille en cours de route casse `index × taille` et donc la reprise. | — |
| mDNS | ✅ Conservé, avec précisions iOS/Android (§5). | Standard géré par l'OS ; seule voie sans entitlement spécial sur iOS. | — |
| Code `XXX-YYY` avec octet d'IP | ❌ Supprimé. | Réduisait le PIN à 900 valeurs (~10 bits). | — |
| Daemon + WebSocket | ❌ Remplacé par un cœur Go en-processus (§10). Confirmé par le prototype G2a sur Windows, Linux et Android (`g2a-report.md`). | iOS interdit de lancer un autre programme et Android a fortement restreint l'exécution de binaires embarqués : un daemon séparé est impossible sur mobile. Un port d'écoute est en plus une surface d'attaque inutile. | Le prototype G2a échoue sur une plateforme de bureau (un processus séparé reste alors un plan B). |
| Tableau de bord web hôte (`IndexHTML`) | ⏸ Outil de développement seulement (`DEV-19`), absent des versions publiées, gelé, à supprimer. | Duplique l'interface Flutter, 2 400 lignes sans tests réels, surface d'attaque (sélecteur natif, dépôt temporaire). | Jamais en production. |
| Chiffrement WebCrypto du Web Share | ❌ Supprimé (§11.5). | `crypto.subtle` n'existe qu'en contexte sécurisé ; le JavaScript est servi par le même canal. | — |
| Chroot virtuel HTTP | ✅ Conservé via `os.Root` (Go ≥ 1.24). | Confinement sans fenêtre TOCTOU. | — |

---

## 4. Couche A — Identité et confiance

**But :** chaque appareil est reconnu par une clé qu'il garde pour toujours, pas par son adresse IP ni son nom.

Aujourd'hui (`session/tls.go`) le certificat est régénéré à chaque démarrage et le client ignore toute vérification : aucune confiance durable n'est possible.

| Id | Exigence | Raison |
|---|---|---|
| `ID-01` | Au premier lancement, l'appareil DOIT générer une paire de clés ECDSA P-256 et un certificat X.509 auto-signé, stockés dans le dossier de configuration avec permissions restreintes à l'utilisateur (Unix `0600` ; Windows : dossier du profil utilisateur). | P-256 est accepté partout (navigateurs du Web Share, toutes les piles TLS). La validité du certificat n'a pas d'importance : on épingle la *clé*. |
| `ID-02` | L'**empreinte** de l'appareil DOIT être `SHA-256` du `SubjectPublicKeyInfo` DER du certificat. | Indépendante du renouvellement du certificat. |
| `ID-03` | L'empreinte DOIT pouvoir s'afficher sous forme courte lisible (base32 groupé) pour comparaison manuelle. | Vérification visuelle en cas de doute. |
| `ID-04` | La liste des appareils de confiance (`trusted.json` : empreinte, alias, date, option « accepter automatiquement ») DOIT être écrite de façon atomique : fichier temporaire, `fsync`, renommage. | Une coupure ne doit pas la corrompre. |
| `ID-05` | Si un appareil connu se présente avec une autre empreinte **sans preuve de l'autorisation de l'utilisateur (le secret du QR code, un PIN vérifié par la PAKE)**, la connexion DOIT être refusée et l'utilisateur alerté. Aucune acceptation silencieuse (pas de « TOFU » silencieux). Une adresse n'est pas une identité : l'appareil qui apporte cette preuve reprend l'adresse, et le journal le dit (décidé le 30 octobre 2026, #217). | C'est exactement la signature d'une attaque MITM. Le secret et le PIN ne paraissent que sur l'écran de l'utilisateur : un attaquant qui a pris l'adresse ne les a pas. Sur le réseau qu'un PC ou un téléphone crée, l'appareil suivant reçoit souvent l'adresse du précédent, et était refusé comme une attaque. |
| `ID-06` | L'utilisateur DOIT pouvoir « oublier » un appareil. Une réinstallation crée une nouvelle identité et impose un nouvel appairage. | Simplicité et révocation. |

---

## 5. Couche B — Découverte

**Rappel de principe :** la découverte n'est ni fiable ni authentifiée. Elle ne fait que proposer « un appareil medXfer semble être à cette adresse ».

### 5.1 Pourquoi mDNS plutôt que du multicast brut (réponse à la question sur iOS)
- **Multicast brut** = votre application envoie elle-même des paquets UDP vers une adresse de groupe (c'est ce que fait le code actuel avec `239.255.255.250`).
- **mDNS / DNS-SD (Bonjour)** = un standard où c'est *le système d'exploitation* qui annonce et recherche les services.
- Sur **iOS**, une application qui envoie ou reçoit du multicast/broadcast doit obtenir de Apple l'entitlement `com.apple.developer.networking.multicast`, accordé sur demande. Utiliser Bonjour ne l'exige pas, mais impose de déclarer `NSBonjourServices` et `NSLocalNetworkUsageDescription` dans l'Info.plist, et l'utilisateur voit la demande d'autorisation « réseau local ». (Sources vérifiées : Annexe B.)
- **Conséquence :** sur iOS, mDNS est le chemin obligatoire. Sur Android, le multicast brut marche mais exige un `MulticastLock`. Le code actuel ne fonctionnerait donc pas sur iOS sans autorisation Apple.

### 5.2 Exigences
| Id | Exigence | Statut |
|---|---|---|
| `DISC-01` | Service mDNS `_medxfer._tcp`, port TCP du listener. Le TXT contient uniquement `v=1` et le rôle `r=` (`sender` quand un envoi attend, `node` sinon), qui n'identifie personne mais dit au receveur quels appareils envoient (WP-6.5). Le nom d'instance est `medXfer-` + 4 caractères hexadécimaux aléatoires tirés à chaque lancement. Le multicast sort par l'interface qui porte l'adresse annoncée, désignée par son index : sur un téléphone, jamais par les données mobiles (veille WP-6.0, §3.2). Sous Windows, l'annonce exige une règle de pare-feu UDP 5353 (J8). | ✅ (WP-6.5) |
| `DISC-02` | iOS : Bonjour via l'API du système, avec `NSBonjourServices = ["_medxfer._tcp"]` et `NSLocalNetworkUsageDescription`. Aucun multicast brut sur iOS. | ✅ |
| `DISC-03` | Android : acquérir un `MulticastLock` du Wi-Fi pendant la découverte. | ✅ |
| `DISC-04` | Repli (bureau et Android seulement) : balise UDP en **diffusion sur chaque sous-réseau** (et diffusion limitée), port 19998, **sans groupe multicast**. Les adresses de `224.0.0.0/24` s'attribuent par l'IANA (revue d'expert, IESG) et ne se prennent pas d'office ; `239.255.255.250` est celle de SSDP/UPnP (veille WP-6.0, §4 ; D6 du plan J6a, validée le 15 octobre 2026). Charge utile : le TXT, plus le nom et le port. Au plus une balise toutes les 2 s. | ✅ (WP-6.5) |
| `DISC-05` | L'appareil DOIT annoncer sa présence **uniquement** quand l'utilisateur l'a demandé (mode « recevoir » ouvert, ou envoi en attente). Ni annonce ni réponse aux sondes de découverte le reste du temps. | ✅ (WP-6.5b) : `xfer send` et `xfer node` sont des demandes explicites ; le cœur en-processus démarre silencieux et l'interface l'active par `set_discoverable` ; le daemon de développement s'annonce tant qu'il sert |
| `DISC-06` | Ni nom réel, ni nom d'hôte, ni liste de fichiers, ni clé publique dans l'annonce. Le nom affiché (alias) n'est transmis qu'*après* authentification (§6, §7). | ✅ |
| `DISC-07` | Le sélecteur côté receveur reconnaît un appareil grâce au suffixe du nom générique (`…-4F2A`) affiché aussi sur l'écran de l'émetteur, à côté du PIN. Un appareil de confiance mutuelle y apparaît sous le nom enregistré chez soi, reconnu par son étiquette (`DISC-11`), jamais par son adresse : une adresse se prend. L'alias ne sort jamais. | ✅ (WP-6.5b, WP-N.3) : `xfer send` affiche « Appears as » ; `pair_show`, `pair_pin_shown` et `status` portent `instance` ; `peers_list` porte `trusted_alias`, reconnu par l'étiquette |
| `DISC-08` | Repli manuel : saisie `IP:port` ou QR (contient l'adresse). | ✅ |
| `DISC-09` | Balayage du sous-réseau (/24, 64 sockets concurrents, délai 350 ms) seulement sur action explicite « Appareil introuvable ? ». | ✅ (déjà dans le code) |
| `DISC-10` | Android : le cœur ne peut pas énumérer les interfaces réseau (`net.Interfaces` et `net.InterfaceAddrs` refusés : `netlinkrib: permission denied`, Android 16, mesuré par G2A-09). La plateforme lui fournit noms, index et adresses par l'action `set_network_info` ; le cœur s'en sert à la place de l'énumération (adresse annoncée, interface multicast, balise) et tolère des erreurs d'envoi UDP passagères. Un PC qui crée son propre réseau ne donne que son interface tant qu'il tourne, puis rend l'énumération au cœur (`{"enumerate": true}`, J6c). | ✅ (WP-6.5b) |
| `DISC-11` | **Étiquettes de reconnaissance.** Deux appareils de confiance mutuelle partagent une clé de 32 octets tirée au hasard à l'établissement de la confiance (`trust_commit`, §6.5). Chacun annonce, pour chacun de ses appareils de confiance (16 au plus), `HMAC-SHA-256(clé, "medXfer-tag-v1" ‖ empreinte de l'annonceur ‖ période)` tronqué à 8 octets, en base32 (champ `t` du TXT et de la balise). La période dure 10 minutes ; la précédente est acceptée. Un inconnu ne voit que des valeurs aléatoires qui changent : ni nom, ni clé, ni identifiant stable. Oublier un appareil arrête d'annoncer et de chercher son étiquette. | ✅ (WP-N.1, WP-N.3) : `xfer recv` et `scan` de `xfer node` affichent « Nom (trusted) », `peers_list` remplit `trusted_alias` par l'étiquette |
| `DISC-12` | **Point d'accès.** Le cœur ne crée ni n'arrête de point d'accès Wi-Fi, et n'expose aucune action pour le faire. Quand aucun réseau n'est disponible, la plateforme le crée avec l'API de son système, puis signale le réseau au cœur avec `set_network_info` (`DISC-10`) :
- Android : un groupe Wi-Fi Direct (`WifiP2pConfig`, nom et mot de passe tirés par l'application), à la bande demandée : `GROUP_OWNER_BAND_5GHZ` par défaut, `GROUP_OWNER_BAND_2GHZ` pour un appareil qui ne capte que le 2,4 GHz ; gardé à la bande obtenue. Sans Wi-Fi ni Wi-Fi Direct, `WifiManager.startLocalOnlyHotspot`, nom et mot de passe tirés par le système (décision C7 du plan J6c). Le point d'accès local lâchait un téléphone écran éteint chaque fois que le créateur cherchait des réseaux (4 fois sur 4) ; le groupe à 2,4 GHz, jamais (0 sur 5, #256, 8 novembre 2026). Le groupe exige le Wi-Fi allumé : Wi-Fi éteint, c'est le point d'accès local, à 2,4 GHz, et l'écran du réseau le dit, avec un bouton pour allumer le Wi-Fi ; allumé, le réseau est recréé à 5 GHz, avec un nouveau QR code, hors session (réserve 5 du rapport J6d). Le téléphone ne doit être connecté à aucun autre réseau : sa radio alterne alors entre les deux canaux, et le débit tombe de 54 à 19 MB/s (essai réel du 2 novembre 2026). L'application le dit sur l'écran du réseau, et pendant une session quand le téléphone rejoint un Wi-Fi ; elle ouvre les réglages pour le quitter, Android ne laissant plus une application se déconnecter. Le 5 GHz est le choix par défaut (décision du 2 novembre 2026) ; l'interrupteur « Haute vitesse (5 GHz) » recrée le réseau à 2,4 GHz pour un appareil qui ne capte pas le 5 GHz, et inversement, hors session. Seul le groupe de medXfer est retiré, jamais celui d'une autre application ;
- Windows : un groupe Wi-Fi Direct en réglages *legacy* (`WiFiDirectAdvertisementPublisher`, module natif de l'application), nom et mot de passe tirés par l'application. Windows y distribue les adresses par son partage de connexion (ICS, `192.168.137.1`), et l'application accepte chaque téléphone qui le rejoint (`WiFiDirectConnectionListener`) : sans cela, Windows le coupe au bout de 2 minutes. Quand le partage de connexion est pris par une autre carte, ou que celle-ci en garde l'adresse, Windows n'en distribue aucune : l'application le fait alors, par un petit serveur DHCP lié à la seule adresse du groupe, et dit que les téléphones seront coupés toutes les 2 minutes, avec la carte en cause (plan J6c, WP-C.5). Quand Windows refuse le groupe, il en donne la raison, que l'application dit : le Wi-Fi indisponible, le Wi-Fi déjà pris (par le point d'accès sans fil mobile de Windows, entre autres), ou aucune raison, la carte ou son pilote ne le permettant pas (un PC sous Windows 10 avec une carte Intel AC 3165, 9 novembre 2026). Après un refus sans raison, l'application annonce par BLE qu'elle ne crée aucun réseau, pour que l'autre appareil le crée, jusqu'à ce qu'elle en crée un (§6.6) ;
- Linux : NetworkManager (`nmcli`, arguments séparés, sans shell).

Le PC crée son réseau à 2,4 GHz : ses cartes n'émettent pas d'elles-mêmes à 5 GHz (sonde J6c). Rejoindre le réseau d'un autre : Android, et Windows par son API Wi-Fi (`wlanapi`, profil de l'utilisateur supprimé au départ, retour au réseau d'avant ; plan J6d, WP-D.3) ; sous Linux, l'utilisateur le rejoint dans les réglages. L'appareil Android qui rejoint par le QR donne au cœur le réseau rejoint, auquel ses sockets sont liées (`pkg/netbind`), et le rejoint seul s'il le perd. Une session perdue ne fait plus arrêter le réseau à celui qui l'a créé : il le garde 10 minutes (`rejoinFor`), le temps que l'autre revienne ; une déconnexion voulue l'arrête aussitôt (essai réel du 2 novembre 2026). Une plateforme = un adaptateur ; aucun script ni commande du système construit à partir de texte. | ✅ côté cœur (A1, #156) : `pkg/hotspot`, `xfer send --create-network` et les actions `hotspot_*` sont retirés, schéma v3. ✅ côté plateformes : Android (J6b), Windows et Linux (J6c) ; groupe à 5 GHz d'Android (J6d, WP-D.4) |
| `DISC-13` | **Plusieurs chemins, le câble.** Un appareil vu sur plusieurs réseaux de cet appareil (un câble et un Wi-Fi) garde un chemin par réseau, avec la nature de l'interface locale : `ethernet`, `wifi` ou `other`, d'après le type de la carte sous Windows, `/sys/class/net` sous Linux, sinon le nom (Android cache `/sys/class/net` à l'application : le partage USB du téléphone, `usb0`, `rndis0` ou `ncm0`, est un câble). L'ordre est stable, le câble d'abord ; `host_ip` est l'adresse du premier. `peers_list` porte `paths`, et l'interface laisse l'utilisateur choisir (décision E2 du plan du câble). Un appareil touché sans choisir de chemin, sans réponse sur le premier, est essayé sur les suivants, et l'échec n'est dit qu'après le dernier : un PC vu par le partage USB d'un téléphone et par le Wi-Fi, son adresse USB ne répondant plus (#263, essai réel du 7 novembre 2026 ; le partage tire de nouvelles adresses à chaque démarrage). Un chemin choisi par l'utilisateur est essayé seul. Le téléphone offre le même choix que le PC : toucher la ligne essaie les chemins dans l'ordre, ses boutons « Câble » et « Wi-Fi » n'en essaient qu'un (essai réel du 9 novembre 2026 : l'A57 joint le PC par son partage USB comme par le Wi-Fi, dans les deux sens). L'énumération écarte les interfaces sans lien et les ponts virtuels (`lxcbr0`, `br-…`, `docker0`…). L'écran du PIN montre une adresse par réseau (`pair_pin_shown` porte `addresses`), les adresses `169.254` comprises : sur un câble direct, ce sont les seules. | ✅ côté cœur (WP-E.1) : `pkg/discovery` (`Path`, `LocalPaths`, `usable`), `pair_pin_shown.addresses` ; l'écran en WP-E.3 |

---

## 6. Couche C — Appairage

**But :** passer de « deux inconnus » à « deux appareils qui se font confiance », sans exposer de secret faible.

Trois états entre deux appareils (lot « Modèle de confiance », [plan](medxfer-plan-confiance.md)) :

| État | Comment on y entre | Ce qu'il permet | Ce qui est écrit sur disque |
|---|---|---|---|
| **Inconnu** | — | Se voir dans la découverte. | Rien |
| **Session** | P1 (QR) ou P2 (PIN) réussi, sur le réseau ou par BLE (§6.6) | Envoyer et recevoir tant que la session dure ; chaque offre reste à accepter. | **Rien** (`PAIR-08`) |
| **Confiance mutuelle** | Demande de confiance acceptée des deux côtés (§6.5) | Se reconnecter par P3, sans PIN ni QR. | L'empreinte de l'autre, **de chaque côté** |

Un appareil inconnu n'est jamais admis autrement que par P1 ou P2 : aucune décision ne repose sur une comparaison d'empreintes à l'œil (`PAIR-12`). L'empreinte reste affichable pour qui veut vérifier (`ID-03`).

### 6.1 Chemin P1 — QR code (secret fort)
Le QR contient : empreinte de l'émetteur, adresse, port, secret aléatoire de 128 bits, expiration.
Format : `medxfer://v1/pair?fp=<empreinte base32>&a=<ip>&p=<port>&s=<secret base64url>&exp=<unix>`

Quand l'émetteur a créé son propre point d'accès (`DISC-12`), l'application ajoute des champs, que le cœur ignore : `ws` (nom du réseau), `wp` (mot de passe), `wt` (sécurité : 1 WPA2, 2 WPA3 en transition, 3 WPA3) et, quand elle est connue, `wb` (bande : 2, 5 ou 6 GHz). L'appareil qui scanne rejoint d'abord ce réseau, puis s'appaire. Si sa radio ne capte pas la bande de `wb`, il le dit aussitôt, au lieu de chercher un réseau qu'il ne peut pas voir : un téléphone à 2,4 GHz seulement face à un réseau à 5 GHz (TECNO BF6, essai réel du 2 novembre 2026). Le mot de passe est un secret du point d'accès : il change à chaque démarrage, s'affiche sur l'écran du QR pour un appareil sans caméra, et n'est jamais journalisé.

Déroulement :
1. Le receveur scanne le QR et se connecte en **épinglant `fp`** (le QR est un canal hors-bande : la caméra est le canal authentifié).
2. Dans le canal TLS déjà authentifié, il envoie `pair_secret` avec le secret.
3. L'émetteur le compare en temps constant. Le secret est à usage unique, durée de vie 120 s par défaut.
4. La session est ouverte. Rien n'est enregistré (`PAIR-08`) : la confiance durable passe par la demande du §6.5.

Pourquoi aucun PAKE ici : le secret a 128 bits d'entropie, et l'attaquant ne peut pas faire de MITM puisque l'empreinte vient du QR.

### 6.2 Chemin P2 — PIN à 6 chiffres (PAKE)
Un PIN court (~20 bits) ne protège que s'il est utilisé *dans un PAKE* : l'attaquant ne peut alors tenter qu'un essai en ligne par tentative de connexion, sans possibilité de force brute hors ligne.

Déroulement (le **répondant** affiche le PIN ; l'**initiateur** le saisit) :
1. L'initiateur ouvre une connexion TLS. L'empreinte du répondant est inconnue : la connexion est en état `UNAUTH` (§7), seules les trames `pair_*` sont permises.
2. Les deux calculent `ekm = ExportKeyingMaterial("EXPORTER-medxfer-v1-pair", nil, 32)`. Cette valeur dépend de la session TLS réelle.
3. Ils exécutent le PAKE, CPace `CPACE-RISTR255-SHA512` (gate G1), en mode initiateur-répondant :
   - le mot de passe est le PIN ;
   - le CI vaut `lv_cat("medXfer-pair-v1", fp_initiateur, fp_répondant)`, l'initiateur en premier, avec le nom du service (brouillon §10.1) ;
   - le sid vaut `ekm`.
4. **Confirmation de clé** (brouillon §10.4) :
   - `mac_key = SHA-512("CPaceMac" ‖ sid ‖ ISK)` ;
   - chacun envoie `HMAC-SHA-512(mac_key, lv_cat(Y, AD))` sur son propre message, et vérifie celui du pair.

   L'ISK contient le CI (les deux empreintes) et le sid (`ekm`) : la confirmation lie donc l'échange au canal.
5. Succès : la connexion passe en état `AUTH`, et seulement alors l'alias est échangé. La session est ouverte ; rien n'est enregistré (`PAIR-08`).

Messages (`pkg/pairing`) :
1. initiateur → répondant : `pair_pin {suite, share: Ya}` ;
2. répondant → initiateur : `pair_pin_reply {share: Yb, confirm: Tb}` ;
3. initiateur → répondant, seulement si `Tb` est bon : `pair_pin_confirm {confirm: Ta, device_name}` ;
4. répondant → initiateur : `pair_accept`, comme pour le QR ; sinon `pair_reject`, neutre.

Un essai compte dès l'envoi de `pair_pin_reply`. Sans essai disponible, le répondant répond sur un PIN factice : l'initiateur ne peut pas distinguer ce cas d'un mauvais PIN (`PAIR-07`).

**Pourquoi le MITM échoue :** un attaquant qui relaie ouvre deux sessions TLS distinctes. Les `ekm` et les empreintes diffèrent d'un côté à l'autre ; les confirmations ne correspondent donc pas. Sans le PIN il ne peut pas non plus dériver la clé PAKE.

**PIN à la demande** (`PAIR-11`) : quand un appareil en choisit un autre dans la liste de la découverte, c'est l'appareil choisi qui affiche un PIN, et celui qui l'a choisi le tape, comme l'association par saisie de code en Bluetooth.
1. initiateur → répondant : `pair_pin_wanted` ;
2. le répondant affiche un PIN, puis répond `pair_pin_ready` ; s'il ne peut en afficher aucun (`PAIR-04`, après les séries de PIN détruits), `pair_reject`, neutre ;
3. l'initiateur fait taper le PIN, puis le déroulement ci-dessus reprend sur la **même** connexion, à `pair_pin`. Le délai `UNAUTH` est prolongé le temps de la saisie, sans dépasser la vie d'un PIN (`PAIR-02`).

Tant que le PIN affiché est valable, chaque nouvelle demande reçoit **le même** PIN, avec ses essais et ses délais : redemander ne donne aucun essai de plus à qui devine (`PAIR-04`). Un envoi ponctuel affiche son PIN d'emblée (avec le QR) et répond aux demandes avec celui-là.

### 6.3 Chemin P3 — Appareil de confiance mutuelle
Aucun code, à une condition : la confiance est **mutuelle** (`PAIR-09`). Le canal (§7) vérifie que l'empreinte du pair est dans `trusted.json` ; en plus, chaque côté déclare dans son premier message de session (`trusts_you`) s'il a l'autre dans sa liste, et P3 ne s'applique que si les deux le déclarent. Sinon, c'est un premier contact : P1 ou P2. L'envoi exige quand même l'acceptation du receveur, sauf si l'utilisateur a activé « accepter automatiquement » pour cet appareil.

### 6.4 Exigences
| Id | Exigence | Statut |
|---|---|---|
| `PAIR-01` | Le PIN DOIT être tiré avec `crypto/rand`, uniformément dans [0, 10^6), sans biais modulo. | ✅ |
| `PAIR-02` | Un PIN est valable 120 s au plus et pour une seule session d'appairage. | ✅ |
| `PAIR-03` | L'identité et le contexte du PAKE DOIVENT inclure `fp_i`, `fp_r` et `ekm` (liaison au canal). | ✅ |
| `PAIR-04` | Après **3** échecs, le PIN est détruit, la session fermée, un nouveau PIN est généré. Après 3 PIN détruits d'affilée, délai exponentiel avant d'en afficher un nouveau. Après **2 séries** de 3 PIN détruits d'affilée, l'attente s'arrête : l'utilisateur doit relancer l'appairage (#105). | ✅ |
| `PAIR-05` | La confirmation de clé est obligatoire dans les deux sens. | ✅ |
| `PAIR-06` | On NE DOIT PAS écrire soi-même de primitive (groupe, corps, hachage vers le groupe, encodage). Le protocole PAKE PEUT être assemblé sur une bibliothèque de groupe reconnue, à condition de vérifier tous les vecteurs de test de sa spécification et d'obtenir une revue externe avant la mise en service. Une **pré-version (RC)** peut être publiée avant cette revue, avec l'avertissement de la gate G4 (§16) ; la version 1.0 finale ne le peut pas (décision du 14 octobre 2026). Un protocole à constantes publiques DOIT les fixer sans connaissance du logarithme discret (voir l'incident de `schollz/pake`, Annexe B). Reformulée à la gate G1 (13 octobre 2026). | ✅ (`pkg/pake` : vecteurs A et B.3 du brouillon 21 ; revue externe : gate G4) |
| `PAIR-07` | Un échec d'appairage NE DOIT révéler ni le nombre d'essais restants ni l'alias du répondant. | ✅ |
| `PAIR-08` | **Éphémère par défaut** : une session ouverte par P1 ou P2 n'écrit rien dans `trusted.json` et disparaît avec elle. Seule une demande de confiance acceptée des deux côtés écrit (`PAIR-10`). | ✅ (lot « Modèle de confiance », WP-C.1 à WP-C.4) |
| `PAIR-09` | **Confiance mutuelle** : les privilèges d'un appareil de confiance (P3) exigent que les deux listes contiennent l'autre, ce que chaque côté déclare à chaque session (`trusts_you`). Une entrée d'un seul côté ne donne rien ; oublier l'autre d'un seul côté retire les privilèges aux deux. | ✅ (lot « Modèle de confiance », WP-C.1 à WP-C.4) |
| `PAIR-10` | **Demande de confiance** en trois messages dans une session déjà authentifiée (§6.5) ; aucune écriture sans le oui des deux ; 60 s sans réponse valent non ; le transfert en cours n'en dépend pas. | ✅ (lot « Modèle de confiance », WP-C.1 à WP-C.4) |
| `PAIR-11` | **PIN à la demande** : l'appareil choisi dans une liste affiche un PIN, l'autre le tape (§6.2) ; une demande répétée reçoit le PIN encore valable. | ✅ (lot « Modèle de confiance », WP-C.1 à WP-C.4) |
| `PAIR-12` | **Premier contact par P1 ou P2 seulement.** Aucun appareil inconnu n'est admis sur une comparaison d'empreintes à l'œil. | ✅ (lot « Modèle de confiance », WP-C.1 à WP-C.4) : la « question de confiance » n'existe plus |

**Risque résiduel accepté pour P2 :** un attaquant actif a 3 chances sur 10^6 par PIN affiché. La voie P1 (QR) est la voie « forte ». PIN configurable à 8 chiffres si besoin.

### 6.5 Confiance mutuelle
Une entrée de `trusted.json` ne prouve que la décision d'un côté : un appareil peut toujours garder la clé publique de l'autre sans lui demander. Ce qui protège A, c'est la liste de A ; la règle de `PAIR-09` rend en plus la confiance **bilatérale dans la durée**.

**Demande de confiance** (`PAIR-10`), dans une session ouverte par P1 ou P2, ou entre appareils de confiance mutuelle :

```
A ──trust_offer──►  B     B demande à son utilisateur : « 'A' propose une confiance mutuelle. Accepter ? » (60 s, sans réponse : non)
A ◄─trust_accept──  B     (ou trust_decline : rien n'est écrit, la session continue)
A ──trust_commit──► B     A écrit l'empreinte de B en envoyant le commit ; B écrit celle de A en le recevant
```

- Un `trust_accept` sans offre en cours, ou un `trust_commit` sans oui de ce côté, est ignoré : l'autre ne peut pas forcer une écriture.
- Si les deux proposent en même temps, chaque offre vaut le oui de l'autre.
- Si le `trust_commit` se perd, l'entrée n'existe que chez A : `PAIR-09` la rend inerte, et elle est signalée à la session suivante.
- Ces messages circulent dans un canal déjà authentifié par P1, P2 ou P3 ; ils ne changent rien à la cryptographie.
- `trust_commit` porte la **clé de reconnaissance** de la paire (`DISC-11`), tirée par le côté qui l'envoie ; chacun la range avec l'entrée de l'autre. Si les deux proposent en même temps, chacun envoie la sienne, et les deux gardent celle de l'appareil à la plus petite empreinte.

### 6.6 Chemin par BLE : se retrouver hors réseau (jalon J6d)
Quand deux appareils ne sont pas sur le même réseau, Bluetooth Low Energy leur sert à en partager un. L'un le crée (`DISC-12`) ; l'autre reçoit par BLE son nom et son mot de passe, chiffrés, et le rejoint seul.

BLE ne porte ni fichier ni alias. La session s'ouvre ensuite sur le Wi-Fi, en TLS (§7), avec les règles habituelles : P3 entre appareils de confiance mutuelle, le PIN pour un inconnu (`PAIR-12`).

Aucune cryptographie nouvelle : CPace (§6.2), déjà dans `pkg/pake`, plus HMAC-SHA-256, HKDF-SHA-256 (RFC 5869) et AES-256-GCM de la bibliothèque standard de Go. Le cœur produit et lit tous les messages ; la plateforme ne fait que les porter (annonce, balayage, GATT), avec l'API de son système (plan J6d, D5). Un appareil sans BLE garde le QR et le PIN (D4).

**Rôles.** Chaque appareil annonce et balaye à la fois.
- L'**initiateur** `I` est l'appareil sur lequel l'utilisateur touche un autre appareil de la liste ; il est client GATT.
- Le **répondant** `R` est l'appareil touché ; il est serveur GATT.
- Pour la reconnaissance, celui qui balaye joue `I`.

**Ce qui passe en clair :**
- l'UUID du service ;
- des valeurs aléatoires fraîches ;
- des étiquettes qui changent à chaque défi ;
- le suffixe du nom générique (`DISC-07`) ;
- les parts CPace.

**Jamais en clair :** une empreinte, un alias, le nom de l'appareil, le mot de passe d'un réseau. Une empreinte ne change jamais : en clair, elle permettrait à n'importe qui à portée radio de suivre un appareil d'un lieu à l'autre.

#### Clés
Notations :
- `K_AB` : la clé de reconnaissance de la paire (`DISC-11`), tirée dans `trust_commit` ;
- `fp_X` : l'empreinte de `X` (`ID-02`) ;
- `c_I`, `c_R`, `n_I`, `n_R` : 16 octets tirés par `crypto/rand`, neufs à chaque message qui les porte.

| Nom | Définition | Sert à |
|---|---|---|
| `K_tag` | `HKDF-SHA-256(K_AB, sel vide, "medXfer-ble-v1 tag", 32)` | les étiquettes |
| `tag(fp, c)` | `HMAC-SHA-256(K_tag, fp ‖ c)`, tronqué à 8 octets | désigner un appareil de confiance pour ce défi |
| `K_i2r ‖ K_r2i`, confiance | `HKDF-SHA-256(K_AB, sel = c_I ‖ c_R, "medXfer-ble-v1 trusted" ‖ fp_I ‖ fp_R, 64)` | chiffrer, chemin de confiance |
| `K_i2r ‖ K_r2i`, PIN | `HKDF-SHA-256(ISK, sel vide, "medXfer-ble-v1 pin", 64)` | chiffrer, chemin du PIN |
| `seal(K, n, …)` | AES-256-GCM sous `K` : nonce = `n` sur 12 octets, gros-boutiste ; AD = `version ‖ type` ; texte = `lv_cat` des champs | les champs chiffrés |

`n` compte les messages chiffrés par `K`, à partir de 0, et ne circule pas. Chaque clé est neuve à chaque échange : un message rejoué, perdu ou déplacé ne se déchiffre pas. Une étiquette ne fait que désigner la clé à essayer : c'est le déchiffrement qui authentifie.

#### Messages
Un message : `version` (1 octet, `1`) ‖ `type` (1 octet) ‖ `lv_cat(champs)`, avec le `lv_cat` du brouillon CPace (A.1.3, déjà dans `pkg/pake`).
- **Taille :** 512 octets au plus, la taille maximale d'un attribut GATT. Des écritures et des lectures longues les portent sans négociation de MTU.
- **Refus :** une version inconnue, un champ de taille fixe d'une autre taille, un champ de trop ou de moins reçoivent `refuse`. La version fixe aussi HKDF, HMAC et AES-GCM : en changer, c'est une version nouvelle.
- **Échange :** chaque message de `I` appelle exactement une réponse de `R`, par la caractéristique « message » du service.

| Type | Message | Sens | En clair | Chiffré |
|---|---|---|---|---|
| 1 | `hello` | I → R | `c_I` | — |
| 2 | `hello_reply` | R → I | 16 étiquettes (128 octets), suffixe (2 octets), `c_R` | — |
| 3 | `trusted` | I → R | `tag(fp_I, c_R)` | `seal(K_i2r, 0)` : demande |
| 4 | `pin_wanted` | I → R | `n_I`, suffixe de I | — |
| 5 | `pin_ready` | R → I | `n_R` | — |
| 6 | `pin` | I → R | suite (`CPACE-RISTR255-SHA512`), `Ya` | — |
| 7 | `pin_reply` | R → I | `Yb`, `Tb` | — |
| 8 | `pin_confirm` | I → R | `Ta` | `seal(K_i2r, 0)` : `fp_I`, demande |
| 9 | `reply` | R → I | — | `seal(K_r2i, 0)` : `fp_R` (chemin du PIN seulement), issue, réseau ou bande |
| 10 | `network` | I → R | — | `seal(K_i2r, 1)` : le réseau créé par I |
| 11 | `ack` | R → I | — | `seal(K_r2i, 1)` : vide |
| 15 | `refuse` | R → I | — | — |

Le contenu des champs :
- **Étiquettes de `hello_reply`** : une étiquette `tag(fp_R, c_I)` par appareil de confiance de R, chacune sous la `K_tag` de sa paire. Il y en a toujours 16, dans un ordre aléatoire : les places libres reçoivent des octets aléatoires. Leur nombre ne dit donc rien.
- **Demande** : `create` et `join`, 1 octet chacun (bit 0 : 2,4 GHz, bit 1 : 5 GHz), ce que I peut créer et rejoindre.
- **Issue** : 1 octet :
  - `1` : R a créé le réseau, qui suit ;
  - `2` : I doit le créer, sur la bande de l'octet qui suit (mêmes bits que la demande) : R connaît les bandes des deux côtés, I ne connaît que les siennes ;
  - `3` : aucun des deux ne le peut.
- **Réseau** :
  - `ssid` (1 à 32 octets) et `pass` (8 à 63) ;
  - `security` : 1 WPA2, 2 WPA3 en transition, 3 WPA3, comme `wt` du QR ;
  - `band` (les bits de la demande) et `port` (2 octets) ;
  - 1 à 4 adresses du créateur sur ce réseau (4 ou 16 octets).

#### Déroulement
```
Reconnaissance, dès qu'un appareil medXfer est vu (par l'UUID) :
I ──hello {c_I}──────────────────────────────►  R
I ◄─hello_reply {16 étiquettes, suffixe, c_R}──  R    I cherche tag(fp_Y, c_I) pour chacun de ses appareils de confiance Y

Appareil de confiance mutuelle (P3 par BLE), quand l'utilisateur de I touche R
(après un hello sur la même connexion, de moins de 60 s ; sinon I en envoie un d'abord) :
I ──trusted {tag(fp_I, c_R), seal(demande)}───►  R    R trouve I par l'étiquette, puis déchiffre
I ◄─reply {seal(issue, réseau)}────────────────  R

Appareil inconnu (P2 par BLE) :
I ──pin_wanted {n_I, suffixe}─────────────────►  R    R montre la demande, le PIN et « Refuser »
I ◄─pin_ready {n_R}────────────────────────────  R
                                                      l'utilisateur de I tape le PIN
I ──pin {suite, Ya}───────────────────────────►  R    un essai est réservé (PAIR-04)
I ◄─pin_reply {Yb, Tb}─────────────────────────  R
I ──pin_confirm {Ta, seal(fp_I, demande)}─────►  R    envoyé seulement si Tb est bon
I ◄─reply {seal(fp_R, issue, réseau)}──────────  R

Si I doit créer le réseau (issue 2), les deux chemins finissent par :
I ──network {seal(réseau)}────────────────────►  R
I ◄─ack {seal()}───────────────────────────────  R
```

**Le chemin du PIN** est CPace, comme au §6.2, avec :
- `PRS` : le PIN ;
- `CI` : `lv_cat("medXfer-ble-pair-v1")` ;
- `sid` : `n_I ‖ n_R`, des valeurs aléatoires apportées par les deux côtés, ce que le brouillon cite comme une option adaptée (§10.9) ;
- `ADa` et `ADb` vides ;
- même confirmation, et R confirme le premier.

Les empreintes ne sont pas dans le CI, contrairement au PIN sur TLS : il faudrait les envoyer en clair avant l'échange. Elles passent chiffrées juste après, sous une clé que seul un côté qui avait le PIN peut tirer de l'ISK. L'ISK passe par une KDF avant tout usage, comme le recommande le brouillon (§4, §10.3).

**Sur le Wi-Fi :**
1. L'appareil qui rejoint le réseau se connecte à l'adresse du créateur et épingle son empreinte : celle de l'appareil de confiance, ou celle reçue chiffrée sur le chemin du PIN.
2. Entre appareils de confiance mutuelle, c'est P3, inchangé (`pair_hello`, `trusts_you`).
3. Sur le chemin du PIN, il envoie `pair_ble {device_name}`. Le créateur vérifie que l'empreinte de la session TLS est celle que BLE vient d'authentifier, une seule fois et dans les 2 minutes, puis répond `pair_accept`, comme pour le QR (§6.1).
4. Une session s'ouvre ; rien n'est écrit (`PAIR-08`).

**Qui crée le réseau.** R décide, d'après sa demande et celle de I :
1. celui qui peut le créer à 5 GHz, si l'autre peut le rejoindre à 5 GHz ;
2. sinon, celui qui peut le créer à 2,4 GHz ;
3. à égalité, R ;
4. si aucun ne le peut : issue 3.

Seul circule un réseau créé par medXfer pour l'occasion, au mot de passe tiré à chaque démarrage (`DISC-12`), jamais un réseau de l'utilisateur (sa box, le Wi-Fi du bureau). Le code de pays ne circule pas : le système et le pilote du créateur appliquent la réglementation. Un réseau que personne n'a rejoint dans les 2 minutes est arrêté.

**Pourquoi cela tient** (adversaire A7, §2.1) :
- **Rejeu.** Chaque clé dépend d'une valeur fraîche de chaque côté : `c_I` et `c_R`, ou `n_I` et `n_R` par le `sid`. Un message enregistré ne se déchiffre dans aucun autre échange.
- **Relais.** Relayer `hello`, ou demander à un appareil ses étiquettes pour le défi d'un autre, fait croire qu'un appareil de confiance est proche, sans rien obtenir de plus. La suite exige `K_AB` ou le PIN, puis l'empreinte sur le Wi-Fi.
- **Homme du milieu sur le chemin du PIN.** Sans le PIN, il n'obtient qu'un essai par échange, et cet essai compte (§6.2). Il ne peut ni lire ni remplacer les empreintes, chiffrées sous une clé tirée de l'ISK.
- **Suivi.** Un inconnu ne voit que des valeurs neuves à chaque défi : toujours 16 étiquettes, quel que soit le nombre d'appareils de confiance, et le suffixe, tiré à chaque lancement.
- **Devinette du PIN.** Un seul PIN et un seul compteur pour BLE et pour TLS : la borne de 18 essais par attente ne change pas (`PAIR-04`).

**Risques résiduels acceptés :**
- Tant que l'application cherche ou attend un appareil, un voisin sait qu'un appareil medXfer est là, par l'UUID. Pendant un lancement, il peut relier ses annonces par le suffixe, comme le permet mDNS sur le réseau local (`DISC-01`).
- Le chemin de confiance n'a pas de confidentialité persistante. Qui volerait plus tard `K_AB` lirait le mot de passe d'un réseau enregistré, mais ce réseau est arrêté et son mot de passe ne resservira pas.
- N'importe qui à portée peut faire afficher une demande de PIN, comme sur le réseau (`PAIR-11`) ; l'utilisateur la refuse.

| Id | Exigence | Statut |
|---|---|---|
| `BLE-01` | **Rôle.** BLE ne sert qu'à mettre deux appareils sur un même réseau. Il ne porte ni fichier, ni alias, ni nom (`DISC-06`) ; la session s'ouvre sur le Wi-Fi, en TLS (§7). Le cœur produit et lit les messages ; la plateforme les porte. Sans BLE : le QR et le PIN. | ✅ cœur et API (WP-D.2 : `pkg/pairing/ble.go`, actions `ble_server`, `ble_client`, `pair_ble`) ; plateformes : Android et Windows (WP-D.3) |
| `BLE-02` | **Annonce.** L'annonce ne porte que l'UUID de service `c46b12c3-1dee-482d-a490-b185212312fb` : ni nom (Windows : `IsDiscoverable` faux), ni donnée de service propre à l'appareil, ni identifiant stable (Windows ajoute un octet de donnée de service, la version des messages, le même partout : sans lui, la recherche filtrée du Galaxy A57 ne voit pas le PC). L'adresse est l'adresse aléatoire du système. La caractéristique « message » est `c46b12c4-1dee-482d-a490-b185212312fb`. Annonce et balayage, filtré sur l'UUID, suivent `DISC-05` : seulement quand l'utilisateur cherche ou attend un appareil, l'application ouverte (plan J6d, D3). | ✅ Android (WP-D.3, `Ble.kt`) : UUID seul, recherche filtrée sur le service, en pause pendant une session ; Windows (WP-D.3, `ble.cpp`) : `GattServiceProvider` caché, recherche filtrée  |
| `BLE-03` | **Messages.** Format, tailles et réponses du §6.6 ; 512 octets au plus. Une version inconnue, une taille fausse, un champ de trop ou de moins : `refuse`, connexion fermée. Aucune taille de MTU n'est exigée. R répond dans la version de I s'il la connaît. | ✅ cœur (WP-D.2) : `pkg/pairing/ble.go`, `FuzzBLEReceive` |
| `BLE-04` | **Reconnaissance.** `hello` porte un défi frais. `hello_reply` porte toujours 16 étiquettes, dans un ordre aléatoire, le suffixe et `c_R`. Seuls les appareils de confiance mutuelle qui ont une clé `K_AB` ont une étiquette ; oublier un appareil retire la sienne. `c_R` vaut pour cette connexion, 60 s au plus. | ✅ cœur (WP-D.2) |
| `BLE-05` | **Appareil de confiance.** `trusted` porte `tag(fp_I, c_R)` et la demande chiffrée. Chaque côté ne calcule qu'avec la clé de sa propre liste : l'échange exige la confiance des deux côtés (`PAIR-09`). Aucune question à l'utilisateur de R, qui voit l'appareil se connecter. | ✅ cœur (WP-D.2) |
| `BLE-06` | **Appareil inconnu.** CPace du §6.2 avec `CI = lv_cat("medXfer-ble-pair-v1")` et `sid = n_I ‖ n_R`. Confirmation dans les deux sens, R le premier (`PAIR-05`). Les empreintes passent chiffrées après la confirmation, jamais en clair. | ✅ cœur (WP-D.2) ; revue G4 |
| `BLE-07` | **Un seul PIN, un seul compteur.** Le PIN de BLE est celui de `PAIR-11`, avec ses essais, ses délais et son arrêt (`PAIR-04`), partagés avec TLS. Un essai compte dès `pin_reply` ; sans essai disponible, R répond sur un PIN factice (`PAIR-07`) ; un succès consomme le PIN (`PAIR-02`). R montre la demande avec le suffixe de I, le PIN et « Refuser ». Une seule demande à la fois : les autres reçoivent `refuse`. | ✅ cœur (WP-D.2) : le même `PIN` que TLS (`TestBLEAndTLSShareAttempts`) ; écran : WP-D.5 |
| `BLE-08` | **Chiffrement.** AES-256-GCM sous les clés du §6.6, une par sens, nonce compté et non transmis. Tout échec (étiquette inconnue, déchiffrement, confirmation) : `refuse`, neutre, et connexion fermée. Le mot de passe du réseau ne circule jamais en clair et n'est jamais journalisé (§13). | ✅ cœur et API (WP-D.2) : mot de passe masqué dans le journal (`TestBLEByPINThenPairBLE`) ; revue G4 |
| `BLE-09` | **Le réseau.** R décide qui le crée, selon la règle du §6.6. Seul circule un réseau créé par medXfer pour l'occasion, au mot de passe tiré à chaque démarrage (`DISC-12`), jamais un réseau de l'utilisateur. Le code de pays ne circule pas. Un réseau que personne n'a rejoint en 2 minutes est arrêté. | ✅ règle et contrôle du réseau dans le cœur (WP-D.2, `Creator`) ; création à la bande convenue : Android (WP-D.3) ; arrêt d'un réseau créé pour BLE que personne n'a rejoint en 2 minutes (WP-D.5, `bleUnjoinedFor`) |
| `BLE-10` | **Sur le Wi-Fi.** Celui qui rejoint épingle l'empreinte du créateur. Sur le chemin du PIN, il envoie `pair_ble`, et le créateur n'accepte que l'empreinte authentifiée par BLE, une fois, dans les 2 minutes (`CHAN-04`). Rien n'est écrit (`PAIR-08`). Un seul PIN sert au réseau et à l'appairage. | ✅ (WP-D.2) : `BLEAuthenticated`, `pair_ble` (`TestBLEByPINThenPairBLE`, `TestPairBLEWithoutBLEIsRefused`) |
| `BLE-11` | **Limites** (des paramètres, règle R4). 4 clients GATT à la fois ; 4 Kio reçus au plus par connexion (comme `LIM-03`). R attend `pin` au plus la vie du PIN (2 min), tout autre message 10 s, et répond en 25 s au plus, création du réseau comprise : une transaction GATT expire à 30 s. L'état d'une connexion disparaît avec elle. Aucune limite par adresse : les adresses BLE changent. **Limite de portée** : une reconnaissance que la radio perd est reprise après 1 s, puis 2 s, puis toutes les 5 s, sans attendre le signalement suivant ; une reconnaissance restée sans réponse, toutes les 5 s. Un appareil que la radio ne signale plus sort de la liste après 6 s, après 15 s si son signal est faible (-80 dBm ou moins) ; un toucher dont la radio perd un échange reprend de zéro, seul, 3 fois en tout, à 600 ms d'intervalle (sur le chemin du PIN, jusqu'à la demande du PIN seulement) ; toujours perdu, l'écran dit « trop loin, rapprochez-vous » si le signal est faible. | Cœur et API (WP-D.2) : 4 Kio par connexion, défi de 60 s, saisie du PIN, 4 connexions ; Android et Windows (WP-D.3) : réponse attendue 25 s au plus ; 📏 WP-D.6 : reconnaissance en 0,6 à 2,7 s, abandonnée à 8 s ; du toucher à la session, 2,4 à 6,7 s ([j6d-report.md](j6d-report.md)) ; limite de portée (#268) : `BleLink.forgetWeakAfter`, `touchTries`, `retrySoonAfter` (#269), « signal faible » dans la ligne de l'appareil |
| `BLE-12` | **Permissions et radio.** Les permissions sont demandées quand BLE sert, avec leur raison. Android 12 et plus : `BLUETOOTH_SCAN` (sans localisation), `BLUETOOTH_ADVERTISE`, `BLUETOOTH_CONNECT`. Android 11 : `BLUETOOTH`, `BLUETOOTH_ADMIN` et la localisation précise, que le système exige pour balayer. Bluetooth éteint (3 appareils sur 4 au début de la sonde) : l'application le dit et propose de l'allumer. Un adaptateur qui ne sait pas s'annoncer (sans rôle périphérique, Windows : `no_advertiser`) balaie quand même : l'appareil voit les autres et les touche, eux ne le voient pas, et l'écran le dit (un PC sous Windows 10, 9 novembre 2026). | ✅ Android (WP-D.3) : permissions demandées à l'usage, demande du système pour allumer le Bluetooth ; Windows (WP-D.3) : aucune permission, la radio allumée par Windows si l'utilisateur le permet, sinon ses réglages ; écrans (WP-D.5) : la raison d'abord, la demande du système sur « Autoriser » |

---

## 7. Couche D — Canal sécurisé

| Id | Exigence | Raison / statut |
|---|---|---|
| `CHAN-01` | Chaque appareil expose **un seul** listener TCP (défaut 18887 ; si occupé, port éphémère annoncé par la découverte). Il parle TLS dès le premier octet. On NE DOIT PAS « deviner » le protocole en lisant le premier octet. | Supprime le chemin « texte clair accepté » de `UpgradeToTLSIfClientHello`. ✅ |
| `CHAN-02` | TLS 1.3 minimum ; ALPN `medxfer/1` obligatoire (refus sinon) ; tickets de session désactivés (`SessionTicketsDisabled: true`) donc ni reprise ni 0-RTT. | Pas de rejeu possible. ✅ |
| `CHAN-03` | Authentification mutuelle : `ClientAuth: tls.RequireAnyClientCert`. La confiance est décidée par `VerifyConnection` (code ci-dessous), appelée des deux côtés. | ✅ |
| `CHAN-04` | Deux états. `UNAUTH` : empreinte inconnue ; seules les trames `pair_*` sont acceptées, délai 30 s, 4 Kio max. Ce délai borne tout délai posé ensuite par l'appelant, qui ne peut ni le lever ni le repousser (#156) ; seule exception, l'attente d'une personne qui tape le PIN lu sur l'autre écran (`AwaitPerson`) : au plus la vie d'un PIN, 2 min, puis 30 s pour la fin de l'échange. `AUTH` : empreinte de confiance, appairage réussi, ou empreinte authentifiée par BLE présentée avec `pair_ble` (`BLE-10`). Toute autre trame en `UNAUTH` ferme la connexion. | ✅ |
| `CHAN-05` | Aucun repli. Handshake raté ou empreinte invalide = connexion fermée avec erreur. Le `return DialPeer(target)` de `DialTLSPeer` est supprimé. | ✅ |
| `CHAN-06` | Une connexion de **données** DOIT s'ouvrir par `DATA_JOIN{transfer_id}` et n'est acceptée que si l'empreinte du pair est celle de la session de contrôle qui a créé ce transfert. Sans `DATA_JOIN` valide dans les 5 s : fermeture. | Remplace les ports 18888+N sans authentification. ✅ |
| `CHAN-07` | Délais : handshake 5 s ; lectures avec échéance ; keep-alive TCP 15 s. | 📏 |
| `CHAN-08` | Les suites de chiffrement sont laissées au choix de la bibliothèque standard de Go (AES-GCM avec accélération matérielle, ChaCha20-Poly1305 sinon). | À valider sur un ancien téléphone (§14). 📏 |

### 7.1 Code de référence
```go
// Appelée côté client ET côté serveur après le handshake TLS 1.3.
// "InsecureSkipVerify" désactive uniquement la vérification par autorité de certification
// (il n'y en a aucune). Notre vérification est ici, et elle est OBLIGATOIRE.
func (c *chanCfg) verify(cs tls.ConnectionState) error {
    if len(cs.PeerCertificates) == 0 {
        return errors.New("certificat pair absent")
    }
    fp := Fingerprint(sha256.Sum256(cs.PeerCertificates[0].RawSubjectPublicKeyInfo))
    switch {
    case c.expected != nil: // QR ou reconnexion : empreinte attendue
        if subtle.ConstantTimeCompare(fp[:], c.expected[:]) != 1 {
            return ErrFingerprintMismatch
        }
    case c.trust.Contains(fp): // appareil déjà de confiance
    case c.allowUnknown: // uniquement pour le PIN : la connexion reste en état UNAUTH
    default:
        return ErrUnknownPeer
    }
    c.peerFP = fp
    return nil
}

cfg := &tls.Config{
    MinVersion:             tls.VersionTLS13,
    Certificates:           []tls.Certificate{self.TLSCert},
    ClientAuth:             tls.RequireAnyClientCert,
    InsecureSkipVerify:     true, // voir commentaire ci-dessus ; verify() fait le travail
    VerifyConnection:       c.verify,
    NextProtos:             []string{"medxfer/1"},
    SessionTicketsDisabled: true,
}
// Liaison au canal, utilisée par le PAKE (§6.2) :
// ekm, _ := cs.ExportKeyingMaterial("EXPORTER-medxfer-v1-pair", nil, 32)
```

---

## 8. Couche E — Protocole de transfert

### 8.1 Trames
Toutes les trames, contrôle comme données, ont le même en-tête de 8 octets (déjà dans le code) :

| Octets | Champ |
|---|---|
| 0–1 | Magic `0x4D58` (`MX`) |
| 2 | Version (`0x01`) |
| 3 | Type |
| 4–7 | Longueur de la charge utile, entier non signé 32 bits, big-endian |

Le champ CRC32 est supprimé : TLS authentifie chaque enregistrement. Le contrôle devient lui aussi une trame (type `0x10`, charge JSON) : la lecture est ainsi **bornée** par la longueur, ce que ne faisait pas `json.Decoder` sur le flux.

### 8.2 Types de trames
| Type | Nom | Charge utile |
|---|---|---|
| `0x10` | `CONTROL` | JSON ≤ 1 Mio (messages ci-dessous) |
| `0x20` | `DATA_JOIN` | `transfer_id` (16 octets) |
| `0x21` | `CHUNK_REQ` | `item` u32 ‖ `index` u32 |
| `0x22` | `CHUNK` | `item` u32 ‖ `index` u32 ‖ `length` u32 ‖ `sha256` (32 octets) ‖ données. Taille de l'en-tête de chunk : **44 octets**. |
| `0x23` | `CHUNK_ERR` | `item` u32 ‖ `index` u32 ‖ `code` u16 (`SOURCE_CHANGED`, `READ_ERROR`). En réponse à un `DIGEST_REQ`, `index` vaut `0xFFFFFFFF`. |
| `0x24` | `DIGEST_REQ` | `item` u32 |
| `0x25` | `DIGEST` | `item` u32 ‖ `digest` (32 octets) |
| `0x26` | `DONE` | `status` u8 (0 : tout reçu ; 1 : des éléments sautés ou en échec) |

Messages `CONTROL` (champ `type`) : `hello`, `pair_secret`, `pair_pake`, `pair_confirm`, `offer`, `offer_items`, `accept`, `reject`, `pause`, `resume`, `skip`, `cancel`, `progress`, `ping`, `error`. `ping` part de chaque côté toutes les 5 s : une session muette pendant 15 s est perdue, et le côté qui l'avait ouverte la rouvre seul s'il s'agit d'un appareil de confiance (WP-B.8). La keepalive de TCP mettait jusqu'à 2 min 30 s à voir un appareil disparu. `progress` porte, quelques fois par seconde, ce que le receveur a reçu et vérifié : l'émetteur l'affiche plutôt que ce qu'il a servi, pour que les deux écrans montrent le même chiffre. La demande et la réponse de digest passent par les connexions de données (`DIGEST_REQ`, `DIGEST`, plan J4a, D7), comme la fin du transfert (`DONE`, D8). Les champs JSON inconnus sont ignorés ; un type inconnu provoque `error{code: unsupported}`.

- `offer_items{transfer_id, items}` : page suivante d'une offre de plus de 1 Mio. L'`offer` porte alors l'en-tête, les premiers éléments et `count`, le nombre total d'éléments ; le receveur valide l'offre complète avant de l'afficher (plan J4a, D2).
- `pause{transfer_id, item?}` et `resume{transfer_id, item?}` : sans `item`, tout le transfert ; avec `item`, un seul élément. `skip{transfer_id, item}` abandonne un élément. Ces décisions s'appliquent chez le receveur, dont l'ordonnanceur cesse ou reprend ses `CHUNK_REQ` ; une demande de l'émetteur lui est transmise (plan J4a, D6).
- `reject{transfer_id, code?}` : `code: busy` quand un transfert est déjà actif avec cet appareil (`LIM-08`).

### 8.3 Déroulement d'un transfert
1. **Offre.** L'émetteur envoie `offer` : `transfer_id` (128 bits aléatoires), liste d'éléments `{item, path, size}`, `total_bytes`, `chunk_size` proposée. Un seul `offer` couvre un fichier, plusieurs fichiers ou un dossier.
2. **Décision.** Le receveur affiche nom, taille, nombre de fichiers et expéditeur, puis répond `accept{transfer_id, chunk_size}` (peut réduire la taille) ou `reject`.
3. **Flux de données.** Le receveur ouvre *N* connexions (défaut 4, de 1 à 8 📏), chacune commence par `DATA_JOIN`.
4. **Le receveur pilote (modèle « pull »).** Il envoie `CHUNK_REQ` pour les plus petits index manquants, avec au plus 2 requêtes en vol par connexion. Les écritures restent presque séquentielles (utile pour les disques durs) et la mémoire est bornée : `N × 2 × chunk_size`.
5. **Vérification à l'arrivée.** À réception d'un `CHUNK`, le receveur recalcule le SHA-256 et compare à celui reçu *avant* d'écrire. Rien n'est créé sur disque pour un élément (dossiers, fichier, état) avant son **premier chunk vérifié** ; le nom de destination est résolu une dernière fois à ce moment-là. Une connexion refusée, un émetteur muet, une annulation ou des hash toujours faux ne laissent donc rien, et un fichier existant que la politique « écraser » remplacerait reste intact tant qu'aucune donnée vérifiée n'est arrivée (#75). Un élément vide se demande comme le chunk 0, de longueur nulle : la réponse de l'émetteur le confirme avant qu'il soit créé.
6. **Fin.** Quand tous les chunks d'un élément sont écrits, le receveur envoie `DIGEST_REQ` sur une de ses connexions de données. L'émetteur répond `DIGEST` = `SHA-256("medxfer-v1-filedigest" ‖ n ‖ hash_0 ‖ … ‖ hash_{n-1})` (hash des hash ; `n`, le nombre de chunks, en u32 big-endian). Il garde le hash des chunks qu'il a servis. Ceux d'une session précédente, ceux avant le premier chunk demandé après une reprise, il les relit et les hache pendant que le reste passe : le `DIGEST` ne relit rien à la fin (WP-B.8). Le receveur compare avec le sien, calculé sur les hash vérifiés et, pour les chunks repris, sur une relecture du disque. En cas de différence, l'élément est repris à zéro une fois, puis échoue. Un élément d'un seul chunk reçu en entier dans la session n'a pas besoin de `DIGEST_REQ` : son digest découle du hash déjà vérifié. Voir §9 pour la finalisation.
7. **Clôture.** Quand tous les éléments sont terminés, le receveur envoie `DONE{status}` sur ses connexions de données, puis les ferme. L'émetteur s'arrête sur ce signal et non sur le compte des octets servis : après une reprise, un saut ou un fichier déjà présent chez le receveur, il sert moins que le total (#33). Une annulation n'envoie pas `DONE`.

**Pourquoi un hash par chunk alors que TLS authentifie déjà ?**
(a) Il permet de vérifier la reprise sur ce qui est *déjà sur disque*. (b) Il détecte un fichier source modifié pendant l'envoi ou une erreur de lecture locale, que TLS ne voit pas. (c) Il reste valable si le transport est remplacé un jour (règle R2). Coût : 32 octets par chunk (2 Mio par défaut).

**Fichier source modifié pendant l'envoi :** l'émetteur note taille et date de modification dans l'offre, les revérifie à l'ouverture du fichier, avant et après le calcul du `DIGEST` et après une erreur de lecture, et sinon répond `CHUNK_ERR SOURCE_CHANGED`. Le transfert de cet élément échoue proprement.

**Pause :** le receveur cesse d'émettre des `CHUNK_REQ` (les connexions restent ouvertes). **Annulation :** `cancel`, fermeture des connexions de données, nettoyage décrit en §9.

**Reprise :** le receveur reconnaît un transfert partiel par une *clé de reprise* `SHA-256(fp_émetteur ‖ chemin ‖ taille ‖ mtime)` stockée dans l'en-tête du journal. En cas de désaccord final (digest différent), l'élément est repris à zéro : cas rare, simple et sûr.

**Petits fichiers :** comme un seul `offer` couvre tout le lot et que les requêtes mélangent les éléments, l'attente par fichier disparaît.

**Décision de WP-4.11 (13 octobre 2026) : le flux TAR (`StreamTar`) est retiré pour de bon.**
- **Le protocole ne limite pas.** Dans un puits nul, il traite 5 400 à 10 800 petits fichiers par seconde, 40 fois ce que le disque suit.
- **Ce qui coûte, c'est de rendre chaque fichier durable.** Sur un téléphone, le `fsync` du fichier et celui de son dossier font plus de 80 % du temps d'un petit fichier.
- **Un flux tar ne réglerait rien :** il écrirait les mêmes fichiers.
- **Le remède retenu :** une offre de petits fichiers ouvre 8 connexions au lieu de 4, et leurs fichiers sont rendus durables par lots (§9.6). Les chiffres sont dans `bench-results/README.md`.

### 8.4 Exigences
| Id | Exigence | Statut |
|---|---|---|
| `XFER-01` | Toute lecture réseau est bornée par la longueur annoncée dans l'en-tête et par les limites du chapitre 12. | ✅ |
| `XFER-02` | La taille de chunk est fixée dans `accept` et ne change plus pendant le transfert. Défaut 2 Mio, mesuré par `bench matrix` entre le PC et le téléphone (D8 du plan J4b, `bench-results/`) ; bornes 256 Kio à 8 Mio. | 📏 |
| `XFER-03` | Le hash de chunk est SHA-256. | ✅ (gate G6 fermée) |
| `XFER-04` | Le receveur NE DOIT PAS écrire un chunk dont le hash ne correspond pas ; il le redemande (3 essais maximum, sur une autre connexion si possible). | ✅ |
| `XFER-05` | Une connexion de données coupée est rétablie avec un délai croissant (50 ms jusqu'à 1 s, 8 tentatives). Si l'émetteur a déjà répondu sur une connexion de données de ce transfert, le récepteur réessaie ensuite toutes les 5 s pendant au plus 2 min (paramètres, règle R4 ; sous le quota de `LIM-01` pour une adresse connue) : le réseau de l'émetteur peut revenir (WP-B.8). Un émetteur jamais joint est abandonné après les 8 tentatives. | ✅ (déjà dans le code) |
| `XFER-06` | Négociation de version : ALPN `medxfer/N`. Un changement incompatible utilise un nouvel ALPN, l'ancien restant servi en parallèle pendant la transition. | ✅ |
| `XFER-07` | L'émetteur DOIT détecter la modification de la source (taille/mtime). | ✅ |
| `XFER-08` | **Un appareil injoignable est nommé.** Une connexion qui ne s'ouvre jamais (pas de réponse, refus, pas de route) a son code : `unreachable` pour un appairage (`action_error`), `sender_unreachable` pour un transfert dont le récepteur n'a jamais atteint l'émetteur (`transfer_failed`, et le message `failed` de la session, champ `code`, pour l'émetteur). L'interface le dit dans ses mots, de chaque côté : le pare-feu de l'appareil qui n'accepte pas les connexions est la cause probable. Un émetteur qui a répondu puis refusé, ou qui s'est perdu en route, n'est pas injoignable. | ✅ (essai réel du 1er novembre : un PC n'acceptait medXfer que sur son câble) |

---

## 9. Couche F — Stockage : intégrité et durabilité

**But :** ne jamais livrer un fichier incorrect, ne jamais perdre plus qu'une courte fenêtre de travail après une coupure, ne jamais toucher à ce qui n'appartient pas au transfert.

### 9.1 Pourquoi cette conception
- **Intégrité** = on prouve que le fichier final est identique à la source (hash de bout en bout).
- **Durabilité** = on sait exactement quelles parties sont réellement sur le disque après une coupure.
- Un appel `fsync` coûte cher. Le faire à chaque chunk ruinerait la vitesse ; ne jamais le faire rend le bitmap de progression mensonger. La solution est le **commit groupé** avec un **invariant d'ordre**.
- Ce schéma suit le principe d'un journal append-only : même garantie qu'une base transactionnelle pour un seul écrivain, sans la dépendance. (Syncthing procède de façon comparable : hash par bloc, fichier temporaire, renommage.)

### 9.2 Disposition des fichiers (côté receveur)
```
<dossier>/<racine>/fichier.ext.part         données (octets écrits à leur offset)
<dossier>/<racine>/fichier.ext.part.state   journal de progression
```
Le fichier ne porte son nom final qu'une fois **complet et vérifié** (renommage).

**Dossier racine (`STO-10`, D11).** Un transfert de plusieurs fichiers est reçu dans sa propre racine. Celle-ci porte un marqueur `.medxfer-transfer`, qui contient la clé du transfert, tant que le transfert n'est pas complet : une reprise y retrouve son dossier (plan J4b). Ce dossier apparaît sous son nom avec son marqueur d'un seul coup :
- il est créé sous un nom temporaire `.medxfer-<clé>` ;
- le marqueur y est écrit ;
- le dossier est renommé.

Un arrêt brutal ne laisse donc jamais un dossier du transfert qui passerait pour celui de l'utilisateur. Un dossier temporaire laissé par un arrêt est supprimé par la reprise du même transfert.

**Journal.**
- *Dans le dossier racine*, le journal d'un fichier n'est créé qu'à son premier commit : un fichier reçu d'une traite n'en a jamais. Un `.part` sans journal lisible y est forcément celui du transfert, laissé par un arrêt avant ce commit : il est repris de zéro sous son nom (`Params.Owned`).
- *Pour un fichier seul*, l'en-tête du journal est écrit avec le fichier, sans `fsync`. Un arrêt brutal le laisse reconnaissable, et le fichier est repris sous son nom.

Ces deux points viennent des tests de chaos (WP-4.10) : sans eux, un arrêt brutal laissait des `.part` orphelins et faisait repartir la reprise sous « nom (1) ».

### 9.3 Format du journal
- **En-tête :** magic `MXST`, version (1 octet), clé de reprise (32 octets), taille du fichier (u64), taille de chunk (u32), nombre de chunks (u32), CRC32 de l'en-tête.
- **Enregistrements de 40 octets :** `index` u32 ‖ `sha256` (32 octets) ‖ `crc32` u32 calculé sur les 36 premiers octets.

### 9.4 Algorithme d'écriture
```go
// Invariant J : un chunk n'apparaît dans le journal QUE si ses octets sont durables.
func (r *Receiver) onChunk(idx uint32, hash [32]byte, data []byte) error {
    if _, err := r.part.WriteAt(data, int64(idx)*int64(r.chunkSize)); err != nil { return err }
    r.pending = append(r.pending, record{idx, hash})
    r.pendingBytes += len(data)
    if r.pendingBytes >= 64<<20 || time.Since(r.lastCommit) >= 2*time.Second {
        return r.commit()
    }
    return nil
}

func (r *Receiver) commit() error {
    if err := r.part.Sync(); err != nil { return err }                       // 1. données durables
    if _, err := r.journal.Write(encode(r.pending)); err != nil { return err } // 2. on le note
    if err := r.journal.Sync(); err != nil { return err }                    // 3. la note est durable
    r.pending, r.pendingBytes, r.lastCommit = r.pending[:0], 0, time.Now()
    return nil
}
```
**Fenêtre de perte maximale après une coupure :** au plus 64 Mio ou 2 s de données, à retélécharger. Les deux valeurs sont des paramètres (règle R4). Le commit est aussi déclenché à la pause, à l'annulation et à la fin.

### 9.5 Reprise après coupure
1. Lire l'en-tête. S'il est invalide, ou si taille, taille de chunk ou clé de reprise ne correspondent pas à l'offre : jeter l'état, recommencer.
2. Lire les enregistrements jusqu'à la fin du fichier ou jusqu'au premier CRC invalide (la queue « déchirée » est ignorée et tronquée).
3. **Vérifier les 8 derniers chunks déclarés complets** en recalculant leur SHA-256 depuis le `.part` ; un désaccord marque le chunk manquant. Mode `verify=full` pour tout revérifier (utile sur clé USB ou carte SD qui mentent sur le `fsync`).
4. Reprendre en ne demandant que les chunks absents.

### 9.6 Finalisation
Tous chunks présents → `digest` correspond → `part.Sync()` → fermeture → **renommage** `.part` vers le nom final (sur Windows, nouvelle tentative 3×100 ms en cas de verrou antivirus) → `fsync` du dossier parent (Unix) → suppression du journal → événement « terminé ».

**Petits fichiers, durabilité par lots (WP-4.11).**
- *Quels fichiers :* sous Linux et Android, ceux reçus d'une traite dans le dossier racine du transfert, donc sans journal, sont finalisés ensemble (`store.Batch`).
- *Déroulement :*
  1. chaque fichier est scellé sans `fsync` ;
  2. un seul `syncfs` rend tout le lot durable ;
  3. viennent ensuite les renommages ;
  4. puis un `fsync` par dossier.
- *Déclenchement :* le lot est vidé à 256 fichiers, à 64 Mio, 2 s après son premier fichier, dès qu'il ne reste rien d'autre à recevoir, et à la fin de la session, même annulée.
- *Garantie inchangée :* aucun nom final avant des données durables (`STO-01`). Un arrêt brutal perd au plus le lot, dont les `.part` sans journal sont repris de zéro (§9.2, journal).
- *Gain :* sur un téléphone, environ 5 fois moins de temps par petit fichier. Un `syncfs` écrit aussi les données en attente des autres programmes sur ce disque : le premier lot d'un transfert peut donc coûter plus cher que les suivants.
- *Ailleurs :* Windows et macOS n'ont pas de `syncfs`, et chaque fichier y est finalisé seul. Sous Windows, le coût d'un petit fichier est sa création (NTFS, antivirus), pas son `fsync`.

### 9.7 Espace disque
Avant l'`accept` : espace libre ≥ ce qui reste à recevoir + max(1 % de la taille totale, 64 Mio). Ce qu'une reprise trouve dans ses `.part`, et une racine que la politique « ignorer » refuse, ne sont pas comptés (`Receiver.CheckSpace`). Une offre qui ne tient pas est refusée avant le premier octet : la ligne de commande l'annonce, le daemon l'indique dans `incoming_offer` (`free_bytes`, `needed_bytes`, `enough_space`) et refuse `respond_offer` (`NO_SPACE`) ; l'offre reste en attente, pour libérer de la place ou choisir un autre dossier.

Si le disque se remplit (`ENOSPC`, `EDQUOT`, `ERROR_DISK_FULL`, `ERROR_HANDLE_DISK_FULL`) : transfert **en pause**, fichiers `.part` conservés, événement `DISK_FULL` (`OnDiskFull` ; `disk_full` dans le daemon). L'élément touché est mis de côté : son fichier est fermé avec son journal, puis rouvert à la reprise, si bien que seul ce qui est durable compte et le reste est redemandé. Un `.part` qui n'a même pas pu recevoir son journal est supprimé (il ne serait pas reconnu comme celui de ce transfert). La reprise est demandée par l'utilisateur (bouton Reprendre, ou Entrée dans le terminal).

Aucune sonde n'écrit dans le dossier de réception (D10 du plan J4b, #36) : les étapes `disk_write` et `fsync` disent déjà si le disque limite. Ne pas utiliser `Truncate` pour pré-allouer sur NTFS (le fichier grossit à l'écriture, comme dans le code actuel).

### 9.8 Chemins sûrs et jamais de suppression (réponse à la faille n°7)
**Le problème actuel** (`engine/disk.go`, `ensureDirectory`) : si l'émetteur envoie le chemin `a/b.txt` alors que `a` est un *fichier* existant chez le receveur, le code supprime ce fichier. Un pair malveillant peut effacer des fichiers du dossier de réception.

| Id | Exigence | Raison |
|---|---|---|
| `STO-09` | Le receveur NE DOIT JAMAIS supprimer un fichier qu'il n'a pas créé *dans ce transfert*. Seuls les `.part` et `.state` du transfert courant sont supprimables, après vérification de leur clé de reprise. | Supprime la classe entière de faille. |
| `STO-10` | Chaque transfert multi-éléments est reçu dans **un dossier racine neuf** (`<destination>/<racine>`), dont le nom est résolu *une fois* par la politique de collision (renommer, ignorer ou écraser). À l'intérieur, rien n'est fusionné avec l'existant. | Les conflits ne se produisent qu'au niveau racine. |
| `STO-11` | Si un composant de chemin existe déjà avec le mauvais type (fichier au lieu de dossier), l'élément échoue avec l'erreur `PATH_CONFLICT` ; rien n'est supprimé. | — |
| `STO-12` | Tout chemin reçu passe par `SafeRelPath` (ci-dessous) **avant** toute opération disque. | — |
| `STO-13` | Les opérations sur fichiers du receveur DOIVENT passer par `os.Root` (Go ≥ 1.24) ouvert sur le dossier de destination, qui refuse `..` et les liens symboliques sortants sans fenêtre TOCTOU. Go 1.25 offre `Root.MkdirAll` et `Root.Rename`. | `go.mod` est en Go 1.25 depuis J4b (plan J4b, D4). |
| `STO-14` | Après normalisation, deux chemins identiques à la casse près (`A.txt`, `a.txt`) ou en doublon DOIVENT faire rejeter l'offre : ils s'écraseraient sous Windows et macOS. | — |

```go
// Rejette par COMPOSANT, jamais par sous-chaîne. Les noms comme "a..b.txt" ou
// "mon fichier.pdf" sont valides (la v7 les rejetait à tort).
// Les règles universelles (chemin absolu, "..", composant vide, NUL, longueur) valent
// partout. Les règles propres à Windows (windowsRules) ne s'appliquent que si le
// RECEVEUR tourne sous Windows : un fichier "rapport: final.txt" reste valable entre
// deux appareils Linux ou Android. Décision D0-1 du plan de la phase 0.
func SafeRelPath(p string) (string, error) {
    windowsRules := runtime.GOOS == "windows"
    if p == "" || len(p) > 1024 || strings.ContainsRune(p, 0) {
        return "", ErrBadPath
    }
    p = strings.ReplaceAll(p, `\`, "/")
    if strings.HasPrefix(p, "/") {
        return "", ErrBadPath // chemin absolu
    }
    parts := strings.Split(p, "/")
    for _, c := range parts {
        switch {
        case c == "", c == ".", c == "..":
            return "", ErrBadPath
        case len(c) > 255:
            return "", ErrBadPath
        case hasControlChar(c): // octets < 0x20
            return "", ErrBadPath
        case windowsRules && strings.ContainsAny(c, `:*?"<>|`): // ':' bloque "C:" et les flux NTFS cachés
            return "", ErrBadPath
        case windowsRules && (strings.HasSuffix(c, ".") || strings.HasSuffix(c, " ")): // refusé par Windows
            return "", ErrBadPath
        case windowsRules && isWindowsReserved(c): // CON, PRN, AUX, NUL, COM1..9, LPT1..9 (avec ou sans extension)
            return "", ErrBadPath
        }
    }
    return strings.Join(parts, "/"), nil
}
```

### 9.9 Exigences d'intégrité et de durabilité
| Id | Exigence | Statut |
|---|---|---|
| `STO-01` | Les octets sont écrits dans `<nom>.part`, jamais sous le nom final. | ✅ |
| `STO-02` | Le renommage final n'a lieu qu'après vérification du `digest`. | ✅ |
| `STO-03` | Invariant J : `fsync` des données, puis écriture du journal, puis `fsync` du journal. | ✅ |
| `STO-04` | Commit groupé : 64 Mio ou 2 s (paramètres). Il tourne en arrière-plan : la réception continue pendant le `fsync`. Quand le suivant est dû et que le précédent n'est pas fini, l'écrivain attend : le disque donne le rythme, et une coupure de courant coûte au plus deux commits à renvoyer. L'erreur d'un commit est rendue à l'appel suivant. | 📏 ; arrière-plan en WP-E.5 du plan du câble : 40 → 57,7 MB/s vers un disque dur qui écrit 64 MB/s |
| `STO-05` | Enregistrements du journal protégés par CRC ; queue déchirée ignorée. | ✅ |
| `STO-06` | À la reprise, vérification des 8 derniers chunks ; mode complet disponible. | ✅ |
| `STO-07` | Disque plein = pause propre, pas d'échec ni de suppression. | ✅ |
| `STO-08` | Mémoire du récepteur bornée : `N × 2 × chunk_size` (supprime la table `pending` non bornée du code actuel). | ✅ |
| `STO-15` | Android : à partir de l'API 30, les fichiers reçus vont directement dans `Download/medXfer/` par chemin ; `.part`, `fsync` et renommage y fonctionnent sans permission (Android 16, G2A-10). Repli MediaStore pour Android 10 et avant, et pour un dossier choisi par l'utilisateur (SAF), avec copie après réception (1 Gio en 2,9 à 3,7 s sur le téléphone de test). | 📏 décision en J6b, Android 10 et SAF non testés |
| `STO-16` | Côté émetteur, les fichiers choisis sont lus **là où ils sont, jamais copiés** : l'interface passe au cœur leur chemin, ou sous Android un descripteur ouvert sur l'URI `content://` du sélecteur système. Un sélecteur qui copie dans un dossier temporaire ou dans le cache (comportement par défaut de certains plugins Flutter) est interdit : les copies doublent la place prise et restent (13 Gio trouvés sur le téléphone de test, #137). | ✅ tableau de bord (#137) ; ✅ cœur : `send` accepte des descripteurs (J6b, WP-B.0) ; 📏 application en J6b |

---

## 10. Couche G — Surface de contrôle (réponse à la faille n°5)

### 10.1 Le problème
Le daemon actuel écoute sur `ws://127.0.0.1:19999/ws` avec `CheckOrigin: true`. Les navigateurs **n'appliquent pas la politique de même origine aux WebSockets** : une page `https://site-quelconque.com` peut exécuter `new WebSocket("ws://127.0.0.1:19999/ws")` et envoyer les actions `share_web_files`, `set_config`, `send`. C'est le détournement de WebSocket inter-sites (CSWSH). Le contrôle « la requête vient de loopback » ne protège pas : la requête vient bien du navigateur de l'utilisateur, donc de loopback.

### 10.2 La solution structurelle : plus de socket de contrôle
| Id | Exigence | Statut |
|---|---|---|
| `API-01` | Le cœur est une **bibliothèque Go** sans socket d'écoute de contrôle. La frontière avec l'interface est de type *passage de messages*, réduite à 5 fonctions C : `medxfer_start(config)`, `medxfer_call(request)`, `medxfer_next_event(timeout_ms)`, `medxfer_stop()`, `medxfer_free(p)`. Les événements sont **lus** par l'interface (isolat Dart de fond, par lots), jamais poussés par un rappel : `SetEventHandler(callback)` est écarté, il fait planter l'application au redémarrage à chaud (G2a, §10.4). | ✅ (J6a) : `pkg/core` et `cmd/libmedxfer` ; le binaire publié n'a plus de socket de contrôle (WP-6.6) |
| `API-02` | Le vocabulaire est celui qui existe déjà (`RequestMessage{id, action, payload}` / `EventMessage{event, data}`), seul le transport change (appel de fonction au lieu de WebSocket). Aucun pointeur n'est partagé : les octets sont copiés, ce qui évite tout conflit entre ramasse-miettes de Dart et de Go. | ✅ (J6a) : même vocabulaire, transports `wsclient.Local` et `pkg/core` |
| `API-03` | Le CLI `xfer` (Termux, serveur sans écran) appelle le même cœur dans son propre processus. | ✅ : `xfer send`, `recv`, `pair`, `node` et `ctl` tournent en-processus, sans daemon |
| `API-04` | Si une interface loopback HTTP/WebSocket est conservée (mode sans écran, tableau de bord web, développement), elle est **désactivée par défaut** et DOIT appliquer les sept contrôles ci-dessous. Le tableau de bord web suit en plus les règles `DEV-19`. | ✅ (J6a) : la surface loopback n'existe qu'avec `devui` ; ses sept contrôles restent testés |
| `API-05` | Plateforme de la frontière : `c-shared` + `dart:ffi` sur Windows, Linux et Android (gate G2a fermée, `g2a-report.md`) ; macOS et iOS restent à trancher par G2b (`c-shared`, `c-archive` ou `gomobile`). | ✅ Windows, Linux, Android · ❓ G2b |
| `API-06` | Le schéma des messages est versionné et testé (fichiers « golden »). | ✅ (J6a) : schéma figé, `pkg/core/testdata/schema-v<version>.json` ; v2 depuis le lot « Modèle de confiance », v3 depuis l'audit (A1), v4 pour l'application (J6b, WP-B.0) |
| `API-07` | La frontière respecte les règles B1 à B11 du §10.4. | ✅ (J6a) : B1 à B7, B9 et B11 dans `cmd/libmedxfer` et `pkg/core`, vérifiés par `smoke.c` sur trois systèmes ; B8 et B10 côté Dart en J6b |
| `API-08` | Android : transferts et écoute tournent sous un service de premier plan. Sans lui, le réseau de l'application est coupé dès qu'elle passe en arrière-plan (port injoignable, transfert figé : G2A-05). | ✅ (G2a, prototype) ; à livrer dans l'application en J6b |

### 10.3 Les sept contrôles si un serveur loopback existe
1. **Listener dédié** lié à `127.0.0.1` uniquement, séparé de celui du Web Share (aujourd'hui `0.0.0.0` avec un test d'adresse).
2. **Port aléatoire** choisi à chaque lancement.
3. **Jeton de 256 bits** généré à chaque lancement, transmis au client légitime par l'amorçage (fichier `0600` ou variable d'environnement), exigé sur toute requête. Les navigateurs ne permettent pas d'en-tête personnalisé sur un WebSocket : utiliser le sous-protocole `Sec-WebSocket-Protocol` ou un premier message d'authentification, et fermer sans réponse sinon. Jamais dans l'URL.
4. **Contrôle de l'en-tête `Origin`** : toute `Origin` non vide qui n'est pas celle du tableau de bord est rejetée (les navigateurs l'envoient toujours ; les clients natifs n'en envoient pas).
5. **Contrôle de l'en-tête `Host`** (`127.0.0.1` ou `localhost`) contre la réassociation DNS (DNS rebinding).
6. **Aucune action à effet de bord en `GET`** (`/api/fs/mkdir` en est une aujourd'hui) et aucun en-tête CORS permissif.
7. **Le jeton n'est injecté dans la page du tableau de bord** que si les contrôles 4 et 5 passent.

```go
func guard(token, allowedOrigin string, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        host, _, _ := net.SplitHostPort(r.Host)
        if host != "127.0.0.1" && host != "localhost" { // anti DNS rebinding
            http.Error(w, "forbidden", http.StatusForbidden); return
        }
        if o := r.Header.Get("Origin"); o != "" && o != allowedOrigin { // anti CSWSH
            http.Error(w, "forbidden", http.StatusForbidden); return
        }
        got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
        if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
            http.Error(w, "unauthorized", http.StatusUnauthorized); return
        }
        next.ServeHTTP(w, r)
    })
}
```

### 10.4 Contrat de la frontière (issu du prototype G2a)
Vérifié sur Windows, Linux et Android (`g2a-report.md`, §6).

| Règle | Contenu |
|---|---|
| B1 | Chaque `char*` rendu par la bibliothèque est libéré par `medxfer_free`, jamais par l'allocateur de l'appelant. |
| B2 | Aucun pointeur Go ne traverse la frontière : les chaînes sont copiées. |
| B3 | Messages en JSON UTF-8. |
| B4 | `medxfer_start` est idempotente : un redémarrage à chaud de Flutter relance Dart, pas le processus. |
| B5 | Chaque goroutine du cœur récupère ses paniques en événement `core_error` ; une panique dans `medxfer_call` devient une erreur JSON. Aucune des cinq fonctions ne laisse passer une panique vers le C (#156). |
| B6 | Les chemins viennent de l'appelant (`data_dir`, `download_dir`). |
| B7 | Événements de progression limités à 10 par seconde et par transfert ; le dernier, à 100 %, passe toujours (#156). |
| B8 | Le cœur ne garde aucun pointeur vers du code Dart : les événements passent par sa file, lue par `medxfer_next_event`. |
| B9 | Le lecteur d'événements s'arrête de façon coordonnée : le cœur le réveille par une sentinelle. On ne tue jamais un isolat bloqué dans un appel natif (un événement perdu lors de la mise au point). `medxfer_next_event` avec un délai négatif attend sans limite. `medxfer_stop` rend la main une fois le cœur arrêté : un `medxfer_start` pendant l'arrêt attend, deux cœurs ne tournent jamais ensemble (#156). |
| B10 | Le lecteur transmet les événements par lots, jusqu'à 256 par message (lenteur de l'interface en rafale : 237 ms un par un, 3 à 5 ms par lots). |
| B11 | La file du cœur est bornée (65 536) ; un événement sans place est compté (`drops`), le moteur n'attend jamais l'interface. |

---

## 11. Module H — Web Share (invités sans installation)

**Rôle :** laisser un téléphone ou un PC sans medXfer envoyer ou recevoir des fichiers via un navigateur. C'est un module **optionnel et désactivé par défaut** (règle R3) ; l'hôte garde le contrôle de chaque transfert.

### 11.1 Problèmes du code actuel (faille n°6 et voisines)
- PIN dérivé de `time.Now().UnixNano()` : prévisible.
- PIN écrit en clair dans les journaux (`log.Printf(... PIN: %s)`) et dans l'événement envoyé à l'interface.
- PIN et jeton placés dans l'URL (historique du navigateur, journaux, en-tête Referer) et dans des cookies non `HttpOnly`.
- Comparaison non constante.
- Blocage par adresse IP seulement, inefficace derrière un NAT (la v7 le disait elle-même).
- Page servie en HTTP clair, sans en-têtes de sécurité.

### 11.2 Conception retenue
**Un jeton porteur, sans cookie.** Il n'y a donc aucune attaque CSRF possible : une page tierce ne peut pas lire le jeton pour l'ajouter à ses requêtes.

1. L'hôte crée une **session invité** : `session_id`, `token` (128 bits, `crypto/rand`), PIN de 6 chiffres, expiration (15 min par défaut, prolongée tant qu'un transfert tourne). Une session à la fois ; elle finit sur « Arrêter le partage », à son expiration ou à la fermeture de l'application, l'écran pouvant être quitté (`WEB-10`).
2. Le QR contient `https://<ip>:<port>/s#t=<token>`, le port étant 19999 par défaut, un autre s'il est pris (une seule règle de pare-feu sous Windows). Le **fragment** `#…` n'est jamais envoyé au serveur, ni dans les journaux ni dans l'en-tête Referer. Le JavaScript de la page le lit et l'envoie ensuite dans `Authorization: Bearer <token>` à chaque appel.
3. **Repli sans caméra :** l'invité saisit un PIN à 6 chiffres sur la page d'accueil ; `POST /api/unlock{pin}` renvoie le jeton. Le PIN est limité à **5 essais par session** (pas par IP) ; au-delà la session est invalidée et l'hôte est alerté. Comparaison en temps constant, délai de 500 ms par échec (déjà dans le code).
4. **HTTPS**, avec un certificat auto-signé *dédié à la session* (ECDSA P-256, `crypto/x509` et `crypto/tls` de Go, jamais la clé d'identité de l'appareil). L'invité voit un avertissement navigateur : c'est inévitable sans autorité de certification publique, et l'écran de l'hôte l'explique, avec l'empreinte du certificat qu'un invité prudent peut comparer. Un mode HTTP « réseau de confiance », avec avertissement visible, n'est pas écrit en J7 : il sera décidé après la gate G5, selon ce que les invités font de l'avertissement (décision D3 du plan J7).

```go
func randomPIN(digits int) string { // uniforme, sans biais, via crypto/rand
    max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
    n, _ := rand.Int(rand.Reader, max)
    return fmt.Sprintf("%0*d", digits, n)
}
```

### 11.3 Exigences
| Id | Exigence | Statut |
|---|---|---|
| `WEB-01` | Le listener Web Share est distinct du contrôle (§10), lié à l'interface réseau choisie, et inexistant tant que le module est désactivé. | ✅ cœur (WP-W.1, WP-W.4) : `pkg/webshare`, un listener par session invité, lié à une seule adresse ; le code de transition retiré, schéma v5. L'écran de l'application vient en WP-W.5. **Jamais les données mobiles** (essais réels du 10 novembre 2026 : sans Wi-Fi, l'A57 servait sur son adresse 4G, qu'un PC atteignait par la 4G d'un autre téléphone ; un PC sur le Wi-Fi d'un téléphone et sur le câble d'un autre servait sur le câble) : `web_share_networks` liste les réseaux possibles, Wi-Fi d'abord ; l'hôte choisit s'il y en a plusieurs ; une adresse hors de la liste est refusée (`address_not_local`), aucune liste donne `no_local_network` et l'écran propose de créer un réseau |
| `WEB-02` | Jeton de 128 bits via `crypto/rand`, transmis par fragment d'URL puis en-tête `Authorization`. Aucun cookie d'authentification. | ✅ cœur (WP-W.1, WP-W.2) : le jeton par le fragment, retiré de l'adresse, puis `Authorization` seulement ; aucun cookie (#157) |
| `WEB-03` | PIN de repli : `crypto/rand`, 5 essais par session, comparaison en temps constant. | ✅ cœur (WP-W.1) : PIN de 6 chiffres, 5 essais par session comptés avant la comparaison, la session finie et l'hôte prévenu après le dernier (#157) |
| `WEB-04` | Ni le PIN, ni le jeton, ni un PIN tenté n'apparaissent dans les journaux, les événements ou l'URL. Seule l'interface de l'hôte les reçoit, pour les montrer. | ✅ cœur (WP-W.1, WP-W.4) : jeton et PIN dans `diag.Secrets` dès leur tirage, seulement dans la réponse à `web_share_start` ; les liens de téléchargement ont leur propre clé (§11.4) |
| `WEB-05` | En-têtes sur toutes les réponses : `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'`, `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`, `Cache-Control: no-store` sur l'API. Cela impose de sortir le JavaScript et le CSS de la page en fichiers servis via `embed.FS`, au lieu du bloc inline actuel. | ✅ cœur (WP-W.1, WP-W.2) : la CSP et les en-têtes sur toutes les réponses ; la page en fichiers séparés, aucune donnée écrite en HTML (#157) |
| `WEB-06` | L'hôte approuve chaque dépôt d'invité, ou l'acceptation automatique qu'il a choisie ; quotas : taille maximale par session, nombre de fichiers, espace disque libre ; un seul transfert actif. | ✅ cœur (WP-W.3, WP-W.4) : l'accord de l'hôte (`web_share_answer`) ou l'acceptation automatique ; quotas, espace libre, un dépôt à la fois |
| `WEB-07` | Les téléchargements se désignent par **identifiant d'élément**, pas par nom de fichier fourni (le code actuel rapproche par `RelPath` ou `Base()`, ambigu). Les noms déposés passent par `SafeRelPath` puis ne gardent que le nom de base, et une collision prend un autre nom (« nom (2).ext »), jamais un fichier existant écrasé. | ✅ cœur (WP-W.1, WP-W.3) : téléchargement par identifiant ; `SafeRelPath` puis le nom de base ; une collision prend « nom (1).ext », jamais un fichier écrasé (#157) |
| `WEB-08` | `ReadHeaderTimeout`, `MaxHeaderBytes`, `http.MaxBytesReader` sur toutes les routes. | ✅ cœur (WP-W.1) : `ReadHeaderTimeout`, `MaxHeaderBytes` de 16 Kio, `MaxBytesReader` par route (#157) |
| `WEB-09` | Épinglage de l'IP de l'invité : optionnel, désactivé par défaut (une IP change et un NAT en partage une). | ✅ cœur (WP-W.1) : l'option `PinIP`, désactivée par défaut |
| `WEB-10` | **Session invité.** Une à la fois ; 15 minutes, prolongées tant qu'un transfert tourne ; finie sur « Arrêter le partage », à la fermeture de l'application, ou après 5 PIN faux, l'hôte alerté. L'hôte peut quitter son écran : la session continue, un bandeau y ramène, et ses choix sont gardés (essai réel du 10 novembre 2026 : quitter l'écran perdait la sélection et la session). Il ajoute ou retire des éléments pendant la session (`web_share_add`, `web_share_remove`) ; un identifiant retiré n'est jamais redonné. Le listener, le certificat, le jeton et le PIN disparaissent avec elle. | ✅ cœur (WP-W.1) : 15 minutes, repoussées pendant un transfert ou un dépôt en attente ; chaque fin dite à l'hôte (`web_share_closed`) |

### 11.4 L'API du portail

Le portail n'appelle que ces routes, toutes en HTTPS sur le listener de la session. Les fichiers de la page (`/s`, `/s/app.js`, `/s/app.css`) sont servis sans jeton ; tout le reste exige `Authorization: Bearer <jeton>`. Les réponses sont en JSON, sauf les fichiers.

| Route | Rôle | Réponses |
|---|---|---|
| `POST /api/unlock` `{pin}` | Le PIN de repli contre le jeton | `200 {token}` ; `401 {error: "invalid_pin", tries_left}` ; `410 {error: "session_closed"}` après le 5e échec |
| `GET /api/session` | Le nom générique de l'hôte (`DISC-07`), l'expiration, les quotas restants | `200` |
| `GET /api/items` | Ce que l'hôte propose | `200 [{id, name, size, folder}]` |
| `GET /api/items/{id}` | Un fichier, ou un dossier en zip à la volée (`WEB-07`) | le contenu, `Content-Disposition` ; `404` |
| `POST /api/items/{id}/link` | Un lien de téléchargement pour cet élément | `200 {url: "/dl/<clé>"}` ; `404` |
| `GET /dl/{clé}` | L'élément du lien, sans en-tête `Authorization` ; `Range` accepté | le contenu ; `404` pour une clé inconnue ou expirée |
| `POST /api/uploads` `{files: [{name, size}]}` | Une demande de dépôt : un ticket | `201 {ticket, state}` ; `413` au-delà des quotas ; `507` sans assez d'espace libre ; `409` si un transfert est déjà actif |
| `GET /api/uploads/{ticket}` | Son état : `pending`, `accepted`, `refused`, `done` | `200 {state, files: [{index, received}]}` |
| `PUT /api/uploads/{ticket}/{index}?offset=N` | Un morceau du fichier `index`, à sa position | `200 {received}` ; `409` si `offset` n'est pas la position attendue ; `413` au-delà de la taille annoncée |
| `DELETE /api/uploads/{ticket}` | L'invité abandonne : les fichiers incomplets sont supprimés | `204` |

Sans jeton valide : `401 {error: "unauthorized"}`. Session finie ou expirée : `410 {error: "session_closed"}`. Aucune réponse ne répète le jeton ou le PIN, sauf `unlock`.

**Pourquoi des liens de téléchargement.** Un navigateur enregistre un fichier en suivant une adresse, sans en-tête `Authorization`. Le lire en entier par le script de la page ne tient pas dans la mémoire d'un téléphone pour 1 Gio, et un *service worker* refuse de s'installer sur un certificat auto-signé. La page demande donc, avec le jeton, un lien vers un seul élément : une clé à elle, de 128 bits (`crypto/rand`), jamais le jeton de session (`WEB-04`), valable 10 minutes, réutilisable pour reprendre un téléchargement, et disparue avec la session. Elle n'ouvre que cet élément.

### 11.5 Pourquoi pas de chiffrement WebCrypto de bout en bout
`window.crypto.subtle` n'existe que dans un *contexte sécurisé* (HTTPS ou `localhost`) : il est absent sur `http://192.168.x.x`. Et en HTTP, un attaquant actif peut modifier le JavaScript qui implémente le chiffrement. Seul HTTPS résout les deux ; une fois HTTPS en place, WebCrypto n'ajoute rien face à A1 et A2.

---

## 12. Limites de ressources (anti-déni de service)

Valeurs par défaut ; toutes sont des paramètres (règle R4).

| Id | Limite | Défaut |
|---|---|---|
| `LIM-01` | Connexions non authentifiées simultanées | 16 ; au plus 5 nouvelles par minute et par IP, poignées de main en cours comprises (#156), 60 pour une IP qui s'est authentifiée (clé connue ou appairage) dans les 10 dernières minutes (#93) |
| `LIM-02` | Durée du handshake TLS | 5 s |
| `LIM-03` | Taille cumulée lue en état `UNAUTH` | 4 Kio |
| `LIM-04` | Charge `CONTROL` | 1 Mio |
| `LIM-05` | Éléments par offre / taille JSON du manifeste | 100 000 / 16 Mio |
| `LIM-06` | Longueur d'un chemin / d'un composant | 1024 / 255 octets |
| `LIM-07` | Flux de données par transfert | 1 à 8 |
| `LIM-08` | Transferts actifs simultanés par appareil de confiance | 1 (les autres attendent) |

---

## 13. Journalisation et confidentialité
- **Niveaux** : erreur, avertissement, information, débogage. Le débogage est désactivé en production.
- **Interdit dans tout journal et tout événement :** PIN, jeton, secret de QR, clé privée, clé PAKE, `ekm`, mot de passe d'un réseau créé, `K_AB` et les clés qui en sont tirées (`DISC-11`, §6.6), contenu de fichier. Un test d'intégration parcourt les journaux d'une session complète et échoue si l'un de ces secrets y apparaît.
- Aucune télémétrie envoyée à l'extérieur, aucun appel réseau hors LAN. Les diagnostics du chapitre 17 restent locaux ; leur partage est une action manuelle de l'utilisateur.
- Les noms et chemins de fichiers ne sont écrits dans les journaux qu'au niveau débogage.

---

## 14. Tests et critères d'acceptation

### 14.1 Tests de sécurité obligatoires
| Test | Vérifie |
|---|---|
| Sonde TLS 1.2, ou handshake sans ALPN | `CHAN-02` : refus |
| Serveur qui coupe le handshake TLS : le client ne retombe jamais en clair | `CHAN-05` |
| Proxy MITM avec son propre certificat : connexion refusée | `CHAN-03`, `ID-05` |
| Proxy MITM pendant un appairage PIN : les confirmations échouent | `PAIR-03`, `PAIR-05` |
| 4ᵉ tentative de PIN erronée sur un même PIN | `PAIR-04` |
| Connexion de données sans `DATA_JOIN`, ou avec l'empreinte d'un autre appareil | `CHAN-06` |
| Page tierce : `Origin: https://evil.example` vers le contrôle loopback | `API-04` contrôle 4 : 403 |
| Requête avec `Host: evil.example` | `API-04` contrôle 5 : 403 |
| Corpus de chemins piégés : `../x`, `..\x`, `/etc/x`, `C:\x`, `a/./b`, `CON`, `x.`, `a:b`, liens symboliques sortants | `STO-12`, `STO-13` |
| Offre contenant `a` (fichier existant) puis `a/b.txt` | `STO-09`, `STO-11` : rien n'est supprimé |
| Offre contenant `A.txt` et `a.txt` | `STO-14` |
| Aucun secret dans les journaux d'une session complète | chapitre 13 |
| `trusted` ou `pin_confirm` rejoué depuis un échange BLE enregistré | `BLE-05`, `BLE-08` : refus |
| Relais BLE pendant un PIN, avec ou sans échange des empreintes | `BLE-06` : les confirmations échouent, l'essai compte |
| 3 essais de PIN répartis entre BLE et TLS, puis un 4ᵉ | `BLE-07`, `PAIR-04` : refus |
| `hello_reply` d'appareils qui ont 0, 1 et 16 appareils de confiance | `BLE-04` : toujours 16 étiquettes, indiscernables |
| Message BLE tronqué, allongé, modifié ou d'une version inconnue | `BLE-03`, `BLE-08` : `refuse` |
| `pair_ble` avec une autre empreinte, une seconde fois, ou après 2 min | `BLE-10` : refus |
| Aucun mot de passe de réseau ni clé BLE dans les journaux d'un échange complet | `BLE-08`, chapitre 13 |
| 5 PIN Web Share erronés depuis 5 adresses différentes | `WEB-03` : la session est invalidée |

### 14.2 Tests de fiabilité
- **Chaos :** boucle de 1000 itérations avec arrêt brutal (`kill -9`) à des instants aléatoires, coupures réseau et disque plein ; critère : hash final identique à la source, aucun `.part` orphelin non nettoyable.
- **Journal déchiré :** tronquer le journal à chaque offset possible ; la reprise DOIT réussir.
- **Fichier source modifié** en cours d'envoi : échec propre `SOURCE_CHANGED`.
- **Fuzzing Go natif** de l'analyseur de trames, du manifeste JSON, de `SafeRelPath` et du lecteur de journal.

**Depuis J4b (WP-4.10) :**
- **Chaos** (`TestChaos`, tag `diag`) : chaque itération reçoit un petit dossier dans une destination qui contient déjà des fichiers de l'utilisateur. Elle y injecte une faute tirée de sa graine, puis reprend jusqu'au bout. Les cinq fautes possibles :
  - processus receveur arrêté net en pleine lecture ;
  - coupure réseau ;
  - disque plein ;
  - écriture déchirée ;
  - `fsync` menteur suivi d'une coupure de courant.
- **Vérifications du chaos** : chaque fichier final est identique à sa source, rien n'est hors de la destination, les fichiers de l'utilisateur sont intacts, et il ne reste à la fin ni `.part`, ni journal, ni marqueur. Le chaos fait 50 itérations par défaut (CI) ; `MEDXFER_CHAOS_ITER=1000` en local, et `MEDXFER_CHAOS_SEED` rejoue une graine en échec.
- **Journal déchiré** : `TestJournalCutAtEveryOffset` tronque le journal à chaque offset.
- **Fuzzing** : `FuzzJournal`, `FuzzSafeRelPath`, `FuzzOfferValidate`, en plus de `FuzzReadFrame`.
- **Limite connue** : un fichier déjà renommé dont le disque a menti sur son `fsync` peut être perdu par une coupure de courant, sans qu'aucun receveur puisse le voir.
- Détecteur de courses (`-race`) dans la CI.

### 14.3 Procédure de benchmark (vitesse, 📏)
1. Mesurer le lien : `iperf3 -P 4 -t 30` entre les deux appareils.
2. Générer un fichier de 10 Gio de données aléatoires incompressibles sur un stockage non limitant (ou noter que le stockage plafonne).
3. Transférer avec TLS actif et les valeurs par défaut (4 flux, chunk de 2 Mio).
4. Résultat = débit utile / débit iperf3. Cible ≥ 80 %. Consigner CPU et température.
5. Répéter sur la matrice : PC↔PC Ethernet, PC↔Android Wi-Fi 5 GHz, Android↔Android, et un téléphone ancien sans accélération AES (valide `CHAN-08`).
6. Mesurer aussi 100 000 petits fichiers (1 à 16 Kio) : c'est ce qui a tranché la décision sur `StreamTar` (§8.3).
7. Obtenir ces mesures marche par marche avec `xfer bench ladder`, et choisir flux et taille de chunk par défaut avec `xfer bench matrix` (`DEV-07`, chapitre 17).

---

## 15. Feuille de route par phases indépendantes

Chaque phase est livrable seule (règle R5). Le projet n'étant pas encore publié, la compatibilité avec les anciennes versions n'est pas requise.

> **Ordre d'exécution : voir `medxfer-feuille-de-route-execution.md`.** Le tableau ci-dessous décrit *ce que contient* chaque phase ; la feuille de route d'exécution fixe l'ordre en jalons J0 à J9. Trois écarts avec le tableau : (1) les phases 1 et 2a sont fusionnées en un seul jalon « Confiance » (J3) ; (2) le port unique et `DATA_JOIN` (`CHAN-01`, `CHAN-06`) sont livrés avec la phase 3 (J4a) ; en attendant, les connexions de données gardent un port par transfert mais passent en TLS mutuel à empreinte épinglée ; (3) l'appairage par PIN (2b) vient après la phase 3.

| Phase | Contenu | Corrige | Critère de sortie |
|---|---|---|---|
| **0 — Correctifs sans changement de protocole** | Jeton + Origin + Host sur le loopback (ou désactivation) ; PIN Web Share en `crypto/rand`, sans journal, comparaison constante ; `SafeRelPath` et fin des suppressions ; ordre `fsync` puis état ; table `pending` bornée. | Failles 5, 6, 7, 8 (partiel) | Tests §14.1 correspondants verts. |
| **0b — Outillage de développement** (en parallèle de la phase 0) | Chapitre 17 : extraction de `pkg/diag`, rapport JSON, `testkit.NewPair`, `xfer ctl`, `xfer bench disk` et `net`, `faultconn`, paquet de diagnostic, tableau de bord web derrière `devui`. | — | Les tests existants passent sans WebSocket ; `DEV-01`, `DEV-05`, `DEV-15`, `DEV-16`, `DEV-19` vérifiés. |
| **1 — Identité et canal** | Chapitres 4 et 7 : clé persistante, empreintes, mTLS, ALPN, port unique, `DATA_JOIN`, suppression du repli en clair. | Failles 1, 2, 3 | Les tests MITM, repli et connexion non autorisée passent. |
| **2a — Appairage par QR** | §6.1 (sans PAKE). | Faille 4 (voie forte) | Appairage réel entre 2 appareils. |
| **2b — Appairage par PIN** | §6.2 après la décision G1. | Faille 4 (voie courte) | Revue externe du PAKE planifiée. |
| **3 — Protocole et stockage v1** | Chapitres 8 et 9 : hash par chunk, `digest`, journal, fenêtre bornée, offre unique pour les lots. Mesures par étape (hash, `fsync`), `xfer bench ladder` complet et `matrix`, offre de type `bench`. | Faille 8 | Chaos 1000 itérations sans corruption ; `DEV-03`, `DEV-04`, `DEV-07`, `DEV-08` vérifiés. |
| **4 — Découverte et frontière cœur/UI** | Chapitre 5 (mDNS) ; chapitre 10 (FFI) après G2a (puis G2b) ; panneau développeur Flutter ; diagnostic à distance sur canal sécurisé ; test de la frontière et moteur factice Dart. | — | **4a** : découverte et pont sur Android et bureau (Windows, Linux) réels. **4b** : macOS et iOS dès qu'un environnement macOS est disponible (G2b, G8). Suppression du daemon ; critères de retrait du tableau de bord web évalués (`DEV-19`, gate G7). |
| **5 — Web Share v2** | Chapitre 11 en entier (jeton porteur, HTTPS, CSP, quotas). | Faille 6 (complète) | Tests §14.1 Web Share verts. |
| **6 — Mesure, optimisation, modules** | Benchmark §14.3 ; décision QUIC selon son critère ; thermique, historique si justifiés. | — | Objectif 80 % atteint ou plan écrit. |

---

## 16. Décisions ouvertes (« gates »)

| Gate | Question | Options | Critères de décision | Bloque |
|---|---|---|---|---|
| **G1** | Quelle bibliothèque PAKE ? | ✅ **Fermée le 13 octobre 2026 :** CPace, suite `CPACE-RISTR255-SHA512`, assemblé dans `pkg/pake` sur `github.com/gtank/ristretto255` (v0.2.0, épinglée), avec `PAIR-06` reformulée. Écartés : SPAKE2 (aucune implémentation Go conforme), `filippo.io/cpace` (expérimental, abandonné), `schollz/pake` (pas de vecteurs). Détail : [medxfer-g1-pake.md](medxfer-g1-pake.md). | Vecteurs de test de la spécification ; constantes publiques fixées sans logarithme connu ; maintenance ; compilation sans CGO sur mobile ; revue externe possible. | Phase 2b seulement |
| **G2a** ✅ fermée (11/10/2026) | Quelle frontière cœur/UI sur Windows, Linux et Android ? | **Retenu :** `c-shared` + `dart:ffi` partout, événements lus par un isolat de fond. `gomobile` non nécessaire. | Résultats et réserves (débit Linux sur machine ancienne, essai Linux sur machine propre) : `g2a-report.md`. | — |
| **G2b** | Même question sur macOS et iOS. | `c-shared` / `c-archive` ou `gomobile`. | Même prototype ; exige macOS et Xcode (voir G8). Le risque est le plus élevé sur iOS. | Phase 4b |
| **G8** | Comment obtenir un environnement macOS ? | Mac d'occasion, Mac loué à distance, exécuteurs macOS d'intégration continue (compilation seulement). | Un vrai iPhone branché à un Mac est nécessaire pour valider Bonjour, la permission « réseau local » et le comportement en arrière-plan. | Phase 4b |
| **G3** | TCP+TLS atteint-il 80 % d'iperf3 ? | Oui / non. | Benchmark §14.3. | Décision QUIC (phase 6) |
| **G4** | Revue cryptographique externe | Qui, quand. medXfer étant open source (Apache 2.0), le porteur du projet organise la revue (décision du 14 octobre 2026). Dossier : `medxfer-g4-dossier-revue.md`. | Avant la version 1.0, couvrant les chapitres 6 (chemin par BLE du §6.6 compris) et 7. Avant la revue, seules des **RC** sont publiées, avec un `SECURITY.md` (signalement privé des failles) et cet avertissement : « *Version RC — cryptographie non auditée. L'appairage par PIN utilise CPace (draft-irtf-cfrg-cpace-21), vérifié contre les vecteurs de test du brouillon et contre une implémentation indépendante. Une revue de sécurité indépendante est en cours d'organisation ; le dossier est public (`docs/medxfer-g4-dossier-revue.md`). Pour l'appairage le plus sûr, utilisez le QR code.* » | Version 1.0 finale (une RC peut sortir avant) |
| **G5** | HTTPS auto-signé du Web Share | Acceptable pour l'utilisateur ? | Test utilisateur sur iOS Safari et Chrome Android. | Phase 5 |
| **G6** ✅ fermée (11/10/2026) | SHA-256 suffisant ? | **Retenu : SHA-256** (`crypto/sha256`). Sur le téléphone de référence (Exynos 1580), 536 Mo/s sur un cœur contre 60 Mo/s pour BLAKE3 en Go, qui n'est pas accéléré sur arm64 ; mesures dans `medxfer-plan-j4a.md` (WP-4.0). | Benchmark de hachage sur téléphones cibles ≥ 1,5× le débit du lien. | — |
| **G7** | Quand supprimer le tableau de bord web ? | Garder / supprimer. | Tous les critères de retrait de `DEV-19` sont remplis par le panneau développeur Flutter et `xfer ctl`. | Fin de la phase 6 au plus tard |

---

## 17. Outils de développement, de mesure et de test

**But :** pouvoir développer, mesurer et tester le cœur Go (et chaque appareil) sans dépendre de Flutter, et comprendre *pourquoi* un transfert est lent ou échoue. Ce chapitre est un module (règle R3) : le cœur doit fonctionner de façon identique s'il est supprimé.

### 17.1 Principes
1. **Local uniquement.** Rien n'est envoyé hors de l'appareil. Le partage d'un rapport est une action manuelle de l'utilisateur (chapitre 13).
2. **Un benchmark passe par le code de production.** Un test de débit qui utilise un chemin différent de celui d'un vrai transfert mesure autre chose. Aujourd'hui le burst réseau (`engine/diagnostics.go`) est du TCP clair sur un seul flux : il ne dit rien du débit réel avec TLS et plusieurs flux.
3. **Les fonctions dangereuses n'existent pas en production.** Injection de fautes, profilage et tableau de bord web sont exclus des binaires publiés *à la compilation* (étiquettes de build), pas seulement désactivés par un réglage.
4. **Mesurer avant de régler.** Flux, taille de chunk et seuils sont des paramètres (règle R4) dont les valeurs par défaut sortent de ces outils.

### 17.2 Organisation du code
| Élément | Contenu |
|---|---|
| `pkg/diag` | Métriques par étape, rapport JSON, paquet de diagnostic. Remplace `api/telemetry.go`, qui se trouve aujourd'hui dans la couche daemon. |
| `pkg/diag/faultconn`, `pkg/diag/faultfs` | Injection de fautes réseau et disque (étiquette `diag`, jamais en production). |
| `pkg/testkit` | Aides de test : `NewPair(t)`, attentes d'événements, comparaison d'arborescences. |
| `cmd/xfer` | Sous-commandes `bench`, `ctl`, `report`, `bundle`, `dev-ui` (cette dernière sous l'étiquette `devui`). |

### 17.3 Télémétrie : mesures par étape et rapport
Le code actuel mesure réseau et disque par chunk. La v8 ajoute le hash et le `fsync`, sinon on ne peut pas expliquer un débit inférieur à 80 % d'iperf3.

| Id | Exigence | Statut |
|---|---|---|
| `DEV-01` | `pkg/diag` est indépendant : le cœur compile et passe ses tests avec un `diag` remplacé par un composant vide. Il s'abonne aux événements du cœur (interface `TransferListener` existante), il n'est jamais appelé par le cœur pour décider quoi que ce soit. | ✅ |
| `DEV-02` | Aucune donnée n'est émise hors de l'appareil par ce module. | ✅ |
| `DEV-03` | Pour chaque chunk, le receveur mesure : attente réseau (à partir du moment où le worker peut prendre le chunk, c'est-à-dire a un tampon libre, jusqu'à sa réception : un disque ou un hash lent retient les tampons et ne doit pas compter comme du temps réseau), hash, écriture, et `fsync` (par commit). L'émetteur mesure : lecture disque, hash, écriture réseau. On conserve par étape le temps cumulé, le nombre d'octets, les percentiles p50 et p95. | ✅ |
| `DEV-04` | **Étape limitante :** celle dont le temps cumulé par octet, ramené au parallélisme de l'étape, est le plus élevé. Cette heuristique est à valider par des scénarios synthétiques : disque factice lent, réseau bridé par `faultconn`, hash artificiellement ralenti. Le classement DOIT retrouver l'étape imposée dans 100 % des scénarios, sinon l'heuristique est corrigée. Validé en J4a (WP-4.5a) : disque lent (`faultfs`, délai d'écriture), réseau bridé (`faultconn`), hash ralenti ; l'étape est retrouvée à chaque passage, avec une confiance haute (×1,5 au moins sur la suivante). | ✅ |
| `DEV-05` | À la fin d'un transfert, `diag` produit un **rapport JSON versionné** (`schema_version`). Par défaut : ni noms de fichiers, ni adresses IP, ni nom d'hôte, ni empreintes. Un réglage explicite peut les inclure. | ✅ |
| `DEV-06` | `xfer report show <fichier>` affiche un rapport lisible ; `xfer report compare a.json b.json` affiche les écarts (débit, étape limitante, retries). Le texte « copiable » actuel est conservé comme vue lisible du même rapport. | ✅ |

Exemple de rapport (valeurs d'illustration) :
```json
{
  "schema_version": 1,
  "app": { "version": "0.9.0", "commit": "abc1234", "go": "go1.24" },
  "device": { "os": "android", "arch": "arm64", "cpu_cores": 8, "role": "receiver" },
  "params": { "streams": 4, "chunk_size": 4194304, "commit_bytes": 67108864,
              "tls_cipher": "TLS_AES_128_GCM_SHA256" },
  "totals": { "bytes": 10737418240, "duration_s": 61.4,
              "avg_mib_s": 166.7, "peak_mib_s": 181.2, "min_mib_s": 120.4 },
  "stages": {
    "net_wait":   { "busy_ms": 52100, "p50_ms": 18, "p95_ms": 41 },
    "hash":       { "busy_ms": 9300,  "p50_ms": 3,  "p95_ms": 5  },
    "disk_write": { "busy_ms": 14800, "p50_ms": 5,  "p95_ms": 22 },
    "fsync":      { "busy_ms": 2100,  "p50_ms": 80, "p95_ms": 210 }
  },
  "bottleneck": { "stage": "net_wait", "confidence": "high" },
  "quartiles": [ { "phase": "0-25%", "avg_mib_s": 171.0 } ],
  "events": { "retries": 0, "reconnects": 1, "hash_mismatches": 0, "pauses": 0 }
}
```

### 17.4 Benchmarks : `xfer bench`
| Id | Exigence | Statut |
|---|---|---|
| `DEV-07` | Sous-commandes, toutes avec `--json` : `bench disk` (écriture et lecture avec `fsync`, déjà dans le code), `bench net --peer` (débit et latence), `bench ladder` (échelle ci-dessous), `bench matrix` (balayage flux × chunk), `bench transfer` (transfert complet synthétique). | ✅ |
| `DEV-08` | Un benchmark réseau ou de transfert DOIT utiliser le code de production : une offre de type `bench` (champ optionnel `kind: "bench"` dans `offer`) traverse le même canal TLS, les mêmes connexions de données et les mêmes hash qu'un transfert réel, mais les octets sont jetés (puits nul). | ✅ |
| `DEV-09` | `--source=zero|random|file` et `--sink=null|disk`. La source `random` utilise un générateur rapide en mémoire pour ne pas devenir le goulot. Les tests de disque utilisent des données aléatoires (pas de zéros, que certains systèmes de fichiers optimisent). | ✅ |

**L'échelle de benchmark.** Chaque marche ajoute une couche ; le débit de chaque marche montre ce que cette couche coûte. L'objectif de 80 % se lit entre la dernière marche et la première.

| Marche | Ce qui est testé | Source |
|---|---|---|
| R0 | Lien brut | `iperf3 -P 4 -t 30` (outil externe) |
| R1 | TCP brut, N flux | `xfer` (intégré) |
| R2 | + TLS 1.3 avec empreintes épinglées | `xfer` |
| R3 | + trames medXfer et SHA-256, puits nul | `xfer` |
| R4 | + écriture disque sans `fsync` | `xfer` |
| R5 | + journal et `fsync` (commit groupé) | `xfer` |
| R6 | Transfert complet (offre, acceptation, `digest`, renommage) | `xfer` |

En J4a, `xfer bench ladder` mesure R1 à R4 entre deux appareils : le côté `--listen` envoie (source `--source random|zero|file`) et affiche la commande à lancer de l'autre côté avec un code et son empreinte ; l'autre côté reçoit sur N flux et mesure. R2 épingle l'empreinte affichée ; R3 à R5 passent par le moteur de transfert sur une offre `bench` (puits `--sink null|disk` pour R3), et donnent l'étape limitante du côté qui reçoit. Depuis J4b (WP-4.9), R5 ajoute le vrai `pkg/store` (journal, commit groupé, `fsync`), et R6 fait un transfert complet par le chemin de `xfer send` / `xfer recv` :
- une offre de type fichier arrive sur une connexion de contrôle (`session.Channel`) ;
- elle est vérifiée (`Validate`, espace libre) puis acceptée ;
- les connexions de données la rejoignent par le hub (`DATA_JOIN`, CHAN-06), sur le même port ;
- le fichier passe par le journal, le `digest` et le renommage.

`bench matrix` balaie flux ∈ {1, 2, 4, 8} et chunk ∈ {1, 2, 4, 8} Mio, et propose comme valeurs par défaut la combinaison dont le débit est à moins de 5 % du meilleur avec la plus faible mémoire (`N × 2 × chunk`). Les résultats sont enregistrés dans `bench-results/*.json` pour comparer les appareils.

`xfer bench matrix --peer` mesure une marche par combinaison (`--step`, R6 par défaut) face au même serveur que `bench ladder --listen`. Avec `--repeat N`, chaque case retient la médiane de N passages, et les passages alternent entre les cases, un tour sur deux en ordre inverse : une baisse passagère du lien touche ainsi toutes les cases, au lieu de tous les passages d'une seule. Le résultat indique la meilleure case et la case recommandée, et dit si les valeurs actuelles (4 × 2 Mio depuis D8 du plan J4b) sont à garder.

### 17.5 Injection de fautes
| Id | Exigence | Statut |
|---|---|---|
| `DEV-10` | `faultconn` enveloppe une `net.Conn` avec un plan déterministe (graine fixe) : latence, gigue, débit maximal, coupure après N octets, remise à zéro de la connexion. `faultfs` enveloppe le stockage : `ENOSPC` après N octets, écriture déchirée (la moitié écrite puis erreur), `fsync` qui ment, écriture lente (délai par écriture). Ces paquets portent l'étiquette `diag`. Un test de CI vérifie qu'ils sont absents du binaire de production. | ✅ |

```go
c := faultconn.Wrap(conn, faultconn.Plan{
    Seed: 42, Latency: 5 * time.Millisecond,
    BandwidthBps: 20 << 20, ResetAfterBytes: 3 << 20,
})
```
Ils servent aux tests de chaos du §14.2 et à la validation de `DEV-04`.

### 17.6 Paquet de diagnostic, traces, profilage
| Id | Exigence | Statut |
|---|---|---|
| `DEV-11` | **Paquet de diagnostic** (`xfer bundle`, ou bouton dans l'interface) : archive contenant les derniers rapports, un tampon circulaire de journaux (2 Mio, secrets masqués), la version et l'appareil, la configuration sans secrets ni chemins personnels, et le journal de l'application (`app.log`) : ce que le cœur ne voit pas, son Bluetooth et ses réseaux (démarrage, création ou refus avec la raison du système, réseau rejoint), les 2 000 dernières lignes, gardées en mémoire, 512 Kio au plus, masquées comme le reste. Jamais de clés, de PIN, de jetons, d'empreintes complètes ni de noms de fichiers. Le test du chapitre 13 s'applique. | ✅ |
| `DEV-12` | **Traces :** `MEDXFER_TRACE=proto` journalise types, tailles et durées des trames, jamais leur contenu. **Profilage :** `net/http/pprof` et `runtime/trace` n'existent que dans un build `diag`, activés par un drapeau explicite, liés à `127.0.0.1`, protégés par le jeton de `API-04`. Sur mobile, on produit des fichiers de profil (`--cpuprofile`, `--memprofile`, `--trace`) dans le dossier de l'application, récupérés via le paquet de diagnostic. | ✅ |
| `DEV-13` | **Mode développeur :** réglage persistant, désactivé par défaut dans les versions publiées. Il affiche une bannière visible et active : le panneau développeur, les traces, les réponses `diag_request`. Il n'active jamais ce qui est exclu à la compilation (`DEV-10`, `DEV-12`). | ✅ |
| `DEV-14` | **Diagnostic à distance sur le canal sécurisé :** messages `diag_request{what: "report"|"ping"}` et `diag_report`. Acceptés seulement si le mode développeur est activé *des deux côtés*, si le pair est un appareil de confiance, avec un indicateur visible et une limite de fréquence. Ils ne renvoient que le rapport JSON (jamais le paquet). Cela permet d'inspecter un téléphone depuis le PC sans ouvrir de port. | ✅ |

### 17.7 Piloter et tester le cœur sans Flutter
| Id | Exigence | Statut |
|---|---|---|
| `DEV-15` | **`testkit.NewPair(t)`** crée deux cœurs en mémoire, avec dossiers temporaires et déjà appairés, et expose les mêmes `Send` / événements que Flutter. Les tests d'`api_test.go` y sont portés : même vocabulaire, plus de port ni de WebSocket. | ✅ |
| `DEV-16` | **`xfer ctl`** lit des requêtes JSON, une par ligne, sur l'entrée standard, et écrit les événements JSON sur la sortie (format NDJSON). Option `--script scenario.jsonl`. Sans réseau. | ✅ |
| `DEV-17` | **Scénarios enregistrés** (fichiers « golden ») : un dialogue JSON (requêtes et événements attendus, avec champs variables masqués) sert de test de non-régression côté Go *et* de moteur factice côté Dart pour développer l'interface sans le cœur. | ✅ |
| `DEV-18` | **Test de la frontière** : un petit programme charge la bibliothèque et appelle `Start`, `Send`, `SetEventHandler`, `Stop` (`API-01`). | ✅ |

```go
func TestSendFolder(t *testing.T) {
    a, b := testkit.NewPair(t) // deux cœurs appairés, dossiers temporaires
    a.Send(t, "send", map[string]any{"paths": []string{src}})
    b.WaitEvent(t, "incoming_offer", 2*time.Second)
    b.Send(t, "respond_offer", map[string]any{"accept": true})
    b.WaitEvent(t, "transfer_complete", 30*time.Second)
    testkit.AssertTreesEqual(t, src, b.DownloadDir())
}
```
(Noms d'API illustratifs ; le vocabulaire des actions est celui qui existe déjà.)

Trois niveaux de test indépendants : (1) le cœur seul en Go, (2) la frontière C par `DEV-18`, (3) l'interface Flutter contre le moteur factice de `DEV-17`.

### 17.8 Le tableau de bord web (`IndexHTML`)
À ne pas confondre avec le **portail Web Share** des invités (chapitre 11), qui est une fonctionnalité du produit et reste.

| Id | Exigence | Statut |
|---|---|---|
| `DEV-19` | Le tableau de bord web est un **outil de développement** : (a) commande `xfer dev-ui` et étiquette de build `devui` ; (b) absent des binaires publiés (vérifié en CI) ; (c) soumis aux sept contrôles du §10.3 ; (d) gel des fonctions : corrections de bugs et de sécurité seulement ; (e) emporte avec lui le sélecteur natif, le dépôt temporaire et la navigation de fichiers (`/api/browse`, `/api/upload`, `/api/fs/*`), qui n'existent pas ailleurs. | ✅ |
| `DEV-19-R` | **Critères de retrait**, évalués à la gate G7 : le panneau développeur Flutter affiche la télémétrie en direct, lance les benchmarks, exporte les rapports et le paquet de diagnostic, fait appairage, envoi et réception ; `xfer ctl` couvre les scripts sans écran. Quand tout est vrai, le tableau de bord est supprimé. | ✅ |

Pour le terminal (Termux, serveur sans écran), `xfer ctl` remplace le tableau de bord ; une petite interface en terminal peut s'y ajouter plus tard sans toucher au cœur.

### 17.9 Intégration continue
| Id | Exigence | Statut |
|---|---|---|
| `DEV-20` | À chaque changement : `go vet`, `go test -race ./...`, graines de fuzzing rejouées, test de présence/absence des paquets réservés (`faultconn`, `pprof`, `devui`) dans le binaire de production. L'exécution se fait sur une **matrice Windows, Linux, macOS** d'exécuteurs hébergés (GitHub Actions) : le développeur n'a pas besoin d'un Mac local pour que le cœur soit testé sur macOS. | ✅ |
| `DEV-21` | Chaque nuit : `bench ladder` en boucle locale. Le réseau local ne représente pas un vrai Wi-Fi, mais il détecte les régressions de CPU (TLS, hash, trames). Un recul de plus de 15 % (paramètre) d'une marche R2 à R3 fait échouer la tâche. Les mesures sur appareils réels se font à la main avant chaque version, sur la matrice du §14.3, et sont archivées dans `bench-results/`. | 📏 |

### 17.10 Tests d'acceptation du chapitre
| Test | Vérifie |
|---|---|
| Compilation et tests avec `diag` remplacé par un composant vide | `DEV-01` |
| Classement de l'étape limitante sur disque lent / réseau bridé / hash ralenti | `DEV-04` |
| Rapport JSON sans nom de fichier, IP ni nom d'hôte par défaut | `DEV-05` |
| `bench ladder` : R6 passe par le même chemin de code que le transfert réel (couverture) | `DEV-08` |
| Binaire de production sans `faultconn`, `pprof`, `devui` | `DEV-10`, `DEV-12`, `DEV-19` |
| Paquet de diagnostic sans secrets, comparé à une liste de motifs interdits | `DEV-11` |
| `diag_request` refusé si l'un des deux appareils n'est pas en mode développeur, ou si le pair n'est pas de confiance | `DEV-13`, `DEV-14` |
| Les scénarios d'`api_test.go` portés sur `NewPair` donnent les mêmes résultats | `DEV-15` |
| Rejouer un scénario enregistré avec `xfer ctl` reproduit les événements attendus | `DEV-16`, `DEV-17` |

---

## Annexe A — Correspondance failles du code actuel → exigences

| Faille | Constat dans le code | Exigences |
|---|---|---|
| 1. Fichier en clair | Les connexions de données (`engine/sender.go`, `receiver.go`) sont du TCP sans TLS | `CHAN-01`, `CHAN-06` |
| 2. Repli en clair, pas de vérification | `session/tls.go` : `InsecureSkipVerify` et repli `DialPeer` | `CHAN-03`, `CHAN-05` |
| 3. Port de données ouvert | N'importe qui peut tirer les chunks (garde = `FileID` MD5 devinable) | `CHAN-06` |
| 4. Code d'appairage faible | 900 valeurs, aucune limite d'essais | `PAIR-01` à `PAIR-05` |
| 5. Détournement du WebSocket local | `CheckOrigin: true` | `API-01` à `API-04` |
| 6. PIN Web Share fragile | Horloge, journaux, URL, cookies | `WEB-02` à `WEB-04` |
| 7. Suppression par un pair | `ensureDirectory` supprime un fichier gênant | `STO-09` à `STO-14` |
| 8. Intégrité et durabilité | Pas de hash de bout en bout, bitmap sans `fsync` ordonné, mémoire non bornée | `STO-01` à `STO-08`, `XFER-03` |

## Annexe B — Sources et état de vérification

**Vérifié :**
- iOS : le multicast/broadcast exige l'entitlement `com.apple.developer.networking.multicast` ; Bonjour ne l'exige pas mais demande `NSBonjourServices` et `NSLocalNetworkUsageDescription`. Apple Developer Forums (réponses de l'équipe DTS) et [documentation de l'entitlement](https://developer.apple.com/tutorials/data/documentation/bundleresources/entitlements/com.apple.developer.networking.multicast.md).
- LocalSend : HTTPS à certificat auto-généré, empreinte SHA-256, multicast + repli par balayage de sous-réseau. [Protocole](https://github.com/localsend/protocol).
- **CPace :**
  - au 13 octobre 2026, c'est encore le brouillon CFRG/IRTF `draft-irtf-cfrg-cpace-21`, transmis à l'éditeur RFC (« *Awaiting First editor* ») et sans numéro de RFC. [Datatracker](https://datatracker.ietf.org/doc/draft-irtf-cfrg-cpace/) ;
  - `pkg/pake` en vérifie les annexes A et B.3 octet pour octet ;
  - `filippo.io/cpace` est marqué expérimental et basé sur un brouillon antérieur. [Module Go](https://pkg.go.dev/filippo.io/cpace?tab=doc) ;
  - pour le chemin par BLE (§6.6), lu dans le texte du brouillon 21 le 2 novembre 2026 : un `sid` fait de valeurs aléatoires apportées par les deux côtés (§10.9 : « *One suitable option for generating sid is concatenation of ephemeral random strings contributed by both parties* ») ; l'ISK passé par une KDF, comme HKDF (RFC 5869), avant tout usage (§4, §10.3) ; les identités de préférence dans le CI (§10.1), ce que le §6.6 écarte pour ne pas envoyer d'empreinte en clair.
- Go : `crypto/hkdf` (RFC 5869) dans la bibliothèque standard depuis Go 1.24 ; vérifié avec `go doc crypto/hkdf` (Go du projet : 1.25).
- BLE sur les appareils de l'utilisateur (annonce de 31 octets, UUID seul sous Windows, deux rôles à la fois, mode caché) : mesuré par la sonde de J6d, [plan J6d](medxfer-plan-j6d.md), WP-D.0.
- **`schollz/pake`** (CVE-2021-31603) : l'initiateur choisissait les points publics, donc pouvait connaître leur logarithme ; l'entropie de la clé tombait à celle du code. La faille a été corrigée depuis. [Analyse](https://redrocket.club/posts/croc/), [message au CFRG](https://mailarchive.ietf.org/arch/msg/cfrg/KtmqZS6CFUwCfkVTlieGw-ZFGfY/), [module](https://pkg.go.dev/github.com/schollz/pake).
- Go 1.24 : `os.Root` / `os.OpenRoot` pour un accès aux fichiers résistant à la traversée. [Notes de version](https://go.dev/doc/go1.24), [article du blog Go](https://golang.google.cn/blog/osroot).

**Non vérifié (recherche non concluante) :**
- BBR dans quic-go upstream : les implémentations trouvées sont des forks. [Exemple](https://github.com/ao-space/gt/issues/1).
- Support d'un PSK externe dans `crypto/tls` : voir le [ticket Go 6379](https://golang.org/issue/6379).
- **Ces deux points ne portent pas la décision de reporter QUIC** : elle repose sur l'absence de gain démontré et sur le coût, et sera tranchée par le benchmark G3.
- BLE (§6.6) : la taille maximale d'un attribut GATT (512 octets) et l'expiration d'une transaction ATT à 30 s viennent de la spécification Bluetooth Core (vol. 3, partie F) ; la localisation précise exigée pour balayer sous Android 11, de la documentation Android. Non revérifiés en ligne. WP-D.3 vérifie les deux premiers sur les appareils ; aucun appareil sous Android 11 n'est disponible pour le troisième.

**Non vérifié, à mesurer :** tous les chiffres de performance (débit de SHA-256, coût du `fsync`, 80 % d'iperf3). Les outils cités au chapitre 17 (`net/http/pprof`, `runtime/trace`, `iperf3`) sont des outils standard dont l'existence est connue, mais ils n'ont pas été revérifiés en ligne dans cette session. Le fait qu'iOS interdise de lancer un autre programme et qu'Android ait restreint l'exécution de binaires embarqués (§3.3) vient de connaissances générales, non revérifié ici ; le prototype G2 le confirmera sur appareils.

## Annexe C — Glossaire
- **AEAD** : chiffrement qui authentifie aussi chaque message (AES-GCM, ChaCha20-Poly1305). Une modification est détectée.
- **ALPN** : champ TLS où chaque côté annonce le protocole applicatif (`medxfer/1`).
- **Empreinte (fingerprint)** : condensé SHA-256 de la clé publique d'un appareil ; son « identité ».
- **Épinglage (pinning)** : n'accepter qu'une empreinte précise, déjà connue.
- **mTLS** : TLS où le client présente aussi un certificat.
- **MITM** : attaquant qui s'interpose entre deux appareils.
- **PAKE** : échange de clé à partir d'un mot de passe faible (PIN), sans que l'attaquant puisse tester hors ligne.
- **`ExportKeyingMaterial`** : valeur dérivée de la session TLS, identique des deux côtés seulement si c'est la même session ; sert à « lier » l'appairage au canal.
- **`fsync`** : demande au système de forcer l'écriture réelle sur le disque.
- **TOFU** : « faire confiance à la première utilisation ». La v8 le refuse en silence (`ID-05`).
- **CSWSH** : détournement de WebSocket inter-sites.
- **SPKI** : structure DER contenant la clé publique d'un certificat.
- **Puits nul / source nulle** : mode où le receveur jette les octets reçus, ou l'émetteur les génère en mémoire, pour isoler une couche.
- **Échelle de benchmark** : suite de mesures où chaque marche ajoute une couche (TLS, hash, disque…) pour voir ce que chacune coûte.
- **NDJSON** : un objet JSON par ligne ; facile à scripter et à rejouer.
- **Fichier « golden »** : enregistrement de référence d'un dialogue ou d'une sortie, rejoué dans les tests.
- **`pprof`** : outil de profilage de Go (CPU, mémoire).
- **Étiquette de build** : option de compilation qui inclut ou exclut du code ; ici elle garde les outils dangereux hors des binaires publiés.
