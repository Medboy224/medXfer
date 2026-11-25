# medXfer — Plan du lot « Revue du cœur » (dans J8, avant la bêta)

> **Issues :** #297 à #314, les constats de la revue ; #283 (G4), liée à #308.
> **Source :** revue intégrale du cœur Go du 25 novembre 2026, au commit `4e137eb`, publiée en issues le même jour. Elle a été faite par une session d'IA (Claude), à la demande du développeur. Comme pour l'audit du 18 octobre, **un constat n'est qu'une hypothèse tant qu'un test ne le reproduit pas**.
> **Objectif :** corriger avant la bêta (WP-8.3, WP-8.8) les défauts de la revue qui touchent la sécurité, la justesse des transferts ou la mesure. Chaque point est reproduit par un test qui échoue avant d'être corrigé. Un point non reproduit est noté comme tel, jamais « corrigé » à l'aveugle.
>
> **Critère de sortie :**
> - chaque constat a un statut final : corrigé avec son test, non reproduit, reporté avec sa raison et son lot, ou écarté avec sa raison ;
> - les constats de sécurité (#299, #307, #309, #308, et ceux de #313 marqués **S**) sont corrigés ;
> - le diagnostic (#304) est juste avant la mesure de WP-8.4 ;
> - la bible ne marque plus ✅ une exigence que le code contredit (`DISC-11`, §3).
>
> **Durée estimée :** 3 à 5 jours, plus la séance sur l'appareil de R-0. Une PR par lot, moins de 600 lignes hors tests, CI verte sur les trois systèmes avant fusion.

## 0. Lecture de la revue

La revue a lu tout le cœur Go. Elle a vérifié par un test jetable, non commité, les points marqués ✅ ci-dessous. Elle n'a trouvé **aucune cause dans le cœur** à la baisse de débit constatée le 25 novembre (12 → 7 MB/s à 2,4 GHz, 65 → 43 MB/s à 5 GHz) : en boucle locale, l'ancien et le nouveau code donnent le même R6. Cette baisse relève de WP-8.4 (§4).

Statuts de vérification :
- ✅ confirmé par un test ou une sonde de la revue ;
- 📖 établi à la lecture du code, pas encore reproduit ;
- 🔎 à vérifier sur l'appareil ;
- 📏 hypothèse de performance, à mesurer avant tout changement.

| Issue | Constat | Vérification |
|---|---|---|
| #297 | Chaque événement de fichier recopie toute la file : coût quadratique (1 000 fichiers : 186 Mo de JSON ; 5 000 : 4,7 Go) | ✅ (simulation, côté Go) |
| #298 | Un appareil de confiance qui se reconnecte remplace la session d'un autre et coupe son transfert | 📖 |
| #299 | Web Share : le quota de l'invité se contourne par un dépassement d'entier | ✅ (HTTP 201 avec 1 Mio de quota) |
| #300 | `xfer send .` et `xfer send ..` donnent des chemins faux | ✅ (sonde sur `BuildOffer`) |
| #301 | `xfer send -i` envoie sans confirmation après 60 s sans réponse | 📖 |
| #302 | `syncfs` sur le stockage partagé d'Android (FUSE) ne synchronise peut-être rien | 🔎 |
| #303 | `xfer trust forget` est annulé par un processus en cours | ✅ (deux `TrustStore` sur le même fichier) |
| #304 | Diagnostic : latences à 0, `fsync` désigné goulot à tort, TLS compté en réseau, comparaison incomplète, aucun rapport de la CLI, moyenne fausse après reprise | 📖 ; le point 2 visible dans le banc |
| #305 | `xfer node` : une demande de PIN bloque toutes les connexions 2 min | 📖 |
| #306 | « unsupported » dans le journal environ 4 fois par seconde | 📖 |
| #307 | Découverte : lectures réseau sans limite de taille | 📖 |
| #308 | Le nombre d'étiquettes annoncées révèle le nombre d'appareils de confiance | 📖 |
| #309 | Les noms reçus d'un pair ne passent pas par `CleanDeviceName` | 📖 |
| #310 à #312 | Pistes de vitesse et de mesure | 📏 |
| #313, #314 | Points mineurs : robustesse, CLI, code mort, couverture | 📖 ; code mort signalé par staticcheck |

## 1. Décisions à prendre

Ce sont des propositions : le développeur tranche avant le lot concerné.

| # | Question | Proposition | Raison |
|---|---|---|---|
| R1 | #298 : une session entrante pendant un transfert avec un autre appareil ? | ❓ **Refuser avec un code `busy`**, que l'appelant traite comme la fin de son rappel. Le même appareil qui revient garde son transfert (D2 de l'audit). | `xfer node` refuse déjà. Demander à l'utilisateur ferait apparaître une question pendant un transfert, souvent sans personne devant l'écran. |
| R2 | #297 : les événements d'un fichier sans la file complète ? | ❓ **Schéma v6** : l'événement d'un fichier ne porte que `item_index`, l'état et l'erreur ; la file complète seulement à l'offre, à l'acceptation et à la fin. L'application applique le changement d'un élément. La même PR met à jour le golden et l'application. | La seule correction qui rende le coût linéaire. Le schéma change, d'où la décision. |
| R3 | #302 : que faire si `syncfs` ne fait rien sous FUSE ? | ❓ Selon R-0 : sur FUSE (`statfs`, `FUSE_SUPER_MAGIC`), un `fsync` par fichier en parallèle ; `syncfs` gardé ailleurs. Sans confirmation, rien ne change. | Le gain des lots (bible §9.6) ne vaut que si la durabilité tient. |
| R4 | #303 : comment `xfer trust` atteint-il un processus en cours ? | ❓ **Relire le fichier avant chaque écriture**, sous un verrou de fichier, et le recharger quand sa date change. | Marche pour la CLI comme pour l'application, sans canal entre processus. |
| R5 | #308 : comment cacher le nombre d'étiquettes ? | ❓ Compléter à 16 étiquettes aléatoires de même forme, tirées à chaque période, dans un ordre mélangé, comme la réponse BLE. Accepter aussi la période suivante. | Le format reste le même pour le receveur. Il faut vérifier que l'annonce tient dans un paquet mDNS. |
| R6 | #283 dans ce lot ? | ❓ **Non** : un lot « Préparation G4 » à part, avant l'envoi du dossier. | Il change le format des étiquettes BLE et le dossier G4, et une partie touche `pkg/pake`, gelé (A3 de l'audit). |
| R7 | Les outils de R-6 dans la CI ? | ❓ Oui, en avertissement d'abord, puis bloquants une fois le code propre. | staticcheck a trouvé du code mort ; govulncheck n'a jamais tourné ; la bible s'est écartée du code deux fois (audit, #308). |

## 2. Les lots

### Lot R-0 — Mesure sur l'appareil · S · 🔎

Fait par le développeur, sur le Galaxy A57. Le protocole vient de #302 :

| Mesure | Où | Ce qui conclut |
|---|---|---|
| `syncfs` après l'écriture de 256 Mo | `/storage/emulated/0/medXfer` (FUSE) | Quelques millisecondes seulement : `syncfs` ne synchronise rien |
| `fsync` du même fichier | Même dossier | La référence |
| `syncfs` après la même écriture | Dossier interne de l'application (ext4 ou f2fs) | La durée attendue d'une vraie synchronisation |

Résultat archivé dans `bench-results/`, puis décision R3.

### Lot R-1 — Sécurité et vie privée · M

| Issue | Constat | Action | Test |
|---|---|---|---|
| #299 | `up.total += f.Size` déborde | Refuser `f.Size > maxBytes - total` avant d'additionner | Trois fichiers de 2^62 octets : `413` |
| #307 | `json.NewDecoder` et `io.ReadAll` sans limite ; une goroutine par connexion sans plafond | `io.LimitReader` à 4 Kio sur la requête et chaque réponse, `fetchPeer` compris ; plafond de connexions du répondeur | Une requête de 1 Mio est coupée à 4 Kio |
| #309 | Noms reçus gardés tels quels, jusqu'à 1 Mio, avec contrôles et bidi | Tout nom reçu passe par `CleanDeviceName`, qui retire aussi le bidi (U+202A à U+202E, U+2066 à U+2069) ; idem dans `xfer node` | `pair_request` avec un nom de 1 Mio et U+202E : 64 caractères propres |
| #308 | Nombre d'étiquettes visible ; période suivante refusée | Décision R5 | Annonces de 0, 1 et 16 appareils de confiance : 16 étiquettes ; reconnaissance à la frontière de période |
| #313 **S** | `AnswerBench` écoute en TCP clair, sur toutes les interfaces, pour une taille choisie par le pair | Lier à l'adresse de la session, n'accepter que l'adresse du pair, borner la taille | Une connexion d'une autre adresse est refusée |
| #313 **S** | Noms réservés Windows incomplets (`CONIN$`, `CONOUT$`, `COM¹` à `COM³`, `LPT¹` à `LPT³`) | Les ajouter à `SafeRelPath` | Corpus de `STO-12` étendu |
| #313 **S** | Bidi accepté dans les noms de fichiers reçus | Refuser, comme un caractère de contrôle | `facture‮fdp.exe` refusé |
| #313 **S** | `sniffing` du Web Share sans plafond de connexions, 10 s chacune | Plafond, comme `LIM-01` | 64 connexions muettes n'empêchent pas un invité |
| #313 | `ifconfig` lancé sans délai | Délai, comme `arp` (audit, Top 9) | Commande bloquée : l'appelant revient |

Une revue de sécurité sur la PR (`/security-review` sous Claude Code). Bible : `DISC-11` (§3).

### Lot R-2 — Sessions et confiance · M

| Issue | Constat | Action | Test |
|---|---|---|---|
| #298 | Une reconnexion P3 remplace la session d'un autre appareil | Décision R1 | Trois démons (`testkit`) : B rappelle A pendant un transfert A ↔ C, et le transfert de C va au bout |
| #303 | L'oubli et le renommage perdus à la sauvegarde suivante d'un autre processus | Décision R4 | Celui de la preuve : la CLI oublie A, le démon touche B, et A n'est plus de confiance |
| #305 | Une seule goroutine d'acceptation dans `xfer node` | Une goroutine par connexion ; l'état de la session reste dans la boucle principale | Une demande de PIN sans réponse n'empêche pas un appareil de confiance d'entrer |
| #306 | `unsupported` journalisé environ 4 fois par seconde | `xfer node` ignore `progress` et `ping` en silence ; le démon ne journalise qu'une fois par type | 10 s de `progress` : une seule ligne |
| #313 | `pause`, `resume`, `skip`, `cancel` du pair sans identifiant de transfert | Vérifier l'identifiant, comme `accept`, `complete` et `failed` | Un `cancel` tardif n'annule pas le transfert suivant |
| #313 | Ports 18887 et 0 tous deux pris : aucun hub, aucune erreur | `StartNode` renvoie l'erreur | Les deux ports occupés : erreur rendue |

### Lot R-3 — Événements et stockage · M

| Issue | Constat | Action | Test |
|---|---|---|---|
| #297 | Coût quadratique des événements ; un `transfer_complete` peut être jeté quand la file est pleine | Décision R2 ; `pkg/core` ne jette jamais un événement terminal | 5 000 petits fichiers : volume des événements linéaire ; file pleine : le terminal arrive |
| #302 | `syncfs` sous FUSE | Selon R-0 et R3 | Sur un système de fichiers marqué FUSE, le chemin `fsync` est pris |
| #313 **S** | `publish` fait `Lstat` puis `Rename` : un fichier apparu entre les deux est écrasé | `renameat2(RENAME_NOREPLACE)` sous Linux et Android, `MoveFileEx` sans `REPLACE_EXISTING` sous Windows, repli actuel ailleurs (`STO-09`) | Fichier créé entre les deux : rien d'écrasé, collision « nom (1) » |
| #313 | `File.Commit` en erreur ne remet pas `pendingBytes` | Le remettre avec `pending` | Commit en erreur (`faultfs`), puis le seuil en octets redéclenche |
| #313 | L'émetteur sans plafond de connexions de données (`LIM-07` : 1 à 8) | Plafonner à `MaxWorkers` | La 9ᵉ connexion est refusée |
| #313 | `FinishTransferTracker` écrit le rapport en tenant `s.mu` | Écrire hors du verrou | `status` répond pendant une écriture lente (`faultfs`) |
| #313 | `SaveConfigTo` non atomique | L'écriture atomique de `pkg/identity` | Coupure au milieu : l'ancien `config.json` reste |
| #313 | Web Share : `complete` renomme sans `fsync` | `fsync` avant le renommage, comme `pkg/store` | Test d'ordre des appels (`faultfs`) |

### Lot R-4 — Ligne de commande · S

| Issue | Constat | Action | Test |
|---|---|---|---|
| #300 | `xfer send .`, `./`, `..` | Chemin relatif au dossier lui-même ; `SafeRelPath` côté émetteur aussi | `BuildOffer` avec `.`, `./`, `..` et `../a` |
| #301 | Délai écoulé pris pour un oui | `askLine` dit si une ligne a été tapée ; sans réponse, refus | Le délai écoulé refuse l'envoi |
| #314 | `recv` : `[Y/n]` vaut oui quand l'entrée est fermée | Même correction que #301 : sans terminal, refus. Une option `--yes` pour les scripts n'existe pas encore : à décider | `xfer recv </dev/null` refuse |
| #314 | Code de sortie 0 sur erreur dans `send`, `recv`, `node` | Code non nul | Un échec donne un code non nul |
| #314 | `node send` coupe aux espaces ; reçoit dans le dossier courant ; mDNS lancé avant `SetTags` | Guillemets ; dossier de réception configuré ; `SetTags` d'abord | Un test par point |
| #314 | `printUsage` cite `daemon`, omet `bench ladder` et `matrix` | Corriger le texte | — |
| #314 | `wsclient` garde tous les événements ; `Watch` appelle `fn` sous le verrou | File bornée ; `fn` hors du verrou | `fn` qui appelle `Events()` ne bloque pas |

### Lot R-5 — Le diagnostic, avant la mesure de WP-8.4 · M

Sans ce lot, le rapport par étape de WP-8.4 désignerait le mauvais goulot.

| Point de #304 | Action | Test |
|---|---|---|
| 1. Latences toujours à 0 | Les remplir depuis les médianes des étapes, ou retirer les champs et leur texte | Rapport sans « 0.0 ms » fictif |
| 2. `fsync` désigné goulot à tort | Mesurer comme étape l'attente réelle de l'écrivain (`<-running` dans `CommitIfDue`) | Banc en boucle locale : `fsync` n'est plus le goulot à 440–510 MB/s ; `DEV-04` revérifié sur ses trois scénarios |
| 3. TLS compté comme réseau | Mesurer à part le temps de déchiffrement, ou au moins le temps processeur du processus | Réseau bridé contre processeur ralenti : deux goulots différents |
| 4. `report compare` incomplet | Ajouter les étapes, l'étape limitante, la fréquence et l'autre Wi-Fi | Deux rapports qui diffèrent par la fréquence : l'écart affiché |
| 5. La CLI n'enregistre aucun rapport | Un rapport à la fin des transferts de `send`, `recv` et `node` | Rapport présent après `xfer send` |
| 6. Moyenne surestimée après une reprise | Moyenne sur les octets reçus pendant la session | Après une reprise à 50 %, la moyenne est celle de la session |

### Lot R-6 — Outils · S (décision R7)

| Outil | Rôle |
|---|---|
| `staticcheck ./...` dans la CI | Code mort et erreurs courantes (#314) |
| `govulncheck ./...`, chaque semaine | Failles connues des dépendances et de Go ; jamais lancé jusqu'ici |
| Couverture publiée par paquet | Elle est de 70,6 % au total, dont 32,9 % pour `cmd/xfer` et 56,0 % pour `pkg/discovery` |
| Avertissement au-delà de 600 lignes par PR, hors tests et golden | La règle du §5 de la feuille de route, vérifiée par l'outil |
| Traçabilité de la bible | Un script liste les exigences ✅ qu'aucun test ne cite (`// STO-12` ou le nom du test), et la CI avertit |

### Reportés, avec leur lot

| Issue | Constat | Lot |
|---|---|---|
| #310 | Lectures entrelacées de l'émetteur | **WP-8.5**, si `disk_read` sur le téléphone le montre en WP-8.4 |
| #311 | `test_network` et `bench net` sur un seul flux court ; type de lien deviné ; `bench disk` lu depuis le cache | **WP-8.4**, pour ce dont la mesure a besoin ; le reste en WP-8.5 |
| #312 | Recherche et balayage relancés pendant les transferts ; `arp -a` toutes les 5 s | **WP-8.5**, après mesure à 2,4 GHz |
| #313 | `RootKey` identique pour deux envois de même forme | **Jalon de dette, après la bêta** (la clé de reprise change) |
| #313 | La porte compte les poignées de main des appareils de confiance dans `MaxUnauth` | **Décision de #93** |
| #313 | Clé de collision sans normalisation Unicode ni pliage NTFS | **J9** (réserve de J4b, avant macOS) |
| #313 | Cinq PIN faux terminent la session Web Share | **WP-8.7**, l'écran de l'hôte le dit |
| #314 | Code mort et hérité : `secureEq`, `isLocalNetworkIP`, `getFreePort`, champs de `TransferStats` et `session.Message`, `DialPeer`, PIN et jeton de `NewDaemonServerAt` | **Jalon de dette, après la bêta** |
| #314 | Version « 1.0.0 » écrite en dur | **WP-8.8** (version 0.9.0, D7 du plan J8) |
| #283 | Suivi par BLE avec un défi répété ; borne de 18 essais du PIN à la demande | **Lot « Préparation G4 »** (décision R6) |

### Écartés

| Issue | Constat | Pourquoi |
|---|---|---|
| #313 | Connexions de données en IPv4 seulement | Choix de la bible : IPv4 partout, l'IPv6 refusé dans le QR (audit, lot A-4) |

## 3. Bible

- **`DISC-11`** promet « ni identifiant stable », que le nombre d'étiquettes contredit (#308). Le statut repasse à 📏 R-1 dans la PR de ce plan. Il revient à ✅ avec le test de R-1, et le texte dit alors « 16 étiquettes, toujours », et « la période précédente et la suivante sont acceptées ».
- **`DEV-03`** : l'étape `fsync` mesure l'attente de l'écrivain, plus la durée du commit en arrière-plan (R-5).
- **`API-06`** : le schéma v6, si R2 est retenue (R-3).
- **`LIM-07`** : le plafond est tenu aussi par l'émetteur, avec son test (R-3).
- **Dossier G4, question 10** : le nombre d'étiquettes, toujours 16 (R-1).

## 4. Hors périmètre

- **La baisse de débit du 25 novembre :** WP-8.4. Il faut d'abord mesurer au même endroit et avec les mêmes appareils, iperf3 compris, en comparant avec les rapports J6c, J6d et J7 (53 à 56 MB/s à 5 GHz, 10 à 12 MB/s à 2,4 GHz). Ensuite, retrouver la version fautive avec les APK archivés.
- **Les optimisations (#310, #312) :** WP-8.5, après la mesure.
- **`pkg/pake` :** gelé jusqu'à G4.
- **#283 :** son propre lot (R6).
- **La réécriture des commentaires existants :** seulement quand on touche la fonction (`AGENTS.md`).

## 5. Risques

| Risque | Parade |
|---|---|
| Le schéma v6 casse l'application | Une seule PR pour le cœur, le golden, les scénarios et l'application ; les tests de widgets sur `FakeCore` avec les nouveaux événements |
| Le refus `busy` bloque un retour légitime | Le même appareil garde son transfert (D2 de l'audit) ; un test pour ce cas en plus de celui de #298 |
| Les tests à trois démons sont instables sous macOS (#184, #240) | Attentes d'événements, jamais de délai fixe ; une instabilité reçoit son issue `test-instable` |
| `renameat2` absent d'un noyau Android ancien | Repli sur le chemin actuel quand l'appel renvoie `ENOSYS` ou `EINVAL` |
