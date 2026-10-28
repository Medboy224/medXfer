# medXfer — Rapport de sortie du jalon J6b (application Flutter MVP)

**Date :** 28 octobre 2026 · **Plan :** [medxfer-plan-j6b.md](medxfer-plan-j6b.md) · **Bible :** chapitre 10 (`API-01` à `API-08`), `DISC-03`, `DISC-10`, `DISC-12`, `STO-15`, `STO-16`, chapitre 17 (`DEV-19-R`) · **PR :** #173 à #206, et celle de ce rapport.

Ce rapport répond au critère de sortie du plan : *scénario complet Android ↔ Windows et Android ↔ Linux au Wi-Fi réel, depuis l'application*. Il évalue aussi les gates G2a (réserves restantes) et G7.

---

## 1. Verdict

**J6b est terminé, avec une réserve** : l'appairage par **QR** n'a pas été essayé de bout en bout sur les vrais appareils. Il passe à **J6c**, avec le point d'accès du PC (§2, critère 1).

medXfer est désormais une **application** sous Android, Windows et Linux :
- on se connecte d'abord :
  - sur le même Wi-Fi, les deux appareils se voient ;
  - sinon, l'un crée la connexion et l'autre la rejoint par PIN ou par lien ;
- une fois connectés, chacun envoie ou reçoit ;
- sous Android, on choisit dans l'application, par catégories ; sur le PC, par dépôt ou par les fenêtres du système, fichiers et dossiers ensemble ;
- un transfert coupé reprend où il s'était arrêté, et la session revient seule entre appareils de confiance.

| Avant J6b | Après J6b |
|---|---|
| Un cœur en-processus, piloté par `xfer ctl` et le tableau de bord web | L'application Flutter (`app/`), au-dessus de `libmedxfer` ; tableau de bord retiré (G7) |
| La progression du fichier égale à celle du lot | Une progression par fichier, plusieurs fichiers à la fois (schéma v4) |
| Une session perdue ne revenait que par « Connecter », vue au bout de 2 min 30 s | Vue en 15 s (`ping`), recomposée seule entre appareils de confiance |
| Un transfert coupé plus de quelques secondes échouait d'un seul côté | Le récepteur attend 2 min ; sinon l'échec est dit des deux côtés, et « Reprendre » repart où il s'était arrêté |

**Réserves** (détail au §6) :
1. L'appairage par QR, et la jonction d'un second téléphone au point d'accès par QR (#169), sont pour J6c.
2. Les à-coups de l'interface sous Windows (réserve 4 de G2a) n'ont pas été mesurés par un outil ; aucun n'a été signalé pendant les essais.
3. Le débit Linux de la réserve 1 de G2a n'a pas été remesuré sur un PC récent.

---

## 2. Critère de sortie (plan, en-tête)

Essais réels du 25 au 28 octobre : Galaxy A57 (Android 16) ↔ PC Windows 11, et Galaxy A57 ↔ Zorin (Linux), au Wi-Fi réel.

| # | Critère | Résultat | Preuve |
|---|---|---|---|
| 1 | Appairage par QR et par PIN | ⚠️ | PIN ✅ dans les deux sens, Windows et Linux. QR : son scan à la caméra et l'intégration au point d'accès sont livrés (WP-B.2b), mais aucun essai réel de bout en bout. Le QR n'existe qu'avec un point d'accès ; celui du PC vient en J6c (décidé le 26 octobre). |
| 2 | Confiance mutuelle | ✅ | Proposée et acceptée, puis reconnexion sans code ; une session perdue revient seule entre appareils de confiance (#202) |
| 3 | Fichiers et dossier dans les deux sens | ✅ | Windows et Linux, empreintes identiques. Sur le PC, fichiers et dossiers ensemble dans un seul envoi (#205), vérifié sur les vrais appareils |
| 4 | Reprise après coupure | ✅ | Coupures du Wi-Fi d'un PC de 90 s et de 2 min 30 s, pendant un envoi depuis le téléphone : le transfert continue, ou s'interrompt des deux côtés et « Reprendre » repart où il s'était arrêté (#195 à #204). Après une reprise, la fin ne relit plus rien (#206) |
| 5 | Aucun fichier copié côté émetteur (`STO-16`) | ✅ | Tailles des dossiers de l'application avant et après un envoi : PC, Zorin et téléphone |

---

## 3. Gates

**G7 : fermée** (WP-B.5, #181) : les critères de `DEV-19-R` sont dans le panneau développeur ; le tableau de bord web est retiré (#183).

**G2a, réserves restantes :**

| Réserve | État |
|---|---|
| 1. Débit sous Linux, 88 à 89 % de `xfer` (portable de 2011) | **Ouverte** : pas de PC Linux récent pour la remesurer |
| 2. Paquet Linux sur une distribution neuve | **Levée** (WP-B.6) : Ubuntu 22.04 et 24.04 neuves, dans la CI |
| 3. Émulateur Android | Sans objet : le vrai téléphone ne montrait aucun écart |
| 4. À-coups de 230 à 240 ms sous Windows | **Ouverte** : non mesurés par un outil pendant les essais, aucun signalé. `lib/perf.dart` (version profile) peut les mesurer |

---

## 4. Ce qui a été livré

| PR | Lot | Contenu |
|---|---|---|
| WP-B.0 à WP-B.2a | Cœur et socle | Schéma v4, `FfiCore`, `FakeCore`, connexion et appairage (voir le plan) |
| #173 | WP-B.2b | Scan du QR à la caméra, point d'accès dans « Connecter » |
| #176 | WP-B.3 | Envoi, réception, écran de transfert, historique |
| #177 | WP-B.4 | Android : `medXfer/` rangé par type, envoi par descripteur, service de premier plan |
| #179, #180 | WP-B.9 | Explorateur par catégories (Android) |
| #181, #183 | WP-B.5 | Panneau développeur ; gate G7, tableau de bord retiré |
| #185 | WP-B.6 | Paquets Windows, Linux et Android |
| #191 | WP-B.10 | Dossier de réception au choix |
| #187 à #206 | WP-B.8 | Essais réels et leurs corrections (§5) |

---

## 5. Les essais réels (WP-B.8)

### 5.1 Ce qui a marché du premier coup

- Découverte mutuelle sur le même Wi-Fi.
- Appairage par PIN.
- Confiance mutuelle.
- Envoi de fichiers et de dossiers dans les deux sens, empreintes identiques.
- `STO-16` vérifié.
- Fermeture de la fenêtre sous Linux.

### 5.2 Ce que les essais ont trouvé, et sa correction

| Constat | Cause | PR |
|---|---|---|
| Écran « Connecter » loin de la maquette : un QR du réseau commun en tête, qui échoue hors de ce réseau ; rien ne disait sous quel nom chercher l'autre | — | #187 |
| Des fenêtres de console s'ouvraient et se fermaient sous Windows | `arp -a`, lancé par la découverte sans `CREATE_NO_WINDOW` | #188 |
| Les deux barres ne montraient pas le même chiffre ; en pause, celle de l'autre appareil avançait encore ; la barre saccadait | L'envoyeur comptait l'envoyé, le receveur le vérifié et écrit ; la pause laissait arriver les morceaux déjà demandés | #189 |
| Fermer la fenêtre laissait tourner un processus sous Windows | L'arrêt du cœur pouvait bloquer | #193, #194 |
| Un échec d'un côté laissait l'autre « en cours » | L'échec n'était pas dit à l'autre appareil | #195 |
| Après une coupure de plus de quelques secondes, le transfert restait figé | Le récepteur abandonnait après 8 essais rapides | #196 |
| Rien pour reprendre un envoi interrompu ; écran noir au premier essai | — | #197 |
| Une session perdue terminait le transfert à tort, ou le laissait sans fin | Les messages de fin perdus avec la session | #198 |
| Un appareil refusé insistait 2 min | Atteint dès la poignée de main, avant toute réponse | #199, #200 |
| Barre de l'envoyeur figée, puis en aller-retour, sans session | Elle ne suivait que les rapports du récepteur | #201, #203 |
| La session ne revenait qu'en appuyant sur « Connecter » | Personne ne la recomposait | #202 |
| Le téléphone voyait la session perdue au bout de 2 min 30 s | Seule la keepalive de TCP, à ses réglages par défaut | #204 |
| Sur le PC, impossible d'envoyer fichiers et dossier ensemble ; mise en page du téléphone | La fenêtre du système choisit l'un ou l'autre | #205 |
| 5 à 8 s d'attente après 100 % sur un transfert repris | Le `DIGEST` relisait à la fin les chunks d'avant la reprise | #206 |

### 5.3 La mise en page du PC (#205)

Le PC suit la maquette « 10. Bureau », avec un menu latéral : **Accueil**, **Historique**, **Appareils de confiance**, **Réglages**. Le dossier de réception est dans les Réglages.

L'Accueil a deux colonnes :
- à gauche, ce qu'on envoie : le dépôt et les deux fenêtres du système remplissent une liste, envoyée en une fois ;
- à droite, la **connexion**, sur le modèle de l'écran « Connecter » du téléphone : les appareils du même réseau, ou créer et rejoindre une connexion.

Décision du 27 octobre : **se connecter ne dépend ni d'une page ni du sens du transfert**. Fenêtre ouverte, le PC s'annonce et cherche les appareils de son réseau.

---

## 6. Réserves et suite

| # | Réserve | Suite |
|---|---|---|
| 1 | Appairage par QR non essayé de bout en bout ; jonction d'un second téléphone par QR (#169) | **J6c** : point d'accès du PC avec QR, et sonde Wi-Fi Direct à 5 GHz (décision J17) |
| 2 | À-coups sous Windows (réserve 4 de G2a) non mesurés | Une mesure de quelques minutes avec `lib/perf.dart` en version profile, en J6c |
| 3 | Débit sous Linux (réserve 1 de G2a) | À remesurer dès qu'un PC Linux récent est disponible |
| 4 | Guide d'utilisation de l'application | **Reporté à la première version** (décision du 28 octobre) : l'application change encore. [guide-utilisation-app.md](guide-utilisation-app.md) garde l'installation et le pare-feu |
| 5 | Hors des critères : diagnostic à distance (`DEV-14`), traces du mode développeur (`DEV-12`) | Plus tard, comme noté en WP-B.5 |

---

## 7. Environnement

- **Android** : Galaxy A57 (SM-A576B), Android 16 ; APK de débogage arm64, installé par adb sans fil.
- **Windows** : PC Windows 11, paquet portable (`scripts/package-windows.ps1`).
- **Linux** : Zorin OS, paquet `tar.gz` (`scripts/package-linux.sh`).
- **Réseaux** : plusieurs Wi-Fi réels, dont le point d'accès du Galaxy A57 et un point d'accès à 2,4 GHz tenu par un autre téléphone, où tout passe par un relais.
