# medXfer — Plan détaillé du jalon J3 (confiance)

> **Source :** feuille de route §3 « J3 » ; bible : chapitres 4 (identité), 6.1 et 6.4 (appairage QR), 7 (canal sécurisé), 12 (`LIM-01` à `LIM-03`) et 14.1 (tests de sécurité).
> **Objectif :** plus aucun octet en clair, plus aucune connexion sans identité vérifiée, appairage par QR.
> **Durée estimée :** 10 à 14 jours. Une PR par lot, CI verte sur les trois systèmes avant fusion (règle du `main` vert).

## 0. État de départ (constaté sur `main` après J2 et G2a)

| Constat | Conséquence pour J3 |
|---|---|
| `session/tls.go` crée un certificat P-256 **en mémoire, régénéré à chaque démarrage**. | WP-3.1 : une identité persistante. Aujourd'hui, aucune confiance durable n'est possible. |
| `ClientTLSConfig` met `InsecureSkipVerify: true` **sans aucune vérification** de remplacement. | WP-3.2 : `VerifyConnection` à épinglage (bible §7.1). |
| `UpgradeToTLSIfClientHello` lit le premier octet et accepte le **texte clair** sinon. Utilisé par le daemon (port 18887, flux tar) et par `xfer node`. | WP-3.2 : TLS dès le premier octet, suppression de la fonction (`CHAN-01`). |
| `DialTLSPeer` retombe sur `DialPeer` (TCP en clair) si le handshake échoue. | WP-3.2 : aucun repli (`CHAN-05`). |
| Les données (`engine.Sender` / `Receiver`, ports 18888+N) circulent en **TCP en clair**, et le port accepte n'importe qui. | WP-3.4 : TLS mutuel épinglé sur les connexions de données. |
| La seule « authentification » est le code `XXX-YYY` : 6 chiffres dont un octet d'IP, soit environ 10 bits utiles. Il est vérifié dans `pair_request` (daemon et `xfer node`) ; avec `xfer send` / `xfer recv`, il sert seulement à trouver l'émetteur. | WP-3.5 : appairage QR (secret de 128 bits) ; WP-3.6 : le code sort du chemin d'authentification. |
| Il n'y a pas de liste d'appareils de confiance. La configuration est dans `GetConfigFilePath()` : `MEDXFER_CONFIG_DIR`, ou `<UserConfigDir>/medxfer`. | WP-3.1 : `identity.key`, `identity.crt` et `trusted.json` à côté de `config.json`. |
| `github.com/skip2/go-qrcode` est déjà une dépendance ; `discovery/qr.go` affiche un QR dans le terminal. | WP-3.5 réutilise ces deux pièces. |
| Issues rattachées à J3 : #19 (le daemon affiche l'IP d'une interface débranchée). | → WP-3.5 : l'adresse mise dans l'URI QR doit être celle d'une interface active. |
| Le prototype G2a a montré que le cœur sera une bibliothèque en J6a. | Le code de J3 vit dans `pkg/`, sans dépendre de `pkg/api`, pour être repris tel quel par la bibliothèque. |

## 1. Ordre d'exécution et dépendances

```
WP-3.1 identité ──► WP-3.2 canal sécurisé ──┬──► WP-3.3 confiance provisoire (CLI)
                                            ├──► WP-3.4 données en TLS épinglé ──► WP-3.7 tests de sécurité
                                            ├──► WP-3.5 appairage QR
                                            └──► WP-3.6 code XXX-YYY retiré, limites
WP-3.8 veille G1 (document seulement, en parallèle)
```

Ordre proposé : **3.1 → 3.2 → 3.6 → 3.3 → 3.4 → 3.5 → 3.7**, avec 3.8 pendant les attentes de CI.

## 2. Les lots

### WP-3.1 — `pkg/identity` (`ID-01` à `ID-06`) · M

**Identité de l'appareil**
- `identity.LoadOrCreate(dir)` lit l'identité, ou la crée au premier lancement :
  - clé ECDSA P-256 dans `identity.key` (PKCS#8 PEM, `0600` sous Unix ; sous Windows, la protection est celle du dossier du profil) ;
  - certificat auto-signé dans `identity.crt` (10 ans de validité ; seule la clé compte) ;
  - écriture atomique : fichier temporaire, `fsync`, renommage, puis `fsync` du dossier sous Unix.
- `Fingerprint` (32 octets) = SHA-256 du `SubjectPublicKeyInfo` DER (`ID-02`).
  - `String()` : base32 complet, sans remplissage ;
  - `Short()` : les 16 premiers caractères en 4 groupes, `ABCD-EFGH-IJKL-MNOP` (80 bits), pour la comparaison à l'œil (`ID-03`).
- Le certificat est renouvelable sans changer l'empreinte : même clé, nouveau certificat.

**Liste de confiance**
- `identity.TrustStore` sur `trusted.json`. Chaque entrée contient l'empreinte, l'alias, `added_at`, `last_address` et `auto_accept`.
- Écriture atomique (`ID-04`). Un fichier absent donne une liste vide ; un temporaire laissé par une coupure est ignoré.
- `Forget(fp)` (`ID-06`).

**Commandes**
- `xfer id` : affiche sa propre empreinte, courte et complète.
- `xfer trust list` et `xfer trust forget <empreinte>`.

**Tests**
- Deux chargements successifs donnent la même empreinte.
- Un nouveau certificat sur la même clé garde l'empreinte.
- Droits `0600` (Unix).
- Une écriture coupée (fichier temporaire seul) laisse l'ancienne liste intacte.
- `Forget` retire l'entrée.

### WP-3.2 — `pkg/securechan` (`CHAN-01` à `CHAN-05`, `CHAN-07`) · L

**Configuration TLS** (bible §7.1)
- TLS 1.3 minimum.
- Authentification mutuelle : `RequireAnyClientCert`.
- `InsecureSkipVerify` reste à `true`, mais **compensé** par `VerifyConnection` :
  - empreinte attendue (`Expected`, cas du QR et des reconnexions) ;
  - sinon, empreinte présente dans la liste de confiance ;
  - sinon, inconnue acceptée seulement si l'appelant l'autorise (`AllowUnknown`, connexion en état `UNAUTH`).
- ALPN `medxfer/1` obligatoire : vérifié après le handshake, refus sinon.
- `SessionTicketsDisabled: true`.

**Connexions**
- `securechan.Listen(addr, cfg)` fait le handshake dans `Accept`, avec un délai de 5 s (`LIM-02`). Il rend une connexion qui expose `PeerFingerprint()` et son état, `UNAUTH` ou `AUTH` (`CHAN-04`).
- `securechan.Dial(ctx, addr, cfg)` : délai de connexion de 5 s, keep-alive de 15 s (`CHAN-07`). C'est la même famille de défaut que #48.
- En état `UNAUTH`, seuls les messages `pair_*` passent, dans la limite de 4 Kio lus et 30 s (`CHAN-04`, `LIM-03`). Toute autre trame ferme la connexion.

**Suppressions** : `UpgradeToTLSIfClientHello`, le repli de `DialTLSPeer` et le certificat éphémère. Les appelants passent par `securechan` :
- le daemon : port 18887 et flux tar ;
- `xfer node`.

**Tests**
- Un client TLS 1.2 est refusé.
- Un client sans ALPN est refusé.
- Un client en clair voit son handshake échouer ; le serveur ne lit jamais de protocole en clair.
- Un serveur qui coupe le handshake : le client renvoie une erreur et ne réessaie jamais en clair.
- Une empreinte attendue différente est refusée (`ErrFingerprintMismatch`).
- En `UNAUTH`, une trame non `pair_*`, ou plus de 4 Kio, ferme la connexion.

### WP-3.6 — Code `XXX-YYY` retiré du chemin d'authentification, limites (`LIM-01` à `LIM-03`) · M
- `pair_request` ne vérifie plus le code : l'accès dépend de l'empreinte (liste de confiance ou appairage QR).
- Le code peut rester un moyen de **trouver** l'émetteur sur le réseau (découverte), en attendant mDNS (J6a). Il ne donne plus aucune confiance.
- Limites des connexions non authentifiées, toutes réglables (règle R4) :
  - 16 en même temps ;
  - au plus 5 nouvelles par minute et par IP ;
  - handshake de 5 s ;
  - 4 Kio lus en état `UNAUTH`.
- **Tests :**
  - la 17ᵉ connexion non authentifiée est refusée ;
  - la 6ᵉ dans la minute depuis une même IP est refusée ;
  - un handshake au goutte-à-goutte est coupé à 5 s.

### WP-3.3 — Confiance provisoire en ligne de commande (`ID-05`) · S
- À la première connexion à une empreinte inconnue (`xfer node`, et `xfer recv` sans URI), la commande affiche :
  - l'empreinte courte du pair ;
  - sa propre empreinte courte ;
  - puis demande « Faire confiance à cet appareil ? [o/N] ». Défaut : non.
- « Se souvenir » l'ajoute à `trusted.json`, sinon la confiance ne dure que la session.
- Si l'adresse d'un appareil de confiance (`last_address`) présente une autre empreinte : refus et alerte (`ID-05`).
- Sans terminal interactif : refus de toute empreinte inconnue. Jamais d'acceptation silencieuse.
- Ce chemin reste disponible, mais sort du parcours normal quand WP-3.5 arrive.
- **Tests** (entrée simulée) :
  - « o » accepte ;
  - « n » et l'entrée vide refusent ;
  - sans terminal, refus ;
  - une empreinte changée est refusée avec le message d'alerte.

### WP-3.4 — Données en TLS mutuel épinglé, ports par transfert conservés (objectif de `CHAN-06` sans `DATA_JOIN`) · L
- `engine` reste indépendant de `securechan` : `Sender` reçoit une fonction d'écoute et `Receiver` une fonction de connexion, toutes deux injectées. Par défaut, ce sont celles de `securechan`, avec l'empreinte du pair du transfert.
- Le listener de données **refuse toute connexion dont l'empreinte n'est pas celle du pair de ce transfert**.
- Même chose pour le flux tar (`StreamTar` / `ExtractTar`).
- `xfer send` / `xfer recv` (envoi ponctuel) :
  - `xfer send` affiche une URI `medxfer://v1/pair?...` et son QR (WP-3.5) ;
  - `xfer recv <uri>` épingle l'empreinte de l'émetteur ;
  - sans URI, c'est la confiance provisoire de WP-3.3 qui s'applique.
- `pkg/testkit` : `StartPair` crée deux identités dans des dossiers temporaires et les déclare de confiance l'une pour l'autre. Les tests existants gardent leur forme.
- **Tests :**
  - transfert complet en TLS, hash identique ;
  - une connexion de données venant d'une autre identité est refusée ;
  - une connexion de données en clair est refusée ;
  - le contrôle de fuites de G2a est repris : goroutines revenues à leur niveau après 20 transferts.
- **Mesure à consigner dans la PR** : débit en boucle locale avec et sans TLS (`xfer bench`), pour préparer la gate G3. Pas de seuil en J3.

### WP-3.5 — Appairage QR, chemin P1 (§6.1, `PAIR-08`) · M
- URI : `medxfer://v1/pair?fp=<base32>&a=<ip>&p=<port>&s=<secret base64url>&exp=<unix>`.
  - Le secret fait 128 bits, tiré avec `crypto/rand`, à usage unique, et vit 120 s.
  - L'adresse est celle d'une interface **active** (#19).
- **`xfer pair --show`** affiche l'URI et le QR dans le terminal, puis attend 120 s.
- **`xfer pair <uri>`** :
  - se connecte en épinglant `fp` ;
  - envoie `pair_secret` dans le canal déjà authentifié ;
  - l'autre côté compare en temps constant ;
  - chacun enregistre l'empreinte de l'autre.
- `--once` : confiance limitée à la session, rien n'est écrit (`PAIR-08`).
- Un échec ne révèle rien : ni l'alias, ni s'il reste des essais (`PAIR-07`).
- Daemon et tableau de bord : actions `pair_show` (renvoie l'URI) et `pair_uri`, sans travail d'interface. Le daemon disparaît en J6a.
- **Tests :**
  - secret réutilisé : refusé ;
  - URI expirée : refusée ;
  - mauvais secret : refusé, avec un message neutre ;
  - empreinte différente de `fp` : refusée ;
  - succès : les deux listes de confiance contiennent l'autre ;
  - `--once` : rien n'est écrit sur le disque.

### WP-3.7 — Tests de sécurité (§14.1, partie J3) · M
- Repris des lots précédents et regroupés dans `pkg/securechan/security_test.go` et `cmd/xfer` :
  - sonde TLS 1.2 ou sans ALPN ;
  - handshake coupé, sans repli en clair ;
  - proxy MITM avec son propre certificat : refus ;
  - connexion de données non autorisée ;
  - changement d'empreinte.
- **« Capture réseau » automatisée** :
  - un transfert complet passe par un relais TCP qui enregistre tous les octets ;
  - le fichier contient un marqueur aléatoire de 64 octets répété ;
  - le test vérifie que **ni le marqueur, ni le nom du fichier n'apparaissent** dans les octets capturés.
  - C'est la version testable du critère de sortie.
- **Vérification manuelle**, une fois : un vrai transfert téléphone ↔ PC capturé avec Wireshark ou `tcpdump`, résultat consigné dans la PR.

### WP-3.8 — Veille G1 : choix du PAKE (sans code de production) · 2 à 3 jours cumulés
- `docs/medxfer-g1-pake.md` compare SPAKE2 (RFC 9382), CPace et `schollz/pake` d'après les critères de la gate :
  - vecteurs de test ;
  - constantes publiques sans logarithme connu ;
  - maintenance ;
  - compilation sans CGO sur mobile ;
  - revue externe possible.
- Il se termine par une **décision recommandée** pour J5 (PIN). Les sources sont citées (règle de preuve, bible §0.4).

## 3. Critère de sortie de J3
1. Une capture d'un transfert complet ne montre aucun contenu lisible : test automatisé de WP-3.7, plus une capture réelle consignée.
2. Les tests MITM, de repli interdit et de connexion de données non autorisée passent sur la CI des trois systèmes.
3. Deux appareils réels (PC et téléphone sous Termux) s'appairent par QR (`xfer pair --show` / `xfer pair <uri>`), puis transfèrent un fichier. Le hash est identique.
4. `UpgradeToTLSIfClientHello` et le repli de `DialTLSPeer` n'existent plus. Aucun chemin du code ne peut faire passer des données en clair.
5. `docs/medxfer-g1-pake.md` est écrit, avec sa recommandation.

## 4. Hors périmètre (renvoyé plus loin)
- `DATA_JOIN` et un listener unique (`CHAN-01` complet, `CHAN-06` avec `DATA_JOIN`) → J4a, avec le protocole v1.
- Appairage par PIN (P2, PAKE) → J5, après la gate G1.
- Appareil de confiance (P3, §6.3) et interface d'appairage → J6b.
- mDNS → J6a (le code `XXX-YYY` peut servir à la découverte jusque-là).
- Le Web Share (chapitre 11) reste hors de ce jalon.

## 5. Décisions à valider avec ce plan
| # | Proposition | Raison |
|---|---|---|
| D1 | Identité dans `identity.key` (PKCS#8 PEM) et `identity.crt`, à côté de `config.json`. Sous Windows, pas de code d'ACL : la protection est celle du dossier du profil. | C'est ce que prévoit `ID-01`. Rien à ajouter sous Windows tant que le dossier reste celui de l'utilisateur. |
| D2 | Empreinte courte de 16 caractères base32, en 4 groupes (80 bits). | Assez pour une comparaison à l'œil et courte à lire à voix haute. L'empreinte complète reste dans `trusted.json`. |
| D3 | Envoi ponctuel (`xfer send` / `xfer recv`) : l'émetteur affiche une URI et son QR ; sans URI, la confiance provisoire demande confirmation des deux côtés. Le code `XXX-YYY` sert seulement à trouver l'émetteur. | Plus aucun envoi sans identité vérifiée, sans attendre le PIN (J5). |
| D4 | Le daemon et le tableau de bord suivent le même canal sécurisé, sans travail d'interface. | Les tests (`testkit`, `xfer ctl`) passent par le daemon, mais il disparaît en J6a. |
| D5 | `engine` reçoit ses fonctions d'écoute et de connexion par injection. Il n'importe pas `securechan`. | Garde les couches séparées (bible §3.2) et laisse `faultconn` s'insérer comme aujourd'hui. |
