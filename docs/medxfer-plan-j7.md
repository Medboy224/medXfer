# medXfer — Plan du jalon J7 : Web Share v2

> **Source :** feuille de route, J7 ; bible chapitre 11 (`WEB-01` à `WEB-09`), gate G5 (§15) ; constats de l'audit du 18 octobre sur Web Share (#157), qui sont des critères de sortie.
>
> **Objectif :** un téléphone ou un PC **sans medXfer** envoie et reçoit des fichiers avec son navigateur. L'hôte montre un QR, l'invité le scanne ou tape un PIN, et l'hôte garde la main sur chaque dépôt. Le module reste **désactivé par défaut** (règle R3).
>
> **Critère de sortie :**
> - les neuf exigences `WEB-01` à `WEB-09` tenues, chacune par un test ;
> - les constats de #157, chacun par un test qui échouait avant ;
> - Web Share utilisable **depuis l'application** (Android, Windows, Linux), pas seulement par `xfer share` ;
> - la gate G5 passée : des personnes réelles franchissent l'avertissement de certificat sur Chrome Android, et sur Safari iOS si un iPhone est disponible (D7) ;
> - un envoi et un dépôt réels, du téléphone d'un invité vers le PC et vers le téléphone, avec les débits mesurés.
>
> **Durée estimée :** 8 à 11 jours. La feuille de route prévoyait 5 à 7 jours pour le serveur seul ; l'intégration dans l'application (WP-W.5) s'ajoute, puisqu'elle n'existe pas encore. Une PR par lot, CI verte sur les trois systèmes avant fusion.

## 0. État de départ (constaté sur `main` le 9 novembre 2026)

| Élément | État |
|---|---|
| Où Web Share tourne | ⚠️ Seulement dans `xfer share` (CLI). **L'application ne l'utilise pas** : aucun écran, aucun appel à ses actions. Le cœur en processus (`pkg/core`) ne démarre pas son serveur. |
| Code | `pkg/api/share.go`, 2 459 lignes, dont environ 1 200 de page HTML avec son JavaScript en ligne. 19 tests dans `pkg/api`. |
| Listener (`WEB-01`) | ❌ Le même serveur HTTP que la sonde `/health`, sur le port du démon, toujours ouvert. Désactivé, il répond `403`, mais il existe. |
| Authentification (`WEB-02`, `WEB-03`) | ❌ Jeton et PIN dans la **requête** de l'URL (`/share?token=…&pin=…`), puis dans des cookies non `HttpOnly` (`medxfer_token`, `medxfer_pin`, 24 h). Le portail compare le PIN avec `==` et pose son cookie sans verrouillage (#157). Le verrouillage, ailleurs, est par adresse IP. |
| PIN | ⚠️ 4 chiffres par défaut (`xfer share --pin`, « Custom 4-digit security PIN ») ; la bible en veut 6. |
| Secrets (`WEB-04`) | ⚠️ Masqués dans le journal (`registerSecrets`), mais présents dans l'URL et dans `status.portal_url`. |
| En-têtes (`WEB-05`) | ⚠️ `Referrer-Policy`, `nosniff`, `X-Frame-Options`, `no-store` : oui. **Pas de CSP** : le JavaScript est en ligne, et il insère des données de l'invité par `innerHTML` (XSS, #157). |
| HTTPS | ❌ HTTP seulement. |
| Dépôts (`WEB-06`, `WEB-07`) | ❌ Approbation par l'hôte : oui. Quotas, espace libre : non. Premier morceau ouvert avec `O_TRUNC`, sans gestion de collision ni `SafeRelPath`. Nom, taille et position non comparés au ticket. Ticket supprimé au premier fichier fini (#157). Téléchargement désigné par **nom** (`?file=`), pas par identifiant. |
| Serveur (`WEB-08`) | ⚠️ `ReadHeaderTimeout` de 10 s (#156). Pas de `MaxHeaderBytes`, pas de `MaxBytesReader`. `ipLockouts` et `lastAuthFail` jamais purgés (#157). |
| Bible | ⚠️ `WEB-01`, `WEB-04` à `WEB-07` et `WEB-09` marquées ✅ alors que le code ne les tient pas (lignes ci-dessus). WP-W.0 corrige ces statuts. |

## 1. Ordre d'exécution

```
WP-W.0 décisions, bible ──► WP-W.1 serveur pkg/webshare ──► WP-W.3 dépôts ──► WP-W.4 cœur, API, xfer share ──► WP-W.5 application ──► WP-W.6 G5, essais réels, rapport
                            WP-W.2 portail (HTML, JS, CSS) ──────────────┘
```

WP-W.1 et WP-W.2 avancent ensemble : le portail ne parle qu'à l'API du serveur, décrite en WP-W.1.

## 2. Les lots

### WP-W.0 — Décisions et bible · S

- Faire trancher les décisions du §4.
- Bible chapitre 11 :
  - remettre à 📏 J7 les exigences marquées ✅ à tort ;
  - écrire l'API du portail (routes, réponses, erreurs) et le modèle de session ;
  - ajouter `WEB-10`, la durée de vie de la session.

### WP-W.1 — Le serveur, `pkg/webshare` · L

Nouveau paquet, séparé de `DaemonServer` (une implémentation derrière une interface, comme les autres modules). `pkg/api/share.go` disparaît en WP-W.4.

- **Listener propre** (`WEB-01`), lié à l'adresse de l'interface choisie et ouvert seulement pendant une session. Port fixe par défaut (D5), un autre s'il est pris.
- **Certificat TLS par session** : ECDSA P-256, auto-signé, tiré à chaque session, jamais la clé d'identité de l'appareil. Uniquement `crypto/x509` et `crypto/tls` de Go, sans dépendance de plus. Son empreinte courte est donnée à l'hôte.
- **Session invité** (`WEB-10`) :
  - jeton de 128 bits, `crypto/rand`, et expiration (15 min par défaut, prolongée tant qu'un transfert tourne) ;
  - PIN de 6 chiffres, uniforme (`randomPIN`, §11.2) ;
  - une session à la fois.
- **Authentification** (`WEB-02`, `WEB-03`) :
  - `Authorization: Bearer <jeton>` sur chaque appel ; aucun cookie, aucun secret dans l'URL ;
  - `POST /api/unlock {pin}` rend le jeton ;
  - 5 essais par **session**, pas par IP ; au-delà, la session est invalidée et l'hôte est alerté ;
  - comparaison en temps constant, 500 ms par échec.
- **En-têtes** (`WEB-05`) : la CSP exacte de la bible sur toutes les réponses, avec `Referrer-Policy: no-referrer`, `nosniff` et `no-store` sur l'API.
- **Limites** (`WEB-08`) : `ReadHeaderTimeout`, `MaxHeaderBytes` et `MaxBytesReader` sur chaque route. Le compteur d'essais vit avec la session et disparaît avec elle.
- **Tests :**
  - un test par exigence, et un par constat de #157, écrit avant la correction ;
  - le jeton et le PIN absents des journaux, des événements et des URL (`WEB-04`), sur le modèle de `TestBundleHoldsNoSecret`.

### WP-W.2 — Le portail · M

- HTML, JavaScript et CSS en fichiers séparés, servis par `embed.FS` (`WEB-05`). Aucun script en ligne, aucun `innerHTML` de données : `textContent` et `createElement`.
- **Parcours de l'invité :**
  - le jeton lu dans le fragment (`/s#t=…`), puis le fragment effacé de la barre d'adresse (`history.replaceState`) ;
  - sans jeton, l'écran du PIN ;
  - ensuite, deux onglets : « Recevoir » (les fichiers de l'hôte) et « Envoyer » (choisir, attendre l'accord de l'hôte, la barre de progression).
- Le téléchargement d'un dossier se fait en zip à la volée, comme aujourd'hui.
- Textes en français et en anglais, selon la langue du navigateur.
- Le portail tient sur un écran de téléphone. L'identité visuelle est celle de medXfer.
- **Tests :**
  - la CSP sans `unsafe-inline` ;
  - une page qui affiche un nom de fichier piégé (`<img onerror>`) sans l'exécuter, vérifiée côté serveur par le contenu servi et par un test du script.

### WP-W.3 — Les dépôts · M

- **Ticket** : l'invité annonce ses fichiers (nom, taille). L'hôte accepte ou refuse le lot, ou accepte tout seul si l'acceptation automatique est activée (`WEB-06`).
- **Morceaux** : chaque morceau est comparé au ticket (fichier annoncé, position, taille). Le ticket vit jusqu'au dernier fichier du lot (#157, 2b et 2c).
- **Noms** (`WEB-07`) :
  - chaque nom passe par `SafeRelPath`, puis on ne garde que le nom de base ;
  - écriture dans un fichier temporaire, renommé à la fin ;
  - jamais `O_TRUNC` sur un fichier existant : en cas de collision, « nom (2).ext », comme pour les transferts (#157, 2a).
- **Quotas** : taille maximale par session, nombre de fichiers, espace disque libre vérifié avant l'accord, un seul transfert actif.
- **Téléchargements** désignés par l'identifiant de l'élément, jamais par un nom (`WEB-07`).

### WP-W.4 — Le cœur, l'API et `xfer share` · M

- **Actions** :
  - `web_share_start {paths, receive_dir, auto_accept}` répond par l'adresse (`https://<ip>:<port>/s`), le jeton (pour le QR), le PIN, l'empreinte du certificat et l'expiration ;
  - `web_share_stop` ;
  - `web_share_answer {ticket, accept}`.
- **Événements** : `web_share_upload_request`, la progression, la fin, la session invalidée, l'expiration.
- Le jeton et le PIN ne vont qu'à l'interface de l'hôte, jamais dans un journal (`WEB-04`).
- **Retrait de l'ancien code** : les actions `share_web_files`, `toggle_web_share`, `regenerate_share_pin` et `set_web_share_auto_accept`, `pkg/api/share.go`, les champs `web_share_*` de `status` et la route `/share`. Le schéma passe en v5 (`pkg/core/testdata`).
- **`xfer share`** passe par `pkg/webshare` :
  - le PIN à 6 chiffres, et l'option `--pin` à 4 chiffres retirée (« retrait du bouton 4 chiffres » de la feuille de route) ;
  - l'URL affichée avec son fragment, et le QR dans le terminal comme aujourd'hui.

### WP-W.5 — L'application · L

- Un écran « Partager avec un navigateur », depuis le menu de l'accueil.
- **Ce que l'hôte voit** :
  - le QR, l'adresse et le PIN, l'expiration ;
  - l'explication de l'avertissement que l'invité verra (« le navigateur dira que la connexion n'est pas privée : c'est normal ici, touchez Paramètres avancés puis Continuer »), avec l'empreinte du certificat ;
  - chaque demande de dépôt (Accepter, Refuser), les transferts en cours, leur fin.
- **Fichiers proposés** : choisis comme pour un envoi (sélecteur, glisser-déposer sur le PC). Les fichiers reçus vont au dossier de réception habituel.
- **Réseau** : celui où l'invité se trouve. Sans réseau commun, le point d'accès de l'appareil, comme pour un QR (DISC-12) ; le QR du portail ne porte pas le réseau, l'invité le rejoint d'abord.
- **Windows** : la règle de pare-feu du port de Web Share, ajoutée comme les autres (`firewall.dart`).
- **Fin de session** : en quittant l'écran ; l'appareil reste éveillé pendant un transfert (#266).
- **Tests** : l'écran avec un faux cœur, comme `connect_test.dart`.

### WP-W.6 — Gate G5, essais réels et rapport · M

- **G5** : deux personnes au moins, qui ne connaissent pas medXfer, franchissent l'avertissement de certificat sans aide, à partir de l'explication de l'écran de l'hôte. On note le temps et les hésitations. Navigateurs : Chrome Android (A57, TECNO), Edge et Firefox sur PC, Safari iOS si un iPhone est disponible (D7).
- **Essais réels** :
  - un fichier de 1 Gio, de l'invité vers le PC et vers le téléphone, et dans l'autre sens ;
  - un dossier téléchargé en zip ;
  - un dépôt de plusieurs fichiers, avec une collision de nom ;
  - le PIN faux 5 fois.
- **Rapport** `docs/j7-report.md`, mise à jour de la feuille de route.

## 3. Hors périmètre

- Chiffrement de bout en bout par WebCrypto (§11.5) : HTTPS suffit face à A1 et A2.
- Plusieurs invités qui transfèrent en même temps : une session, un transfert actif (`WEB-06`).
- Web Share par Internet ou par un relais : réseau local uniquement.
- Le mode HTTP « réseau de confiance » : décidé après G5 (D3).
- Le guide d'utilisation : reporté à la première version.

## 4. Décisions

Toutes les propositions acceptées le 10 novembre 2026.

| # | Question | Décision | Autre option |
|---|---|---|---|
| D1 | Web Share dans l'application | ✅ **Oui, en J7** : sans écran, le module n'existe pour aucun utilisateur. | Le garder en CLI jusqu'à J8 : J7 plus court (5 à 7 j), mais rien de visible. |
| D2 | Réécrire ou corriger `share.go` | ✅ **Réécrire dans `pkg/webshare`**, l'ancien code retiré en WP-W.4 : ses choix (secrets dans l'URL, cookies, JavaScript en ligne) touchent toutes les routes. | Corriger sur place : moins de travail au départ, mais chaque correction tire sur les autres. |
| D3 | Le mode HTTP « réseau de confiance » (§11.2) | ✅ **Pas en J7.** HTTPS seulement ; on décide après G5, selon ce que les invités font de l'avertissement. | L'écrire dès J7, comme le prévoit la bible. |
| D4 | La session invité | ✅ **15 minutes, prolongées tant qu'un transfert tourne, une à la fois, finie quand l'hôte quitte l'écran.** | Sans expiration tant que l'écran est ouvert. |
| D5 | Le port | ✅ **Fixe par défaut (19999, celui de `xfer share`)**, un autre s'il est pris : une seule règle de pare-feu sous Windows. | Un port tiré à chaque session : une règle par programme plutôt que par port. |
| D6 | L'empreinte du certificat | ✅ **Montrée sur l'écran de l'hôte** : un invité prudent peut la comparer à celle du navigateur. Rien ne l'y oblige. | Ne pas la montrer : un écran plus simple. |
| D7 | Safari iOS pour G5 | ✅ **Un iPhone sera disponible pour G5** (10 novembre). | Sans iPhone : Safari iOS en réserve du rapport. |

## 5. Risques

| Risque | Parade |
|---|---|
| L'invité ne franchit pas l'avertissement de certificat | L'écran de l'hôte l'explique pas à pas ; G5 le mesure ; le mode HTTP reste possible (D3). |
| Un gros dépôt par le navigateur (mémoire, mise en veille du téléphone de l'invité) | Envoi par morceaux (`File.slice`), repris au dernier morceau reçu ; la reprise est mesurée en WP-W.6. |
| Le pare-feu du PC hôte bloque le port | La règle ajoutée par l'application (WP-W.5), et le code `unreachable` dit à l'hôte. |
| Une adresse qui change pendant la session (point d'accès recréé) | La session est liée à l'adresse : un nouveau QR, comme pour l'appairage. |
