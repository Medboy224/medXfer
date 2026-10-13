# medXfer — Plan détaillé du jalon J5 (appairage par PIN)

> **Source :** feuille de route « J5 » ; bible §6.2 (chemin P2), §6.4 (`PAIR-01` à `PAIR-08`), §3.2 (interface `Pairer`), §14.1 (tests MITM) ; veille [medxfer-g1-pake.md](medxfer-g1-pake.md).
> **Objectif :** deux appareils qui ne se connaissent pas s'appairent avec un **PIN à 6 chiffres** lu sur l'un et saisi sur l'autre, sans qu'un attaquant puisse deviner le PIN hors ligne ni s'interposer. Concrètement :
> - le PIN ne sert que dans un **PAKE**, CPace, lié au canal TLS (`fp_i`, `fp_r`, `ekm`) ;
> - la **confirmation de clé** est obligatoire dans les deux sens ;
> - **3 essais** par PIN, puis un nouveau PIN, avec un délai qui croît ;
> - un échec ne révèle rien.
>
> **Durée estimée :** 5 à 8 jours, plus la revue externe (gate G4) à contractualiser ou dater. Une PR par lot, CI verte sur les trois systèmes avant fusion.

## 0. État de départ (constaté sur `main` après J4b)

| Constat | Conséquence pour J5 |
|---|---|
| L'appairage a deux chemins : le QR (P1, secret de 128 bits, `pair_secret`) et `pair_hello`, où l'utilisateur compare les empreintes (WP-3.3, D3 de J3). Pas de PIN. | WP-5.3 : chemin P2, à côté des deux autres. |
| Gate G1 tranchée le 13 octobre 2026 : **CPace, suite `CPACE-RISTR255-SHA512`**, assemblée sur `github.com/gtank/ristretto255`, et `PAIR-06` reformulé. La revérification du début de J5 ne change rien : brouillon 21 en attente de l'éditeur RFC, aucune implémentation importable et maintenue. | WP-5.1 : `pkg/pake` ; D1 et D2. |
| `securechan` ne donne pas encore l'`ekm` (aucun `ExportKeyingMaterial` dans le code). | WP-5.2. |
| La connexion d'un inconnu est en état `UNAUTH`, limitée par le Gate (LIM-01) ; seules les trames d'appairage y passent. | Le PAKE tourne dans cet état ; question ouverte #93 sur le Gate. |

## 1. Ordre d'exécution et dépendances

```
WP-5.0 G1 consignée (bible, dépendances) ──► WP-5.1 pkg/pake (CPace, vecteurs) ──┐
                                            WP-5.2 ekm dans securechan ─────────┼─► WP-5.3 protocole P2 (pairing)
                                                                                 │          │
                                                                                 │          ▼
                                                                                 │   WP-5.4 CLI et daemon
                                                                                 │          │
                                                                                 └──────────▼
                                                                          WP-5.5 MITM, journaux, essai réel ──► sortie
```

WP-5.1 et WP-5.2 sont indépendants. La revue externe (G4) peut être contractualisée dès que WP-5.1 est fusionné.

## 2. Les lots

### WP-5.0 — G1 consignée · S

- **Bible :**
  - `PAIR-06` reformulé (veille §5) ;
  - gate G1 fermée : choix et raisons ;
  - Annexe B : statut de CPace (brouillon 21, en attente de l'éditeur RFC) et nouveau lien pour la faille de `schollz/pake`, l'ancien répondant 404.
- **Dépendances :** `github.com/gtank/ristretto255` et `filippo.io/edwards25519`, en pur Go. Je vérifie l'absence de CGO pour android/arm64, windows, linux et darwin.

### WP-5.1 — `pkg/pake` : CPace `CPACE-RISTR255-SHA512` · M

- **Assemblage du protocole seulement** (`PAIR-06` reformulé) :
  - `prepend_len`, `lv_cat` et chaîne du générateur ;
  - générateur `g = SetUniformBytes(SHA-512(gen_str))` ;
  - scalaire secret tiré avec `crypto/rand`, partage `Y = y·g` ;
  - `K = y·Y_pair`, puis `ISK` et `sid_output` selon le brouillon 21.
- **API :** `Start(pin, ci, sid, role) → (state, share)`, puis `Finish(state, peerShare) → (isk, err)`. Le paquet ne connaît ni le réseau ni les messages. La bibliothèque de groupe est derrière un seul fichier adaptateur (D9).
- **Contrôles d'acceptation** (veille §5) :
  1. tous les vecteurs de l'annexe A et de l'annexe B.3 du brouillon passent **octet pour octet** : générateur, Ya, Yb, K, ISK, `sid_output`. Le test cite la révision du brouillon ;
  2. un partage invalide ou non canonique est refusé ; un `K` égal à l'élément neutre est refusé ; le décodage est fuzzé ;
  3. le PIN et l'ISK ne sont jamais formatés dans une erreur ni dans un journal.
- **Contre-vérification :** les sorties sont comparées une fois à celles de `the-sarge/cpace` (même suite, brouillon 21), sans en dépendre.
- **Point non vérifié par la veille :** la correspondance entre la dérivation d'élément du brouillon et `SetUniformBytes`. Les vecteurs B.3 la confirment ou l'infirment dès le premier test.

### WP-5.2 — Liaison au canal (`ekm`) · S

- `securechan.Conn.ChannelBinding()` renvoie `ExportKeyingMaterial("EXPORTER-medxfer-v1-pair", nil, 32)` (TLS 1.3).
- **Tests :**
  - les deux côtés d'une même connexion obtiennent la même valeur ;
  - deux connexions obtiennent des valeurs différentes : c'est ce qui fait échouer un MITM qui relaie.

### WP-5.3 — Chemin P2 dans `pkg/pairing` · M

**Messages**, dans la connexion TLS en état `UNAUTH` :
1. initiateur → répondant : `pair_pin {Ya}` ;
2. répondant → initiateur : `pair_pin_reply {Yb, conf_r}` ;
3. initiateur → répondant : `pair_pin_confirm {conf_i, device_name}` ;
4. répondant → initiateur : `pair_accept` (déjà existant), avec son nom et l'offre d'un envoi ponctuel.

**Liaison au canal et confirmation :**
- CI = `lv_cat("medXfer-pair-v1", fp_i, fp_r)` ; sid = `ekm` (`PAIR-03`) ;
- confirmation du brouillon §10.4 (bible §6.2, étape 4), comparée en temps constant, dans les deux sens (`PAIR-05`) ;
- `pair_pin` nomme la suite (`CPACE-RISTR255-SHA512`) : un répondant qui ne la connaît pas refuse, sans compter d'essai (D9).

**Côté répondant :**
- le PIN est tiré avec `crypto/rand`, sans biais modulo (`PAIR-01`), et vit 120 s, pour une seule session (`PAIR-02`) ;
- un essai compte **dès que le répondant envoie `pair_pin_reply`** : l'initiateur a alors testé son PIN, qu'il confirme ou non. Les essais sont réservés, donc des connexions simultanées n'en obtiennent pas plus de 3 par PIN ;
- après 3 essais ratés, le PIN est détruit, la connexion fermée et un nouveau PIN tiré. Après 3 PIN détruits d'affilée, il y a un délai avant d'en afficher un nouveau (D5) (`PAIR-04`) ;
- en cas d'échec, une réponse neutre et identique (`pair_reject`), sans nombre d'essais restants ni alias (`PAIR-07`). Les noms ne circulent qu'après la confirmation ;
- sans essai disponible (PIN épuisé, délai en cours, PIN déjà utilisé), le répondant joue l'échange sur un PIN factice : l'initiateur voit `pair_pin_reply` puis `pair_reject`, comme pour un mauvais PIN (`PAIR-07`).

**En cas de succès :**
- la connexion passe en état `AUTH` (`Authenticate`) ;
- l'empreinte est enregistrée, sauf en mode « un seul envoi » (`PAIR-08`) ;
- la suite est celle du QR : `pair_accept`, l'offre, `DATA_JOIN`.

**Interface :** `pairing.NewPIN` (le PIN, ses essais, son écran), passé à `pairing.Serve` par `Options.PIN`, et `pairing.JoinPIN`, au plus près du `Pairer.ByPIN` de la bible §3.2. Le même `Serve` accepte le QR et le PIN (D6) ; `inv` peut être nul quand seul le PIN est offert.

### WP-5.4 — Ligne de commande et daemon · S à M

- **`xfer pair --pin`** (répondant) affiche le PIN groupé par 3 (`123 456`) et son expiration. **`xfer pair <adresse> --pin`** (initiateur) le demande.
- **`xfer send --pin`** : un envoi ponctuel qui affiche un PIN au lieu d'un QR, pour un appareil sans caméra.
- **Daemon :**
  - actions `pair_pin_show` (répondant) et `pair_pin` (initiateur) ;
  - événements `pair_pin_shown` (le PIN, pour l'écran local seulement) et `pair_failed` (sans détail) ;
  - le tableau de bord de développement (`devui`) gagne les deux boutons.

### WP-5.5 — Tests de sécurité, journaux, essai réel · S à M

- **Tests de la bible §14.1 :**
  - un **proxy MITM** qui relaie un appairage par PIN fait échouer les confirmations, alors même qu'il connaît le PIN, puisque les `ekm` diffèrent (`PAIR-03`, `PAIR-05`) ;
  - une **4ᵉ tentative** fausse sur un même PIN est refusée (`PAIR-04`) ;
  - le **délai** après 3 PIN détruits.
- **Journaux** (bible chapitre 13) : aucun PIN, ISK, `ekm` ni partage n'apparaît dans les journaux, le paquet de diagnostic ou les événements. Seul `pair_pin_shown` porte le PIN, vers l'écran du répondant.
- **Fuzzing** des messages `pair_pin*` et du décodage des partages.
- **Essai réel** PC ↔ téléphone (Termux) : appairage par PIN dans les deux sens ; PIN faux trois fois, puis nouveau PIN ; envoi ponctuel par PIN.
- **Dossier pour la revue externe :** description de `pkg/pake` et du chemin P2, correspondance avec le brouillon, liste des tests (G4).

## 3. Critère de sortie de J5

1. **Appairage par PIN réussi** entre le PC et le téléphone, dans les deux sens, y compris un envoi ponctuel.
2. **Vecteurs :** annexe A et annexe B.3 du brouillon 21 vérifiés octet pour octet, dans la CI.
3. **Sécurité :** MITM, 4ᵉ tentative et délai testés dans la CI ; rien de secret dans les journaux.
4. **Gate G4 :** revue cryptographique externe des chapitres 6 et 7 **contractualisée ou datée**. C'est une décision qui te revient : prestataire, budget, date.
5. Bible à jour : `PAIR-06` reformulé, G1 fermée, Annexe B.

## 4. Hors périmètre

- Écrans de l'application Flutter (saisie du PIN, affichage) : J6b. J5 fournit la ligne de commande, le daemon et les événements.
- Le PIN de repli de Web Share (5 essais par session) : J7. C'est un autre mécanisme, sans PAKE.
- PIN à 8 chiffres : paramètre prévu par la bible (« si besoin ») ; 6 par défaut, 8 seulement sur demande (D4).

## 5. Décisions à valider avec ce plan

| # | Décision | Raison |
|---|---|---|
| D1 | **CPace `CPACE-RISTR255-SHA512`** sur `gtank/ristretto255` (gate G1, validée le 13 octobre 2026). | Choix du CFRG, aucune constante publique, vecteurs B.3, aucune arithmétique de corps à écrire. |
| D2 | **`PAIR-06` reformulé** (validé le 13 octobre 2026) : pas de primitive écrite par nous ; assemblage permis sur une bibliothèque de groupe reconnue, avec tous les vecteurs et une revue externe avant la mise en service. | Aucune bibliothèque Go ne remplit les cinq critères de la gate. |
| D3 | **Ordre des messages :** l'initiateur envoie son partage d'abord ; le répondant répond avec le sien et **sa confirmation** ; l'initiateur confirme ensuite et donne son nom. | Trois messages, un seul aller-retour de plus que le QR. Chaque connexion ne teste qu'un PIN : CPace interdit l'attaque hors ligne, quel que soit l'ordre des confirmations. |
| D4 | **PIN de 6 chiffres** par défaut, affiché groupé (`123 456`) ; 8 chiffres en option (`--pin-digits 8`). | Bible §6.4 : 3 chances sur 10⁶ par PIN affiché ; la voie forte reste le QR. |
| D5 | **Délai après 3 PIN détruits d'affilée** : 30 s, puis doublé à chaque nouvelle série, plafonné à 10 min ; remis à zéro par un appairage réussi. | `PAIR-04` demande un délai exponentiel sans le chiffrer. Il borne un attaquant à quelques dizaines d'essais par heure. |
| D6 | **Les trois chemins coexistent** : QR (P1), PIN (P2) et comparaison d'empreintes (`pair_hello`, WP-3.3). Le côté qui affiche le QR est aussi celui qui affiche le PIN. | Le PIN sert aux appareils sans caméra ; la comparaison d'empreintes reste pour un appareil sans écran de saisie. |
| D7 | **Pas de dépendance à `the-sarge/cpace`**, seulement une comparaison ponctuelle de sorties pendant WP-5.1. | Il se déclare lui-même « *not production-ready* », sans revue. |
| D8 | La **revue externe (G4)** porte sur `pkg/pake`, le chemin P2 et les chapitres 6 et 7 de la bible. Elle conditionne la **publication**, pas la fusion des lots. | Bible, phase 2b ; feuille de route J5. |
| D9 | **Modularité.** Changer de PAKE, de suite ou de bibliothèque ne doit toucher qu'un endroit :
- le reste du code passe par l'interface d'appairage (`Pairer`, bible §3.2), et `pkg/pairing` ignore quel PAKE tourne dessous ;
- dans `pkg/pake`, la bibliothèque de groupe est derrière **un seul fichier adaptateur** ;
- les messages `pair_pin` portent l'identifiant de la suite (`CPACE-RISTR255-SHA512`) : une version future en propose une autre sans casser l'ancienne, et une suite inconnue est refusée proprement. | Demande du 13 octobre 2026 : toute implémentation doit pouvoir changer plus tard sans réécrire le reste. Bible règle R1 (on ne connaît que l'interface de la couche du dessous). |
| D10 | **On ne réécrit pas la bibliothèque de groupe** (ristretto255, RFC 9496). Ce qui est propre à medXfer est déjà à nous : le protocole dans `pkg/pake`, et tout l'appairage autour. La bibliothèque reste sous contrôle autrement :
- version épinglée, mise à jour seulement volontairement ;
- vecteurs officiels dans la CI, qui refusent tout changement de comportement ;
- adaptateur (D9), qui permet de la remplacer, par exemple si Go l'intègre un jour. | L'arithmétique de courbe à temps constant est le code où une erreur reste invisible : l'appairage marche en apparence, mais une fuite par le temps de calcul ou un point mal validé suffit à casser la sécurité. C'est ce qu'interdit `PAIR-06`, même reformulé. |
