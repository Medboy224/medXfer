# medXfer — Rapport de sortie du lot « Noms des appareils de confiance »

**Date :** 19 octobre 2026 · **Plan :** [medxfer-plan-noms.md](medxfer-plan-noms.md) · **Bible :** chapitre 5 (`DISC-06`, `DISC-07`, `DISC-11`), §6.5 · **Issue :** #150 (et #136) · **PR :** #151 (plan), #152 (WP-N.1), #153 (WP-N.2), #154 (WP-N.3), et celle de ce rapport (WP-N.4).

Ce rapport répond au critère de sortie du plan : *un appareil de confiance mutuelle est reconnu dans les scans de la ligne de commande et du cœur, par une étiquette tournante et non par son adresse ; l'annonce ne contient ni nom, ni clé, ni identifiant stable ; les noms enregistrés ont un sens (plus de « localhost »)*.

---

## 1. Verdict

**Le lot est terminé. Les trois parties du critère sont remplies** (§2). Elles sont vérifiées par les tests et par des essais réels entre le PC Windows, le téléphone (Termux) et le PC Zorin.

Dans une liste de découverte, un appareil de confiance mutuelle apparaît maintenant sous son nom : `[2] Galaxy A57 essai (trusted)`. Un inconnu reste `medXfer-xxxx`. Le nom ne passe jamais sur le réseau.

## 2. Critère de sortie

| Partie | État | Preuve |
|---|---|---|
| Reconnaissance par étiquette tournante, plus par l'adresse | ✅ | `pairing.Recognize`, utilisé par `listedName` (ligne de commande) et `peers_list` (cœur). Un appareil qui prend l'adresse d'un appareil de confiance, sans l'étiquette, reste anonyme (`TestListedName`, `TestRecognizeByTag`, `TestScanDoesNotRecognizeByAddress`). |
| L'annonce ne porte ni nom, ni clé, ni identifiant stable | ✅ | L'annonce porte le nom générique, le rôle, le port et des étiquettes `HMAC-SHA-256(K_AB, fp ‖ période)[:8]` qui changent toutes les 10 minutes (`TestTagShape`, `TestDaemonAnnouncesNoName`, inchangé). |
| Des noms qui ont un sens | ✅ | `xfer name`. Par défaut, le nom de la machine ou, sous Android, le fabricant et le modèle : « Samsung SM-A576B » au lieu de « localhost » (#136, `TestDefaultDeviceName`, vérifié sur le téléphone). `xfer trust rename` pour un nom choisi chez soi. |

## 3. Ce qui a été livré

| Lot | PR | Contenu |
|---|---|---|
| Plan | #151 | Le modèle, les décisions N1 à N6, validées le 18 octobre. |
| WP-N.1 | #152 | `pkg/pairing/tag.go` (`Tag`, `AnnounceTags`, `Recognize`) ; la clé de la paire passe dans `trust_commit` (une seule clé même si les deux proposent) ; `Peer.TagKey` ; le champ `t` dans le TXT mDNS, la balise et le réflecteur ; l'annonce mDNS se renouvelle quand les étiquettes changent. Bible `DISC-07` et `DISC-11`, dossier G4 (question 10). |
| WP-N.2 | #153 | `DefaultDeviceName`, `CleanDeviceName`, `xfer name`, `xfer trust rename` (drapeau `renamed`) ; le vrai nom part à l'appairage dans tous les modes. |
| WP-N.3 | #154 | Les scans affichent « Nom (trusted) » : `xfer recv`, `scan` de `xfer node`, `peers_list` du cœur, le tableau de bord. Les étiquettes de toutes les vues d'un appareil sont réunies (mDNS et balise). |
| WP-N.4 | ce rapport | Guide (§1, §3, §5, §6), essais réels avec le PC Windows, ce rapport. |

## 4. Essais réels

Configurations temporaires isolées, pour ne pas toucher aux identités ni aux listes de confiance des appareils. Elles ont été supprimées ensuite.

**18 octobre, téléphone ↔ Zorin** (WP-N.3) :
- avant la confiance, Zorin voit `medXfer-6cf3` ;
- après `pair` par PIN puis `trust`, Zorin voit « Galaxy A57 test (trusted) » et le téléphone voit « Zorin test (trusted) ».

**19 octobre, PC Windows ↔ téléphone, avec Zorin sans confiance** (réseau du point d'accès en 10.55.213.0/24) :

| Phase | Scan du PC | Scan du téléphone |
|---|---|---|
| Avant | `[1] medXfer-a2ba` (Zorin), `[2] medXfer-6bf8` (téléphone) | — |
| Après `pair` par PIN puis `trust` | `[1] medXfer-a2ba`, `[2] Galaxy A57 essai (trusted)` | `[1] medXfer-a2ba`, `[2] PC Naruto essai (trusted)` |
| Après `xfer trust forget` sur le PC seulement | `[1] medXfer-16ea` | `[1] medXfer-1049` |

Dans la dernière phase, le téléphone a encore le PC dans sa liste, mais le PC ne lui annonce plus d'étiquette : la reconnaissance cesse **des deux côtés**, comme la confiance. Zorin, sans confiance, n'a jamais été nommé.

## 5. Décisions prises pendant le lot (en plus de N1 à N6)

| # | Décision | Raison |
|---|---|---|
| F1 | Quand les deux appareils proposent la confiance en même temps, les deux gardent la clé tirée par l'appareil à la **plus petite empreinte**. | Chacun envoie sa propre clé dans son `trust_commit` ; sans règle, ils finiraient avec des clés différentes. |
| F2 | L'étiquette inclut l'empreinte de l'**annonceur**. | Un appareil ne prend pas sa propre étiquette pour celle de l'autre : les deux partagent la clé. |
| F3 | Les étiquettes de toutes les vues d'un appareil (mDNS, balise) sont **réunies**. | L'annonce mDNS se renouvelle au plus toutes les 30 s ; juste après une confiance, seule la balise porte déjà les nouvelles étiquettes. |
| F4 | Sous Android, le modèle est lu par `/system/bin/getprop`. | Le script `getprop` de Termux, premier dans le `PATH`, ne peut pas être lancé par un binaire Go statique (« permission denied »). |
| F5 | Le nom qu'Android affiche dans ses réglages n'est pas lisible depuis Termux : l'application le passera au cœur avec `set_config`. | `settings get global device_name` échoue sans permission. |

## 6. Réserves et suites

1. **Revue G4** : la question 10 du dossier demande si 8 octets et 10 minutes suffisent contre le suivi.
2. **Noms déjà enregistrés** : les configurations des appareils de l'utilisateur gardent leur nom actuel (« A57 », « Windows », « ^$ ») ; `xfer name` permet de les changer.
3. **Confiances établies avant ce lot** : elles n'ont pas de clé de reconnaissance, et ne sont donc pas reconnues tant que la confiance n'est pas redemandée (N4). Les listes étaient vides après les essais.

## 7. Suite

**J6b**, l'application Flutter MVP. Sa liste d'appareils affichera `trusted_alias` de `peers_list`, et l'application passera au cœur le nom du téléphone tel qu'Android le connaît.
