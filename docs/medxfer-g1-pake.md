# Gate G1 — choix du PAKE pour l'appairage par PIN

> Veille WP-3.8 (plan J3), sans code de production. Sources vérifiées le 11/10/2026.
> Elle prépare J5 (chemin P2, bible §6.2). **La décision reste à prendre.** Ce document se termine par une recommandation.

## 1. Résumé

**Recommandation : CPace, suite `CPACE-RISTR255-SHA512`, assemblée sur la bibliothèque de groupe `github.com/gtank/ristretto255`, plus `crypto/sha512` et `crypto/hmac`.**

Les contrôles d'acceptation sont donnés au §5. La mise en service se fait après une revue externe.

Cette recommandation demande de **reformuler `PAIR-06`** :
- **Pourquoi :** aucune bibliothèque Go ne remplit aujourd'hui les cinq critères de la gate.
- **Ce qui serait interdit :** écrire soi-même une primitive (groupe, corps, hachage vers le groupe, encodages).
- **Ce qui serait permis :** assembler le protocole CPace sur une bibliothèque de groupe reconnue, si tous les vecteurs de la spécification passent octet pour octet.

**Plan B, si `PAIR-06` reste à la lettre :** reprendre par copie le paquet CPace de Russ Cox. Il implémente la suite recommandée X25519 et vérifie tous les vecteurs du brouillon 21, mais c'est un paquet interne, non maintenu comme bibliothèque, qui contient sa propre arithmétique de corps (§4.4).

**Écartés :**
- `schollz/pake` : pas de spécification ni de vecteurs, API Go dépréciées ;
- `filippo.io/cpace` : abandonné en 2021, brouillon ancien ;
- `bytemare/cpace` : preuve de concept, vecteurs auto-générés ;
- SPAKE2 : aucune implémentation Go conforme à la RFC 9382 trouvée.

## 2. Ce que le PAKE doit faire chez medXfer

Rappel de la bible (§6.2 et §6.4) :
- **Mot de passe :** un PIN à 6 chiffres, affiché par le répondant et saisi par l'initiateur. Il est tiré avec `crypto/rand`, vaut pour une session et vit 120 s au plus (`PAIR-01`, `PAIR-02`).
- **Canal :** le PAKE tourne dans le TLS déjà établi, en état `UNAUTH`. Il est lié au canal par `fp_i`, `fp_r` et `ekm = ExportKeyingMaterial("EXPORTER-medxfer-v1-pair", nil, 32)` (`PAIR-03`).
- **Confirmation de clé :** obligatoire dans les deux sens (`PAIR-05`).
- **Échecs :** au bout de 3, le PIN est détruit (`PAIR-04`). Un échec ne révèle rien (`PAIR-07`).
- **Bibliothèque :** pas de cryptographie du PAKE écrite par nous, des vecteurs de test vérifiés, et des constantes publiques sans logarithme discret connu (`PAIR-06`, voir l'incident de `croc` au §4.5).

Le besoin est un **PAKE équilibré** : les deux appareils connaissent le même PIN. Un PAKE augmenté (OPAQUE) n'a pas d'intérêt ici.

## 3. Les deux protocoles candidats

### 3.1 CPace ✅ FONDÉ
- **Sélection :** le CFRG a terminé sa sélection en avril 2020 par « *one balanced PAKE: CPace; one augmented PAKE: OPAQUE* », « *recommended by the CFRG for usage in IETF protocols* ». Trois des quatre experts du panel préféraient CPace à SPAKE2 pour le PAKE équilibré. [Diapositives des présidents du CFRG](https://datatracker.ietf.org/meeting/interim-2020-cfrg-01/materials/slides-interim-2020-cfrg-01-sessa-results-of-the-pake-selection-process-00.pdf), p. 11–12.
- **Statut :** le brouillon `draft-irtf-cfrg-cpace-21` (avril 2026) est « *Sent to the RFC Editor* » (IESG : « *IESG Review Completed* », éditeur RFC : « *Awaiting First editor* »). Il n'a pas encore de numéro de RFC. [Datatracker](https://datatracker.ietf.org/doc/draft-irtf-cfrg-cpace/).
  - La bible (Annexe B) le décrit encore comme « toujours un brouillon ». C'est vrai, mais il a avancé depuis.
- **Vecteurs de test :**
  - l'annexe A couvre les fonctions utilitaires : `prepend_len`, `lv_cat`, `o_cat`, transcriptions ;
  - l'annexe B couvre sept suites, dont X25519/SHA-512 (B.1), ristretto255/SHA-512 (B.3) et P-256/SHA-256 (B.5).
- **Constantes publiques : aucune.** Le générateur est dérivé du mot de passe, du CI et du sid par hachage vers le groupe. Il n'existe donc pas de points M/N dont quelqu'un pourrait connaître le logarithme : la faille de `croc` ne peut pas se produire par construction.
- **Suites :**
  - « *We RECOMMEND* » : `CPACE-X25519-SHA512` et `CPACE-P256_XMD:SHA-256_SSWU_NU_-SHA256` (§5 du brouillon) ;
  - ristretto255 est une suite décrite, avec ses vecteurs (B.3). Le brouillon écrit que CPace « *can also securely be implemented* » avec elle et recommande ristretto255 avec SHA-512 (§8.3).
- **Générateur ristretto255 :** `gen_str_hash = H.hash(gen_str, 64)`, puis la fonction de dérivation d'élément du groupe (§8.3).
- **Liaison au canal et confirmation :**
  - le **CI** (*channel identifier*) sert à « *bind a session key … to a specific networking channel* » ;
  - le **sid** est « *RECOMMENDED* » s'il est disponible (§4.1) ;
  - une confirmation de clé explicite par MAC est « *advisable* » (§10.4).

  Correspondance avec la bible : CI = `lv_cat(fp_i, fp_r)`, sid = `ekm`, confirmation HMAC dans les deux sens. Cela couvre `PAIR-03` et `PAIR-05`.

### 3.2 SPAKE2 (RFC 9382)
- **Statut :** RFC de catégorie *Informational*, non retenue par la sélection du CFRG. [RFC 9382](https://www.rfc-editor.org/rfc/rfc9382.html)
- **Constantes M et N :** elles sont dérivées de graines publiques par SHA-256 (annexe A), méthode « *designed to eliminate concerns related to knowing discrete logs of M and N* » (§7).
  - Le CFRG a demandé aux auteurs de traiter « *the issue of a single discrete log relationship necessary for the security of all sessions* » (diapositives citées, p. 8).
- **Vecteurs :** une seule suite en a, `P256-SHA256-HKDF-HMAC` (annexe B).
- **Confirmation de clé :** obligatoire dans le protocole (§3.3).
- Une question de brevet (US 7,047,408) a été soulevée par le CFRG pour SPAKE2 (p. 8). Son statut n'est **pas vérifié** ici.

## 4. Implémentations Go évaluées

### 4.1 Grille

Critères de la gate G1 (bible chapitre 16) :
- **V** : vecteurs de la spécification ;
- **C** : constantes publiques sans logarithme connu ;
- **M** : maintenance ;
- **G** : compilation sans CGO sur mobile ;
- **R** : revue externe possible.

| Candidat | V | C | M | G | R | Verdict |
|---|---|---|---|---|---|---|
| `filippo.io/cpace` | ✗ | ✓ | ✗ | ✓ ? | ✗ | Écarté |
| `bytemare/cpace` | ✗ | ✓ | ✗ | ? | ✗ | Écarté |
| `the-sarge/cpace` | ? | ✓ | ? | ? | ✗ | Non évalué (trop récent) |
| `rsc.io/cmd/mote/internal/cpace` | ✓ | ✓ | ✗ | ✓ | ~ | **Plan B** |
| `schollz/pake/v3` | ✗ | ~ | ✓ | ✓ ? | ✗ | Écarté |
| SPAKE2 en Go (plusieurs) | ✗ | ~ | ✗ | ? | ✗ | Écarté |
| **CPace sur `gtank/ristretto255`** | ✓ (à notre charge) | ✓ | ✓ | ✓ | ✓ | **Recommandé** |

### 4.2 `filippo.io/cpace`
- **Brouillon :** « *loosely based on draft-haase-cpace-01* », un brouillon individuel antérieur à celui du CFRG.
- **Avertissement :** « *This is an experimental implementation. It might change and be broken … so far is really only a weekend project. It's also not standardized.* »
- **Activité :** une seule version, du 1ᵉʳ janvier 2021.
- [pkg.go.dev](https://pkg.go.dev/filippo.io/cpace)

### 4.3 `bytemare/cpace`
- **Statut :** « *THIS IMPLEMENTATION IS PROOF OF CONCEPT … Deploy it: Don't, yet.* » 3 étoiles, aucune version publiée. [GitHub](https://github.com/bytemare/cpace)
- **Vecteurs :** ses tests relisent un fichier que la bibliothèque **génère elle-même** (`TestGenerateVectorFile`). C'est un contrôle de cohérence, pas de conformité au brouillon.
- **Défaut de test :** la vérification de `DSI1` compare en fait `DSI2`. [tests/cpace_test.go](https://raw.githubusercontent.com/bytemare/cpace/main/tests/cpace_test.go)
- `the-sarge/cpace` (Go, brouillon 21, ristretto255) a 1 étoile ; sa dernière mise à jour date d'octobre 2026. Il est trop récent pour être jugé. [Thème GitHub « cpace »](https://github.com/topics/cpace)

### 4.4 `rsc.io/cmd/mote/internal/cpace` (Russ Cox), plan B
- **Ce qu'il implémente :** « *the cipher suite CPACE-X25519-SHA512 … draft-irtf-cfrg-cpace-21* ». Version v0.1.3 du 18 septembre 2026, licence BSD-3. [pkg.go.dev](https://pkg.go.dev/rsc.io/cmd/mote/internal/cpace)
- **Vecteurs vérifiés :** annexes A.1.2, A.1.4, A.3.3, A.3.5 et A.3.7 ; B.1.1 (générateur et Elligator 2) ; B.1.2 à B.1.7 (protocole complet, ISK, sid) ; B.1.10 (points de petit ordre). [cpace_test.go](https://raw.githubusercontent.com/rsc/cmd/main/mote/internal/cpace/cpace_test.go)
- **Dépendances :**
  - seulement la bibliothèque standard : `crypto/ecdh`, `crypto/sha512`, `crypto/hmac`, `math/bits`… ;
  - mais le paquet contient sa propre arithmétique de corps (`field.go`) et son Elligator 2 (`elligator.go`). [Sources](https://github.com/rsc/cmd/tree/main/mote/internal/cpace)
- **Limites :**
  - le chemin `internal` empêche de l'importer, il faudrait donc le **copier** ;
  - il n'est pas maintenu comme bibliothèque ;
  - aucun audit n'est mentionné ;
  - la revue externe porterait aussi sur son arithmétique de corps.

### 4.5 `schollz/pake/v3` (utilisé par `croc`)
- **Version :** v3.2.0 du 31 juillet 2026, maintenue. [pkg.go.dev](https://pkg.go.dev/github.com/schollz/pake/v3)
- **Protocole :** dérivé du « *PAKE2* » du livre de Boneh et Shoup. Le paquet ne revendique pas « *full RFC 9382 conformance* » et ne fournit aucun vecteur de spécification.
- **Points publics :** depuis la correction, ils sont « *hard-coded to prevent an application from allowing users to supply their own points* ». Ils sont obtenus par SHA-1 des graines `croc1` et `croc2`, puis incrément jusqu'à une abscisse valide, avec un script Sage pour vérifier. C'est acceptable sur le fond, mais propre à ce projet.
- **Historique (CVE-2021-31603) :**
  - l'initiateur, c'est-à-dire le destinataire, choisissait les points U et V, donc pouvait connaître leurs logarithmes ;
  - cela ramenait l'entropie de la clé à celle du code et permettait une force brute hors ligne ;
  - sources : [analyse de RedRocket](https://redrocket.club/posts/croc/) et [message au CFRG du 7 mai 2021](https://mailarchive.ietf.org/arch/msg/cfrg/KtmqZS6CFUwCfkVTlieGw-ZFGfY/) ;
  - le lien de la bible vers le récit de l'auteur (`schollz.com/blog/croc9`) a répondu **404** lors de cette vérification.
- **API utilisées :** `crypto/elliptic` et `math/big`, via la courbe `siec` ([imports](https://pkg.go.dev/github.com/schollz/pake/v3?tab=imports)). Go les signale toutes deux comme inadaptées :
  - les méthodes de `Curve` sont « *Deprecated: this is a low-level unsafe API* », et les courbes personnalisées « *not guaranteed to provide any security property* » ([crypto/elliptic](https://pkg.go.dev/crypto/elliptic)) ;
  - `big.Int` « *may leak the Int's value through timing side-channels* » et « *is not well-suited to implement cryptographic operations* » ([math/big](https://pkg.go.dev/math/big)).

### 4.6 SPAKE2 en Go
- **`gospake2` (CzarJoti / ValiantChip) :** se dit conforme à la RFC 9382, avec edwards25519 par défaut. Version v0.1.6 de février 2026, aucun importateur. Ses vecteurs RFC et ses M/N ne sont pas documentés, et le chemin d'import de l'exemple diffère de celui du module. [pkg.go.dev](https://pkg.go.dev/github.com/CzarJoti/gospake2)
- **`gospake2` de Vasudev Kamath :** vise l'interopérabilité avec `python-spake2`, pas la RFC 9382. « *not been formally audited* ». [README](https://salsa.debian.org/go-team/packages/golang-debian-vasudev-gospake2/-/blob/debian/sid/README.md)
- **`jtejido/spake2` :** fait pour les mesures de performance, sans les M/N de la RFC. [pkg.go.dev](https://beta.pkg.go.dev/github.com/jtejido/spake2)

### 4.7 Bibliothèques de groupe
- **`github.com/gtank/ristretto255` :**
  - v0.2.0 du 6 août 2025, licence BSD-3, 87 importateurs ;
  - implémente le groupe de la RFC 9496 ;
  - `SetUniformBytes` prend 64 octets et « *can be used for hash-to-group operations* » ;
  - dépend seulement de `filippo.io/edwards25519` et de son paquet `field`. [pkg.go.dev](https://pkg.go.dev/github.com/gtank/ristretto255), [imports](https://pkg.go.dev/github.com/gtank/ristretto255?tab=imports)
  - **Essai mené ici :** aucun fichier CGO dans les deux modules. Compilation `CGO_ENABLED=0` réussie pour `android/arm64`, `windows/amd64`, `linux/arm64`, et pour les paquets en `ios/arm64` (sur iOS, c'est l'édition de liens d'un *exécutable* qui exige CGO, pas la bibliothèque).
- **`github.com/cloudflare/circl/group`** (P-256, P-384, P-521, ristretto255, `HashToElement`) : v1.6.5 d'août 2026. Mais « *Scalar operations … of P256, P384, and P521 are currently not constant time* ». C'est une raison de ne pas l'utiliser pour la suite P-256. [pkg.go.dev](https://pkg.go.dev/github.com/cloudflare/circl/group)

## 5. Décision recommandée pour J5

**CPace, suite `CPACE-RISTR255-SHA512`, implémentée dans `pkg/pake` derrière l'interface `Pairer.ByPIN` (bible §3.2), sur `gtank/ristretto255` et la bibliothèque standard.**

**Pourquoi CPace plutôt que SPAKE2 :**
- c'est le choix du CFRG ;
- il n'a aucune constante publique, donc aucune classe de faille « à la `croc` » ;
- il a des vecteurs pour la suite choisie ;
- sa normalisation est presque achevée.

**Pourquoi ristretto255 plutôt que X25519 :**
- c'est un groupe d'ordre premier, sans points de petit ordre à filtrer ;
- la bibliothèque fournit directement la dérivation d'élément sur 64 octets, donc **aucune arithmétique de corps ni Elligator à écrire** ;
- `gtank/ristretto255` est maintenu, largement importé, et pur Go.

X25519 est la suite que le brouillon « RECOMMEND ». Mais en Go, aucune bibliothèque importable ne fournit son Elligator 2 : il faudrait l'écrire ou le copier.

**Ce que medXfer écrirait :** seulement l'assemblage du protocole.
- `prepend_len`, `lv_cat` et la chaîne du générateur ;
- l'appel à `SetUniformBytes` ;
- la multiplication scalaire et le hachage de l'ISK ;
- la confirmation HMAC.

Soit environ 150 lignes (estimation), déterministes, vérifiables octet pour octet.

**Correspondance avec la bible :**
- CI = `lv_cat(fp_i, fp_r)` et sid = `ekm` (`PAIR-03`) ;
- confirmation HMAC dans les deux sens, selon le §10.4 du brouillon (`PAIR-05`) ;
- 3 essais par PIN (`PAIR-04`).

**Contrôles d'acceptation**, avant toute mise en service :
1. Tous les vecteurs de l'annexe A et de l'annexe B.3 du brouillon passent octet pour octet : générateur, Ya/Yb, K, ISK, `sid_output`. Le test cite la révision du brouillon.
2. Les éléments reçus invalides ou non canoniques sont refusés et le décodage est fuzzé. Une clé K égale à l'élément neutre est refusée.
3. Tests du §14.1 : un proxy MITM pendant un appairage par PIN fait échouer les confirmations (`PAIR-03`, `PAIR-05`) ; une 4ᵉ tentative erronée sur un même PIN est refusée (`PAIR-04`).
4. Aucun PIN, ISK ni `ekm` n'apparaît dans les journaux (bible chapitre 13).
5. **Revue externe** de `pkg/pake` avant la publication (bible, phase 2b).

**Modification de la bible à décider :** reformuler `PAIR-06`, par exemple ainsi.

> On NE DOIT PAS écrire soi-même de primitive (groupe, corps, hachage vers le groupe, encodage). Le protocole PAKE PEUT être assemblé sur une bibliothèque de groupe reconnue, à condition de vérifier tous les vecteurs de test de sa spécification et d'obtenir une revue externe avant la mise en service. Un protocole à constantes publiques DOIT les fixer sans connaissance du logarithme discret.

Il faudrait aussi mettre à jour l'Annexe B : statut de CPace, nouveau lien pour la faille de `croc`.

**À revérifier au début de J5 :**
- si une implémentation CPace importable et maintenue est apparue (par exemple dans `golang.org/x/crypto`), elle remplace l'assemblage maison, à condition de passer les mêmes contrôles ;
- si le brouillon est devenu une RFC, mettre la référence à jour et revérifier les vecteurs.

## 6. Non vérifié

- La correspondance exacte entre la « dérivation d'élément » du brouillon pour ristretto255 et `SetUniformBytes`, c'est-à-dire la fonction de dérivation de la RFC 9496. Elle est très probable ; les vecteurs B.3 la confirmeront ou l'infirmeront au premier test de J5.
- L'absence de dépendance CGO pour `filippo.io/cpace`, `bytemare/cpace`, `schollz/pake` et les `gospake2` (non nécessaire, puisqu'ils sont écartés).
- Le statut du brevet US 7,047,408 (SPAKE2), non pertinent si CPace est retenu.
