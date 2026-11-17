# medXfer — Plan du jalon J8 : refonte, lecteurs, mesure, optimisation, bêta publique

> **Source :** feuille de route, J8 ; bible §14.3 (procédure de benchmark), gate G3 (§15), `DEV-21` (§17.9) ; réserves des rapports J4b, J6c, J6d et J7 ; constats de l'audit laissés « après mesure » ([medxfer-plan-audit.md](medxfer-plan-audit.md)) ; maquettes « Version 4 » du 15 au 17 novembre ; réponses de l'utilisateur du 17 novembre (§4).
>
> **Objectif :** donner à medXfer son apparence définitive et des lecteurs d'images, de vidéos et de musique ; savoir précisément où il perd du débit et gagner là où la mesure le montre ; publier une **bêta** installable sur Windows, Linux et Android, avec son guide.
>
> **Critère de sortie :**
> - la refonte « Version 4 » dans l'application, sur Android, Windows et Linux ;
> - les lecteurs d'images, de vidéos et de musique, avant l'envoi comme pour les fichiers reçus, sur les trois systèmes ;
> - la gate G3 tranchée : le débit utile atteint 80 % d'`iperf3` sur la matrice du §14.3, ou un plan écrit dit pourquoi et comment y aller ;
> - chaque optimisation mesurée avant et après, sur les mêmes appareils, archivée dans `bench-results/` ;
> - le benchmark nocturne (`DEV-21`) en CI ;
> - les 8 exigences encore 📏 de la bible ont leur test ou leur mesure, et passent à ✅ ;
> - les réserves de J7 sur les écrans levées ;
> - la bêta publiée : test ouvert du Play Store (après le test fermé imposé, D6), installateur Windows avec les règles du pare-feu, paquets Linux, l'APK et les paquets sur GitHub ;
> - `SECURITY.md`, un vrai `README` et le guide d'utilisation (reporté jusqu'ici parce que l'application changeait encore).
>
> **Durée estimée :** 18 à 26 jours, au lieu des 3 à 5 de la feuille de route. Elle prévoyait la mesure et l'ingénierie de livraison ; s'y ajoutent la refonte « Version 4 », les lecteurs (demandés le 17 novembre) et les finitions de Web Share (J7). Les 14 jours du test fermé du Play Store courent pendant la mesure et l'optimisation. Une PR par lot, CI verte sur les trois systèmes avant fusion.

## 0. État de départ (constaté sur `main` le 17 novembre 2026)

| Élément | État |
|---|---|
| Apparence | Version 3 dans l'application (Figtree, accent bleu par défaut, thème clair par défaut). Les maquettes « Version 4 » sont prêtes dans le canevas, 16 écrans. |
| Lecteurs | ❌ Aucun : l'application montre des vignettes, mais n'ouvre ni image, ni vidéo, ni musique. |
| Débits mesurés | Wi-Fi Direct à 5 GHz, PC ↔ Galaxy A57 : ≈ 52 MB/s et 38 à 40 MB/s selon le sens ; câble vers un PC rapide : 83 MB/s ; Web Share : 53 MB/s vers Chrome sur le téléphone, 48 MB/s depuis Edge, **26 MB/s depuis Chrome sur le téléphone** (réserve 1 de J7). |
| `iperf3` | ❌ **Jamais mesuré** (rapport J4b) : la cible de 80 % n'a pas encore de référence. |
| Échelle R0 à R6 | ✅ `xfer bench ladder` et `bench matrix` existent (J4a, J4b) ; mesures d'octobre dans `bench-results/` (PC Windows, Android en boucle locale, 5 GHz). |
| Petits fichiers | ⚠️ Le point faible connu : sous Windows, environ 2 ms par fichier même sans `fsync` (création NTFS, antivirus ; rapport J4b). La matrice demande 100 000 fichiers de 1 à 16 Kio, jamais mesurés. |
| CI | ✅ Tests Go sur Windows, Linux, macOS ; Flutter ; binaire de production vérifié. ❌ Pas de tâche nocturne (`DEV-21`). |
| Exigences 📏 | 8 : `BLE-11`, `CHAN-07`, `CHAN-08` (ancien téléphone sans accélération AES), `XFER-02` (chunk par défaut), `STO-04` (commit groupé à remesurer à plus haut débit), `STO-15` (dossier choisi par SAF), `STO-16` (côté application), `DEV-21`. |
| Tableau de bord web | ✅ Déjà retiré (gate G7, #183) : rien à faire ici. |
| Paquets | ⚠️ Zip portable Windows, `tar.gz` Linux, APK signé d'une clé de **développement**, version `0.1.0+1`. Pas d'installateur, pas de clé de publication. **Pas encore de compte développeur Google Play.** |
| Pare-feu Windows | ✅ L'application voit le blocage et propose « Autoriser medXfer » (2 novembre). Reste : les règles posées par l'installateur. |
| Documents | ❌ `README.md` d'une ligne, pas de `SECURITY.md`, guide d'utilisation reporté. |
| Revue G4 (cryptographie BLE) | ⏳ L'utilisateur l'organise : la bêta porte l'avertissement prévu tant qu'elle n'est pas faite. |

## 1. Ordre d'exécution

```
WP-8.0 décisions, compte Play ──► WP-8.1 refonte V4 ──► WP-8.2 lecteurs ──► WP-8.3 bêta Android, test fermé (14 jours) ─┐
                                                                                                                        ├──► WP-8.8 bêta publique ──► WP-8.9 guide, essais, rapport
                                   WP-8.4 mesure (G3) ──► WP-8.5 optimisations ──► WP-8.6 nocturne CI ─────────────────┤
                                   WP-8.7 finitions Web Share ─────────────────────────────────────────────────────────┘
```

- **La refonte passe en premier** (D1), puis les lecteurs, qui se dessinent dans la même identité : les testeurs de la bêta voient l'application définitive.
- **Le test fermé démarre dès le premier `.aab` signé** (WP-8.3) : ses 14 jours, imposés par Google, courent pendant la mesure, l'optimisation et les finitions, qui arrivent aux testeurs par des mises à jour.
- **La mesure passe avant toute optimisation** : aucun changement de performance sans chiffre avant et après. Elle peut commencer dès WP-8.1, le cœur ne dépendant pas de l'apparence ; les essais réels se font une séance à la fois.

## 2. Les lots

### WP-8.0 — Décisions et état de départ · S

- Les décisions du §4 (prises le 17 novembre).
- **Compte développeur Google Play** : créé par l'utilisateur (compte personnel, frais d'inscription uniques), avec la vérification d'identité demandée par Google.
- ✅ **Bibliothèque des lecteurs** (D8) : les paquets lus le 17 novembre ; un adaptateur par système, tous libres, rien de non maintenu livré. La taille ajoutée à l'APK est mesurée en WP-8.2.
- ✅ Bible : chaque exigence 📏 rattachée au lot qui la ferme ; G3 « en cours ».

### WP-8.1 — La refonte « Version 4 » · L

Les maquettes « Version 4 » du canevas, mêmes parcours et mêmes fonctions, nouvelle apparence :

- thème : sombre par défaut, un seul accent (quatre au choix), police Archivo (libre, licence OFL) à la place de Figtree, dans `app/lib/theme.dart` ;
- l'accueil : la sélection en plein écran, le panier flottant, les photos par jour sur 3 colonnes avec « Tout ce jour », les vidéos sur 2 colonnes, la musique en liste ;
- la vitesse en grand sur l'écran de transfert, « Passer ce fichier » distinct d'« Annuler », l'annulation confirmée ;
- le bureau (écran 16) : la colonne de navigation, la carte d'envoi avec la vitesse en grand ;
- les mêmes repères partout : case carrée pour un choix multiple, rond pour un choix unique, ↗ envoi, ↙ réception ;
- les tests de widgets mis à jour ; contrastes et zones à toucher vérifiés (44 px au moins) ;
- un essai réel de chaque parcours sur le téléphone et le PC avant la fusion.

### WP-8.2 — Les lecteurs d'images, de vidéos et de musique · L

Demandés le 17 novembre : vérifier ce qu'on envoie, et ouvrir ce qu'on reçoit, sans quitter medXfer.

- **Maquettes d'abord**, dans le canevas « Version 4 » : le bouton ⤢ sur les vignettes, les trois lecteurs sur le téléphone et sur le bureau. L'utilisateur les valide avant le code.
- **Ouvrir sans ambiguïté** (D9) : chaque vignette de Photos, Vidéos et Musique porte un bouton ⤢ visible qui ouvre le lecteur ; toucher ailleurs sélectionne, comme aujourd'hui. Dans le lecteur, la case « Sélectionner » reste à portée, et la sélection est la même que dans la grille.
- **Où** :
  - avant l'envoi, depuis les onglets de l'accueil ;
  - les fichiers reçus, depuis l'écran de réception et l'historique ;
  - sur Android, Windows et Linux.
- **Images** : plein écran, zoom au pincement et à la molette, passage à l'image voisine par glissement ou par flèches, avec Flutter seul (`InteractiveViewer`), sans bibliothèque de plus.
- **Vidéos et musique** : lecture, pause, position, volume ; passage au morceau ou à la vidéo voisin ; plein écran pour la vidéo. Une interface `MediaPlayer` dans l'application, et un adaptateur par système (D8) ; un autre pourra le remplacer sans toucher aux écrans. Un format que le système ne lit pas le dit, et propose « Ouvrir avec une autre application ».
- **Linux** : le paquet `.deb` dépend de la libmpv de la distribution ; sans elle (le `tar.gz`), les images s'ouvrent, et la vidéo et la musique disent quel paquet installer.
- **Lus là où ils sont** (`STO-16`) : aucun fichier copié pour être lu ; sous Android, l'URI `content://` directement.
- Hors de ce lot : la lecture en arrière-plan, écran éteint, et les sous-titres. À rouvrir après la bêta si les testeurs les demandent.
- Accessibilité : chaque commande nommée pour le lecteur d'écran, 44 px au moins, le clavier sur le bureau (espace, flèches, Échap).
- Tests de widgets avec un `MediaPlayer` factice ; essais réels : une photo, une vidéo de 1 Go et un morceau, sur le téléphone et sur le PC.

### WP-8.3 — La bêta Android et le test fermé · M

- **Clé de publication** : créée par l'utilisateur et gardée par lui, jamais dans le dépôt ; Google Play garde la clé de signature de l'application (signature par Play).
- Un bundle `.aab`, la version 0.9.0 (D7), le nom « medXfer (bêta) » et l'avertissement sur la revue de la cryptographie BLE.
- La fiche du Play Store : description, captures des écrans « Version 4 », politique de confidentialité (courte : medXfer n'envoie rien hors du réseau local), questionnaire sur la sécurité des données.
- **Test fermé** : au moins 12 testeurs (le groupe WhatsApp), inscrits sans interruption pendant 14 jours, comme Google l'impose aux comptes personnels récents. Les mises à jour des lots suivants leur arrivent par le même canal.

### WP-8.4 — La mesure, gate G3 · M

Procédure du §14.3, appareil par appareil, sur la matrice :

| Paire | Lien | Appareils |
|---|---|---|
| PC ↔ PC | Câble Ethernet | PC Naruto ↔ second PC (ou Zorin, disque dur) |
| PC ↔ Android | Wi-Fi Direct à 5 GHz, et le Wi-Fi de la maison | PC Naruto ↔ Galaxy A57 |
| Android ↔ Android | Point d'accès du téléphone (2,4 GHz : le TECNO ne capte pas le 5 GHz) | Galaxy A57 ↔ TECNO BF6 |
| Ancien téléphone (`CHAN-08`) | Le même | TECNO BF6 (32 bits), si sa puce n'a pas l'accélération AES (`/proc/cpuinfo`, à relever) |

- **R0** : `iperf3 -P 4 -t 30` dans les deux sens (Termux sur les téléphones, binaire libre sur les PC ; un outil de mesure, jamais livré).
- **R1 à R6** : `xfer bench ladder`, puis `bench matrix` pour le chunk et le nombre de flux (`XFER-02`).
- **Fichiers** : un fichier de 4 Gio de données aléatoires (D2), et **100 000 petits fichiers** de 1 à 16 Kio.
- Consigner CPU et température ; archiver chaque mesure dans `bench-results/`.
- **Web Share** (réserve 1 de J7) : mesurer l'envoi depuis Chrome sur le téléphone, dans Chrome lui-même (sa console réseau), avant de toucher au serveur.
- Résultat : un tableau « débit utile / iperf3 » par paire et par sens, et l'étape limitante de chacune (le rapport par étape).

### WP-8.5 — Les optimisations ciblées · L

Chaque piste n'est engagée que si WP-8.4 la désigne, et finit par sa mesure avant/après. Candidates connues :

- **Petits fichiers** : le coût par fichier (création, métadonnées, `fsync`, trames), d'abord sous Windows ;
- **Commit groupé** (`STO-04`) : remesuré au débit du câble et du 5 GHz ;
- **Émetteur** : les empreintes gardées en mémoire jusqu'à la fin (`sender.go`), la sérialisation de chaque élément pour mesurer sa taille (`offer.go`) : deux constats de l'audit laissés « après mesure » ;
- **Sens téléphone → PC** (38 à 40 MB/s contre 52) : lecture du stockage Android, nombre de flux ;
- **Chunk par défaut** (`XFER-02`) selon `bench matrix` ;
- **Envoi depuis Chrome** (Web Share), si la mesure montre que le serveur y est pour quelque chose.

Si G3 reste sous 80 % après ces lots, la décision QUIC de la bible (registre du §3) est rouverte, avec ses chiffres.

### WP-8.6 — Le benchmark nocturne · S

- `DEV-21` : chaque nuit, `bench ladder` en boucle locale sur un exécuteur Linux ; un recul de plus de 15 % d'une marche R2 à R3 fait échouer la tâche.
- La référence est le résultat de la nuit précédente sur le même exécuteur, archivé comme artefact de la CI.

### WP-8.7 — Les finitions de Web Share · M

Réserves 2 à 10 du rapport J7 :

- sur Android, un dossier se choisit depuis l'écran (réserve 2) ;
- la page de l'invité, la session finie, dit quoi faire selon qu'il a scanné le QR ou tapé l'adresse (3) ;
- la sélection est gardée après la fin d'un partage par les PIN faux (4) ;
- l'écran dit le nom du Wi-Fi quand la permission le permet (7) ;
- l'hôte voit la progression d'un téléchargement de l'invité, pas seulement son début et sa fin (10) ;
- à reproduire avant de corriger : l'adresse ouverte au troisième rechargement à 2,4 GHz (5), le triple démarrage des téléchargements de Chrome (6), « Connecter » qui crée un réseau à 2,4 GHz (8), le cœur muet après 6 h (9) ;
- en option (rapport J7, §3.3) : l'écran de l'hôte dit « Un invité voit l'avertissement de sécurité », compté sur l'appareil seulement, pour préparer les questions de la bêta.

### WP-8.8 — La bêta publique · L

- **Android** : le **test ouvert** du Play Store (D6), que tout le monde peut rejoindre, dès que Google l'autorise après le test fermé ; l'application et sa fiche marquées « bêta ». Le groupe WhatsApp continue en parallèle pour les retours. L'APK publié aussi sur GitHub.
- **Windows** : un installateur Inno Setup (D3) qui pose les règles du pare-feu (UDP 5353 mDNS, UDP 19998 balise, TCP 18887) sur les profils Privé et Public, et les retire à la désinstallation ; le zip portable gardé ; signature selon D4.
- **Linux** : `tar.gz` et `.deb` (D5), essayés sur une installation neuve (`scripts/test-fresh-linux.sh`).
- **Version** : 0.9.0 « bêta » (D7), le même numéro dans l'application, le cœur et les paquets.
- **Licences** : la liste des composants livrés et de leur licence, vérifiée libre (aucune dépendance propriétaire), affichée dans l'application.
- **`SECURITY.md`** : comment signaler une faille, ce qui est couvert, l'état de la revue G4.
- **`README.md`** : ce qu'est medXfer, les plateformes, l'installation, la compilation, les liens vers la bible et le guide.
- Une version GitHub avec les paquets, leurs empreintes SHA-256 et les notes de version.

### WP-8.9 — Guide, essais réels et rapport · M

- Le guide d'utilisation, sur les écrans définitifs : se connecter, envoyer, recevoir, ouvrir une image, une vidéo ou un morceau, partager avec un navigateur, le pare-feu de Windows, les réglages, rejoindre la bêta et donner son avis.
- Les essais réels, ensemble : une installation neuve par plateforme depuis les paquets publiés, puis un envoi dans chaque sens de la matrice.
- Le rapport J8 : G3, les gains mesurés, les exigences fermées, le bilan du test fermé, les réserves.

## 3. Hors périmètre

- macOS et iOS : J9, après la gate G8.
- La gate G5 et la décision D3 de J7 (mode HTTP du Web Share) : pendant la bêta, avec ses testeurs.
- La version 1.0.0 : après les retours de la bêta, G5 et la revue G4 (D7).
- La lecture en arrière-plan et les sous-titres (WP-8.2).
- BLE sous Linux : sur le second PC, quand Zorin y sera installé.
- Un second téléphone dans le groupe à 5 GHz (réserve 1 de J6d) : dès qu'un téléphone qui capte le 5 GHz est disponible ; la matrice s'en passe.
- QUIC : seulement si G3 échoue après WP-8.5 (décision de la bible).

## 4. Décisions

Prises le 17 novembre 2026 : D1 et D6 par l'utilisateur, D9 sur sa réponse ; D2 à D5 et D7, propositions acceptées ; D8 revue en WP-8.0, après lecture des paquets.

| # | Question | Décision | Pourquoi |
|---|---|---|---|
| D1 | Où place-t-on la refonte « Version 4 » ? | ✅ **En premier** (WP-8.1), puis les lecteurs | Demande de l'utilisateur : la bêta montre l'apparence définitive, et le guide ne s'écrit qu'une fois. |
| D2 | La taille du gros fichier de mesure | ✅ **4 Gio** (sur `M:` côté PC), au lieu des 10 Gio du §14.3, et l'échelle R1 à R5 sans fichier | Le disque `C:` n'a que 8 Go de libre et le téléphone partage son stockage ; à 50 MB/s, 4 Gio durent 80 s, assez pour un débit stable. La différence est notée dans le rapport. |
| D3 | L'installateur Windows | ✅ **Inno Setup** (libre), avec le zip portable gardé | Il pose les règles du pare-feu et les retire, sans exiger de signature comme MSIX. |
| D4 | La signature du code sous Windows | ✅ **Demander SignPath Foundation** (signature gratuite pour les projets libres), sans signature en attendant | Un certificat payant coûte chaque année ; sans signature, Windows SmartScreen avertit au premier lancement, ce que le guide expliquera. |
| D5 | Les paquets Linux | ✅ **`tar.gz` et `.deb`** | `.deb` couvre Zorin, Ubuntu et Debian ; Flatpak ou AppImage plus tard si des testeurs le demandent. |
| D6 | La distribution Android | ✅ **Test ouvert du Play Store**, ouvert à tous, l'application marquée « bêta » ; WhatsApp en parallèle pour les retours ; l'APK aussi sur GitHub | Demande de l'utilisateur. Un compte personnel récent doit d'abord tenir un **test fermé** de 12 testeurs pendant 14 jours avant toute publication plus large (règle de Google, à revérifier à l'inscription) : le groupe WhatsApp le fournit (WP-8.3), puis le test ouvert prend le relais (WP-8.8). |
| D7 | Le numéro de la version publiée | ✅ **0.9.0, « bêta »**, puis 1.0.0 après les retours de la bêta, la gate G5 et la revue G4 | Une v1 annoncée avant G5 et la revue G4 promettrait plus qu'elle ne tient ; la bêta porte l'avertissement sur la cryptographie BLE. |
| D8 | La bibliothèque de lecture vidéo et audio | ✅ **Revue en WP-8.0 (17 novembre)** : le lecteur officiel de Flutter, `video_player` (BSD-3), derrière l'interface `MediaPlayer`, avec un adaptateur par système : **Android**, son implémentation officielle sur ExoPlayer (Media3, Apache 2.0) ; **Windows**, `video_player_win` (BSD-3), qui lit par Media Foundation, intégré à Windows (une DLL d'environ 130 Ko, aucune bibliothèque tierce) ; **Linux**, `media_kit` (MIT) sur la libmpv **du système**, déclarée comme dépendance du paquet `.deb` et jamais livrée par medXfer. `media_kit` partout est écarté | Vérifié dans les paquets publiés : sous Windows, `media_kit` télécharge une libmpv de septembre 2023, d'un projet de compilation archivé depuis octobre 2024, dont la licence (LGPL ou GPL) n'est écrite nulle part ; un FFmpeg de 2023 livré ne recevrait plus de corrections de sécurité. Sous Android, il livre la variante « default » de libmpv, en plus d'ExoPlayer qu'on peut avoir seul. Un format que le système ne lit pas (certains MKV sous Windows, par exemple) donne un message clair et « Ouvrir avec une autre application ». |
| D9 | Comment ouvrir le lecteur depuis une vignette | ✅ **Un bouton ⤢ visible** sur chaque vignette ; toucher ailleurs sélectionne | Réponse de l'utilisateur : rien de caché, et la sélection garde la plus grande zone. |

## 5. Risques

| Risque | Parade |
|---|---|
| La refonte casse des parcours déjà essayés | Les tests de widgets d'abord, puis un essai réel de chaque parcours avant la fusion de WP-8.1. |
| Un lecteur ne lit pas un format courant (Media Foundation sous Windows est plus limité qu'ExoPlayer) | Les formats essayés en WP-8.2 sur chaque système et listés dans le guide ; le message et « Ouvrir avec une autre application » ; l'interface `MediaPlayer` permet un autre adaptateur. |
| La mesure de la matrice demande beaucoup d'essais réels, et un seul travail lourd à la fois sur le PC | Une paire par séance, préparée à l'avance (fichiers sur `M:`, `iperf3` installé) ; les gestes physiques par l'utilisateur. |
| G3 échoue sur le Wi-Fi pour une cause radio, pas logicielle | Le rapport par étape sépare l'attente du réseau du reste ; le câble sert de contrôle. |
| Le test fermé est refusé ou recommence (un testeur qui se désinscrit, une activité jugée trop faible) | Plus de 12 testeurs inscrits, une mise à jour par semaine pour les faire revenir ; la demande d'accès à la publication faite dès les 14 jours atteints. |
| Le Play Store demande des formalités (vérification d'identité, fiche, politique de confidentialité, sécurité des données) | Préparées dès WP-8.0 ; la politique de confidentialité est courte, medXfer n'envoyant rien hors du réseau local. |
| La signature gratuite n'est pas accordée à temps | La bêta part sans signature, avec l'explication dans le guide. |
