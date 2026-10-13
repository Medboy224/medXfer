# medXfer — Plan détaillé du jalon J4b (stockage v1)

> **Source :** feuille de route §3 « J4b » ; bible chapitre 9 (stockage), §8.3 (clé de reprise), chapitre 17 (`DEV-07`, échelle R5 et R6, `bench matrix`), §14.2 (chaos et fuzzing).
> **Objectif :** ne jamais livrer un fichier incorrect, ne jamais perdre plus qu'une courte fenêtre de travail après une coupure, ne jamais toucher à ce qui n'appartient pas au transfert. Concrètement :
> - les octets s'écrivent dans `<nom>.part`, et le fichier ne prend son nom qu'une fois complet et vérifié ;
> - un **journal** de 40 octets par chunk dit exactement ce qui est durable, et garde les hash pour le digest ;
> - chaque transfert de plusieurs fichiers est reçu dans **un dossier racine neuf**, par `os.Root` ;
> - un disque plein **met en pause**, il ne fait pas échouer ;
> - un **test de chaos** de 1 000 itérations (arrêt brutal, coupure, disque plein) ne produit aucune corruption.
>
> **Durée estimée :** 6 à 9 jours. Une PR par lot, CI verte sur les trois systèmes avant fusion.

## 0. État de départ (constaté sur `main` après J4a)

| Constat | Conséquence pour J4b |
|---|---|
| `engine.DiskManager` écrit **directement sous le nom final** : un fichier partiel apparaît sous son vrai nom (bible §9.2). | WP-4.6 : `<nom>.part`, renommage à la fin (`STO-01`, `STO-02`). |
| L'état de reprise (`<nom>.medxfer`) est un en-tête de 32 octets (le `FileID`) suivi d'un octet par chunk. Pas de magic, pas de CRC, pas de hash : une queue déchirée ou un état forgé ne sont pas détectés, et le digest doit **relire** les chunks repris, des deux côtés (rapport J4a, limite connue). | WP-4.6 : journal du §9.3 (`MXST`, enregistrements de 40 octets avec SHA-256 et CRC), vérification des 8 derniers chunks (`STO-05`, `STO-06`). |
| La clé de reprise est le `FileID` = MD5(nom, taille, date) **calculé par l'émetteur** et envoyé dans l'offre. | D2 : clé de reprise = SHA-256(empreinte de l'émetteur ‖ chemin ‖ taille ‖ date), calculée par le receveur (bible §8.3). |
| L'invariant J (`fsync` des données, puis état, puis `fsync` de l'état) et le commit groupé (64 Mio ou 2 s) existent déjà dans `DiskManager.flush`. | Repris tels quels dans le nouveau journal. |
| Collisions résolues **fichier par fichier** : un dossier reçu se fusionne avec un dossier existant du même nom. | WP-4.7 : un dossier racine neuf par transfert (`STO-10`). |
| Aucun `os.Root` : chemins validés par `SafeRelPath`, puis `filepath.Join`. Fenêtre TOCTOU sur les liens symboliques. En Go 1.24, `os.Root` n'a ni `MkdirAll` ni `Rename`. | WP-4.7 et D4. |
| `Offer.Validate` refuse les chemins en double exacts (#75), pas ceux qui ne diffèrent que par la casse. | WP-4.7 (`STO-14`). |
| Un disque plein fait **échouer** l'élément (`TestEngineReportsFullDisk`) ; aucune vérification d'espace avant `accept`. | WP-4.8 (`STO-07`, bible §9.7). |
| Le daemon lance une sonde disque « pre-flight » qui écrit 8 Mio **dans le dossier de réception** pendant le transfert (#36). | WP-4.8 et D10. |
| `bench ladder` s'arrête à R4 ; pas de `bench matrix` ; flux et chunk par défaut (4 × 4 Mio) jamais mesurés sur appareils. | WP-4.9. |
| Pas de test de chaos ; `FuzzReadFrame` existe, pas de fuzzing du journal ni de `SafeRelPath`. | WP-4.10. |
| `StreamTar` retiré en J4a (D3 de J4a) ; la mesure sur 100 000 petits fichiers reste à faire. | WP-4.11. |
| Un partiel périmé (source changée) reste sous le nom final, et la nouvelle version part vers « nom (1) » (rapport J4a). Une offre de plusieurs chemins s'appelle `batch_N_files`. | WP-4.6 (le partiel devient un `.part` avec sa clé) et D5. |
| Issue ouverte rattachée : #36. | Fermée par WP-4.8. |

## 1. Ordre d'exécution et dépendances

```
D4 (version de Go) ──► WP-4.6 .part, journal, reprise, finalisation ──► WP-4.7 racine par transfert, os.Root
                                   │                                              │
                                   ▼                                              ▼
                       WP-4.8 espace disque, disque plein ◄────────────────────────┘
                                   │
                                   ▼
                       WP-4.9 R5, R6, bench matrix, valeurs par défaut
                                   │
                                   ▼
                       WP-4.10 chaos 1 000 itérations, fuzzing ──► WP-4.11 décision StreamTar ──► sortie de J4
```

WP-4.9 peut commencer dès WP-4.6 (R5 a besoin du journal) ; WP-4.10 a besoin de WP-4.6 à WP-4.8.

## 2. Les lots

### WP-4.6 — `.part`, journal, reprise, finalisation (`STO-01` à `STO-06`, `STO-08`) · L

**Nouveau paquet `pkg/store`** (D1), qui remplace `engine.DiskManager` côté receveur.

**Fichiers :** `<racine>/<chemin>.part` (données, écrites à leur offset) et `<racine>/<chemin>.part.state` (journal).

**Journal** (bible §9.3) :
- en-tête : magic `MXST`, version, clé de reprise (32 octets), taille du fichier (u64), taille de chunk (u32), nombre de chunks (u32), CRC32 de l'en-tête ;
- enregistrements de 40 octets : `index` u32 ‖ `sha256` ‖ CRC32 des 36 premiers octets ;
- écriture en ajout seulement.

**Commit groupé** (`STO-03`, `STO-04`) : invariant J (`fsync` du `.part`, puis écriture des enregistrements, puis `fsync` du journal), toutes les 64 Mio ou 2 s (paramètres, règle R4). Commit aussi à la pause, à l'annulation et à la fin.

**Reprise** (bible §9.5) :
1. en-tête invalide, ou taille, chunk ou clé différents de l'offre : l'état n'est pas repris ;
2. lecture des enregistrements jusqu'au premier CRC invalide ; la queue déchirée est tronquée ;
3. **vérification des 8 derniers chunks** déclarés complets (SHA-256 relu depuis le `.part`) ; un désaccord marque le chunk manquant ; option `verify=full` ;
4. seuls les chunks absents sont demandés.

**Digest sans relecture :** les hash du journal alimentent le digest de fin. La relecture des chunks repris disparaît côté receveur (limite connue de J4a). Côté émetteur, elle demeure pour les chunks qu'il n'a pas servis dans la session.

**Finalisation** (bible §9.6) : tous les chunks présents → digest identique → `fsync` du `.part` → fermeture → **renommage** `.part` → nom final (sous Windows, 3 nouvelles tentatives espacées de 100 ms en cas de verrou d'antivirus) → `fsync` du dossier parent (Unix) → suppression du journal → événement « terminé ».

**Ancien format :** voir D3.

**Tests :**
- invariant J sur disque simulé : coupure de courant entre chaque étape, et le journal ne ment jamais (tests actuels de durabilité, portés) ;
- queue déchirée tronquée ;
- en-tête ou clé faux : l'état n'est pas repris ;
- chunk corrompu parmi les 8 derniers : redemandé ;
- `verify=full` ;
- le digest n'est plus relu côté receveur (compteur de lectures) ;
- renommage réessayé sous verrou (Windows, fichier tenu ouvert par le test) ;
- aucun fichier ne porte son nom final avant la fin.

### WP-4.7 — Racine par transfert, `os.Root`, doublons (`STO-09` à `STO-14`) · M

- **Racine neuve** (`STO-10`) : une offre de plusieurs éléments est reçue dans `<destination>/<racine>`, nom résolu **une fois** par la politique de collision. À l'intérieur, rien n'est fusionné avec l'existant. Un fichier seul reste reçu directement dans la destination. Voir D5 pour « écraser » et pour le nom d'une offre de plusieurs chemins.
- **`os.Root`** (`STO-13`) : toutes les opérations sur fichiers du receveur passent par un `os.Root` ouvert sur la destination (D4).
- **`PATH_CONFLICT`** (`STO-11`) : un composant qui existe avec le mauvais type fait échouer l'élément ; rien n'est supprimé.
- **Jamais de suppression** (`STO-09`) : seuls les `.part` et `.part.state` du transfert courant sont supprimables, après vérification de leur clé de reprise.
- **Doublons à la casse près** (`STO-14`) : `Offer.Validate` refuse deux chemins identiques à la casse près (D6).
- **Tests :**
  - dossier reçu deux fois → « dossier (1) », rien de fusionné ;
  - lien symbolique sortant planté dans la destination, chemin passant par ce lien : refusé (`os.Root`) ;
  - fichier existant à la place d'un dossier → `PATH_CONFLICT`, fichier intact ;
  - offre `A.txt` + `a.txt` refusée ;
  - un `.part` d'une autre clé n'est jamais supprimé.

### WP-4.8 — Espace disque, disque plein (`STO-07`, bible §9.7, #36) · M

- **Avant `accept`** : espace libre ≥ taille restant à recevoir + max(1 %, 64 Mio). Sinon, la ligne de commande refuse avec un message clair ; le daemon le signale dans `incoming_offer`, et `respond_offer` est refusé (D9).
- **Disque plein en cours de route** (`ENOSPC`, `ERROR_DISK_FULL`) : le transfert passe **en pause**. Les `.part` et le journal sont gardés, un événement `DISK_FULL` est émis, et la reprise est demandée par l'utilisateur.
- **#36** : la sonde « pre-flight » n'écrit plus dans le dossier de réception (D10).
- **Tests :**
  - `faultfs` : disque plein → pause et non échec, puis reprise après libération ;
  - offre plus grande que l'espace libre → refusée avant tout octet ;
  - aucun fichier de sonde dans le dossier de réception (#36).

### WP-4.9 — R5, R6, `bench matrix`, valeurs par défaut (`DEV-07`) · M

- **R5** : R4 + journal et `fsync` groupé (le vrai `pkg/store`, sur une offre `bench`).
- **R6** : transfert complet (offre, acceptation, digest, renommage), par le même chemin que `xfer send` / `xfer recv`.
- **`xfer bench matrix`** : balaye flux ∈ {1, 2, 4, 8} × chunk ∈ {1, 2, 4, 8} Mio. Propose la combinaison à moins de 5 % du meilleur débit avec la plus faible mémoire (`N × 2 × chunk`). Résultats dans `bench-results/*.json`.
- **Valeurs par défaut** : choisies d'après `bench matrix` entre le PC et le téléphone, sur le hotspot direct (D8).
- **Tests :** R5 et R6 en boucle locale ; matrice réduite en test ; règle de choix des valeurs par défaut.

### WP-4.10 — Chaos et fuzzing (§14.2) · M à L

- **Chaos** : 1 000 itérations à graine fixe. Chacune transfère un petit lot (petits fichiers, fichier de plusieurs chunks, fichier vide), y injecte **une** faute tirée au sort, puis reprend jusqu'au bout. Fautes :
  - **arrêt brutal du receveur** (processus `xfer recv` tué : rien n'est fermé proprement) ;
  - coupure réseau en plein chunk (`faultconn`) ;
  - disque plein (`faultfs`) ;
  - écriture déchirée (`faultfs`) ;
  - `fsync` qui ment, suivi d'une « coupure de courant » simulée.
- **Après chaque itération**, on vérifie :
  - tous les fichiers sont identiques à la source (SHA-256), ou bien encore en `.part` reprenable ;
  - aucun fichier n'est hors de la destination ;
  - aucun fichier préexistant n'a été modifié ;
  - aucun fichier final n'existe sans être complet.
- **Où ça tourne** : 50 itérations en CI (étiquette `diag`), 1 000 en local, une tâche lourde à la fois (D7). Une graine en échec est consignée et rejouable.
- **Fuzzing** : analyseur du journal, `SafeRelPath`, `Offer.Validate` ; `FuzzReadFrame` existe déjà.

### WP-4.11 — Décision sur `StreamTar` · S

- On mesure 100 000 petits fichiers (1 à 16 Kio) dans une offre, entre le PC et le téléphone, débit et durée.
- On garde l'offre seule si le débit en petits fichiers reste dans un rapport raisonnable à celui des gros fichiers ; sinon, on rouvre la question (bible §8.3, ⏸). Le chiffre et la décision sont consignés dans la bible.

**Résultat (13 octobre 2026) :**
- **Le constat :** 100 000 fichiers de 1 à 16 Kio (872 Mo), du PC vers le téléphone, mettaient 338 s en 5 GHz, soit 2,6 MB/s : 15 fois moins qu'un gros fichier. Le rapport n'était pas raisonnable.
- **La cause n'est pas le protocole :** dans un puits nul, il traite 5 400 à 10 800 petits fichiers par seconde. Ce qui coûte, c'est la durabilité de chaque fichier : sur le téléphone, le `fsync` du fichier et celui de son dossier font plus de 80 % du temps.
- **StreamTar est retiré pour de bon :** il écrirait les mêmes fichiers.
- **Les remèdes, validés le même jour :**
  - les fichiers reçus d'une traite dans le dossier racine sont rendus durables par lots, avec un `syncfs` par lot sous Linux et Android (bible §9.6) ;
  - une offre de petits fichiers ouvre 8 connexions au lieu de 4.
- **En 2,4 GHz, sur les mêmes 100 000 fichiers :** 9 min 58 s avant, 3 min 26 s avec le lot seul, **2 min 34 s** avec le lot et 8 connexions, soit 3,9 fois plus vite.
- Détails : `bench-results/README.md`.

## 3. Critère de sortie de J4b (et de J4)

1. **Chaos :** 1 000 itérations sans corruption, sans fichier hors de la destination, sans fichier préexistant modifié ; 50 itérations vertes en CI.
2. **Échelle :** un transfert complet est mesuré sur l'échelle de R1 à R6 entre le PC et le téléphone, et les valeurs par défaut sont choisies par `bench matrix`.
3. **Essai réel PC ↔ téléphone :** réception tuée (`kill -9`) en plein gros fichier, puis reprise : seuls les chunks manquants sont redemandés, et le hash est identique. Disque plein simulé : pause, puis reprise.
4. **Performance :** pas plus de 10 % de baisse entre deux appareils par rapport à J4a ; le coût de R5 (journal et `fsync`) est mesuré et consigné.
5. **Issues et gate :** #36 fermée ; G6 fermée (fait en J4a).

## 4. Hors périmètre

- Écriture directe dans `Download/medXfer/` sur Android et repli MediaStore (`STO-15`) : J6b.
- Interface du disque plein et des conflits (Flutter) : J6a et J6b ; J4b fournit les événements.
- Appairage par PIN (gate G1) : J5.

## 5. Décisions à valider avec ce plan

**D1 à D10 validées le 12 octobre 2026.**

| Id | Décision | Raison |
|---|---|---|
| D1 | Un nouveau paquet **`pkg/store`** porte le `.part`, le journal, la reprise, la finalisation et, en WP-4.7, `SafeRelPath`, les collisions et `os.Root`. `engine.DiskManager` disparaît ; `engine` ne garde que les diagnostics et les crochets de test. | Même principe que `pkg/transfer` en J4a : le code qui survivra dans la bibliothèque de J6a, sans dépendance vers le daemon. |
| D2 | **Clé de reprise** = SHA-256(empreinte de l'émetteur ‖ chemin ‖ taille ‖ date de modification), calculée **par le receveur** à partir de l'offre et de la clé épinglée. Le `FileID` (MD5) sort de l'offre. | Bible §8.3. L'empreinte empêche qu'un autre appareil reprenne un partiel qui n'est pas le sien ; MD5 disparaît du protocole. |
| D3 | **Pas de reprise des partiels d'avant J4b** (`.medxfer`) : ils ne sont ni repris ni supprimés (`STO-09`). Un fichier dont un ancien partiel occupe le nom part vers « nom (1) ». | Pas de compatibilité ascendante (D5 de J4a) ; ne jamais supprimer ce qu'on n'a pas créé. |
| D4 | **Monter Go à 1.25** (dernière version corrective) pour `os.Root.MkdirAll` et `os.Root.Rename`. Sinon, en restant en 1.24 : dossiers créés composant par composant, et renommage par `os.Rename` après vérification par le `Root`, avec une petite fenêtre TOCTOU. | `STO-13` sans fenêtre TOCTOU. Les binaires Android sont compilés sur le PC : seuls le PC et la CI changent de version. Termux propose déjà Go 1.26.4 (constaté le 12 octobre 2026), si l'on compile un jour sur le téléphone. |
| D5 | **Racine** : un dossier offert garde son nom ; une offre de plusieurs chemins reçoit une racine `medXfer <date et heure>` (au lieu de `batch_N_files`). En collision de racine, **« écraser » se comporte comme « renommer »** : une racine n'est jamais vidée ni fusionnée. « Ignorer » refuse l'offre entière. | `STO-09` et `STO-10` : écraser un dossier supposerait de supprimer des fichiers de l'utilisateur. « Écraser » garde son sens pour un fichier seul. |
| D6 | Les doublons à la casse près sont refusés **sur tous les systèmes**, y compris Linux et Android. | Le stockage partagé d'Android et les systèmes Windows et macOS sont insensibles à la casse : une offre légitime sous Linux écraserait un fichier ailleurs. |
| D7 | Chaos : **1 000 itérations en local**, une tâche lourde à la fois (le PC a gelé le 11 octobre), **50 en CI**. Graines fixes, échecs rejouables. | 1 000 itérations dépasseraient le budget de temps de la CI. |
| D8 | Valeurs par défaut (flux, chunk) choisies par `bench matrix` entre le PC et le téléphone. On ne change les valeurs actuelles (4 × 4 Mio) que si une combinaison fait mieux selon la règle de la bible (à moins de 5 % du meilleur, avec moins de mémoire). **Mesuré le 13 octobre 2026 : 4 flux × 2 Mio retenus.**
- Sur le hotspot 2,4 GHz, le bruit dépassait la tolérance, et moins de 4 flux ralentit les petits fichiers.
- En 5 GHz (Wi-Fi Direct), 4 × 2 Mio et 4 × 4 Mio ont été mesurés en alternance, 7 tours dans chaque sens : même débit à 2,5 % près, avec moitié moins de mémoire.
- Détails : `bench-results/README.md`. | Bible §17.4. |
| D9 | Espace insuffisant avant `accept` : **refus** dans la ligne de commande, avec un message ; dans le daemon, signalé dans `incoming_offer` et `respond_offer` refusé. | Bible §9.7 : mieux vaut refuser avant le premier octet que remplir le disque. |
| D10 | La sonde disque « pre-flight » du daemon est **supprimée** : les mesures par étape (`disk_write`, `fsync`) disent déjà si le disque limite, sans rien écrire en plus. `xfer bench disk` reste pour un test explicite. Ferme #36. | Elle écrivait 8 Mio dans le dossier de réception pendant le transfert. |
| D11 | Ajoutée en WP-4.7 : la racine d'un transfert porte un **marqueur** `.medxfer-transfer` contenant la clé du transfert (SHA-256 de l'empreinte de l'émetteur, du nom de la racine, du nombre d'éléments et de la taille). Il est créé avec le premier fichier, retiré quand tout est reçu, gardé si le transfert s'arrête en cours ou partiellement. Une reprise reprend le dossier dont le marqueur porte sa clé. | Sans lui, une reprise ne saurait pas que « Photos (1) » est son dossier et créerait « Photos (2) ». Une lecture suffit, au lieu d'interroger les partiels de chaque fichier. Un marqueur d'un autre transfert n'est jamais touché (`STO-09`). |
