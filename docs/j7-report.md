# medXfer — Rapport de sortie du jalon J7 (Web Share v2)

**Date :** 15 novembre 2026 · **Plan :** [medxfer-plan-j7.md](medxfer-plan-j7.md) · **Bible :** chapitre 11, `WEB-01` à `WEB-10`, gate G5 (§15) · **PR :** #274 à #281, #286, et celle de ce rapport.

Ce rapport répond au critère de sortie du plan :
- les exigences `WEB-01` à `WEB-09` tenues, chacune par un test (et `WEB-10`, ajoutée en WP-W.0) ;
- les constats de l'audit du 18 octobre (#157), chacun par un test qui échouait avant ;
- Web Share utilisable depuis l'application, pas seulement par `xfer share` ;
- la gate G5 : des personnes réelles franchissent l'avertissement de certificat ;
- des envois et des dépôts réels, avec les débits mesurés.

---

## 1. Verdict

**J7 est terminé. La gate G5 est reportée à la bêta** (décision du 15 novembre, §3), et la décision D3 avec elle. Tout le reste du critère est tenu ; les réserves du §6 ne bloquent pas.

Un téléphone ou un PC **sans medXfer** envoie et reçoit des fichiers avec son navigateur. L'hôte ouvre « Partager avec un navigateur », l'invité scanne le QR ou tape l'adresse et le PIN, et l'hôte accepte chaque dépôt.

| Avant J7 | Après J7 |
|---|---|
| Seulement dans `xfer share` ; l'application ne s'en servait pas | Un écran dans l'application (Android, Windows, Linux), qui garde son état quand on le quitte |
| HTTP, jeton et PIN dans l'URL puis dans des cookies | HTTPS (certificat tiré à chaque session), jeton dans le fragment puis en en-tête, aucun cookie |
| PIN à 4 chiffres, verrouillage par adresse IP | PIN à 6 chiffres, 5 essais par session, puis la session finie et l'hôte prévenu |
| JavaScript en ligne, noms de l'invité insérés par `innerHTML` | Page, script et style en fichiers séparés, CSP stricte, aucun `innerHTML` de données |
| Dépôt ouvert avec `O_TRUNC` : un nom existant était écrasé | Fichier temporaire renommé à la fin ; une collision donne « nom (1).ext » |
| Le serveur sur le port du démon, toujours ouvert | Un listener par session, lié à une seule adresse, **jamais celle des données mobiles** |

---

## 2. Critère de sortie (plan, en-tête)

Essais réels des 10, 11 et 15 novembre : un iPhone (Safari), Galaxy A57 (Android 16), TECNO BF6 (Android 12), PC Windows 11 (Intel AC 8265).

| # | Critère | Résultat | Preuve |
|---|---|---|---|
| 1 | `WEB-01` à `WEB-10`, chacune par un test | ✅ | Bible §11, colonne des statuts ; tests de `pkg/webshare` et `pkg/api` |
| 2 | Les constats de #157, chacun par un test qui échouait avant | ✅ | Les tests écrits en WP-W.1 à WP-W.4 (#276 à #279) ; #157 fermée avec #279 |
| 3 | Web Share depuis l'application | ✅ | WP-W.5 (#280), corrigé après les essais (#281) |
| 4 | Gate G5 | ⏭️ reportée | À la bêta, avec ses testeurs (§3). Essais indicatifs : Chrome Android et Safari iPhone franchissent l'avertissement |
| 5 | Envoi et dépôt réels, téléphone et PC, débits mesurés | ✅ | §5 |

---

## 3. Gate G5 (l'avertissement de certificat)

**Question (bible §15) :** le HTTPS auto-signé est-il acceptable pour l'invité ? Concrètement : une personne qui ne connaît pas medXfer franchit-elle seule l'avertissement du navigateur, à partir de l'explication de l'écran de l'hôte ?

### 3.1 La décision : reportée à la bêta

Le 15 novembre, l'utilisateur a reporté G5 à la bêta, quand l'application s'installera depuis le Play Store et que ses testeurs donneront leurs retours. Ils sont exactement les personnes que G5 cherche : ils ne connaissent pas medXfer, ont leurs propres téléphones et navigateurs, et personne n'est à côté d'eux pour aider.

Rien n'attend G5, sauf la décision D3 (le mode HTTP « réseau de confiance »), reportée avec elle. D'ici là, HTTPS seulement, le choix le plus sûr ; Web Share reste désactivé par défaut. Comme G4, la gate attend sans bloquer la suite.

### 3.2 Les essais indicatifs

Faits par l'utilisateur, qui connaît medXfer : ils montrent que le parcours marche, pas qu'un inconnu le trouve seul. Le temps va du premier refus du certificat, vu dans le journal du serveur, au téléchargement.

| Navigateur | Avertissement franchi | Du premier refus au téléchargement | Remarque |
|---|---|---|---|
| Chrome Android | ✅ | 7 s | — |
| Safari iPhone | ✅ | 47 s | L'écran de l'hôte ne donnait que les mots de Chrome : il a fallu lire pour trouver le chemin de Safari. Corrigé (#286) : une ligne par navigateur |

Le certificat est tiré à chaque partage : un navigateur qui a accepté celui d'un partage revoit l'avertissement au suivant. Un participant peut donc reprendre le même téléphone, à condition de relancer le partage.

### 3.3 Ce qu'on demandera aux testeurs de la bêta

À chaque testeur qui a essayé « Partager avec un navigateur », trois questions :
1. L'invité a-t-il franchi seul l'avertissement « connexion non privée » ?
2. En combien de temps, à peu près ?
3. Quelqu'un a-t-il abandonné, et sur quel navigateur ?

En option (J8) : le serveur de l'hôte voit déjà chaque refus du certificat (ses erreurs de connexion). L'écran pourrait dire « Un invité voit l'avertissement de sécurité » et compter, sur l'appareil seulement et sans rien envoyer, les invités qui le franchissent.

**G5 passe** si les invités des testeurs franchissent l'avertissement seuls sur Chrome Android ; Safari est noté à part. **D3** se décide sur ces retours.

---

## 4. Ce qui a été livré

| PR | Lot | Contenu |
|---|---|---|
| #274 | Plan | Plan J7 |
| #275 | WP-W.0 | Les décisions D1 à D7 ; le chapitre 11 de la bible : statuts corrigés, API du portail, `WEB-10` |
| #276 | WP-W.1 | Le serveur `pkg/webshare` : listener par session, TLS, jeton, PIN, en-têtes, limites |
| #277 | WP-W.3 | Les dépôts : ticket, accord de l'hôte, morceaux vérifiés, collisions, quotas |
| #278 | WP-W.2 | La page de l'invité : fichiers séparés, CSP, français et anglais, liens de téléchargement à clé propre |
| #279 | WP-W.4 | Les actions `web_share_*` dans le cœur, schéma v5 ; l'ancien `share.go` retiré ; `xfer share` |
| #280 | WP-W.5 | L'écran « Partager avec un navigateur » ; la session qui survit à l'écran quitté |
| #281 | WP-W.6 | Les corrections des essais réels (§5.3) |
| #286 | WP-W.6 | L'avertissement du certificat expliqué navigateur par navigateur (§3.2) |
| ce rapport | WP-W.6 | Essais réels, G5 |

---

## 5. Les essais réels (WP-W.6)

### 5.1 Débits

1 Gio, sur le groupe Wi-Fi Direct à 5 GHz de l'A57 (802.11ac, 867 Mbit/s). L'empreinte SHA-256 du fichier reçu est chaque fois celle de l'original.

| Sens | Débit | Pour comparer : medXfer entre les mêmes appareils |
|---|---|---|
| PC hôte → Chrome sur l'A57 | **56 MB/s** (10 nov.), **52,9 MB/s** (11 nov., après #281) | ~52 MB/s |
| Edge sur le PC → A57 hôte | **48,3 MB/s** | ~38–40 MB/s |
| A57 hôte → Edge sur le PC, pendant un envoi medXfer | **46 MB/s** | ~38–40 MB/s |
| Chrome sur l'A57 → PC hôte | **26 MB/s** | ~52 MB/s |

Le navigateur fait aussi bien que medXfer, sauf l'envoi depuis Chrome sur le téléphone. Le même serveur reçoit 48 MB/s depuis Edge sur le PC, avec les mêmes morceaux de 8 Mio envoyés un par un : la limite est donc du côté de Chrome sur le téléphone (chiffrement, lecture du fichier), pas du serveur.

Sur le point d'accès à 2,4 GHz de l'A57 (802.11n, 144 Mbit/s), PC → Chrome sur l'A57 : 10,1 MB/s, la limite du lien.

**Envoi medXfer et partage par navigateur en même temps** (l'A57 hôte) : les deux sont arrivés, le réseau est resté allumé.

### 5.2 Le parcours

| Essai | Résultat |
|---|---|
| Un dossier téléchargé en zip | ✅ Les 3 fichiers et le sous-dossier, intacts |
| Plusieurs fichiers déposés, accord demandé à l'hôte | ✅ Arrivés dans le dossier de réception, intacts |
| Les mêmes fichiers déposés une seconde fois | ✅ « essai-a (1).bin »… : rien d'écrasé |
| 5 PIN faux | ✅ L'invité voit le compte à rebours puis « Le partage est terminé » ; l'hôte : « Le partage s'est arrêté : 5 codes faux ont été tapés. » |
| L'A57 sans Wi-Fi, données mobiles allumées | ✅ Aucune adresse proposée ; l'écran le dit et propose de créer un réseau |
| PC sur le Wi-Fi de l'A57 et sur le câble du TECNO | ✅ Le choix est proposé, le Wi-Fi d'abord, avec la consigne pour l'invité |
| Le réseau de l'A57 pendant un partage, l'écran quitté | ✅ Il reste ; il s'éteint à « Arrêter le partage » |
| 404 Mo vers Safari sur un iPhone, en changeant d'application et l'écran verrouillé en cours de route | ✅ Arrivé entier ; la barre de Safari a continué normalement (point d'accès 2,4 GHz du TECNO, débit non significatif) |

### 5.3 Ce que les essais ont trouvé, et sa correction (#281)

| Constat | Correction |
|---|---|
| Sans Wi-Fi, l'A57 servait sur son adresse 4G | Jamais les données mobiles : la liste des réseaux de l'invité, Wi-Fi d'abord ; une adresse hors liste refusée (`address_not_local`), aucun réseau dit (`no_local_network`) |
| Un PC sur deux réseaux servait sur celui de sa route vers Internet | Action `web_share_networks` ; l'écran propose le choix et dit où doit être l'invité |
| Le réseau créé sur l'écran Connecter s'éteignait en le quittant | Il reste tant qu'un partage par navigateur s'en sert |
| « Arrêter le partage » sous la barre de gestes d'Android | La marge du bas du système gardée |
| Un téléchargement abandonné annoncé « terminé » | `download_done` seulement si tout le contenu est parti |
| Chrome redonnait un fichier depuis son cache, la session finie | `Cache-Control: no-store` sur toutes les réponses |

---

## 6. Réserves et suite

| # | Réserve | Suite |
|---|---|---|
| G5 | Gate G5, et la décision D3 qui en dépend (§3) | À la bêta : les trois questions aux testeurs ; en option, l'écran de l'hôte qui voit l'avertissement (J8) |
| 1 | Envoi depuis Chrome sur le téléphone à 26 MB/s (§5.1) | Lot vitesse (J8) : mesurer dans Chrome avant de toucher au serveur |
| 2 | Sur Android, « Ajouter des fichiers » de l'écran ne propose pas de dossier : il faut passer par l'onglet des fichiers et « Navigateur », et rien ne le dit | Finitions des écrans Web Share |
| 3 | La page de l'invité, la session finie, demande « un nouveau code QR » même à qui a tapé l'adresse | Finitions |
| 4 | Après la fin d'un partage par les PIN faux, la sélection est à refaire | Finitions |
| 5 | Sur le point d'accès à 2,4 GHz, Chrome sur l'A57 n'a ouvert l'adresse qu'au troisième rechargement | À reproduire ; à rapprocher du propriétaire du groupe vu « hors ligne » par Chrome |
| 6 | Chrome lance chaque téléchargement trois fois avant de le garder | Sans effet sur l'hôte depuis #281 (« terminé » une seule fois) ; à comprendre |
| 7 | L'écran de l'hôte ne dit pas le nom du Wi-Fi, seulement « le même Wi-Fi que cet appareil » et l'adresse | Il demande la permission de localisation sous Android |
| 8 | « Connecter » a créé une fois un point d'accès à 2,4 GHz plutôt que le groupe à 5 GHz | À reproduire |
| 9 | Une fois (10 nov.), après « Arrêter le partage », l'application Android n'a plus rien reçu de son cœur ; l'application tournait depuis 6 h | Non reproduit ; à surveiller |
| 10 | L'hôte ne voit pas la progression d'un téléchargement de l'invité, seulement son début et sa fin (essai iPhone, 404 Mo) | Finitions : le cœur compte déjà les octets envoyés (#281) |
| 11 | Guide d'utilisation de l'application | **Reporté à la première version** : l'application change encore |

**Suite** (feuille de route) : J8, mesure, optimisation, finitions v1.

---

## 7. Environnement

- **iPhone** : Safari (essais du 15 novembre).
- **Android** : Galaxy A57 (SM-A576B), Android 16, arm64 ; TECNO BF6, Android 12, 32 bits. APK de débogage (`dev.medxfer.app.debug`) installés par adb.
- **Windows** : PC Windows 11, Intel Dual Band Wireless-AC 8265 ; application de débogage ; Edge.
- **Réseaux** : le groupe Wi-Fi Direct de l'A57 (5 GHz) et son point d'accès à 2,4 GHz ; le point d'accès à 2,4 GHz du TECNO (15 novembre) ; son partage de connexion USB pour le câble du PC.
- **Mesures** : octets envoyés ou reçus par la carte Wi-Fi du PC, relevés toutes les 0,5 s ; le débit est compté de la première rafale à la dernière.
