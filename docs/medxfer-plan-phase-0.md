# medXfer — Plan détaillé de la phase 0 (« Correctifs sans changement de protocole »)

**Référence :** [medxfer-bible-technique-v8.md](medxfer-bible-technique-v8.md), §15.
**Base de code analysée :** dépôt `Medboy224/medXfer`, commit `4aa3b8b` (« License set to apache 2.0 »). Les numéros de ligne ci-dessous viennent de ce commit ; ils se décaleront quand les premières tâches seront fusionnées, d'où la désignation par nom de fonction.
**Estimation :** indicative, pour une personne qui connaît le code : environ 5,5 jours-personne au total (voir §10).

---

## 0. Résumé

La phase 0 ferme quatre trous du code actuel **sans modifier le protocole réseau ni le format des messages** (donc sans casser la compatibilité entre les deux côtés d'un transfert) :

| Tâche | Faille | Exigences de la bible | Ce que ça change |
|---|---|---|---|
| **A — Verrouiller l'API de contrôle locale** | 5 + **nouvelle faille 9** (`/status` public) | `API-04` (contrôles 3 à 7) | Jeton aléatoire, contrôle de `Origin` et de `Host` sur `/ws` et les routes `/api/*` du tableau de bord ; `/status` n'est plus lisible depuis le réseau |
| **B — Secrets du Web Share** | 6 | `WEB-02` (partiel), `WEB-03`, `WEB-04` | PIN aléatoire, plus de secret dans les journaux ni dans les événements, comparaisons en temps constant |
| **C — Chemins sûrs, plus de suppression** | 7 | `STO-09`, `STO-11`, `STO-12` | `SafeRelPath`, fin de la suppression de fichiers par un pair |
| **D — Durabilité et mémoire du récepteur** | 8 (partiel) | `STO-03`, `STO-08`, `LIM-05/06` (partiel) | `fsync` avant d'écrire la progression, fenêtre de réception bornée, métadonnées validées |

### 0.1 Constat nouveau découvert en préparant ce plan
**Faille 9 — `/status` est lisible par tout le réseau sans authentification.** `handleHTTPStatus` (`pkg/api/server.go:572`) n'a aucun contrôle, et le serveur écoute sur `0.0.0.0`. La réponse contient `web_share_pin`, `web_share_token`, `pairing_code` et `portal_url` (qui embarque le PIN et le jeton). Un appareil du LAN qui fait `GET http://<ip>:19999/status` obtient donc tout ce qu'il faut pour entrer dans le Web Share (quand il est activé) et pour s'appairer au nœud sur le port 18887. C'est la correction la plus urgente : voir **PR 0**.

### 0.2 Ce que la phase 0 NE fait PAS (reporté, avec la phase prévue)
| Sujet | Reporté à | Pourquoi |
|---|---|---|
| Séparer le listener de contrôle (127.0.0.1) du listener Web Share (LAN) | Phase 5 (`WEB-01`) | Tous les tests d'`api_test.go` utilisent un seul port pour `/ws` et `/share` ; la séparation est un refactoring à part. En attendant, les routes de contrôle sont protégées par jeton, `Host`, `Origin` et vérification loopback. |
| Fragment d'URL, jeton porteur sans cookie, HTTPS, CSP, 5 essais par session | Phase 5 | Redessine le portail (JavaScript inline à externaliser). |
| Écrasement sans collision par `upload_chunk` (`handleShareUploadChunk` ouvre `O_TRUNC` sur un fichier existant) | Phase 5 (`WEB-07`) | Demande de mémoriser le nom résolu par ticket. Risque documenté ci-dessous (§8). |
| Chiffrement des données, repli en clair, appairage | Phases 1 et 2 | Changent le protocole. |
| Fichier `.part`, journal, `os.Root`, dossier racine par transfert, collisions de casse | Phase 3 | Changent le stockage de bout en bout. |
| Disque plein → pause propre (`STO-07`) | Phase 3 | Phase 0 : l'erreur est propagée, les fichiers partiels sont conservés. |
| Bouton « PIN 4 chiffres » du tableau de bord | Phase 5 | Signalé comme risque, non modifié (voir §8). |

---

## 1. Décisions à prendre avant de commencer

Les quatre décisions ont été **validées**.

| Id | Question | Décision | Conséquence |
|---|---|---|---|
| **D0-1** | `SafeRelPath` doit-il rejeter les caractères interdits **sous Windows** (`: * ? " < > \|`, noms réservés, point/espace final) sur **toutes** les plateformes ? | ✅ **Non : règles Windows appliquées seulement quand le receveur tourne sous Windows** ; règles universelles (pas de `..`, pas de chemin absolu, pas de NUL, pas de composant vide) partout. | Évite de casser Linux↔Android pour un nom comme `rapport: final.txt`. Bible §9.8 alignée. |
| **D0-2** | Comment un client obtient-il le jeton de contrôle ? | ✅ Variable d'environnement `MEDXFER_CONTROL_TOKEN`, **ou** fichier `control.token` (droits `0600`) dans le dossier de configuration. **Précision :** l'interface Flutter n'est pas commencée ; les seuls clients actuels sont le tableau de bord web (qui reçoit le jeton injecté dans sa page) et les scripts. Aucune adaptation Flutter n'est à prévoir dans la phase 0. À terme, la phase 4 remplace le WebSocket par un appel en-processus : Flutter n'aura jamais besoin de ce jeton. | La tâche A ne dépend plus d'aucun travail Flutter. |
| **D0-3** | Passer `go.mod` de Go 1.22 à 1.24 ? | ✅ **Oui**, dès le PR 1, avec la directive `go 1.24` (minimum requis pour `os.Root`). Termux fournit Go 1.26.4, Go 1.27 est installé sur le PC : les deux dépassent 1.24. | Permet `os.Root` en phase 3. On évite d'exiger plus que nécessaire. |
| **D0-4** | Garder `/health` accessible sans jeton ? | ✅ Oui, mais limité au loopback et au contrôle `Host`. | Un lanceur peut vérifier que le daemon est vivant sans connaître le jeton. |

---

## 2. Pré-requis : état de référence (étape P0-0)

À faire dans **votre** copie de travail du dépôt (la copie que j'ai lue est un clone temporaire, rien n'a été modifié dans votre dépôt).

1. Créer la branche `phase-0` depuis `main`.
2. Fermer l'application medXfer si elle tourne : les tests ouvrent des ports fixes (18887, 19998).
3. Lancer :
   ```bash
   go build ./...
   go vet ./...
   go test -skip TestHotspotWebSocketCommands ./pkg/...
   ```
4. **Ne pas lancer `TestHotspotWebSocketCommands` sur votre PC** : sous Windows il démarre un vrai réseau Wi-Fi Direct via PowerShell. Il doit être marqué à part (étiquette `hardware`, voir PR 1).
5. Attendez-vous à : une fenêtre du pare-feu Windows (le binaire de test écoute sur `0.0.0.0`), et des paquets UDP de découverte émis sur votre réseau pendant les tests.
6. `go test -race` exige CGO donc un compilateur C sous Windows. Si indisponible, l'exécuter sous Linux ou WSL.
7. Enregistrer les résultats (réussites, échecs, durée) dans `phase0-baseline.txt`. **Tout échec présent à ce stade est noté comme préexistant** et n'est pas attribué à la phase 0.

---

## 3. Ordre des PR

Les quatre tâches sont indépendantes (règle R5) ; elles se fusionnent séparément. Ordre recommandé, du plus urgent au plus risqué en volume de tests à migrer :

| PR | Contenu | Taille |
|---|---|---|
| **PR 0 — correctif immédiat** | Fermer `/status` (voir A0). 10 lignes. | S |
| **PR 1 — préparation** | `go.mod` → 1.24 ; étiquette `hardware` sur le test de hotspot ; fichier `phase0-baseline.txt`. | S |
| **PR 2 — tâche C** | `SafeRelPath` + fin des suppressions. Pas d'impact sur l'API. | M |
| **PR 3 — tâche D** | Durabilité, fenêtre, validation. Touche `engine/` seulement. | L |
| **PR 4 — tâche B** | Secrets du Web Share. | S |
| **PR 5 — tâche A (reste)** | Jeton, `Origin`, `Host`, tableau de bord, migration des tests. | L |

---

## 4. Tâche A — Verrouiller l'API de contrôle locale

### A.1 Le problème
- `upgrader.CheckOrigin` renvoie `true` (`pkg/api/server.go:32`). Une page web quelconque peut ouvrir `ws://127.0.0.1:19999/ws` : les navigateurs n'appliquent pas la politique de même origine aux WebSockets.
- Le contrôle `isLoopbackRequest` (`server.go:528`) ne protège pas de cela : la requête vient bien du navigateur de l'utilisateur, donc de loopback.
- `/status` n'a aucun contrôle (faille 9, §0.1).

### A.2 Conception
Une seule fonction `control(...)` enveloppe les routes de contrôle. Contrôles appliqués dans cet ordre :

1. L'adresse distante est loopback (existant : `isLoopbackRequest`).
2. L'en-tête `Host` est `127.0.0.1:<port>`, `localhost:<port>` ou `[::1]:<port>` avec le port du listener (contre la réassociation DNS).
3. Si `Origin` est présent, il doit être `http://` + un des hôtes ci-dessus (les navigateurs en envoient toujours un sur WebSocket ; les clients natifs n'en envoient pas).
4. Le jeton de contrôle (256 bits) est présenté, comparé en temps constant.

**Routes concernées :** `/ws`, `/status`, `/api/browse`, `/api/upload`, `/api/fs/list`, `/api/fs/mkdir`.
**Cas particuliers :** `/` (tableau de bord) exige loopback + `Host` mais **pas** le jeton (c'est elle qui le reçoit) ; `/health` exige loopback + `Host` (décision D0-4).
**Non concernées :** `/share` et `/api/share/*` (portail invité, protégé par son propre PIN/jeton).

### A.3 Fichiers et changements

| Fichier | Changement |
|---|---|
| `pkg/api/server.go`, struct `DaemonServer` | Nouveau champ `controlToken string`. |
| `pkg/api/server.go`, `NewDaemonServer` | Générer le jeton : variable `MEDXFER_CONTROL_TOKEN` si elle fait au moins 32 caractères, sinon 32 octets de `crypto/rand` en hexadécimal (en traitant l'erreur de `rand.Read`, que le code actuel ignore). Ne jamais journaliser la valeur. |
| `pkg/api/server.go`, nouvelles méthodes | `ControlToken() string`, `control(next http.HandlerFunc) http.HandlerFunc`, `hostAllowed(h string) bool`, `originAllowed(o string) bool`, `extractToken(r *http.Request) string`. |
| `pkg/api/server.go`, `Serve` (lignes 462–478) | Envelopper les routes de contrôle avec `s.control(...)`. Garder `/` avec un contrôle réduit. |
| `pkg/api/server.go`, `upgrader` (ligne 31) | Remplacer `CheckOrigin` par `func(r) bool { o := r.Header.Get("Origin"); return o == "" \|\| s.originAllowed(o) }` (la variable globale devient un champ du serveur ou une fonction fermée) ; ajouter `Subprotocols: []string{"medxfer.v1"}`. |
| `pkg/api/server.go`, `handleIndex` (ligne 543) | Remplacer le marqueur `__MEDXFER_CONTROL_TOKEN__` du HTML par le jeton ; ajouter `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, garder `Cache-Control: no-store`. |
| `pkg/api/web.go`, `IndexHTML` | Ajouter `const CONTROL_TOKEN = "__MEDXFER_CONTROL_TOKEN__";`. Ligne 640 : `new WebSocket(url, ["medxfer.v1", "token." + CONTROL_TOKEN])`. Lignes 2039 et 2118 : `fetch(url, { headers: { "Authorization": "Bearer " + CONTROL_TOKEN } })`. Ligne 1646 : après `xhr.open(...)`, `xhr.setRequestHeader("Authorization", "Bearer " + CONTROL_TOKEN)`. |
| `cmd/xfer/main.go`, `handleDaemon` | Écrire `control.token` (droits `0600`) dans le dossier de configuration (`filepath.Dir(api.GetConfigFilePath())`), afficher son **chemin** (jamais la valeur), afficher `http://127.0.0.1:<port>/`. |
| `cmd/xfer/main.go`, `handleShare` | Ne pas enregistrer les routes de contrôle (option `srv.DisableControlSurface()`) : le mode `share` n'a besoin que du portail. |

**Pourquoi le jeton passe par un sous-protocole :** un navigateur ne permet pas d'ajouter un en-tête `Authorization` à un WebSocket. Les clients natifs (Dart, Go) peuvent utiliser l'en-tête ; le serveur accepte les deux formes.

### A.4 Code de référence
```go
// Contrôle commun à toutes les routes de contrôle.
func (s *DaemonServer) control(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if !isLoopbackRequest(r) || !s.hostAllowed(r.Host) {
            http.Error(w, "forbidden", http.StatusForbidden)
            return
        }
        if o := r.Header.Get("Origin"); o != "" && !s.originAllowed(o) {
            http.Error(w, "forbidden", http.StatusForbidden)
            return
        }
        if !secureEq(extractToken(r), s.controlToken) {
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }
        next(w, r)
    }
}

func (s *DaemonServer) hostAllowed(h string) bool {
    host, port, err := net.SplitHostPort(h) // « [::1]:19999 » donne « ::1 »
    if err != nil || port != strconv.Itoa(s.httpPort) {
        return false
    }
    return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func (s *DaemonServer) originAllowed(o string) bool {
    u, err := url.Parse(o)
    return err == nil && u.Scheme == "http" && s.hostAllowed(u.Host)
}

func extractToken(r *http.Request) string {
    if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
        return strings.TrimPrefix(a, "Bearer ")
    }
    for _, p := range websocket.Subprotocols(r) {
        if strings.HasPrefix(p, "token.") {
            return strings.TrimPrefix(p, "token.")
        }
    }
    return ""
}

// Commun aux tâches A et B.
func secureEq(a, b string) bool {
    return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
```

### A.5 PR 0 — correctif immédiat de `/status`
Avant toute la tâche A : dans `Serve`, remplacer `mux.HandleFunc("/status", s.handleHTTPStatus)` par une version qui exige loopback, et ajouter dans `handleHTTPStatus` un `if !isLoopbackRequest(r) { 403 }`. Ajouter le test `TestStatusRefusedFromRemote` (adresse distante `10.0.0.5:1234` → 403, corps sans `web_share_pin`). Les tests existants (`/status` depuis `127.0.0.1`) restent verts.

### A.6 Tests
**À ajouter** (`pkg/api/control_guard_test.go`) :

| Test | Attendu |
|---|---|
| `/ws`, `/status`, `/api/fs/list`, `/api/fs/mkdir`, `/api/upload`, `/api/browse` sans jeton | 401 |
| Mêmes routes avec un mauvais jeton | 401 |
| `Origin: https://evil.example` + bon jeton sur `/ws` | 403 (défense en profondeur) |
| `Host: evil.example` (simule la réassociation DNS) | 403 |
| Adresse distante non loopback + bon jeton | 403 |
| Bon jeton en en-tête `Authorization`, puis par sous-protocole `token.<hex>` | 200 / connexion établie |
| Le corps de `/status` pour une requête refusée ne contient ni `web_share_pin` ni `web_share_token` ni `pairing_code` | vrai |
| La constante `IndexHTML` ne contient pas de jeton réel, seulement le marqueur ; le HTML servi par `/` contient le jeton réel | vrai |
| Le HTML servi avec un `Host` invalide ne contient pas le jeton | vrai |
| Mode `DisableControlSurface` : `/ws` renvoie 404 | vrai |

**À migrer** : 23 appels `websocket.DefaultDialer.Dial(...)` et 5 appels HTTP vers `/health`, `/status`, `/`, `/api/fs/list`, `/api/fs/mkdir` dans `api_test.go`. Créer `pkg/api/testhelpers_test.go` :
```go
func dialWS(t *testing.T, s *DaemonServer, port int) *websocket.Conn   // ajoute l'en-tête Authorization
func controlGet(t *testing.T, s *DaemonServer, port int, path string) *http.Response
```
puis remplacer mécaniquement. Les tests qui appellent un gestionnaire directement avec `httptest` (par exemple `TestRemoteClientAccessRestriction`) ne changent pas, puisque l'enveloppe est posée dans `Serve`. Ces aides seront remplacées par `testkit.NewPair` en phase 0b.

### A.7 Critères de sortie
- Tous les tests de `pkg/api` verts après migration, plus les nouveaux.
- Vérification manuelle depuis un **autre appareil du LAN** (remplacer `<ip>`) :
  ```bash
  curl -i http://<ip>:19999/status        # attendu : 403
  curl -i http://<ip>:19999/api/fs/list   # attendu : 403
  curl -i http://<ip>:19999/share         # attendu : 200 ou 403 (désactivé), jamais de PIN dans la réponse
  ```
- Vérification manuelle depuis la machine, avec une page de test hébergée ailleurs (`new WebSocket("ws://127.0.0.1:19999/ws")`) : la connexion est refusée.

### A.8 Compatibilité et risques
- **Aucun client Flutter n'existe encore** (D0-2) : la tâche A ne casse donc aucune intégration. Les scripts ou outils qui appelaient `/ws` ou `/status` doivent lire le jeton (variable d'environnement ou fichier).
- Le serveur écoute toujours sur `0.0.0.0` (le Web Share en a besoin). La protection repose donc sur les quatre contrôles, pas sur l'adresse d'écoute. C'est un écart assumé avec le §10.3 contrôle 1 de la bible, corrigé en phase 5.
- Risque de régression : le port `Host` est comparé à `s.httpPort`. Si le listener est lié à un autre port (repli de `Listen`), `httpPort` doit être mis à jour avant le démarrage du service, ce qui est déjà le cas dans `Listen` et `Serve`.

---

## 5. Tâche B — Secrets du Web Share

### B.1 Les problèmes (code actuel)
| Constat | Où |
|---|---|
| PIN dérivé de l'horloge : `time.Now().UnixNano()%900000` | `server.go:123`, `244`, `246` |
| `rand.Read` sans vérifier l'erreur (jeton nul si la lecture échoue) | `server.go:125`, `249` |
| PIN tenté écrit dans les journaux | `server.go:209`, `272` |
| PIN actuel et 6 caractères du jeton écrits dans les journaux à chaque rotation | `server.go:253` |
| PIN tenté diffusé dans l'événement `web_share_auth_failed` (`attempted_pin`) | `server.go:275` |
| Comparaisons `==` sur PIN et jetons | `share.go:38, 43, 48, 55, 58, 61` |
| Chaque URL de téléchargement renvoyée par `/api/share/list` embarque `&token=…&pin=…` | `share.go:237–260` |
| Noms de fichier non encodés dans l'URL (`&`, `#`, `%` cassent le lien) | `share.go:250, 260` |
| Aucun en-tête de sécurité sur le portail ; l'URL contient le jeton, donc l'en-tête Referer le divulgue | `share.go`, `handleSharePortal` |

### B.2 Changements

| Fichier | Changement |
|---|---|
| `pkg/api/server.go` | Ajouter `randomPIN(digits int) string` et `randomHex(n int) string` (voir code). `NewDaemonServer` et `regeneratePINInternal` les utilisent ; toute erreur de `crypto/rand` est traitée (arrêt du démarrage, ou rotation annulée avec journalisation de l'erreur, sans secret). |
| `pkg/api/server.go:209` | `log.Printf("[Security] Web Share failed attempt #%d from %s", rec.FailCount, clientIP)`. |
| `pkg/api/server.go:253` | `log.Printf("[Security] Web Share credentials rotated")`. |
| `pkg/api/server.go:272–277` | Supprimer le PIN du journal ; supprimer la clé `attempted_pin` de l'événement. Le tableau de bord n'utilise que `client_ip` et `timestamp` (vérifié dans `showSecurityAlert`). |
| `pkg/api/server.go`, `RecordAuthFailure` et `notifyAuthFailure` | Supprimer le paramètre `attemptedPIN`. Mettre à jour les appelants dans `share.go` (`verifyWebShareAccess`, `validatePIN`). |
| `pkg/api/share.go`, `checkAuth` | Remplacer les six comparaisons par `secureEq`. |
| `pkg/api/share.go`, `handleShareList` | Supprimer `authSuffix` : le portail ajoute déjà jeton et PIN dans `startOrResumeDownload`. Encoder les noms avec `url.QueryEscape`. |
| `pkg/api/share.go`, nouvelle fonction `shareHeaders(next)` | Poser `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY` sur toutes les routes `/share` et `/api/share/*`, et `Cache-Control: no-store` sur `/share` et `/api/share/list`. Enregistrer dans `Serve`. |

Le **CSP**, les cookies `HttpOnly`/`Secure`, le jeton porteur et les 5 essais par session sont reportés en phase 5 : le portail actuel lit les cookies en JavaScript et contient du JavaScript inline.

### B.3 Code de référence
```go
func randomHex(n int) (string, error) {
    b := make([]byte, n)
    if _, err := rand.Read(b); err != nil {
        return "", err
    }
    return hex.EncodeToString(b), nil
}

// Uniforme dans [0, 10^digits), sans biais modulo.
func randomPIN(digits int) (string, error) {
    max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
    n, err := rand.Int(rand.Reader, max)
    if err != nil {
        return "", err
    }
    return fmt.Sprintf("%0*d", digits, n), nil
}
```

### B.4 Tests
**À modifier :** `TestWebShareInvalidPINNotification` vérifie aujourd'hui `AttemptedPIN == "9999"` ; il doit vérifier que l'événement **ne contient pas** de champ `attempted_pin` et que le `client_ip` reste présent.

**À ajouter :**
| Test | Attendu |
|---|---|
| `TestRandomPIN` | 6 caractères numériques ; 10 000 tirages ne produisent pas une suite constante ni croissante ; tous les chiffres 0–9 apparaissent en première position (sans biais grossier) |
| `TestNoSecretsInLogs` | Redirige `log` vers un tampon, provoque 6 échecs de PIN + une rotation, vérifie que ni le PIN tenté, ni le PIN courant, ni le jeton n'apparaissent |
| `TestShareListNoCredentialsInURLs` | Les URL de `/api/share/list` ne contiennent ni `pin=` ni `token=` |
| `TestShareListEncodesNames` | Un fichier `a&b#c.txt` est téléchargeable via l'URL renvoyée |
| `TestShareHeaders` | Les trois en-têtes sont présents sur `/share` et sur `/api/share/list` ; `Cache-Control: no-store` aussi |
| Tests de lockout existants | Inchangés, restent verts |

### B.5 Critères de sortie
Tests verts ; `grep` des journaux d'un test complet ne trouve aucun PIN ni jeton ; portail toujours fonctionnel à la main (téléchargement, dépôt avec ticket).

---

## 6. Tâche C — Chemins sûrs et fin des suppressions

### C.1 Le problème (précis)
`ensureDirectory` (`pkg/engine/disk.go:90–119`) parcourt tous les composants du dossier cible, **depuis la racine du disque**, et supprime (`os.Remove`, lignes 112–113) tout fichier ordinaire qui se trouve là où un dossier est nécessaire, ainsi que son `.medxfer`. Scénario d'attaque : le dossier de réception contient le fichier `Contrat.pdf`. Un pair malveillant propose l'élément `Contrat.pdf/x.txt`. `ResolveCollision` regarde seulement le chemin final (`Contrat.pdf/x.txt`, introuvable donc libre), puis `CreateAndPreallocate` appelle `ensureDirectory`, qui **supprime `Contrat.pdf`**. Cela fonctionne avec la politique par défaut (renommage automatique).

Autres constats :
- `PeekResumeOffset` (`disk.go:47`) construit un chemin à partir du nom fourni par le pair, sans validation ; appelé par `cmd/xfer/main.go:350, 731`, `api/handler.go:675`, `collision.go:100`.
- `ExtractTar` (`tar_stream.go:211`) rejette tout nom commençant par `..` (donc aussi `..foo`, légitime) et n'applique pas les règles Windows.
- La normalisation de `PullWithMetadata` (`receiver.go:265–271`) neutralise silencieusement les `..` au lieu de signaler l'erreur ; le comportement est sûr mais masque des offres suspectes.
- `CreateAndPreallocate` supprime `finalPath` et `statePath` quand l'état ne correspond pas (`disk.go:173–174`). **Analyse :** ce chemin n'est atteint par `PullWithMetadata` que si `ResolveCollision` l'a choisi, et il ne choisit un nom ayant un `.medxfer` non concordant que pour la politique « écraser » (choix explicite de l'utilisateur). On le conserve, avec un test qui le prouve.

### C.2 Changements

| Fichier | Changement |
|---|---|
| **Nouveau** `pkg/engine/safepath.go` | `SafeRelPath(p string) (string, error)` et `ErrBadPath`, `ErrPathConflict`. Règles universelles : non vide, ≤ 1024 octets, pas de NUL, `\` converti en `/`, pas de chemin absolu, aucun composant vide / `.` / `..`, composant ≤ 255 octets, pas de caractère de contrôle. Règles Windows (appliquées si `runtime.GOOS == "windows"` selon D0-1) : pas de `: * ? " < > \|`, pas de point ni d'espace final, pas de noms réservés (`CON`, `PRN`, `AUX`, `NUL`, `COM1`–`COM9`, `LPT1`–`LPT9`, avec ou sans extension). Un composant comme `a..b.txt` ou `mon fichier.pdf` est valide. |
| **Nouveau** `pkg/engine/safepath_test.go` | Corpus ci-dessous. |
| `pkg/engine/disk.go`, `ensureDirectory` | Remplacer par `ensureSubdirs(root, relDir string) error` : parcourt **seulement** les composants sous `root` ; si un composant existe et n'est pas un dossier, renvoie `ErrPathConflict` (rien n'est supprimé) ; un lien symbolique n'est accepté que s'il pointe vers un dossier contenu dans `root` (`EvalSymlinks` des deux côtés). Crée les dossiers manquants avec `os.Mkdir`. |
| `pkg/engine/disk.go`, `CreateAndPreallocate` | Valider `fileName` avec `SafeRelPath` ; appeler `ensureSubdirs(outputDir, filepath.Dir(normPath))` ; ajouter un commentaire expliquant pourquoi les deux `os.Remove` des lignes 173–174 sont sûrs (le nom a été choisi par `ResolveCollision`). |
| `pkg/engine/disk.go`, `PeekResumeOffset` | Valider avec `SafeRelPath` ; en cas d'erreur, renvoyer `0, nil` (comme pour un fichier absent). |
| `pkg/engine/collision.go`, `ResolveCollision` | Valider `fileName` en tête de fonction ; erreur renvoyée à l'appelant. |
| `pkg/engine/receiver.go`, `PullWithMetadata` (lignes 265–271) | Remplacer la normalisation par `SafeRelPath` ; en cas d'erreur : `listener.OnError(err)` et `return err`. **Changement de comportement :** une offre piégée échoue au lieu d'être réécrite en silence. |
| `pkg/engine/tar_stream.go`, `ExtractTar` (ligne 211) | Utiliser `SafeRelPath(header.Name)` ; supprimer le test `HasPrefix(cleanRel, "..")`. Conserver le contrôle de préfixe de `targetPath` en défense en profondeur. |

### C.3 Code de référence
```go
func ensureSubdirs(root, relDir string) error {
    if relDir == "" || relDir == "." {
        return nil
    }
    realRoot, err := filepath.EvalSymlinks(root)
    if err != nil {
        return err
    }
    cur := root
    for _, part := range strings.Split(filepath.ToSlash(relDir), "/") {
        cur = filepath.Join(cur, part)
        fi, err := os.Lstat(cur)
        switch {
        case err == nil && fi.Mode()&os.ModeSymlink != 0:
            target, e := filepath.EvalSymlinks(cur)
            if e != nil || !within(realRoot, target) {
                return fmt.Errorf("%w: %s", ErrPathConflict, part)
            }
            if ti, e := os.Stat(target); e != nil || !ti.IsDir() {
                return fmt.Errorf("%w: %s", ErrPathConflict, part)
            }
        case err == nil && !fi.IsDir():
            return fmt.Errorf("%w: %s", ErrPathConflict, part) // plus aucune suppression
        case err == nil: // dossier existant
        case os.IsNotExist(err):
            if e := os.Mkdir(cur, 0o755); e != nil && !os.IsExist(e) {
                return e
            }
        default:
            return err
        }
    }
    return nil
}
```
(`within(root, p)` : `filepath.Rel(root, p)` ne commence pas par `..`.) La fenêtre TOCTOU entre le test et la création est connue ; `os.Root` la supprimera en phase 3 (`STO-13`).

### C.4 Tests
**Corpus de `safepath_test.go`** (tableau, exécuté sur toutes les plateformes ; les cas Windows sont activés par un paramètre explicite pour être testables sous Linux) :

| Valides | Invalides |
|---|---|
| `a.txt`, `dir/sub/f.bin`, `mon fichier (1).pdf`, `a..b.txt`, `.gitignore`, `é/ü.txt`, `backup (1).tar.gz` | ``, `/abs`, `\abs`, `C:\x`, `C:x`, `../x`, `a/../../x`, `a/./b`, `a//b`, `a/b/..`, octet NUL, caractère de contrôle, composant de 256 octets, chemin de 1025 octets |
| | Règles Windows : `CON`, `con.txt`, `NUL.tar.gz`, `x.`, `x `, `a:b`, `a*b`, `a?b`, `a<b` |

**À ajouter dans `pkg/engine` :**
| Test | Attendu |
|---|---|
| `TestEnsureSubdirsNeverDeletes` | Dossier de réception contenant le fichier `Contrat.pdf` ; offre `Contrat.pdf/x.txt` → erreur `ErrPathConflict`, `Contrat.pdf` intact, contenu identique |
| `TestPullRejectsTraversalOffer` | Offre `../x.txt` et `a/../../x.txt` → `Pull` échoue, rien n'est écrit hors du dossier |
| `TestForgedStateFileDoesNotDestroyUserFile` | Fichier utilisateur `victim.docx` + faux `victim.docx.medxfer` forgé ; offre `victim.docx` : politique renommage → `victim (1).docx` créé, original intact ; politique ignorer → original intact |
| `TestPeekResumeOffsetRejectsBadPath` | `../../etc/passwd` → `0, nil` sans lecture hors du dossier |
| `TestExtractTarRejectsReservedNamesOnWindowsMode` | Règles Windows actives → rejet ; `..foo` (nom légitime) → accepté |
| `TestSymlinkOutsideRootRejected` | Lien symbolique `out -> /tmp` dans le dossier de réception ; offre `out/x.txt` → `ErrPathConflict` (test ignoré si la plateforme ne permet pas de créer le lien) |
| `TestSymlinkInsideRootAccepted` | Lien vers un sous-dossier du dossier de réception → accepté |

Les tests existants (`TestPathTraversalProtection`, `TestFolderBatchTransfer`, collision) doivent rester verts sans modification.

### C.5 Critères de sortie
Tests verts ; `grep -n "os.Remove" pkg/engine` ne montre plus que : `disk.go` (état du transfert courant, `Finalize`), `collision.go` (politique « écraser » explicite), `diagnostics.go` (fichier de benchmark).

---

## 7. Tâche D — Durabilité et mémoire du récepteur

### D.1 Les problèmes
1. **La progression peut être en avance sur le disque.** `WriteChunkAt` (`disk.go:224`) marque le chunk terminé en mémoire, puis écrit le bitmap dans `.medxfer` tous les 32 chunks *(test sur l'index, pas sur le nombre d'écritures)* ou toutes les 2 s, **sans jamais appeler `fsync`** ni sur le fichier de données ni sur l'état. Après une coupure de courant, le bitmap peut déclarer « complet » un chunk dont les octets ne sont pas sur le disque : la reprise produit un fichier corrompu sans le savoir.
2. **`Finalize` supprime l'état avant que les données soient durables** (`disk.go:275–301`), et `receiver.go` appelle `listener.OnComplete` avant `Finalize` (lignes ~692–694, 319–321, 338–340) : l'utilisateur voit « terminé » alors que le fichier peut encore être incomplet sur le disque.
3. **La table `pending` du récepteur n'est pas bornée** (`receiver.go:381`). Si un chunk bas est bloqué (reconnexion, jusqu'à 4 tentatives) pendant que les autres travailleurs continuent, tous les chunks suivants s'accumulent en mémoire, chacun dans un tampon de 8 Mio : un gros fichier peut occuper toute la RAM.
4. **Les métadonnées du pair ne sont pas validées** (`PullWithMetadata`, ligne 248) : un `ChunkSize` minuscule avec un `FileSize` énorme fait allouer des dizaines de millions de tâches (`newTaskDispatcher` : `make([]chunkTask, 0, totalChunks)`). Un `FileSize` négatif n'est pas rejeté.
5. **Constat annexe :** côté émetteur, `buf := (*bufPtr)[:s.chunkSize]` (`sender.go:231`) panique si `chunkSize` dépasse la capacité du tampon (8 Mio + 28 octets). `xfer send -chunk 16` plante donc ; une valeur négative devient un `uint32` géant.

### D.2 Changements

#### D1 — Progression durable (invariant J de la bible)
| Fichier | Changement |
|---|---|
| `pkg/engine/disk.go`, struct `DiskManager` | Ajouter `durable []bool` (chunks écrits **et** synchronisés), `pendingDurable []uint32`, `pendingBytes int64`, `lastCommit time.Time`. `completed` reste le drapeau en mémoire utilisé par le récepteur. |
| `pkg/engine/disk.go`, constantes | `commitBytes = 64 << 20`, `commitInterval = 2 * time.Second` (paramètres, règle R4). |
| `pkg/engine/disk.go`, `WriteChunkAt` | Écrire les données, marquer `completed`, ajouter l'index à `pendingDurable`. Si `pendingBytes ≥ commitBytes` ou `time.Since(lastCommit) ≥ commitInterval` : `commitLocked()`. |
| `pkg/engine/disk.go`, nouvelle `commitLocked` | (1) `file.Sync()` ; (2) marquer `durable` les chunks en attente ; (3) écrire le bitmap **de `durable`** dans l'état ; (4) `stateFile.Sync()`. Renvoie l'erreur au lieu de l'ignorer. |
| `pkg/engine/disk.go`, `CreateAndPreallocate` | Au chargement, `durable[i] = completed[i]` pour les chunks présents dans le bitmap. |
| `pkg/engine/disk.go`, `Close` | Appeler `commitLocked` avant de fermer. |

L'ordre données → état est l'**invariant J** (§9.4 de la bible). Il garantit qu'après n'importe quelle coupure, tout chunk déclaré complet dans le bitmap est réellement sur le disque.

#### D2 — Finalisation sûre
| Fichier | Changement |
|---|---|
| `pkg/engine/disk.go`, `Finalize` | `file.Sync()` **avant** de supprimer l'état. Si la synchronisation échoue : conserver l'état (le transfert reste reprenable) et renvoyer l'erreur. Garder la fermeture asynchrone du fichier de données. |
| `pkg/engine/receiver.go` (3 endroits) | Appeler `Finalize` **avant** `listener.OnComplete` ; en cas d'erreur, `listener.OnError`. |

Coût attendu : un `fsync` de plus par fichier (pénalisant pour des milliers de petits fichiers). C'est volontaire (priorité Sécurité > Fiabilité > Vitesse) ; la phase 3 regroupera les synchronisations entre fichiers. À mesurer (D.4).

#### D3 — Fenêtre de réception bornée
| Fichier | Changement |
|---|---|
| `pkg/engine/receiver.go`, `taskDispatcher` | **Remplacer le tableau par un tas-minimum** (`container/heap`) trié par index. `PushFront` devient `Push`. Nouvelle méthode `PopWithin(limit uint64) (task chunkTask, ok bool, blocked bool)` qui renvoie la tâche d'**index minimal** si elle est `< limit`. |
| `pkg/engine/receiver.go`, `PullWithMetadata` | `nextExpected` devient un compteur atomique partagé, mis à jour aux trois endroits où il est incrémenté. Fenêtre `W = max(8, workers × 3)`. Les travailleurs appellent `PopWithin(nextExpected + W)` (aussi pour le préchargement) ; quand `blocked`, ils attendent 2 ms (en testant `workerCtx.Done()`). |

**Piège à éviter (pourquoi un tas et pas « la tâche de tête ») :** avec `PushFront`, deux travailleurs en échec peuvent produire la file `[7, 0, …]`. Si le test de fenêtre ne regarde que la tête (7, hors fenêtre), le chunk 0 situé derrière n'est jamais pris et le transfert se bloque. Le tas garantit que la tâche `nextExpected` est toujours prenable.

**Borne de mémoire obtenue :** tous les chunks détenus à un instant donné (en vol, dans `writerQueue`, dans `pending`) ont un index `< nextExpected + W`. Donc au plus `W` tampons, soit `W × (8 Mio + 28)` : 96 Mio pour 4 travailleurs. (Les tampons du pool font toujours 8 Mio quelle que soit la taille de chunk ; l'ajuster est une amélioration de la phase 3.)

#### D4 — Validation des métadonnées
| Fichier | Changement |
|---|---|
| `pkg/engine/receiver.go`, `PullWithMetadata` | Après la valeur par défaut de `ChunkSize`, appeler `validateMeta(meta)` : `FileSize ≥ 0` ; `64 Kio ≤ ChunkSize ≤ 8 Mio` ; `totalChunks ≤ 1<<21` (2 097 152, soit 128 Gio avec des chunks de 64 Kio). Erreur claire et `listener.OnError`. |
| `pkg/engine/sender.go`, `NewSender` | Borner `chunkSize` dans [64 Kio, 8 Mio] (valeur hors bornes → valeur par défaut 4 Mio, avec un avertissement journalisé). |
| `cmd/xfer/main.go`, `handleSend` | Rejeter `-chunk` ≤ 0 ou > 8 avec un message d'erreur (au lieu d'un plantage). |

### D.3 Tests
**À ajouter dans `pkg/engine` :**

| Test | Attendu |
|---|---|
| `TestDurabilityOrdering` | Un faux fichier (interface minimale `WriteAt/Sync/Close`) enregistre l'ordre des appels. Pour chaque écriture d'état, un `Sync` des données l'a précédée. Une **simulation de coupure** (le faux fichier ne garde que les octets synchronisés) puis rechargement via `CreateAndPreallocate` : tout chunk marqué complet a ses octets corrects. |
| `TestCommitByBytesAndByTime` | Le commit se déclenche à 64 Mio écrits ou après 2 s (horloge injectée). |
| `TestFinalizeSyncFailureKeepsState` | `Sync` en échec → l'état `.medxfer` est conservé, `Finalize` renvoie l'erreur, `OnComplete` n'est pas appelé. |
| `TestWindowBoundsMemory` | Transfert avec un travailleur bloqué volontairement sur le chunk 0 : le nombre maximal de chunks détenus reste ≤ `W` (compteur de tampons empruntés au pool), puis le transfert se termine quand le blocage est levé. |
| `TestDispatcherNoDeadlockAfterRetries` | File `[7, 0]` produite par deux `Push` : `PopWithin` renvoie 0 d'abord. |
| `TestValidateMeta` | `ChunkSize=1` + `FileSize=1<<40` → rejeté sans allocation importante ; `FileSize=-1` rejeté ; valeurs normales acceptées. |
| `TestSenderClampsChunkSize` | `NewSender(4, 16<<20)` ne panique pas au service ; `-chunk -1` rejeté par la CLI. |

Les tests existants de transfert, de reprise et de reconnexion (`transport_test.go`) doivent rester verts. `TestEndToEndTransferResume` utilise un seul travailleur et des chunks de 512 Kio : il valide aussi que la fenêtre ne bloque pas.

### D.4 Mesure de l'impact (informatif)
Avant et après la tâche D, mesurer sur la boucle locale un transfert de 1 Gio (un helper de test ou un petit programme) avec 4 travailleurs et un chunk de 4 Mio, plus 2 000 petits fichiers de 4 Kio. Consigner les durées dans le message de PR. Si le transfert gros fichier est plus de 15 % plus lent ou si les petits fichiers sont plus de 2× plus lents, ajuster `commitBytes` / `commitInterval` avant de fusionner ; ne pas affaiblir l'ordre données → état. Cette mesure sera refaite avec `xfer bench` (phase 0b).

### D.5 Critères de sortie
Tests verts ; plus de `WriteAt` d'état sans `Sync` préalable des données (revue de code) ; mémoire maximale mesurée sur le test de blocage ≤ `W` tampons ; aucune régression de débit au-delà des seuils ci-dessus.

---

## 8. Risques connus conservés après la phase 0

| Risque | Gravité | Traitement prévu |
|---|---|---|
| Le serveur écoute encore sur `0.0.0.0` (le Web Share le nécessite). Une faille dans une route de contrôle serait donc atteignable depuis le LAN. | Moyenne | Séparation des listeners en phase 5. Mitigation : quatre contrôles sur chaque route (A.2). |
| `handleShareUploadChunk` écrase un fichier existant de même nom sans collision (`O_TRUNC` au chunk 0), après approbation de l'hôte qui ne voit que les noms. | Moyenne | Phase 5 (`WEB-07`). |
| Bouton « 4D » : un PIN à 4 chiffres (10 000 valeurs) est offert au tableau de bord ; verrouillage par IP uniquement. | Moyenne | Retrait en phase 5 avec le PIN de repli à 5 essais par session. |
| Les données restent en clair sur le réseau (`engine/sender.go`, `receiver.go`) et le repli en clair existe (`session/tls.go`). | **Élevée** | Phase 1. La phase 0 ne l'améliore pas. |
| Code d'appairage à 900 valeurs, sans limite d'essais. | **Élevée** | Phases 1 et 2. |
| Fenêtre TOCTOU sur les liens symboliques (`ensureSubdirs`). | Faible | `os.Root` en phase 3. |
| Un `fsync` par fichier ralentit les très gros lots de petits fichiers. | Faible | Regroupement inter-fichiers en phase 3. |

---

## 9. Définition de « terminé » pour la phase 0

- [ ] `phase0-baseline.txt` enregistré ; aucune nouvelle régression par rapport à lui.
- [ ] PR 0 à 5 fusionnées, chacune avec ses tests et une description reliant les identifiants d'exigence (`API-04`, `WEB-03`, `STO-09`, `STO-03`, `STO-08`…).
- [ ] `go vet ./...` propre ; `go test -skip TestHotspotWebSocketCommands ./pkg/...` vert ; `go test -race` vert sur Linux ou WSL.
- [ ] Les trois commandes `curl` du §4.A.7 donnent le résultat attendu depuis un autre appareil du LAN.
- [ ] Aucun PIN, jeton ou secret dans les journaux d'une session complète (`TestNoSecretsInLogs`).
- [ ] Le tableau de bord web se connecte avec le jeton injecté, et un script peut lire le jeton par variable d'environnement ou fichier (D0-2).
- [ ] Les risques du §8 sont copiés dans le suivi du projet avec leur phase cible.

## 10. Estimation indicative

| Élément | Jours-personne |
|---|---|
| P0-0 état de référence, PR 0, PR 1 | 1 |
| Tâche C (PR 2) | 1 |
| Tâche D (PR 3) | 2 |
| Tâche B (PR 4) | 0,5 |
| Tâche A (PR 5), dont migration des 23 connexions de test | 1 |
| **Total** | **≈ 5,5** |

Ces chiffres sont des ordres de grandeur, pas des engagements ; la tâche D est la plus incertaine (concurrence dans `receiver.go`).
