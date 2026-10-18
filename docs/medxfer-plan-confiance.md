# medXfer — Plan du lot « Modèle de confiance » (avant J6b)

> **Source :** proposition de l'utilisateur du 18 octobre 2026 (« Appairage non sécurisé ») ; bible chapitre 6 (`PAIR-01` à `PAIR-08`), §6.3 (chemin P3), `ID-03`, `ID-05` ; chapitre 10 (`API-02`, `API-06`).
> **Objectif :** un appareil inconnu ne devient un pair qu'avec une preuve forte, et rien n'est mémorisé sans l'accord des deux :
> - **premier contact par PIN ou QR code seulement** : la question de confiance (comparer les empreintes à l'œil) disparaît ;
> - **zéro persistance par défaut** : une session est jetable tant qu'on ne demande rien ;
> - **confiance bilatérale** (A⇔B) : les privilèges d'appareil de confiance ne s'appliquent que si les deux appareils ont dit oui, et le restent seulement tant que les deux le veulent.
>
> **Critère de sortie :** aucun chemin du code n'admet un appareil inconnu sans PIN ni QR ; rien n'est écrit dans `trusted.json` sans le oui des deux côtés ; une entrée présente d'un seul côté ne donne aucun privilège. Vérifié par les tests et par des essais réels.
>
> **État :** terminé le 18 octobre 2026, voir [report-confiance.md](report-confiance.md).
>
> **Durée estimée :** 4 à 6 jours. Une PR par lot, CI verte sur les trois systèmes avant fusion. À finir **avant J6b**, pour que l'application Flutter se construise sur ce modèle.

## 0. État de départ (constaté sur `main` le 18 octobre 2026)

| Constat | Conséquence |
|---|---|
| La bible prévoit trois chemins : P1 (QR), P2 (PIN), P3 (appareil de confiance). Le code en a un **quatrième**, ajouté en WP-3.3 : la **question de confiance**. Les deux écrans affichent les empreintes, l'utilisateur répond `y` (session), `a` (toujours) ou `N`. | C'est le chemin fragile : sûr seulement si l'on compare vraiment 2 × 16 caractères. En pratique on tape `y`, et un attaquant actif sur le réseau (faux point d'accès, détournement d'adresse) peut s'interposer au premier contact. Il est supprimé. |
| La question existe à **six endroits** : `xfer node` (entrant et sortant), `xfer recv` (choix dans la liste), `xfer send` (receveur sans QR), le cœur (entrant `admitIncoming`, sortant `pairWith`) avec les actions et événements `trust_request`, `trust_respond`, `trust_expired`. | Tous traités par ce lot. |
| La persistance est incohérente : la question propose « session » ou « toujours » ; le PIN et le QR **enregistrent par défaut**, sauf `--once` (`PAIR-08` ne couvre que l'envoi ponctuel). | Tout devient éphémère par défaut. |
| `trusted.json` est **unilatéral** : A peut faire confiance à B sans que B le sache, et les entrées s'accumulent. | La confiance devient bilatérale, vérifiée à chaque connexion. |
| `testkit.StartPair` inscrit **chacun des deux** appareils chez l'autre. | Les tests d'intégration restent valables : c'est déjà une confiance mutuelle. |
| Le passif est déjà couvert : tout passe en TLS 1.3, avec des clés épinglées. | Le risque traité ici est l'**attaque active au premier contact**, et l'accumulation de confiance non voulue. |

## 1. Le modèle cible

### 1.1 Trois états entre deux appareils

| État | Comment on y entre | Ce qu'il permet | Ce qui est écrit sur disque |
|---|---|---|---|
| **Inconnu** | — | Se voir dans la découverte. Rien d'autre. | Rien |
| **Session** | PIN (P2) ou QR (P1) réussi | Envoyer et recevoir tant que la session dure. Chaque offre reste à accepter. | **Rien** |
| **Confiance mutuelle** | Dans une session, les deux appareils disent oui à la demande de confiance | Se reconnecter **sans PIN ni QR** (P3). Chaque offre reste à accepter, sauf « accepter automatiquement » réglé pour cet appareil (§6.3). | L'empreinte de l'autre, **de chaque côté** |

### 1.2 Le premier contact : PIN ou QR, rien d'autre

- **QR code** (P1) : inchangé.
- **PIN** (P2) : inchangé côté cryptographie (CPace, `PAIR-01` à `PAIR-07`). Nouveau : le **PIN à la demande**. Quand B choisit A dans la liste (`pair 1`, `xfer recv` puis `1`, bouton « Appairer » de l'application), A affiche aussitôt un PIN, et B est invité à le taper. C'est le schéma « l'un affiche, l'autre tape » du Bluetooth. Un attaquant au milieu ne voit pas le PIN affiché sur l'écran de A, et le PAKE lié au canal (`PAIR-03`) fait échouer tout relais.
- **`xfer send`** affiche par défaut le **QR et le PIN** : le receveur scanne, ou tape le PIN.
- Plus aucune comparaison d'empreintes pour décider. L'empreinte reste affichable (`ID-03`, `xfer id`, `xfer trust list`) pour qui veut vérifier.

### 1.3 La confiance bilatérale, vérifiée à chaque connexion

Une entrée dans `trusted.json` ne prouve que la décision d'un côté : B peut toujours enregistrer la clé publique de A sans lui demander. Ce qui protège A, c'est la liste de A. Pour que « A⇔B » reste vrai dans la durée :

1. **À chaque connexion**, chaque appareil déclare dans le canal chiffré s'il a l'autre dans sa liste (`trusts_you`).
2. **Le chemin P3** (pas de PIN) ne s'applique que si **les deux** listes contiennent l'autre **et** les deux le déclarent. Sinon, c'est un premier contact : PIN ou QR.
3. Une entrée d'un seul côté est **inerte**. L'appareil le signale (« confiance incomplète : 'X' ne vous a plus dans sa liste ») et propose de la retirer.
4. **`xfer trust forget`** d'un seul côté suffit donc à retirer les privilèges **aux deux**.

### 1.4 La demande de confiance

Elle n'est possible que dans une session ouverte par PIN ou QR. Trois messages dans le canal chiffré :

```
A ──trust_offer──►  B     B : « 'A' propose une confiance mutuelle. Accepter ? [y/N] » (60 s, sans réponse : non)
A ◄─trust_accept──  B     (ou trust_decline : rien n'est écrit, la session continue)
A ──trust_commit──► B     A écrit l'empreinte de B à l'envoi du commit ; B écrit celle de A à sa réception
```

Si le `trust_commit` se perd, A a une entrée et B non : la règle de §1.3 la rend inerte, et elle est signalée à la connexion suivante. Le transfert en cours ne dépend jamais de la négociation.

## 2. Ordre d'exécution et dépendances

```
WP-C.1 pairing (protocole) ──► WP-C.2 cœur, schéma v2 ──► WP-C.5 tableau de bord, guide, essais réels
                          └──► WP-C.3 send / recv / pair ─┤
                          └──► WP-C.4 node ───────────────┘
```

WP-C.3 et WP-C.4 sont indépendants de WP-C.2 (la ligne de commande n'utilise pas le cœur pour l'appairage) et l'un de l'autre.

## 3. Les lots

### WP-C.1 — `pkg/pairing` : PIN à la demande, déclaration mutuelle, demande de confiance · M

- **PIN à la demande** : message `pair_pin_wanted` de l'initiateur. Le répondant tire un PIN (`PAIR-01`, `PAIR-02`, `PAIR-04` inchangés), le montre par son callback `Show`, puis l'échange CPace se déroule sur la même connexion. Le délai `Unauth` est prolongé le temps de la saisie (120 s au plus, `PAIR-02`). Le `Gate` borne toujours les demandes.
- **Déclaration mutuelle** (`trusts_you`) dans le premier message de session et sa réponse ; fonction `Mutual(trustStore, peer, declared)`.
- **Demande de confiance** : `trust_offer`, `trust_accept`, `trust_decline`, `trust_commit`, avec leurs fonctions côté demandeur et côté répondant ; écriture dans `trusted.json` selon §1.4 uniquement.
- **Bible** : chapitre 6 réécrit selon le §1 ; `PAIR-08` reformulée ; nouvelles exigences `PAIR-09` à `PAIR-12` (§6) ; menaces A2 et A3 mises à jour. **Dossier G4** : une section « modèle de confiance » (aucun changement de cryptographie, seulement des messages dans un canal déjà authentifié).
- **Tests** :
  - un relais entre les deux appareils fait échouer le PIN à la demande ;
  - aucune écriture sans les deux oui ;
  - `trust_commit` perdu : entrée inerte des deux côtés ;
  - entrée d'un seul côté : pas de P3, PIN demandé ;
  - `trust forget` d'un côté : plus de P3 pour les deux.

### WP-C.2 — Le cœur : schéma v2 · M

- `admitIncoming` et `pairWith` : plus de question. Inconnu ⇒ PIN à la demande ou QR ; P3 seulement si mutuel.
- **Schéma v2** (`SchemaVersion = 2`, fichiers « golden » régénérés, `API-06`) :
  - retirés : action `trust_respond`, événements `trust_request`, `trust_expired` ;
  - ajoutés : actions `trust_offer`, `trust_answer` ; événements `pin_requested` (afficher le PIN demandé), `trust_offered`, `trust_established`, `trust_declined`, `trust_incomplete` ;
  - `once` devient `trust` dans `pair_pin`, `pair_pin_show`, `pair_show`, `pair_uri` : éphémère par défaut.
- `testkit` inchangé dans son principe (confiance des deux côtés) ; tests du cœur adaptés.

### WP-C.3 — Ligne de commande : `send`, `recv`, `pair` · M

- `xfer send` : QR **et** PIN affichés ; éphémère ; `--trust` propose la confiance une fois l'appairage fait. La question pour un receveur sans QR disparaît.
- `xfer recv` : choix dans la liste ⇒ PIN à la demande (« PIN shown on the other device: ») ; `--trust` pareil.
- `xfer pair` : sa raison d'être est la confiance durable. Lancer la commande des deux côtés vaut oui des deux côtés : pas de question supplémentaire. `--once` disparaît.
- `--trust` des deux côtés : pas de question, les deux ont déjà dit oui.

### WP-C.4 — `xfer node` · S à M

- `pair <n | ip | ip:port>` vers un inconnu ⇒ PIN à la demande ; vers un appareil de confiance mutuelle ⇒ P3.
- Session éphémère par défaut ; `pair … --trust` propose la confiance après l'appairage.
- Nouvelle commande **`trust request`** dans une session ; la question s'affiche chez l'autre.
- La question de confiance et `trustQuestion` disparaissent.

### WP-C.5 — Tableau de bord, guide, essais réels · S

- **Tableau de bord** (gelé, `DEV-19`) : adaptation minimale pour qu'il reste utilisable : afficher le PIN demandé, répondre à une demande de confiance.
- **Guide** : §2 à §6 réécrits (« Pourquoi parfois un PIN, parfois une question, parfois rien ? » n'a plus lieu d'être).
- **Essais réels** téléphone ↔ Zorin ↔ PC Windows : premier contact par PIN à la demande et par QR ; session éphémère (rien sur disque) ; demande acceptée puis reconnexion sans PIN ; demande refusée ; `trust forget` d'un côté ; mauvais PIN.

## 4. Hors périmètre

- La cryptographie de l'appairage (CPace, QR, TLS) : inchangée.
- Le portail Web Share (`/share`) et son PIN de repli (`WEB-03`) : un navigateur n'a pas d'identité, il n'entre pas dans ce modèle.
- L'interface Flutter : J6b, sur ce modèle.

## 5. Décisions (validées le 18 octobre 2026)

| # | Décision | Raison |
|---|---|---|
| D1 | **Premier contact par PIN ou QR seulement.** La question de confiance (comparaison d'empreintes) est supprimée partout : ligne de commande, cœur, tableau de bord. | C'est le seul chemin attaquable par un tiers actif au premier contact. La bible ne l'a jamais prévu. |
| D2 | **PIN à la demande** : l'appareil choisi dans la liste affiche un PIN, l'autre le tape. `xfer send` affiche QR et PIN par défaut. | Garde le geste « je choisis dans la liste » ; 6 chiffres à taper, sûr même si l'utilisateur ne vérifie rien. |
| D3 | **Éphémère par défaut partout.** La confiance se demande : `--trust` (`send`, `recv`, `pair` du nœud), `trust request` (nœud), action `trust_offer` (cœur). | Plus de clés fantômes ; une session sans demande ne laisse aucune trace. |
| D4 | **Confiance bilatérale vérifiée à chaque connexion** (§1.3). Une entrée d'un seul côté est inerte ; `trust forget` d'un côté retire les privilèges aux deux. | La négociation seule ne garantit rien dans la durée ; cette règle rend « A⇔B » vrai à tout moment. |
| D5 | **Demande de confiance en trois messages**, 60 s sans réponse valent non, sans effet sur le transfert en cours. | §1.4 ; un message perdu ne laisse qu'une entrée inerte. |
| D6 | **`xfer pair` établit directement la confiance mutuelle** ; `--once` disparaît. | Lancer la commande des deux côtés exprime déjà les deux oui. |
| D7 | **Schéma du cœur v2.** `trust_request`, `trust_respond`, `trust_expired` retirés ; actions et événements du §3 ajoutés ; `once` devient `trust`. | `API-02` fige les noms d'une version ; aucune application n'utilise encore la v1 : c'est le moment. |
| D8 | **Tableau de bord : adaptation minimale** (PIN demandé, réponse à une demande), rien de plus. | Il est gelé et sera retiré (G7) ; il doit seulement rester utilisable pour les essais. |
| D9 | **`trusted.json` existant gardé tel quel**, sans migration. Une entrée redevient active seulement si l'autre côté a aussi la sienne. | La règle de D4 traite les anciennes entrées comme les nouvelles. |
| D10 | Les textes de la ligne de commande restent **en anglais**, comme le reste (`[y/N]`). | Cohérence ; la traduction viendra avec l'application. |
