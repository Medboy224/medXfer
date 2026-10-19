# medXfer — Rapport de sortie du lot « Audit »

**Date :** 19 octobre 2026 · **Plan :** [medxfer-plan-audit.md](medxfer-plan-audit.md) · **Issue :** #156 (et #157 pour J7) · **PR :** #158 (plan), #159 (A-1), #160 (A-2), #162 (A-3), et celle de ce rapport (A-4).

Ce rapport répond au critère du lot : *chaque constat de l'audit externe du 18 octobre est corrigé, reporté à un jalon nommé, ou écarté avec sa raison ; chaque correction est vérifiée par un test qui échouait avant elle*.

---

## 1. Verdict

**Le lot est terminé.** Les deux failles critiques ont disparu avec le code qui les portait : l'injection PowerShell et le blocage définitif du point d'accès. Tous les autres constats sont traités, reportés ou écartés (§2).

Pour chaque correction, un test a d'abord été écrit, puis lancé sur l'ancien code, où il a échoué. Six exceptions sont dites franchement au §4.

## 2. Bilan

| Lot | PR | Constats | Corrigés | dont préventifs (non reproduits) | Ne s'appliquent plus |
|---|---|---|---|---|---|
| A-1 Point d'accès | #159 | 3 | 3 (par retrait) | — | — |
| A-2 Canal et appairage | #160 | 9 | 9 | — | — |
| A-3 Cœur, frontière C, moteur | #162 | 16 | 16 | 1 (`Suspend`) | — |
| A-4 Le reste | ce rapport | 21 | 19 | 1 (`OnItem`) | 2 |
| **Total** | | **49** | **47** | **2** | **2** |
| Reportés | — | 9 | — | — | — |
| Non fondés ou dépassés | — | 4 | — | — | — |

Le lot a aussi fait apparaître un test instable, trouvé par la CI Windows de #160. Il a été rendu déterministe (#161, §5).

## 3. Ce qui a été livré

| Lot | Contenu principal |
|---|---|
| **A-1** (#159) | Le cœur ne crée plus de point d'accès : `pkg/hotspot`, `--create-network`, 4 actions et 3 événements retirés ; schéma du cœur **v3**. Bible `DISC-12` : la plateforme le crée, puis le signale par `set_network_info`. |
| **A-2** (#160) | La porte d'admission compte les poignées de main en cours et oublie les adresses inactives. Le délai non authentifié est une borne, avec une seule exception explicite, `AwaitPerson`. `Channel.Send` rétablit le délai de l'appelant. Pages d'offre vides refusées. L'expiration du QR ne coupe plus la question de confiance. Affichages du PIN ordonnés. |
| **A-3** (#162) | Frontière C : un seul cœur à la fois ; aucune panique ne traverse vers le C ; délai négatif = attente sans limite. Les reconnexions qui reçoivent des données ne comptent plus. Le 100 % passe le limiteur. `status` ne bloque plus le serveur, et `arp` est borné. Le mode node n'a plus qu'un propriétaire pour la session. Faux `REFUSED` après un échange d'adresses (DHCP) corrigé. |
| **A-4** (ce rapport) | Masquage des chemins et des noms ; balise entendue par la recherche par code ; fichiers cachés en collision ; DEL refusé ; IPv6 refusée dans le QR ; nouvelles tentatives de renommage et certificat « pas encore valide » dans `identity` ; délai de `bench net` à chaque étape ; QR et PIN affichés avant « waiting » ; textes en anglais ; dossier de configuration remis par `ctl` et les tests ; remarques sur `pkg/pake` ajoutées au dossier G4 (question 11). |

## 4. Ce qui n'a pas pu être reproduit, et pourquoi

| Constat | Ce qui a été fait |
|---|---|
| A-2, Top 11 : deux affichages du PIN dans le désordre | La course se joue entre deux instructions et ne se provoque pas sans point d'injection. Le test vérifie le mécanisme : les numéros de changement. |
| A-3, `store.Suspend` lit le journal sans `commitMu` | Aucune course signalée par `-race` en 5 essais. Le verrou a été ajouté par précaution. |
| A-3, Top 8 : `activeSession` partagée dans `xfer node` | La course mémoire n'est pas signalée par `-race` dans le scénario à 3 nœuds : un verrou de la liste de confiance, pris par hasard, ordonne les deux accès. Le défaut de logique, lui, est reproduit : l'ancien binaire perd la nouvelle session dans 5 essais sur 8, le nouveau dans aucun des 5. |
| A-4, `OnItem` et `OnDiskFull` sans `e.mu` | Pas de course mémoire : `resume` est posé avant `Run`. Seules les lignes à l'écran pouvaient se mêler à la barre de progression ; le verrou a été ajouté. |
| A-4, `BenchmarkDisk` | Ne s'applique plus : le tirage aléatoire est hors de la boucle chronométrée depuis WP-2.6. |
| A-4, QR Wi-Fi sans échappement (`qr.go`) | Ne s'applique plus : la fonction a été retirée avec le point d'accès (A-1). |

## 5. Décisions prises pendant le lot

| # | Décision | Raison |
|---|---|---|
| D1 | Deux fichiers de même nom deviennent `doc.pdf` et **`doc (1).pdf`**, pas `doc (2).pdf` comme l'écrivait le plan. | C'est la numérotation de la réception (`FormatCandidateName`) : un seul usage dans tout medXfer. |
| D2 | Une session avec **un autre** appareil arrête les transferts de l'ancienne ; le **même** appareil qui revient garde le sien. | Ne pas couper un transfert qui reprend après une micro-coupure. |
| D3 | Une clé d'appareil de confiance vue à l'ancienne adresse d'un autre appareil de confiance n'est pas une interception. | L'adresse change de main avec le DHCP. ID-05 refuse toujours une clé inconnue. |
| D4 | Le délai non authentifié n'est levé que par `Authenticate`. La saisie d'un PIN l'allonge explicitement, au plus de la vie d'un PIN. | Un appelant ne peut plus, par mégarde, garder une place de la porte sans fin. |
| D5 | Le serveur HTTP borne les en-têtes (10 s) et les connexions inactives (2 min), mais ni les lectures ni les écritures. | Ces deux bornes couperaient la WebSocket et les envois. Le reste de Web Share revient à J7. |
| D6 | `pkg/pake` reste tel quel, sauf `errInvalidElement`. Ses trois remarques sont des questions pour G4. | Le code relu doit être celui du dossier. |
| D7 | Le test instable `TestDashboardNeverCopiesFiles` (#161) venait du code, pas du test : le dossier des anciennes copies était nommé trop tard. | La règle « un test instable est rendu déterministe » s'applique au code fautif. |

## 6. Reportés, avec leur jalon

| Constat | Jalon |
|---|---|
| Web Share : PIN comparé avec `==` sans verrouillage, envoi par morceaux sans gestion des collisions, XSS du portail, `ipLockouts` jamais purgés | **J7** (#157) |
| `FileBytes` porte les totaux du lot, `Bottleneck` jamais rempli | **J6b**, selon les besoins de l'application |
| Empreintes gardées en mémoire par l'émetteur, `Split` qui sérialise chaque élément, `README` d'une ligne | **J8** (mesure d'abord) |
| `pkg/pake` : `finished` sans verrou, ISK gardée, fonctions de test compilées | **G4** (question 11 du dossier) |

## 7. Vérifications

- `go vet` et `go test ./...` passent, avec et sans les options `diag devui`, sur chaque PR. La CI passe sur Windows, Linux et macOS.
- `go test -race` a été lancé sur Linux (Zorin) pour chaque paquet touché.
- Essais réels :
  - `smoke.c` contre la vraie bibliothèque (Linux) ;
  - scénario à 3 nœuds `xfer node` (binaires `-race`) ;
  - syntaxe du tableau de bord vérifiée avec `node --check` (téléphone).
- Les configurations temporaires et tous les fichiers d'essai ont été supprimés du téléphone, de Zorin et du PC.
