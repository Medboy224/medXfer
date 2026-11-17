# medXfer — Plan du jalon J8 : mesure, optimisation, finitions v1

> **Source :** feuille de route, J8 ; bible §14.3 (procédure de benchmark), gate G3 (§15), `DEV-21` (§17.9) ; réserves des rapports J4b, J6c, J6d et J7 ; constats de l'audit laissés « après mesure » ([medxfer-plan-audit.md](medxfer-plan-audit.md)) ; maquettes « Version 4 » du 15 au 17 novembre.
>
> **Objectif :** savoir précisément où medXfer perd du débit, gagner là où la mesure le montre, puis livrer une première version publique installable sur Windows, Linux et Android, avec son guide.
>
> **Critère de sortie :**
> - la gate G3 tranchée : le débit utile atteint 80 % d'`iperf3` sur la matrice du §14.3, ou un plan écrit dit pourquoi et comment y aller ;
> - chaque optimisation mesurée avant et après, sur les mêmes appareils, archivée dans `bench-results/` ;
> - le benchmark nocturne (`DEV-21`) en CI ;
> - les 8 exigences encore 📏 de la bible ont leur test ou leur mesure, et passent à ✅ ;
> - les réserves de J7 sur les écrans levées ;
> - des paquets publiables : installateur Windows (avec les règles du pare-feu), paquets Linux, Android signé avec la clé de publication et déposé en test fermé sur le Play Store ;
> - `SECURITY.md`, un vrai `README` et le guide d'utilisation (reporté jusqu'ici parce que l'application changeait encore).
>
> **Durée estimée :** 12 à 18 jours, au lieu des 3 à 5 de la feuille de route. Elle prévoyait la mesure et l'ingénierie de livraison ; s'y ajoutent les finitions de Web Share (J7) et, selon la décision D1, la refonte « Version 4 ». Une PR par lot, CI verte sur les trois systèmes avant fusion.

## 0. État de départ (constaté sur `main` le 17 novembre 2026)

| Élément | État |
|---|---|
| Débits mesurés | Wi-Fi Direct à 5 GHz, PC ↔ Galaxy A57 : ≈ 52 MB/s et 38 à 40 MB/s selon le sens ; câble vers un PC rapide : 83 MB/s ; Web Share : 53 MB/s vers Chrome sur le téléphone, 48 MB/s depuis Edge, **26 MB/s depuis Chrome sur le téléphone** (réserve 1 de J7). |
| `iperf3` | ❌ **Jamais mesuré** (rapport J4b) : la cible de 80 % n'a pas encore de référence. |
| Échelle R0 à R6 | ✅ `xfer bench ladder` et `bench matrix` existent (J4a, J4b) ; mesures d'octobre dans `bench-results/` (PC Windows, Android en boucle locale, 5 GHz). |
| Petits fichiers | ⚠️ Le point faible connu : sous Windows, environ 2 ms par fichier même sans `fsync` (création NTFS, antivirus ; rapport J4b). La matrice demande 100 000 fichiers de 1 à 16 Kio, jamais mesurés. |
| CI | ✅ Tests Go sur Windows, Linux, macOS ; Flutter ; binaire de production vérifié. ❌ Pas de tâche nocturne (`DEV-21`). |
| Exigences 📏 | 8 : `BLE-11`, `CHAN-07`, `CHAN-08` (ancien téléphone sans accélération AES), `XFER-02` (chunk par défaut), `STO-04` (commit groupé à remesurer à plus haut débit), `STO-15` (dossier choisi par SAF), `STO-16` (côté application), `DEV-21`. |
| Tableau de bord web | ✅ Déjà retiré (gate G7, #183) : rien à faire ici. |
| Paquets | ⚠️ Zip portable Windows, `tar.gz` Linux, APK signé d'une clé de **développement**, version `0.1.0+1`. Pas d'installateur, pas de clé de publication, pas de Play Store. |
| Pare-feu Windows | ✅ L'application voit le blocage et propose « Autoriser medXfer » (2 novembre). Reste : les règles posées par l'installateur. |
| Documents | ❌ `README.md` d'une ligne, pas de `SECURITY.md`, guide d'utilisation reporté. |
| Revue G4 (cryptographie BLE) | ⏳ L'utilisateur l'organise : la version publiée porte l'avertissement prévu tant qu'elle n'est pas faite. |

## 1. Ordre d'exécution

```
WP-8.0 décisions ──► WP-8.1 mesure (G3) ──► WP-8.2 optimisations ──► WP-8.3 nocturne CI
                └──► WP-8.4 finitions Web Share ──► WP-8.5 refonte V4 (D1) ──► WP-8.6 livraison ──► WP-8.7 guide, essais réels, rapport
```

La mesure passe avant toute optimisation : aucun changement de performance sans chiffre avant et après. Les finitions des écrans avancent en parallèle de la mesure, qui demande surtout des essais réels.

## 2. Les lots

### WP-8.0 — Décisions et état de départ · S

- Faire trancher les décisions du §4.
- Bible : rattacher chaque exigence 📏 au lot qui la ferme ; noter G3 « en cours ».

### WP-8.1 — La mesure, gate G3 · M

Procédure du §14.3, appareil par appareil, sur la matrice :

| Paire | Lien | Appareils |
|---|---|---|
| PC ↔ PC | Câble Ethernet | PC Naruto ↔ second PC (ou Zorin, disque dur) |
| PC ↔ Android | Wi-Fi Direct à 5 GHz, et le Wi-Fi de la maison | PC Naruto ↔ Galaxy A57 |
| Android ↔ Android | Point d'accès du téléphone (2,4 GHz : le TECNO ne capte pas le 5 GHz) | Galaxy A57 ↔ TECNO BF6 |
| Ancien téléphone (`CHAN-08`) | Le même | TECNO BF6 (32 bits), si sa puce n'a pas l'accélération AES (`/proc/cpuinfo`, à relever) |

- **R0** : `iperf3 -P 4 -t 30` dans les deux sens (Termux sur les téléphones, binaire libre sur les PC ; un outil de mesure, jamais livré).
- **R1 à R6** : `xfer bench ladder`, puis `bench matrix` pour le chunk et le nombre de flux (`XFER-02`).
- **Fichiers** : un gros fichier de données aléatoires (taille : D2), et **100 000 petits fichiers** de 1 à 16 Kio.
- Consigner CPU et température ; archiver chaque mesure dans `bench-results/`.
- **Web Share** (réserve 1 de J7) : mesurer l'envoi depuis Chrome sur le téléphone, dans Chrome lui-même (sa console réseau), avant de toucher au serveur.
- Résultat : un tableau « débit utile / iperf3 » par paire et par sens, et l'étape limitante de chacune (le rapport par étape).

### WP-8.2 — Les optimisations ciblées · L

Chaque piste n'est engagée que si WP-8.1 la désigne, et finit par sa mesure avant/après. Candidates connues :

- **Petits fichiers** : le coût par fichier (création, métadonnées, `fsync`, trames), d'abord sous Windows ;
- **Commit groupé** (`STO-04`) : remesuré au débit du câble et du 5 GHz ;
- **Émetteur** : les empreintes gardées en mémoire jusqu'à la fin (`sender.go`), la sérialisation de chaque élément pour mesurer sa taille (`offer.go`) : deux constats de l'audit laissés « après mesure » ;
- **Sens téléphone → PC** (38 à 40 MB/s contre 52) : lecture du stockage Android, nombre de flux ;
- **Chunk par défaut** (`XFER-02`) selon `bench matrix` ;
- **Envoi depuis Chrome** (Web Share), si la mesure montre que le serveur y est pour quelque chose.

Si G3 reste sous 80 % après ces lots, la décision QUIC de la bible (registre du §3) est rouverte, avec ses chiffres.

### WP-8.3 — Le benchmark nocturne · S

- `DEV-21` : chaque nuit, `bench ladder` en boucle locale sur un exécuteur Linux ; un recul de plus de 15 % d'une marche R2 à R3 fait échouer la tâche.
- La référence est le résultat de la nuit précédente sur le même exécuteur, archivé comme artefact de la CI.

### WP-8.4 — Les finitions de Web Share · M

Réserves 2 à 10 du rapport J7 :

- sur Android, un dossier se choisit depuis l'écran (réserve 2) ;
- la page de l'invité, la session finie, dit quoi faire selon qu'il a scanné le QR ou tapé l'adresse (3) ;
- la sélection est gardée après la fin d'un partage par les PIN faux (4) ;
- l'écran dit le nom du Wi-Fi quand la permission le permet (7) ;
- l'hôte voit la progression d'un téléchargement de l'invité, pas seulement son début et sa fin (10) ;
- à reproduire avant de corriger : l'adresse ouverte au troisième rechargement à 2,4 GHz (5), le triple démarrage des téléchargements de Chrome (6), « Connecter » qui crée un réseau à 2,4 GHz (8), le cœur muet après 6 h (9) ;
- en option (rapport J7, §3.3) : l'écran de l'hôte dit « Un invité voit l'avertissement de sécurité », compté sur l'appareil seulement, pour préparer les questions de la bêta.

### WP-8.5 — La refonte « Version 4 » · L (selon D1)

Les maquettes « Version 4 » du canevas, mêmes parcours et mêmes fonctions, nouvelle apparence :

- thème : sombre par défaut, un seul accent (quatre au choix), police Archivo (libre, licence OFL) à la place de Figtree, dans `app/lib/theme.dart` ;
- l'accueil : la sélection en plein écran, le panier flottant, les photos par jour sur 3 colonnes avec « Tout ce jour », les vidéos sur 2 colonnes, la musique en liste ;
- la vitesse en grand sur l'écran de transfert, « Passer ce fichier » distinct d'« Annuler », l'annulation confirmée ;
- les mêmes repères partout : case carrée pour un choix multiple, rond pour un choix unique, ↗ envoi, ↙ réception ;
- les tests de widgets mis à jour ; contrastes et zones à toucher vérifiés (44 px au moins).

### WP-8.6 — L'ingénierie de livraison · L

- **Windows** : un installateur (D3) qui pose les règles du pare-feu (UDP 5353 mDNS, UDP 19998 balise, TCP 18887) sur les profils Privé et Public, et les retire à la désinstallation ; signature selon D4.
- **Linux** : les paquets de D5, essayés sur une installation neuve (`scripts/test-fresh-linux.sh`).
- **Android** : la clé de publication (créée et gardée par l'utilisateur, jamais dans le dépôt), un bundle `.aab`, la fiche et le test fermé du Play Store pour la bêta (D6).
- **Version** : numéro et nom selon D7, le même dans l'application, le cœur et les paquets.
- **Licences** : la liste des composants livrés et de leur licence, vérifiée libre (aucune dépendance propriétaire), affichée dans l'application.
- **`SECURITY.md`** : comment signaler une faille, ce qui est couvert, l'état de la revue G4.
- **`README.md`** : ce qu'est medXfer, les plateformes, l'installation, la compilation, les liens vers la bible et le guide.

### WP-8.7 — Guide, essais réels et rapport · M

- Le guide d'utilisation, sur les écrans définitifs (après WP-8.5) : se connecter, envoyer, recevoir, partager avec un navigateur, le pare-feu de Windows, les réglages.
- Les essais réels, ensemble : une installation neuve par plateforme depuis les paquets publiés, puis un envoi dans chaque sens de la matrice.
- Le rapport J8 : G3, les gains mesurés, les exigences fermées, les réserves.

## 3. Hors périmètre

- macOS et iOS : J9, après la gate G8.
- La gate G5 et la décision D3 de J7 (mode HTTP du Web Share) : à la bêta, avec ses testeurs.
- BLE sous Linux : sur le second PC, quand Zorin y sera installé.
- Un second téléphone dans le groupe à 5 GHz (réserve 1 de J6d) : dès qu'un téléphone qui capte le 5 GHz est disponible ; la matrice s'en passe.
- QUIC : seulement si G3 échoue après WP-8.2 (décision de la bible).

## 4. Décisions

| # | Question | Proposition | Pourquoi |
|---|---|---|---|
| D1 | La refonte « Version 4 » entre-t-elle dans J8 ? | **Oui, en WP-8.5**, avant la livraison | La bêta du Play Store est la première impression : ses testeurs doivent voir l'apparence définitive, et le guide ne s'écrit qu'une fois. |
| D2 | La taille du gros fichier de mesure | **4 Gio** (sur `M:` côté PC), au lieu des 10 Gio du §14.3, et l'échelle R1 à R5 sans fichier | Le disque `C:` n'a que 8 Go de libre et le téléphone partage son stockage ; à 50 MB/s, 4 Gio durent 80 s, assez pour un débit stable. La différence est notée dans le rapport. |
| D3 | L'installateur Windows | **Inno Setup** (libre), avec le zip portable gardé | Il pose les règles du pare-feu et les retire, sans exiger de signature comme MSIX ; c'est le choix courant des applications Flutter pour Windows. |
| D4 | La signature du code sous Windows | **Demander SignPath Foundation** (signature gratuite pour les projets libres), sans signature en attendant | Un certificat payant coûte chaque année ; sans signature, Windows SmartScreen avertit au premier lancement, ce que le guide expliquera. |
| D5 | Les paquets Linux | **`tar.gz` et `.deb`** | `.deb` couvre Zorin, Ubuntu et Debian ; Flatpak ou AppImage plus tard si des testeurs le demandent. |
| D6 | La distribution Android | **Test fermé du Play Store** pour la bêta, et l'APK publié sur GitHub | C'est le canal choisi pour la bêta (testeurs réunis sur WhatsApp) ; F-Droid plus tard, l'application étant entièrement libre. |
| D7 | Le numéro de la première version publique | **0.9.0, « bêta »**, puis 1.0.0 après les retours de la bêta et la gate G5 | Une v1 annoncée avant G5 et la revue G4 promettrait plus qu'elle ne tient ; la bêta porte l'avertissement sur la cryptographie BLE. |

## 5. Risques

| Risque | Parade |
|---|---|
| La mesure de la matrice demande beaucoup d'essais réels, et un seul travail lourd à la fois sur le PC | Une paire par séance, préparée à l'avance (fichiers sur `M:`, `iperf3` installé) ; les gestes physiques par l'utilisateur. |
| G3 échoue sur le Wi-Fi pour une cause radio, pas logicielle | Le rapport par étape sépare l'attente du réseau du reste ; le câble sert de contrôle. |
| La refonte V4 casse des parcours déjà essayés | Les tests de widgets d'abord, puis un essai réel de chaque parcours avant WP-8.6. |
| Le Play Store demande des formalités (fiche, politique de confidentialité, compte développeur) | Préparées dès WP-8.0 ; la politique de confidentialité est courte, medXfer n'envoyant rien hors du réseau local. |
| Un compte développeur personnel récent doit faire tourner un test fermé (une douzaine de testeurs, deux semaines, d'après les règles de Google à vérifier au moment de l'inscription) avant toute publication ouverte | C'est justement la bêta prévue : le groupe WhatsApp fournit les testeurs ; le test fermé commence dès que le premier `.aab` est signé, sans attendre la fin de J8. |
| La signature gratuite n'est pas accordée à temps | La bêta part sans signature, avec l'explication dans le guide. |
