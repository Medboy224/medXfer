# medXfer — Guide d'utilisation de la ligne de commande

> Pour envoyer et recevoir des fichiers avec `xfer`, sur PC ou dans Termux. Ce guide décrit la version actuelle (octobre 2026). L'application graphique viendra plus tard.

## 1. L'essentiel en 30 secondes

Les deux appareils doivent être sur le **même réseau** (même Wi-Fi, ou le point d'accès de l'un des deux).

| Je veux… | Sur l'appareil qui envoie | Sur l'appareil qui reçoit |
|---|---|---|
| Envoyer, l'autre a une caméra | `xfer send fichier` | Scanner le QR, ou copier la commande `xfer recv 'medxfer://…'` affichée |
| Envoyer, l'autre n'a pas de caméra | `xfer send --pin fichier` | `xfer recv --pin ADRESSE`, puis taper le PIN affiché |
| Envoyer sans rien recopier | `xfer send fichier` | `xfer recv`, choisir l'appareil dans la liste, puis répondre à la question de confiance (§3) |

`ADRESSE` est affichée par l'émetteur, par exemple `10.86.69.174:18888`.

Pour recevoir dans un autre dossier : `xfer recv … --out DOSSIER`.

## 2. Pourquoi parfois un PIN, parfois une question, parfois rien ?

medXfer ne transfère qu'entre appareils qui se sont **reconnus**. Il y a quatre façons de se reconnaître, de la plus simple à la plus prudente :

| Situation | Ce que vous voyez | Pourquoi |
|---|---|---|
| **Les deux appareils se font déjà confiance** | Rien : seulement « Confirm download? » | Ils se sont appairés avant (`always`). |
| **QR code** | Rien à taper, le QR suffit | Le QR contient l'empreinte de l'émetteur et un secret à usage unique. C'est la voie la plus sûre. |
| **PIN** (`--pin`) | Un PIN de 6 chiffres à recopier | Pour un appareil sans caméra. Le PIN change après 3 erreurs ; après 6 PIN faux de suite, l'attente s'arrête : relancez la commande. |
| **Comparaison d'empreintes** | `[?] New device …` avec deux empreintes | Quand on choisit l'appareil dans la liste, sans QR ni PIN. |

**C'est normal de ne pas taper de PIN** quand on choisit l'émetteur dans la liste de `xfer recv` : c'est le chemin « comparaison d'empreintes », ou « confiance » si les appareils se connaissent déjà.

## 3. La question de confiance

```
[?] New device 'medXfer-3fa2'.
    Its fingerprint:  VBXJ-OWIT-Y77L-5SVW
    Ours:             H5LS-AC4T-RLXV-UQL6
    Check that the other screen shows the same two fingerprints.
    Trust it? [y]es for this session / [a]lways / [N]o:
```

- **Vérifiez que l'autre écran affiche les deux mêmes empreintes**, inversées. Sinon, répondez `N` : un autre appareil se fait passer pour lui.
- `y` (ou `o`) : confiance pour ce transfert seulement.
- `a` (ou `t`) : confiance **toujours** ; la prochaine fois, plus de question.
- Rien, ou `N` : refusé. C'est la réponse par défaut.

Si un appareil connu se présente un jour avec une **autre** empreinte, medXfer refuse et affiche `REFUSED` : c'est le signe d'une possible interception.

## 4. Appairer deux appareils une fois pour toutes

Ensuite, plus aucune question entre eux.

| Méthode | Appareil A | Appareil B |
|---|---|---|
| QR | `xfer pair --show` | `xfer pair 'medxfer://…'` (la commande affichée par A) |
| PIN | `xfer pair --pin` | `xfer pair --pin ADRESSE`, puis taper le PIN |

Ajoutez `--once` pour ne rien enregistrer (confiance limitée à la session).

## 5. Gérer les appareils de confiance

| Commande | Effet |
|---|---|
| `xfer id` | L'empreinte de cet appareil |
| `xfer trust list` | Les appareils de confiance |
| `xfer trust forget EMPREINTE` | Oublier un appareil : la prochaine fois, il faudra QR, PIN ou question |

## 6. Mode interactif : `xfer node`

`xfer node` garde une session ouverte, avec les commandes `scan`, `pair <n|ip|code>`, `pair --show`, `send <fichier>`, `stop`, `disconnect` et `exit`. Pratique pour plusieurs envois de suite au même appareil.

## 7. Outils de développement

| Commande | Usage |
|---|---|
| `xfer daemon` / `xfer dev-ui` | Le tableau de bord dans le navigateur (`http://localhost:19999`), dans les builds de développement |
| `xfer report list` / `show` | Rapports des derniers transferts : débit, étape limitante |
| `xfer bundle` | Paquet de diagnostic pour signaler un problème (sans secret ni nom de fichier) |
| `xfer bench …` | Mesures de disque et de réseau |

## 8. En cas de problème

| Message | Que faire |
|---|---|
| `pairing refused: check the PIN…` | PIN mal recopié, ou changé après 3 erreurs : regardez le PIN affiché **maintenant** |
| `Too many wrong PINs: pairing stopped` | Relancez la commande côté émetteur ; si ce n'était pas vous, quelqu'un a essayé de deviner le PIN |
| `This QR code has expired` | `xfer send` renouvelle son QR : utilisez celui affiché maintenant. Pour `xfer pair --show`, relancez la commande |
| L'appareil n'apparaît pas dans la liste | Même réseau ? Sinon, passez l'adresse directement : `xfer recv --ip ADRESSE`, `--pin ADRESSE`, ou le QR |
| L'adresse affichée ne répond pas | Sur un téléphone avec Wi-Fi **et** données mobiles, l'adresse Wi-Fi doit s'afficher depuis la correction de #109. Sinon, signalez-le avec `xfer bundle` |

## 9. Windows : rendre le PC visible des autres appareils

**Symptôme :** le PC trouve les autres appareils, mais eux ne le voient pas dans leur liste. Le transfert marche quand même avec l'adresse, le QR ou le PIN.

**Cause :** le pare-feu de Windows bloque les recherches qui arrivent (mDNS, port UDP 5353), surtout quand le Wi-Fi est en profil « Privé » et que medXfer n'a été autorisé que pour le profil « Public », ou l'inverse.

**Solution, une fois pour toutes :** dans un PowerShell lancé **en administrateur** :

```powershell
New-NetFirewallRule -DisplayName "medXfer mDNS" -Direction Inbound -Protocol UDP -LocalPort 5353 -Action Allow -Profile Private,Public
```

Le profil du Wi-Fi (Privé ou Public) n'a alors plus d'importance : la règle couvre les deux. Garder « Privé » à la maison est le choix habituel.

Quand Windows affiche sa propre fenêtre « Autoriser l'accès » au premier lancement de medXfer, cochez **les deux** cases (réseaux privés et publics).

L'installateur de la version publiée posera ces règles lui-même, et l'application signalera un PC invisible.

