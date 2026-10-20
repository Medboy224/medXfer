# medXfer — Guide d'utilisation de la ligne de commande

> Pour envoyer et recevoir des fichiers avec `xfer`, sur PC ou dans Termux. Ce guide décrit la version actuelle (octobre 2026). L'application graphique viendra plus tard.

## 1. L'essentiel en 30 secondes

Les deux appareils doivent être sur le **même réseau** (même Wi-Fi, ou le point d'accès de l'un des deux). `xfer` ne crée pas ce point d'accès : activez-le dans les réglages du téléphone ou du PC.

| Je veux… | Sur l'appareil qui envoie | Sur l'appareil qui reçoit |
|---|---|---|
| Envoyer, l'autre a une caméra | `xfer send fichier` | Scanner le QR, ou copier la commande `xfer recv 'medxfer://…'` affichée |
| Envoyer sans caméra | `xfer send fichier` | `xfer recv`, choisir l'appareil dans la liste, puis taper le PIN que l'émetteur affiche |
| Envoyer en donnant l'adresse | `xfer send fichier` | `xfer recv --pin ADRESSE`, puis taper le PIN affiché |
| Ne plus taper de PIN avec cet appareil | `xfer send --trust fichier` | `xfer recv --trust` : si les deux acceptent, la prochaine fois il n'y aura plus de PIN (§3) |

`xfer send` affiche **à la fois** le QR code et le PIN. `ADRESSE` est affichée par l'émetteur, par exemple `10.86.69.174:18887`. Tous les modes de medXfer utilisent ce même port, 18887.

Dans la liste, un appareil inconnu apparaît sous un nom générique (`medXfer-3fa2`), un appareil de confiance mutuelle sous **son nom** : `[1] PC de Naruto (trusted)` (§3).

Pour recevoir dans un autre dossier : `xfer recv … --out DOSSIER`.

## 2. Pourquoi parfois un PIN, parfois rien ?

medXfer ne transfère qu'entre appareils qui se sont **reconnus**. Un appareil vu pour la première fois se reconnaît par un **QR code** ou un **PIN**, jamais autrement : personne n'a à comparer de longues empreintes à l'œil.

| Situation | Ce que vous voyez | Ce qui est enregistré |
|---|---|---|
| **QR code** | Rien à taper : le QR suffit | Rien. Le QR contient l'empreinte de l'émetteur et un secret à usage unique : c'est la voie la plus sûre. |
| **PIN** | L'émetteur affiche 6 chiffres, vous les tapez | Rien. Le PIN change après 3 erreurs ; après 6 PIN faux de suite, l'attente s'arrête : relancez la commande. |
| **Appareils de confiance mutuelle** | Rien : seulement « Confirm download? » | Ils l'ont demandé tous les deux, une fois (§3). |

Par défaut, **une session ne laisse aucune trace** : la fois suivante, il faudra de nouveau le QR code ou le PIN.

**Choisir l'appareil dans la liste** (`xfer recv`, `pair 1` dans `xfer node`) fait afficher un PIN par l'appareil choisi : c'est ce PIN qu'on tape. Comme en Bluetooth, l'un affiche, l'autre tape.

## 3. La confiance mutuelle

Deux appareils qui se servent souvent l'un de l'autre peuvent se faire confiance, pour ne plus taper de PIN. Il faut que **les deux** le veuillent :

- l'un le propose : `--trust` sur `xfer send`, `xfer recv` ou `pair` dans `xfer node`, ou la commande `trust` dans une session de `xfer node` ;
- l'autre voit :

  ```
  [?] 'medXfer-3fa2' (key VBXJ-OWIT-Y77L-5SVW) offers mutual trust: both devices would then reconnect without a PIN.
      Accept? [y/N]:
  ```

  et répond `y` (ou `o`). Rien, ou `N` : refusé. Sans réponse en 60 s, c'est non. Si les deux ont mis `--trust`, aucune question n'est posée.

Si les deux acceptent, chacun enregistre l'autre (`xfer trust list`). Sinon, rien n'est enregistré, et le transfert a lieu quand même.

Ensuite, dans les listes (`xfer recv`, `scan` de `xfer node`), chacun voit l'autre **sous son nom**, suivi de « (trusted) ». Ce nom ne passe jamais sur le réseau : les autres appareils ne voient que `medXfer-xxxx`. Chaque appareil reconnaît l'autre grâce à une étiquette secrète qui change toutes les 10 minutes, jamais grâce à son adresse, qu'un autre appareil pourrait prendre.

La confiance reste **mutuelle** : si l'un des deux oublie l'autre (`xfer trust forget`), elle cesse **pour les deux**, et l'autre est prévenu (« … no longer trusts … ») avant de repasser par le PIN.

Si un appareil connu se présente un jour avec une **autre** empreinte, medXfer refuse et affiche `REFUSED` : c'est le signe d'une possible interception.

## 4. Appairer deux appareils une fois pour toutes

`xfer pair` sert à établir la confiance mutuelle, sans rien envoyer. Lancé des deux côtés, il ne pose aucune question.

| Méthode | Appareil A | Appareil B |
|---|---|---|
| QR | `xfer pair --show` | `xfer pair 'medxfer://…'` (la commande affichée par A) |
| PIN | `xfer pair --pin` | `xfer pair --pin ADRESSE`, puis taper le PIN |

Si l'autre côté refuse, la commande le dit, et rien n'est enregistré.

## 5. Gérer les appareils de confiance

| Commande | Effet |
|---|---|
| `xfer id` | L'empreinte de cet appareil |
| `xfer trust list` | Les appareils de confiance |
| `xfer trust forget EMPREINTE` | Oublier un appareil : la confiance cesse pour les deux, il faudra de nouveau le QR ou le PIN, et il ne s'affiche plus sous son nom |
| `xfer trust rename EMPREINTE NOM` | Renommer un appareil de confiance, **chez vous seulement** ; le nom qu'il envoie ne le remplacera plus |
| `xfer name [NOM]` | Afficher ou choisir le nom de cet appareil, celui que les autres verront une fois la confiance établie. Par défaut : le nom de la machine, ou le modèle du téléphone sous Android |

## 6. Mode interactif : `xfer node`

`xfer node` garde une session ouverte, pratique pour plusieurs envois de suite au même appareil. Ses commandes :

- `scan` : la liste des appareils, les appareils de confiance mutuelle sous leur nom ;
- `pair <n>` : appairer l'appareil n° n de la liste. L'autre appareil affiche un PIN (« it must type this PIN »), que vous tapez ici. `pair <ip>`, `pair <ip:port>` et `pair <code>` marchent aussi. Avec un appareil de confiance mutuelle, pas de PIN ;
- `pair --show` : afficher un QR code pour l'autre appareil ; `pair --pin` : afficher un PIN à l'avance ;
- `trust` : proposer la confiance mutuelle à l'appareil appairé ; `--trust` sur `pair` fait de même dès l'appairage ;
- `send <fichier>`, `stop`, `disconnect`, `exit`.

Si un autre medXfer occupe déjà le port 18887 sur l'appareil (un autre `xfer node`, `xfer share`), `xfer node` le signale et prend un autre port. L'adresse affichée avec le PIN ou le QR code contient alors ce port : c'est elle qu'il faut taper.

## 7. Outils de développement

| Commande | Usage |
|---|---|
| `xfer report list` / `show` | Rapports des derniers transferts : débit, étape limitante |
| `xfer bundle` | Paquet de diagnostic pour signaler un problème (sans secret ni nom de fichier) |
| `xfer bench …` | Mesures de disque et de réseau |
| `xfer ctl` | Piloter un cœur par des lignes JSON, pour les scripts sans écran |

Dans l'application, ces outils sont dans le panneau développeur : Réglages, puis « Mode développeur ».

## 8. En cas de problème

| Message | Que faire |
|---|---|
| `pairing refused: check the PIN…` | PIN mal recopié, ou changé après 3 erreurs : regardez le PIN affiché **maintenant** |
| `Too many wrong PINs: pairing stopped` | Relancez la commande côté émetteur ; si ce n'était pas vous, quelqu'un a essayé de deviner le PIN |
| `This QR code has expired` | `xfer send` renouvelle son QR : utilisez celui affiché maintenant. Pour `xfer pair --show`, relancez la commande |
| L'appareil n'apparaît pas dans la liste | Même réseau ? Sinon, passez l'adresse directement : `xfer recv --ip ADRESSE`, `--pin ADRESSE`, ou le QR |
| L'adresse affichée ne répond pas | Sur un téléphone avec Wi-Fi **et** données mobiles, l'adresse Wi-Fi doit s'afficher depuis la correction de #109. Sinon, signalez-le avec `xfer bundle` |
| `The sender could not be reached at …` | Le receveur n'arrive pas à télécharger chez l'émetteur. Si l'émetteur est un PC Windows, son pare-feu bloque le port : voir §9, puis renvoyez |
| `Port 18887 is taken … using port N` | Un autre medXfer tourne déjà sur cet appareil (`xfer node`, `xfer share`). Fermez-le, ou ouvrez aussi le port N dans le pare-feu de Windows |
| `… no longer trusts this device / node: that gives nothing (pairing by PIN)` | La confiance n'existe plus que d'un côté (l'autre vous a oublié, ou l'inverse). Tapez le PIN ; pour ne plus en taper, reproposez la confiance (§3) |
| `The other device shows no PIN (too many wrong ones?)` | Trop de PIN faux chez l'autre appareil : il n'en affiche plus. Relancez-y la commande (`pair --pin` dans `xfer node`) |
| `No answer to the trust offer: the session stays temporary` | L'autre n'a pas répondu en 60 s : rien n'est enregistré, recommencez si besoin |

## 9. Windows : le pare-feu

**Symptômes :**

- le PC trouve les autres appareils, mais eux ne le voient pas dans leur liste ;
- un autre appareil n'arrive pas à s'appairer avec le PC ;
- le PC envoie, mais le receveur ne reçoit rien et finit par afficher `The sender could not be reached`. Dans l'autre sens, tout marche.

**Cause :** le pare-feu de Windows bloque ce qui arrive vers medXfer :

- les recherches de la découverte (mDNS, port UDP 5353, et la balise de medXfer, port UDP 19998) ;
- le port de medXfer, TCP 18887, où l'autre appareil se connecte pour s'appairer **et pour télécharger** : c'est toujours le receveur qui se connecte à l'émetteur.

Le problème se pose surtout quand le Wi-Fi est en profil « Privé » et que medXfer n'a été autorisé que pour le profil « Public », ou l'inverse.

**Solution, une fois pour toutes :** dans un PowerShell lancé **en administrateur** :

```powershell
New-NetFirewallRule -DisplayName "medXfer mDNS" -Direction Inbound -Protocol UDP -LocalPort 5353 -Action Allow -Profile Private,Public
New-NetFirewallRule -DisplayName "medXfer découverte" -Direction Inbound -Protocol UDP -LocalPort 19998 -Action Allow -Profile Private,Public
New-NetFirewallRule -DisplayName "medXfer" -Direction Inbound -Protocol TCP -LocalPort 18887 -Action Allow -Profile Private,Public
```

Le profil du Wi-Fi (Privé ou Public) n'a alors plus d'importance : les règles couvrent les deux. Garder « Privé » à la maison est le choix habituel.

Quand Windows affiche sa propre fenêtre « Autoriser l'accès » au premier lancement de medXfer, cochez **les deux** cases (réseaux privés et publics).

L'installateur de la version publiée posera ces règles lui-même, et l'application signalera un PC invisible.

