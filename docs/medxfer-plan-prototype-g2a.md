# medXfer — Plan du prototype G2a (pont Flutter ↔ cœur Go)

**Référence :** [medxfer-bible-technique-v8.md](medxfer-bible-technique-v8.md), §10 (`API-01` à `API-06`), gate **G2a**.
**Portée :** Windows, Linux, Android. macOS et iOS sont traités par G2b, qui exige un Mac (gate G8) et n'est **pas** dans ce plan.
**Nature :** prototype **jetable**. Il vit sur une branche `spike/g2a` (dossier `spikes/g2a/`), n'est jamais fusionné dans `main` ; son seul livrable est un rapport et des décisions.
**Durée visée :** 5 à 6 jours-personne. **Plafond :** 8 jours (voir §8).

---

## 1. Objectif

Répondre, **mesures à l'appui**, à une seule question : *le cœur Go peut-il être chargé comme bibliothèque dans une application Flutter sur Windows, Linux et Android, avec la frontière à 4 fonctions de la bible, sans instabilité ni perte de performance notable ?*

Si oui, le daemon et son WebSocket peuvent être supprimés (phase 4a). Si non, on connaît le plan B avant d'avoir construit l'interface.

### 1.1 Les huit questions auxquelles le prototype doit répondre
| Id | Question |
|---|---|
| Q1 | Peut-on produire la bibliothèque Go (`c-shared`) de façon reproductible pour Windows, Linux (x86_64) et Android (arm64 et x86_64 émulateur) depuis une machine Windows ? |
| Q2 | Flutter charge-t-il la bibliothèque et échange-t-il des messages JSON de façon fiable sur les trois plateformes ? |
| Q3 | Quel modèle d'événements Go → Dart est le plus sûr : rappel (`NativeCallable.listener`) ou lecture bloquante dans un isolat de fond ? |
| Q4 | Le cycle de vie est-il maîtrisé (redémarrage à chaud de Flutter, arrêt/redémarrage du cœur, mise en arrière-plan sur Android, panique Go) ? |
| Q5 | Le vrai moteur de transfert (`pkg/engine`) fonctionne-t-il tel quel dans cette configuration, à débit comparable à la ligne de commande ? |
| Q6 | Que se passe-t-il côté réseau sur Android (énumération des interfaces, écoute TCP, multicast) ? |
| Q7 | Où et comment le cœur peut-il écrire les fichiers reçus sous Android (stockage cloisonné) ? |
| Q8 | Quel est le coût en taille et en temps de démarrage ? |

---

## 2. Périmètre

**Dans le prototype :**
- Une bibliothèque Go exposant la frontière C minimale (§4).
- Une application Flutter **minimale** (un écran de test avec boutons et un journal), sans design.
- Un petit répartiteur JSON de 4 actions : `ping`, `get_network`, `send_file`, `receive_file`, qui s'appuie sur `pkg/engine` et `pkg/discovery` existants.

**Hors prototype (volontairement) :**
- iOS et macOS (G2b, sans Mac).
- mDNS, appairage, TLS, sécurité (phases 1, 2, 4).
- Refonte des gestionnaires `DaemonServer` (ils prennent un `*websocket.Conn` ; la refonte en interface de réponse est un travail de phase 4, dont le prototype **mesure le coût** sans le faire).
- Service de premier plan Android, notifications, design de l'interface.

---

## 3. Environnement à préparer (étape S0)

| Plateforme | Pré-requis |
|---|---|
| **Windows (poste de développement)** | Go 1.24 ou plus (1.27 installé) ; Flutter stable avec Windows desktop activé ; **Visual Studio** avec la charge de travail « Développement Desktop en C++ » ; un compilateur C pour cgo (MinGW-w64, par exemple via MSYS2 — `gcc` dans le `PATH`) ; `flutter doctor` sans erreur. |
| **Android** | Android Studio, SDK, **NDK** (version à noter, voir ci-dessous) ; un téléphone Android avec le mode développeur et le débogage USB ; un émulateur (image x86_64, API récente). Le projet utilise déjà Termux : indiquer le modèle et la version d'Android du téléphone. |
| **Linux** | WSL2 avec Ubuntu ; dans WSL : Go, Flutter, `clang`, `cmake`, `ninja-build`, `pkg-config`, `libgtk-3-dev` (liste à confirmer avec `flutter doctor`). Garder le projet dans le système de fichiers de WSL, pas dans `/mnt/c`. Vérifier d'abord que l'application compteur par défaut ouvre une fenêtre (`flutter run -d linux`). |
| **Intégration continue** | Dépôt déjà public : exécuteurs GitHub Actions Linux et Windows gratuits. Un exécuteur macOS ne sert qu'à la compilation du cœur (pas de Flutter macOS dans ce plan). |

**À consigner dès S0 dans le rapport :** versions exactes de Go, Flutter, Dart, NDK, Visual Studio, MinGW-w64, Ubuntu, Android. Les notes de version de Go indiquent la version minimale du NDK exigée pour `GOOS=android` ; ne pas la deviner, la lire.

**Condition de départ :** Dart ≥ 3.1 (pour `NativeCallable.listener`). Si `flutter --version` annonce moins, mettre Flutter à jour.

---

## 4. La frontière à tester

La bible fixe 4 fonctions (`Start`, `Send`, `SetEventHandler`, `Stop`) avec des messages JSON. Version C concrète à implémenter :

```c
// Retourne un JSON {"ok":true,...} ou {"ok":false,"error":"..."}. Toujours alloué par la bibliothèque.
char* medxfer_start(const char* config_json);   // idempotent (voir règle B4)
char* medxfer_call(const char* request_json);   // requête {id, action, payload}, réponse JSON
void  medxfer_set_event_callback(void (*cb)(const char* event_json)); // modèle « rappel »
char* medxfer_next_event(int timeout_ms);       // modèle « lecture » ; NULL si délai écoulé
void  medxfer_stop(void);
void  medxfer_free(char* p);                    // libère tout char* renvoyé par la bibliothèque
```

### 4.1 Règles de contrat (à respecter ET à vérifier)
| Id | Règle |
|---|---|
| B1 | **Propriété de la mémoire :** celui qui alloue libère, avec le même allocateur. Un `char*` renvoyé par Go se libère **uniquement** par `medxfer_free`. Dart ne libère jamais un pointeur Go avec son propre `malloc` (le CRT peut différer, notamment sous Windows). Go copie les chaînes reçues (`C.GoString`) et ne garde jamais un pointeur C. |
| B2 | **Aucun pointeur Go ne traverse la frontière** après le retour de l'appel (règle cgo). |
| B3 | **Les messages sont du JSON UTF-8.** Mêmes noms d'actions et d'événements que `api/models.go`, sans lien avec WebSocket. |
| B4 | **`medxfer_start` est idempotent.** Un deuxième appel avec la même configuration renvoie `{"ok":true,"already_started":true}`. Indispensable : le redémarrage à chaud de Flutter relance Dart mais pas le processus, donc le runtime Go reste chargé. |
| B5 | **Toute `goroutine` lancée par le cœur est protégée par un `recover`** qui convertit la panique en événement `core_error`. Une panique dans `medxfer_call` est attrapée à la frontière. |
| B6 | **Les chemins viennent de l'appelant** (`config.data_dir`, `config.download_dir`). Le cœur ne suppose ni `$HOME` ni variable d'environnement (inexistants ou trompeurs sur Android). |
| B7 | **Les événements de progression sont limités** (10 par seconde par transfert, comme `daemonListener`) pour ne pas saturer le fil d'interface. |

### 4.2 Squelette Go (extrait)
```go
package main

/*
#include <stdlib.h>
typedef void (*event_cb)(const char*);
static void call_cb(event_cb cb, const char* s) { cb(s); }
*/
import "C"

import "unsafe"

//export medxfer_call
func medxfer_call(req *C.char) (out *C.char) {
    defer func() {
        if r := recover(); r != nil { // règle B5
            out = C.CString(`{"ok":false,"error":"internal panic"}`)
        }
    }()
    return C.CString(dispatch(C.GoString(req))) // dispatch : ping, get_network, send_file, receive_file
}

//export medxfer_free
func medxfer_free(p *C.char) { C.free(unsafe.Pointer(p)) }

func main() {} // exigé par -buildmode=c-shared
```

### 4.3 Squelette Dart (extrait)
```dart
final lib = DynamicLibrary.open(
    Platform.isWindows ? 'medxfer.dll' : 'libmedxfer.so');
final _call = lib.lookupFunction<Pointer<Utf8> Function(Pointer<Utf8>),
    Pointer<Utf8> Function(Pointer<Utf8>)>('medxfer_call');
final _free = lib.lookupFunction<Void Function(Pointer<Utf8>),
    void Function(Pointer<Utf8>)>('medxfer_free');

String request(String json) {
  final p = json.toNativeUtf8();      // alloué par Dart
  final r = _call(p);                 // alloué par Go
  calloc.free(p);                     // Dart libère ce qu'il a alloué
  final s = r.toDartString();
  _free(r);                           // Go libère ce qu'il a alloué
  return s;
}
```
Modèle « lecture » : un isolat de fond appelle `medxfer_next_event(500)` en boucle (appel bloquant sans gêner l'interface) et poste les événements à l'isolat principal par un `SendPort`.
Modèle « rappel » : `NativeCallable<Void Function(Pointer<Utf8>)>.listener(...)` ; Go appelle le pointeur de fonction depuis son propre fil.

### 4.4 Commandes de compilation (à adapter et à consigner)
```bash
# Windows (depuis le poste de développement, gcc de MinGW-w64 dans le PATH)
CGO_ENABLED=1 go build -buildmode=c-shared -o medxfer.dll ./spikes/g2a/core

# Linux (dans WSL2)
CGO_ENABLED=1 go build -buildmode=c-shared -o libmedxfer.so ./spikes/g2a/core

# Android arm64 (compilateur C du NDK ; chemin à renseigner)
CGO_ENABLED=1 GOOS=android GOARCH=arm64 CC=<ndk>/.../aarch64-linux-android<API>-clang \
  go build -buildmode=c-shared -o android/app/src/main/jniLibs/arm64-v8a/libmedxfer.so ./spikes/g2a/core
# idem GOARCH=amd64 vers x86_64/ pour l'émulateur
```
Les commandes exactes (variables, chemins, fichier `.cmd` du compilateur du NDK sous Windows) sont **à établir pendant S3** et consignées dans un script `build.ps1` / `build.sh`.

---

## 5. Étapes

| Étape | Contenu | Durée visée |
|---|---|---|
| **S0** | Installer et valider les outils (§3). Application compteur Flutter qui tourne sur Windows, Android et Linux. | 0,5 à 1 j |
| **S1** | Windows : bibliothèque « écho » (`ping`), appel depuis Dart, modèles d'événements rappel **et** lecture. Empaquetage `flutter build windows` testé sans MSYS2. | 1 j |
| **S2** | Linux (WSL2) : mêmes tests ; `flutter build linux` exécuté sur une Ubuntu sans Go. | 0,5 j |
| **S3** | Android : compilation `c-shared` avec le NDK, `jniLibs`, permissions, émulateur puis téléphone. | 1 à 1,5 j |
| **S4** | Moteur réel : `send_file` / `receive_file` appuyés sur `engine.Sender` / `Receiver` ; transfert de boucle locale dans l'application ; transfert PC (`xfer send`) → téléphone et téléphone → PC. | 1 j |
| **S5** | Robustesse et mesures : panique, redémarrage à chaud, arrière-plan, fuites, coût du pont, taille, démarrage ; sondes réseau et stockage Android (Q6, Q7). | 1 j |
| **S6** | Rapport, décision, mise à jour de la bible. | 0,5 j |

---

## 6. Critères de réussite

Chaque critère est testé sur **chaque plateforme concernée**. « Bloquant » : s'il échoue sur une plateforme visée, G2a n'est pas fermé pour elle.

### 6.1 Critères bloquants
| Id | Critère | Mesure / test | Plateformes |
|---|---|---|---|
| **G2A-01** | **Compilation reproductible** : depuis un dépôt propre, **une commande** par plateforme produit la bibliothèque ; les pré-requis sont documentés ; le temps de compilation est noté. | Script exécuté sur une machine/clone neuf. | Win, Linux, Android (arm64 + x86_64) |
| **G2A-02** | **Aller-retour fiable** : 1 000 appels `ping` consécutifs, aucune erreur, aucune fuite mémoire visible (mémoire stable à ±5 % après 10 000 appels). Latence médiane d'un aller-retour consignée (seuil d'alerte : 5 ms). | Boucle de test Dart + outil de mesure mémoire de la plateforme. | Win, Linux, Android |
| **G2A-03** | **Événements Go → Dart sans perte** : 10 000 événements numérotés à 100 par seconde, puis une rafale de 1 000 par seconde pendant 60 s. Aucun perdu, aucun hors ordre, aucun blocage de l'interface (pas de gel visible). Le modèle retenu (rappel ou lecture) est celui qui passe sur **toutes** les plateformes ; sinon, le choix est documenté par plateforme. | Compteurs dans l'application. | Win, Linux, Android |
| **G2A-04** | **Panique contenue** : une panique provoquée dans `dispatch` est renvoyée en JSON d'erreur ; une panique dans une goroutine du cœur est convertie en événement `core_error` grâce à la règle B5 ; l'application ne plante pas. | Actions de test `panic_call` et `panic_goroutine`. | Win, Linux, Android |
| **G2A-05** | **Cycle de vie** : `Start` → `Stop` → `Start` fonctionne ; redémarrage à chaud de Flutter 20 fois de suite sans plantage ni double démarrage (règle B4) ; sur Android, application en arrière-plan 5 minutes avec un écouteur TCP actif, puis retour au premier plan : l'état est cohérent ; application tuée puis relancée : pas de processus orphelin. | Scénarios manuels scriptés + journal. | Win, Linux, Android |
| **G2A-06** | **Moteur réel intact** : transfert de boucle locale de 1 Gio dans l'application, hash final identique ; débit ≥ **95 %** de celui de la ligne de commande `xfer` sur la même machine, mesuré sur 3 essais. | Comparaison `xfer send/recv` vs application. | Win, Linux, Android (comparé à Termux si disponible) |
| **G2A-07** | **Transfert entre appareils** : `xfer send` (PC) → application Android, et application Android → `xfer recv` (PC), 1 Gio, hash identique, aucun plantage, sur le Wi-Fi réel. | Vérification SHA-256 des deux côtés. | Win ↔ Android ; Linux ↔ Android si possible |
| **G2A-08** | **Empaquetage sans outils de développement** : la version `flutter build` s'exécute sur une machine **propre** (sans Go, sans MinGW/MSYS2, sans NDK). Aucune DLL ou bibliothèque système manquante. | Dépendances inspectées avec un outil de dépendances (Windows) et `ldd` (Linux) ; essai sur machine ou VM propre. | Win, Linux |

### 6.2 Critères informatifs (le résultat est consigné ; il ne décide pas de G2a mais oriente la phase 4)
| Id | Critère | Ce qu'on consigne |
|---|---|---|
| **G2A-09** | **Réseau sous Android** | Dans l'application, sur chaque version d'Android disponible (émulateur récent + téléphone) : résultats de `net.Interfaces()`, `net.InterfaceAddrs()`, écoute TCP sur `0.0.0.0`, adhésion multicast UDP, en précisant les erreurs. **Le code actuel (`discovery/interfaces.go`) contient déjà des replis pour un blocage de netlink sous Android 11 et plus**, ce qui laisse penser que l'énumération peut échouer ; le prototype le confirme ou l'infirme. Si elle échoue, l'API du cœur doit accepter les informations réseau fournies par la plateforme (nouvelle action `set_network_info`). |
| **G2A-10** | **Stockage sous Android** | Où le cœur peut écrire sans aide (dossier propre à l'application) ; si `/storage/emulated/0/Download` est accessible en direct ; coût de l'approche « recevoir dans le dossier privé puis copier vers le dossier public par `MediaStore` » (durée de la copie de 1 Gio, double écriture). **Cette question touche la phase 3** : le stockage actuel renomme `.part` → nom final dans un dossier choisi, ce qui n'est pas possible avec les API de stockage cloisonné. Le prototype ne la résout pas, il la chiffre. |
| **G2A-11** | **Taille** | Taille de la bibliothèque par ABI (stripée) et supplément de taille de l'APK ; seuil d'alerte : 20 Mo par ABI. |
| **G2A-12** | **Démarrage à froid** | Délai entre le lancement de l'application et la première réponse de `ping` ; seuil d'alerte : 300 ms sur le téléphone. |
| **G2A-13** | **Coût de la refonte des gestionnaires** | Nombre de fichiers, fonctions et lignes à modifier pour remplacer `*websocket.Conn` par une interface de réponse dans `pkg/api/handler.go` et `server.go`. Sert à chiffrer la phase 4a. |
| **G2A-14** | **Android : deux approches** | Seulement si G2A-03, 04, 05 ou 09 échoue en approche A : essai de l'approche B (`gomobile bind` + Kotlin + canal Flutter) sur le même scénario, et comparaison (complexité de compilation, démarrage, événements). |

---

## 7. Arbre de décision

| Résultat | Décision |
|---|---|
| Tous les critères bloquants passent sur Windows, Linux et Android | **G2a fermé.** `c-shared` + `dart:ffi` partout ; la bible §10 est confirmée et complétée par les règles B1 à B7. |
| Passent sur desktop, échec sur Android | Essai de l'approche B (G2A-14). Si B passe : desktop en `c-shared`, Android en `gomobile` ; deux ponts à maintenir. Si B échoue aussi : on réexamine le cœur sur Android (point d'arrêt du projet, à discuter). |
| Échec sur une plateforme de bureau | Sur cette plateforme, **processus séparé** (plan B de la bible, §3.3) avec le jeton de contrôle et le WebSocket durci de la phase 0. Android reste en bibliothèque. |
| Un seul modèle d'événements passe (rappel ou lecture) | On retient celui qui passe partout ; on le documente dans la bible. |
| G2A-06 sous 95 % | Chercher la cause (copies JSON, verrous) ; sans correction possible, le cœur prend le contrôle des transferts et la frontière ne porte que commandes et progression (c'est déjà le cas) : revoir la fréquence des événements. |

---

## 8. Plafond et arrêt

- **Plafond total : 8 jours-personne.** Au-delà, arrêter et rédiger le rapport avec ce qui est établi.
- **Arrêt anticipé :** si S1 (Windows, écho minimal) n'est pas au vert après 2 jours, stopper : le problème est dans l'environnement ou l'approche, pas dans les détails, et il faut en parler avant de continuer.
- **Pas de dérive de périmètre :** aucune fonction de sécurité, de découverte ou d'interface n'est ajoutée au prototype, même « pendant qu'on y est ».

---

## 9. Livrables

1. `spikes/g2a/` : bibliothèque Go, application Flutter minimale, scripts `build.ps1` et `build.sh`, tests.
2. `g2a-report.md` : tableau de résultats par critère et par plateforme (réussite / échec / valeur mesurée), versions d'outils, journaux utiles, captures de mesures.
3. Mise à jour de la bible : confirmation ou amendement de `API-01` à `API-06`, règles B1 à B7 intégrées, fermeture ou réouverture de G2a, nouvelles exigences éventuelles (`set_network_info`, destination du stockage sous Android).
4. Liste des changements à prévoir dans le cœur pour la phase 4a (dont G2A-13).

---

## 10. Ce qui reste ouvert après ce prototype

- **G2b** (macOS et iOS) : exige un Mac ; le risque principal y est iOS.
- Service de premier plan Android et comportement longue durée.
- Intégration de la découverte (mDNS) côté plateforme.
- Décision sur la destination des fichiers reçus sous Android, qui conditionne la conception du stockage en phase 3.
