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
Cinq plateformes : **Windows, Linux, Android, macOS, iOS**. Le poste de développement est Windows ; il n'y a, à ce jour, ni Mac ni iPhone. Ordre de validation : (1) Windows, Linux (via WSL2), Android, validés par le prototype G2a (`medxfer-plan-prototype-g2a.md`) ; (2) macOS et iOS dès qu'un environnement macOS est disponible (gates G2b et G8). Le cœur Go est testé sur macOS dès la phase 0b par l'intégration continue (`DEV-20`).

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

Hors périmètre : un autre processus malveillant tournant sous le même compte utilisateur (on réduit seulement l'exposition avec des permissions de fichiers strictes).

### 2.2 Menace → contrôle → exigences
| Menace | Contrôle | Exigences |
|---|---|---|
| A1 lit les fichiers | TLS 1.3 sur *toutes* les connexions, aucun texte clair | `CHAN-01`, `CHAN-02`, `CHAN-05` |
| A2 intercepte (MITM) | Épinglage d'empreinte (mTLS) + PAKE lié au canal | `CHAN-03`, `PAIR-03`, `PAIR-05` |
| A2 force un repli en clair | Aucun chemin de repli ; handshake raté = connexion fermée | `CHAN-05` |
| A3 télécharge sans appairage | Connexion de données acceptée seulement si l'empreinte du pair correspond au transfert | `CHAN-06` |
| A3 devine le code | PAKE, 3 essais, durée de vie limitée | `PAIR-04` |
| A4 écrit hors du dossier / supprime des fichiers | Chemins validés composant par composant, jamais de suppression, dossier racine par transfert | `STO-09` à `STO-14` |
| A4 épuise les ressources | Limites chiffrées du chapitre 12 | `LIM-01` à `LIM-08` |
| A5 pilote l'application | Aucun socket de contrôle ; sinon contrôles Origin, Host, jeton | `API-01` à `API-04` |
| A6 force le PIN | 5 essais par *session*, pas par IP ; jeton porteur sans cookie | `WEB-03`, `WEB-04` |
| Crash ou disque défaillant | Journal ordonné + hash final | `STO-03` à `STO-08` |

---

## 3. Architecture en couches et registre de décisions

### 3.1 Les couches
```
 G  Surface de contrôle   cœur Go appelé en-processus (FFI) | CLI | loopback optionnel
 F  Stockage              fichier .part + journal + renommage atomique
 E  Protocole de transfert OFFER / CHUNK / DIGEST
 D  Canal sécurisé        TLS 1.3, mTLS, empreintes épinglées, ALPN
 C  Appairage             QR (secret fort)  |  PIN (PAKE)  |  appareil de confiance
 B  Découverte            mDNS | balise UDP | saisie manuelle   (non fiable par conception)
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
    ChannelBinding() []byte               // ExportKeyingMaterial, 32 octets
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
| SPAKE2 imposé | ❓ PAKE à choisir (gate G1). QR sans PAKE dès la phase 2. | Le choix de bibliothèque est la décision la plus risquée ; elle ne doit pas bloquer le reste. | — |
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
| Daemon + WebSocket | ❌ Remplacé par un cœur Go en-processus (§10). | iOS interdit de lancer un autre programme et Android a fortement restreint l'exécution de binaires embarqués : un daemon séparé est impossible sur mobile. Un port d'écoute est en plus une surface d'attaque inutile. | Le prototype G2a échoue sur une plateforme de bureau (un processus séparé reste alors un plan B). |
| Tableau de bord web hôte (`IndexHTML`) | ⏸ Outil de développement seulement (`DEV-19`), absent des versions publiées, gelé, à supprimer. | Duplique l'interface Flutter, 2 400 lignes sans tests réels, surface d'attaque (sélecteur natif, dépôt temporaire). | Jamais en production. |
| Chiffrement WebCrypto du Web Share | ❌ Supprimé (§11.4). | `crypto.subtle` n'existe qu'en contexte sécurisé ; le JavaScript est servi par le même canal. | — |
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
| `ID-05` | Si un appareil connu se présente avec une autre empreinte, la connexion DOIT être refusée et l'utilisateur alerté. Aucune acceptation silencieuse (pas de « TOFU » silencieux). | C'est exactement la signature d'une attaque MITM. |
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
| `DISC-01` | Service mDNS `_medxfer._tcp`, port TCP du listener. Le TXT contient uniquement `v=1`. Le nom d'instance est `medXfer-` + 4 caractères hexadécimaux aléatoires tirés à chaque lancement. | ✅ |
| `DISC-02` | iOS : Bonjour via l'API du système, avec `NSBonjourServices = ["_medxfer._tcp"]` et `NSLocalNetworkUsageDescription`. Aucun multicast brut sur iOS. | ✅ |
| `DISC-03` | Android : acquérir un `MulticastLock` du Wi-Fi pendant la découverte. | ✅ |
| `DISC-04` | Repli (bureau et Android seulement) : balise UDP vers un groupe de la plage non routée `224.0.0.0/24`. Ne pas réutiliser `239.255.255.250` (SSDP/UPnP). Charge utile identique au TXT. Au plus une balise toutes les 2 s. | 📏 |
| `DISC-05` | L'appareil DOIT annoncer sa présence **uniquement** quand l'utilisateur l'a demandé (mode « recevoir » ouvert, ou envoi en attente). Le code actuel émet en permanence toutes les 600 ms. | ✅ |
| `DISC-06` | Ni nom réel, ni nom d'hôte, ni liste de fichiers, ni clé publique dans l'annonce. Le nom affiché (alias) n'est transmis qu'*après* authentification (§6, §7). | ✅ |
| `DISC-07` | Le sélecteur côté receveur reconnaît un appareil grâce au suffixe du nom générique (`…-4F2A`) affiché aussi sur l'écran de l'émetteur, à côté du PIN. | ✅ |
| `DISC-08` | Repli manuel : saisie `IP:port` ou QR (contient l'adresse). | ✅ |
| `DISC-09` | Balayage du sous-réseau (/24, 64 sockets concurrents, délai 350 ms) seulement sur action explicite « Appareil introuvable ? ». | ✅ (déjà dans le code) |

---

## 6. Couche C — Appairage

**But :** passer de « deux inconnus » à « deux appareils qui se font confiance », sans exposer de secret faible.
Les trois chemins aboutissent au même état : *l'empreinte du pair est dans la liste de confiance*.

### 6.1 Chemin P1 — QR code (secret fort)
Le QR contient : empreinte de l'émetteur, adresse, port, secret aléatoire de 128 bits, expiration.
Format : `medxfer://v1/pair?fp=<empreinte base32>&a=<ip>&p=<port>&s=<secret base64url>&exp=<unix>`

Déroulement :
1. Le receveur scanne le QR et se connecte en **épinglant `fp`** (le QR est un canal hors-bande : la caméra est le canal authentifié).
2. Dans le canal TLS déjà authentifié, il envoie `pair_secret` avec le secret.
3. L'émetteur le compare en temps constant. Le secret est à usage unique, durée de vie 120 s par défaut.
4. Chacun enregistre l'empreinte de l'autre.

Pourquoi aucun PAKE ici : le secret a 128 bits d'entropie, et l'attaquant ne peut pas faire de MITM puisque l'empreinte vient du QR.

### 6.2 Chemin P2 — PIN à 6 chiffres (PAKE)
Un PIN court (~20 bits) ne protège que s'il est utilisé *dans un PAKE* : l'attaquant ne peut alors tenter qu'un essai en ligne par tentative de connexion, sans possibilité de force brute hors ligne.

Déroulement (le **répondant** affiche le PIN ; l'**initiateur** le saisit) :
1. L'initiateur ouvre une connexion TLS. L'empreinte du répondant est inconnue : la connexion est en état `UNAUTH` (§7), seules les trames `pair_*` sont permises.
2. Les deux calculent `ekm = ExportKeyingMaterial("EXPORTER-medxfer-v1-pair", nil, 32)`. Cette valeur dépend de la session TLS réelle.
3. Ils exécutent le PAKE avec le PIN comme mot de passe, en incluant dans les identités/contexte du PAKE : `fp_initiateur ‖ fp_répondant ‖ ekm`.
4. **Confirmation de clé :** chacun envoie `HMAC(clé_PAKE, rôle ‖ fp_i ‖ fp_r ‖ ekm)` et vérifie celui du pair.
5. Succès : chacun enregistre l'empreinte de l'autre, la connexion passe en état `AUTH`, et seulement alors l'alias est échangé.

**Pourquoi le MITM échoue :** un attaquant qui relaie ouvre deux sessions TLS distinctes. Les `ekm` et les empreintes diffèrent d'un côté à l'autre ; les confirmations ne correspondent donc pas. Sans le PIN il ne peut pas non plus dériver la clé PAKE.

### 6.3 Chemin P3 — Appareil de confiance
Aucun code : le canal (§7) vérifie que l'empreinte du pair est dans `trusted.json`. L'envoi exige quand même l'acceptation du receveur, sauf si l'utilisateur a activé « accepter automatiquement » pour cet appareil.

### 6.4 Exigences
| Id | Exigence | Statut |
|---|---|---|
| `PAIR-01` | Le PIN DOIT être tiré avec `crypto/rand`, uniformément dans [0, 10^6), sans biais modulo. | ✅ |
| `PAIR-02` | Un PIN est valable 120 s au plus et pour une seule session d'appairage. | ✅ |
| `PAIR-03` | L'identité et le contexte du PAKE DOIVENT inclure `fp_i`, `fp_r` et `ekm` (liaison au canal). | ✅ |
| `PAIR-04` | Après **3** échecs, le PIN est détruit, la session fermée, un nouveau PIN est généré. Après 3 PIN détruits d'affilée, délai exponentiel avant d'en afficher un nouveau. | ✅ |
| `PAIR-05` | La confirmation de clé est obligatoire dans les deux sens. | ✅ |
| `PAIR-06` | On NE DOIT PAS écrire soi-même la cryptographie du PAKE. La bibliothèque retenue DOIT vérifier les vecteurs de test de sa spécification et fixer les constantes publiques sans connaissance du logarithme discret (voir l'incident de `croc`, Annexe B). | ❓ gate G1 |
| `PAIR-07` | Un échec d'appairage NE DOIT révéler ni le nombre d'essais restants ni l'alias du répondant. | ✅ |
| `PAIR-08` | Mode « un seul envoi » (sans mémoriser l'appareil) : la confiance n'est pas écrite dans `trusted.json` et disparaît avec la session, sauf demande explicite. | ✅ |

**Risque résiduel accepté pour P2 :** un attaquant actif a 3 chances sur 10^6 par PIN affiché. La voie P1 (QR) est la voie « forte ». PIN configurable à 8 chiffres si besoin.

---

## 7. Couche D — Canal sécurisé

| Id | Exigence | Raison / statut |
|---|---|---|
| `CHAN-01` | Chaque appareil expose **un seul** listener TCP (défaut 18887 ; si occupé, port éphémère annoncé par la découverte). Il parle TLS dès le premier octet. On NE DOIT PAS « deviner » le protocole en lisant le premier octet. | Supprime le chemin « texte clair accepté » de `UpgradeToTLSIfClientHello`. ✅ |
| `CHAN-02` | TLS 1.3 minimum ; ALPN `medxfer/1` obligatoire (refus sinon) ; tickets de session désactivés (`SessionTicketsDisabled: true`) donc ni reprise ni 0-RTT. | Pas de rejeu possible. ✅ |
| `CHAN-03` | Authentification mutuelle : `ClientAuth: tls.RequireAnyClientCert`. La confiance est décidée par `VerifyConnection` (code ci-dessous), appelée des deux côtés. | ✅ |
| `CHAN-04` | Deux états. `UNAUTH` : empreinte inconnue ; seules les trames `pair_*` sont acceptées, délai 30 s, 4 Kio max. `AUTH` : empreinte de confiance ou appairage réussi. Toute autre trame en `UNAUTH` ferme la connexion. | ✅ |
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
| `0x23` | `CHUNK_ERR` | `item` u32 ‖ `index` u32 ‖ `code` u16 (`SOURCE_CHANGED`, `READ_ERROR`) |

Messages `CONTROL` (champ `type`) : `hello`, `pair_secret`, `pair_pake`, `pair_confirm`, `offer`, `accept`, `reject`, `pause`, `resume`, `cancel`, `digest_req`, `digest`, `done`, `error`. Les champs JSON inconnus sont ignorés ; un type inconnu provoque `error{code: unsupported}`.

### 8.3 Déroulement d'un transfert
1. **Offre.** L'émetteur envoie `offer` : `transfer_id` (128 bits aléatoires), liste d'éléments `{item, path, size}`, `total_bytes`, `chunk_size` proposée. Un seul `offer` couvre un fichier, plusieurs fichiers ou un dossier.
2. **Décision.** Le receveur affiche nom, taille, nombre de fichiers et expéditeur, puis répond `accept{transfer_id, chunk_size}` (peut réduire la taille) ou `reject`.
3. **Flux de données.** Le receveur ouvre *N* connexions (défaut 4, de 1 à 8 📏), chacune commence par `DATA_JOIN`.
4. **Le receveur pilote (modèle « pull »).** Il envoie `CHUNK_REQ` pour les plus petits index manquants, avec au plus 2 requêtes en vol par connexion. Les écritures restent presque séquentielles (utile pour les disques durs) et la mémoire est bornée : `N × 2 × chunk_size`.
5. **Vérification à l'arrivée.** À réception d'un `CHUNK`, le receveur recalcule le SHA-256 et compare à celui reçu *avant* d'écrire.
6. **Fin.** Quand tous les chunks d'un élément sont écrits, le receveur envoie `digest_req`. L'émetteur répond `digest` = `SHA-256("medxfer-v1-filedigest" ‖ n ‖ hash_0 ‖ … ‖ hash_{n-1})` (hash des hash). Le receveur compare avec le sien. Voir §9 pour la finalisation.
7. **Clôture.** `done{transfer_id, status}`.

**Pourquoi un hash par chunk alors que TLS authentifie déjà ?**
(a) Il permet de vérifier la reprise sur ce qui est *déjà sur disque*. (b) Il détecte un fichier source modifié pendant l'envoi ou une erreur de lecture locale, que TLS ne voit pas. (c) Il reste valable si le transport est remplacé un jour (règle R2). Coût : 32 octets par chunk de 4 Mio.

**Fichier source modifié pendant l'envoi :** l'émetteur note taille et date de modification dans l'offre, les revérifie avant `digest`, et sinon répond `CHUNK_ERR SOURCE_CHANGED`. Le transfert de cet élément échoue proprement.

**Pause :** le receveur cesse d'émettre des `CHUNK_REQ` (les connexions restent ouvertes). **Annulation :** `cancel`, fermeture des connexions de données, nettoyage décrit en §9.

**Reprise :** le receveur reconnaît un transfert partiel par une *clé de reprise* `SHA-256(fp_émetteur ‖ chemin ‖ taille ‖ mtime)` stockée dans l'en-tête du journal. En cas de désaccord final (digest différent), l'élément est repris à zéro : cas rare, simple et sûr.

**Petits fichiers :** comme un seul `offer` couvre tout le lot et que les requêtes mélangent les éléments, l'attente par fichier disparaît. Le flux TAR (`StreamTar`) n'est donc plus nécessaire dans la v1 (⏸ ; réintégration si le benchmark §14.3 sur 100 000 petits fichiers montre un écart significatif).

### 8.4 Exigences
| Id | Exigence | Statut |
|---|---|---|
| `XFER-01` | Toute lecture réseau est bornée par la longueur annoncée dans l'en-tête et par les limites du chapitre 12. | ✅ |
| `XFER-02` | La taille de chunk est fixée dans `accept` et ne change plus pendant le transfert. Défaut 4 Mio ; bornes 256 Kio à 16 Mio. | 📏 |
| `XFER-03` | Le hash de chunk est SHA-256. | ✅ / gate G6 |
| `XFER-04` | Le receveur NE DOIT PAS écrire un chunk dont le hash ne correspond pas ; il le redemande (3 essais maximum, sur une autre connexion si possible). | ✅ |
| `XFER-05` | Une connexion de données coupée est rétablie avec un délai croissant (50 ms jusqu'à 1 s, 8 tentatives). | ✅ (déjà dans le code) |
| `XFER-06` | Négociation de version : ALPN `medxfer/N`. Un changement incompatible utilise un nouvel ALPN, l'ancien restant servi en parallèle pendant la transition. | ✅ |
| `XFER-07` | L'émetteur DOIT détecter la modification de la source (taille/mtime). | ✅ |

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
Le fichier ne porte son nom final qu'une fois **complet et vérifié** (renommage). Aujourd'hui, un fichier partiel apparaît déjà sous son vrai nom.

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

### 9.7 Espace disque
Avant l'`accept` : espace libre ≥ taille totale + max(1 %, 64 Mio). Si le disque se remplit (`ENOSPC` / `ERROR_DISK_FULL`) : transfert **en pause**, fichiers `.part` conservés, événement `DISK_FULL`. Ne pas utiliser `Truncate` pour pré-allouer sur NTFS (le fichier grossit à l'écriture, comme dans le code actuel).

### 9.8 Chemins sûrs et jamais de suppression (réponse à la faille n°7)
**Le problème actuel** (`engine/disk.go`, `ensureDirectory`) : si l'émetteur envoie le chemin `a/b.txt` alors que `a` est un *fichier* existant chez le receveur, le code supprime ce fichier. Un pair malveillant peut effacer des fichiers du dossier de réception.

| Id | Exigence | Raison |
|---|---|---|
| `STO-09` | Le receveur NE DOIT JAMAIS supprimer un fichier qu'il n'a pas créé *dans ce transfert*. Seuls les `.part` et `.state` du transfert courant sont supprimables, après vérification de leur clé de reprise. | Supprime la classe entière de faille. |
| `STO-10` | Chaque transfert multi-éléments est reçu dans **un dossier racine neuf** (`<destination>/<racine>`), dont le nom est résolu *une fois* par la politique de collision (renommer, ignorer ou écraser). À l'intérieur, rien n'est fusionné avec l'existant. | Les conflits ne se produisent qu'au niveau racine. |
| `STO-11` | Si un composant de chemin existe déjà avec le mauvais type (fichier au lieu de dossier), l'élément échoue avec l'erreur `PATH_CONFLICT` ; rien n'est supprimé. | — |
| `STO-12` | Tout chemin reçu passe par `SafeRelPath` (ci-dessous) **avant** toute opération disque. | — |
| `STO-13` | Les opérations sur fichiers du receveur DOIVENT passer par `os.Root` (Go ≥ 1.24) ouvert sur le dossier de destination, qui refuse `..` et les liens symboliques sortants sans fenêtre TOCTOU. Selon la version de Go, `Root` peut ne pas offrir `MkdirAll` : créer les dossiers composant par composant. | `go.mod` indique Go 1.22 : monter la version. |
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
| `STO-04` | Commit groupé : 64 Mio ou 2 s (paramètres). | 📏 |
| `STO-05` | Enregistrements du journal protégés par CRC ; queue déchirée ignorée. | ✅ |
| `STO-06` | À la reprise, vérification des 8 derniers chunks ; mode complet disponible. | ✅ |
| `STO-07` | Disque plein = pause propre, pas d'échec ni de suppression. | ✅ |
| `STO-08` | Mémoire du récepteur bornée : `N × 2 × chunk_size` (supprime la table `pending` non bornée du code actuel). | ✅ |

---

## 10. Couche G — Surface de contrôle (réponse à la faille n°5)

### 10.1 Le problème
Le daemon actuel écoute sur `ws://127.0.0.1:19999/ws` avec `CheckOrigin: true`. Les navigateurs **n'appliquent pas la politique de même origine aux WebSockets** : une page `https://site-quelconque.com` peut exécuter `new WebSocket("ws://127.0.0.1:19999/ws")` et envoyer les actions `share_web_files`, `set_config`, `send`. C'est le détournement de WebSocket inter-sites (CSWSH). Le contrôle « la requête vient de loopback » ne protège pas : la requête vient bien du navigateur de l'utilisateur, donc de loopback.

### 10.2 La solution structurelle : plus de socket de contrôle
| Id | Exigence | Statut |
|---|---|---|
| `API-01` | Le cœur est une **bibliothèque Go** sans socket d'écoute de contrôle. La frontière avec l'interface est de type *passage de messages*, réduite à 4 fonctions : `Start(configJSON)`, `Send(requestJSON)`, `SetEventHandler(callback)`, `Stop()`. | ✅ |
| `API-02` | Le vocabulaire est celui qui existe déjà (`RequestMessage{id, action, payload}` / `EventMessage{event, data}`), seul le transport change (appel de fonction au lieu de WebSocket). Aucun pointeur n'est partagé : les octets sont copiés, ce qui évite tout conflit entre ramasse-miettes de Dart et de Go. | ✅ |
| `API-03` | Le CLI `xfer` (Termux, serveur sans écran) appelle le même cœur dans son propre processus. | ✅ |
| `API-04` | Si une interface loopback HTTP/WebSocket est conservée (mode sans écran, tableau de bord web, développement), elle est **désactivée par défaut** et DOIT appliquer les sept contrôles ci-dessous. Le tableau de bord web suit en plus les règles `DEV-19`. | ✅ |
| `API-05` | Plateforme de la frontière : bureau en `c-shared` + `dart:ffi` ; mobile par `gomobile` ou `c-shared`. | ❓ gates G2a (Windows, Linux, Android) puis G2b (macOS, iOS) : prototype avant de figer |
| `API-06` | Le schéma des messages est versionné et testé (fichiers « golden »). | ✅ |

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

1. L'hôte crée une **session invité** : `session_id`, `token` (128 bits, `crypto/rand`), expiration (15 min par défaut).
2. Le QR contient `https://<ip>:<port>/s#t=<token>`. Le **fragment** `#…` n'est jamais envoyé au serveur, ni dans les journaux ni dans l'en-tête Referer. Le JavaScript de la page le lit et l'envoie ensuite dans `Authorization: Bearer <token>` à chaque appel.
3. **Repli sans caméra :** l'invité saisit un PIN à 6 chiffres sur la page d'accueil ; `POST /api/unlock{pin}` renvoie le jeton. Le PIN est limité à **5 essais par session** (pas par IP) ; au-delà la session est invalidée et l'hôte est alerté. Comparaison en temps constant, délai de 500 ms par échec (déjà dans le code).
4. **HTTPS par défaut**, avec un certificat auto-signé *dédié à la session* (jamais la clé d'identité de l'appareil). L'invité voit un avertissement navigateur : c'est inévitable sans autorité de certification publique, et l'écran de l'hôte l'explique. Un mode HTTP existe seulement via une option explicite « réseau de confiance » avec avertissement visible.

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
| `WEB-01` | Le listener Web Share est distinct du contrôle (§10), lié à l'interface réseau choisie, et inexistant tant que le module est désactivé. | ✅ |
| `WEB-02` | Jeton de 128 bits via `crypto/rand`, transmis par fragment d'URL puis en-tête `Authorization`. Aucun cookie d'authentification. | ✅ |
| `WEB-03` | PIN de repli : `crypto/rand`, 5 essais par session, comparaison en temps constant. | ✅ |
| `WEB-04` | Ni le PIN, ni le jeton, ni un PIN tenté n'apparaissent dans les journaux, les événements ou l'URL. | ✅ |
| `WEB-05` | En-têtes sur toutes les réponses : `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'`, `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`, `Cache-Control: no-store` sur l'API. Cela impose de sortir le JavaScript et le CSS de la page en fichiers servis via `embed.FS`, au lieu du bloc inline actuel. | ✅ |
| `WEB-06` | L'hôte approuve chaque dépôt d'invité (déjà fait) ; quotas : taille maximale par session, nombre de fichiers, espace disque libre ; un seul transfert actif. | ✅ |
| `WEB-07` | Les téléchargements se désignent par **identifiant d'élément**, pas par nom de fichier fourni (le code actuel rapproche par `RelPath` ou `Base()`, ambigu). Les noms déposés passent par `SafeRelPath` puis ne gardent que le nom de base. | ✅ |
| `WEB-08` | `ReadHeaderTimeout`, `MaxHeaderBytes`, `http.MaxBytesReader` sur toutes les routes. | ✅ |
| `WEB-09` | Épinglage de l'IP de l'invité : optionnel, désactivé par défaut (une IP change et un NAT en partage une). | ✅ |

### 11.4 Pourquoi pas de chiffrement WebCrypto de bout en bout
`window.crypto.subtle` n'existe que dans un *contexte sécurisé* (HTTPS ou `localhost`) : il est absent sur `http://192.168.x.x`. Et en HTTP, un attaquant actif peut modifier le JavaScript qui implémente le chiffrement. Seul HTTPS résout les deux ; une fois HTTPS en place, WebCrypto n'ajoute rien face à A1 et A2.

---

## 12. Limites de ressources (anti-déni de service)

Valeurs par défaut ; toutes sont des paramètres (règle R4).

| Id | Limite | Défaut |
|---|---|---|
| `LIM-01` | Connexions non authentifiées simultanées | 16 ; au plus 5 nouvelles par minute et par IP |
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
- **Interdit dans tout journal et tout événement :** PIN, jeton, secret de QR, clé privée, clé PAKE, `ekm`, contenu de fichier. Un test d'intégration parcourt les journaux d'une session complète et échoue si l'un de ces secrets y apparaît.
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
| 5 PIN Web Share erronés depuis 5 adresses différentes | `WEB-03` : la session est invalidée |

### 14.2 Tests de fiabilité
- **Chaos :** boucle de 1000 itérations avec arrêt brutal (`kill -9`) à des instants aléatoires, coupures réseau et disque plein ; critère : hash final identique à la source, aucun `.part` orphelin non nettoyable.
- **Journal déchiré :** tronquer le journal à chaque offset possible ; la reprise DOIT réussir.
- **Fichier source modifié** en cours d'envoi : échec propre `SOURCE_CHANGED`.
- **Fuzzing Go natif** de l'analyseur de trames, du manifeste JSON, de `SafeRelPath` et du lecteur de journal.
- Détecteur de courses (`-race`) dans la CI.

### 14.3 Procédure de benchmark (vitesse, 📏)
1. Mesurer le lien : `iperf3 -P 4 -t 30` entre les deux appareils.
2. Générer un fichier de 10 Gio de données aléatoires incompressibles sur un stockage non limitant (ou noter que le stockage plafonne).
3. Transférer avec TLS actif, 4 flux, chunk de 4 Mio.
4. Résultat = débit utile / débit iperf3. Cible ≥ 80 %. Consigner CPU et température.
5. Répéter sur la matrice : PC↔PC Ethernet, PC↔Android Wi-Fi 5 GHz, Android↔Android, et un téléphone ancien sans accélération AES (valide `CHAN-08`).
6. Mesurer aussi 100 000 fichiers de 4 Kio (valide la décision sur `StreamTar`).
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
| **G1** | Quelle bibliothèque PAKE ? | (a) SPAKE2 (RFC 9382) avec une implémentation Go à identifier ; (b) CPace via `filippo.io/cpace`, marquée expérimentale ; (c) `schollz/pake` (utilisée par `croc`). | Vecteurs de test de la spécification ; constantes publiques fixées sans logarithme connu ; maintenance ; compilation sans CGO sur mobile ; revue externe possible. | Phase 2b seulement |
| **G2a** | Quelle frontière cœur/UI sur Windows, Linux et Android ? | `c-shared` + `dart:ffi`, ou `gomobile` sur Android. | Prototype : démarrer le cœur, envoyer une requête, recevoir un événement. Réalisable sans Mac. | Phase 4a |
| **G2b** | Même question sur macOS et iOS. | `c-shared` / `c-archive` ou `gomobile`. | Même prototype ; exige macOS et Xcode (voir G8). Le risque est le plus élevé sur iOS. | Phase 4b |
| **G8** | Comment obtenir un environnement macOS ? | Mac d'occasion, Mac loué à distance, exécuteurs macOS d'intégration continue (compilation seulement). | Un vrai iPhone branché à un Mac est nécessaire pour valider Bonjour, la permission « réseau local » et le comportement en arrière-plan. | Phase 4b |
| **G3** | TCP+TLS atteint-il 80 % d'iperf3 ? | Oui / non. | Benchmark §14.3. | Décision QUIC (phase 6) |
| **G4** | Revue cryptographique externe | Qui, quand. | Avant la version 1.0, couvrant chapitres 6 et 7. | Version 1.0 |
| **G5** | HTTPS auto-signé du Web Share | Acceptable pour l'utilisateur ? | Test utilisateur sur iOS Safari et Chrome Android. | Phase 5 |
| **G6** | SHA-256 suffisant ? | SHA-256 / BLAKE3. | Benchmark de hachage sur téléphones cibles ≥ 1,5× le débit du lien. | Phase 3 |
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
| `DEV-03` | Pour chaque chunk, le receveur mesure : attente réseau (de `CHUNK_REQ` à la réception), hash, écriture, et `fsync` (par commit). L'émetteur mesure : lecture disque, hash, écriture réseau. On conserve par étape le temps cumulé, le nombre d'octets, les percentiles p50 et p95. | ✅ |
| `DEV-04` | **Étape limitante :** celle dont le temps cumulé par octet, ramené au parallélisme de l'étape, est le plus élevé. Cette heuristique est à valider par des scénarios synthétiques : disque factice lent, réseau bridé par `faultconn`, hash artificiellement ralenti. Le classement DOIT retrouver l'étape imposée dans 100 % des scénarios, sinon l'heuristique est corrigée. | 📏 |
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

`bench matrix` balaie flux ∈ {1, 2, 4, 8} et chunk ∈ {1, 2, 4, 8} Mio, et propose comme valeurs par défaut la combinaison dont le débit est à moins de 5 % du meilleur avec la plus faible mémoire (`N × 2 × chunk`). Les résultats sont enregistrés dans `bench-results/*.json` pour comparer les appareils.

### 17.5 Injection de fautes
| Id | Exigence | Statut |
|---|---|---|
| `DEV-10` | `faultconn` enveloppe une `net.Conn` avec un plan déterministe (graine fixe) : latence, gigue, débit maximal, coupure après N octets, remise à zéro de la connexion. `faultfs` enveloppe le stockage : `ENOSPC` après N octets, écriture déchirée (la moitié écrite puis erreur), `fsync` qui ment. Ces paquets portent l'étiquette `diag`. Un test de CI vérifie qu'ils sont absents du binaire de production. | ✅ |

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
| `DEV-11` | **Paquet de diagnostic** (`xfer bundle`, ou bouton dans l'interface) : archive contenant les derniers rapports, un tampon circulaire de journaux (2 Mio, secrets masqués), la version et l'appareil, la configuration sans secrets ni chemins personnels. Jamais de clés, de PIN, de jetons, d'empreintes complètes ni de noms de fichiers. Le test du chapitre 13 s'applique. | ✅ |
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
- CPace : toujours un brouillon CFRG/IRTF (révision 21, avril 2026), pas encore une RFC ; `filippo.io/cpace` est marqué expérimental et basé sur un brouillon antérieur. [Brouillon](https://datatracker.ietf.org/doc/html/draft-irtf-cfrg-cpace), [module Go](https://pkg.go.dev/filippo.io/cpace?tab=doc).
- `schollz/pake` (croc) : une faille corrigée dans la version 9 de croc — points publics non fixés, donc choisis par un attaquant. [Récit de l'auteur](https://schollz.com/blog/croc9), [module](https://pkg.go.dev/github.com/schollz/pake).
- Go 1.24 : `os.Root` / `os.OpenRoot` pour un accès aux fichiers résistant à la traversée. [Notes de version](https://go.dev/doc/go1.24), [article du blog Go](https://golang.google.cn/blog/osroot).

**Non vérifié (recherche non concluante) :**
- BBR dans quic-go upstream : les implémentations trouvées sont des forks. [Exemple](https://github.com/ao-space/gt/issues/1).
- Support d'un PSK externe dans `crypto/tls` : voir le [ticket Go 6379](https://golang.org/issue/6379).
- **Ces deux points ne portent pas la décision de reporter QUIC** : elle repose sur l'absence de gain démontré et sur le coût, et sera tranchée par le benchmark G3.

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
