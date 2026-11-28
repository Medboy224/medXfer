# medXfer — Travailler à deux agents

> **Pour qui :** les deux agents d'IA du projet, Claude (Claude Code) et Gemini (Gemini Code Assist), et le développeur.
> **Pourquoi :** deux agents vont deux fois plus vite à une condition : ne jamais se marcher dessus.
> - Pas deux branches dans le même dossier.
> - Pas deux essais sur le même téléphone.
> - Pas deux compilations lourdes à la fois sur le PC.
> - Pas deux lots qui touchent les mêmes fichiers.
>
> Ce document complète `AGENTS.md`, dont toutes les règles valent pour les deux agents. Le runbook GitHub est dans `.agents/skills/github-orchestration/SKILL.md`.

## 1. Les rôles

| Qui | Fait | Ne fait pas |
|---|---|---|
| **Le développeur** | Décide : lots, découpages, fusions, choix des tables « Décisions ». Relit les PR. Fait les gestes physiques des essais réels (§5.4). | — |
| **Chaque agent** | Prend un lot, l'écrit, le teste, le mesure, ouvre sa PR. Fait lui-même ses essais réels quand il peut (§5). Relit une PR de l'autre agent quand le développeur le demande (§6). | Fusionner, pousser sur `main`, prendre un lot déjà pris, élargir son lot |

**Tout est fondé.** Deux agents en parallèle ne valent que si chacun produit du travail solide :
- **Un changement part d'un constat** : un test qui échoue sur l'ancien code, une mesure, ou une lecture du code qui cite le fichier et la ligne. Sans constat, pas de changement ; un défaut non reproduit est dit « préventif ».
- **Une affirmation se vérifie avant d'être écrite** : dans le code, dans une sortie de commande, dans une source citée. Ce qui n'a pas été vérifié est dit tel quel.
- **Rien sans besoin** : pas de document, de code, de commentaire ni d'issue qui ne serve pas le lot ou un constat réel.

**Ce que le développeur attend de toute contribution :**
- **La vitesse :** medXfer vise le transfert local le plus rapide. Les petits fichiers sont le point faible connu. Une optimisation se mesure avant et après (`bench-results/`).
- **La sécurité d'abord** (R2) : rien ne s'optimise à ses dépens.
- **Du logiciel 100 % libre :** aucune dépendance propriétaire livrée.
- **Une interface dense, le contenu d'abord, une identité visuelle propre à medXfer.**
- **Les réponses au développeur en français**, du début à la fin de la session, tableaux et protocoles compris.

## 2. Qui prend quel lot

- **Un lot = une issue.** L'agent qui la prend :
  - y met son étiquette, `agent:claude` ou `agent:gemini` ;
  - ajoute un commentaire : « Pris par Gemini, branche `fix/…` ».
- **Une issue étiquetée est à l'autre agent :** on ne la touche pas.
- **Les fichiers de deux lots ouverts sont disjoints.**
  - Avant de commencer, regarder les fichiers des PR ouvertes : `gh pr list`, puis `gh pr diff N --name-only`.
  - Si un fichier est commun, le dire au développeur avant d'écrire.
- **Répartition par défaut,** que le développeur peut changer à tout moment :

| Domaine | Agent | Dossiers |
|---|---|---|
| Cœur Go, protocole, stockage, mesure, ligne de commande, revue du cœur, préparation de G4 | Claude | `pkg/`, `cmd/`, `internal/`, `bench-results/` |
| Natif Android et Windows, connexion, interface Flutter | Gemini | `app/android/`, `app/windows/`, `app/linux/`, `app/lib/` |

- Un lot qui traverse la frontière (par exemple, un événement du cœur affiché par l'application) se découpe en deux PR, une par côté, ou se fait par un seul agent avec l'accord du développeur.

## 3. Les dossiers de travail

- **Le dossier principal du dépôt reste au développeur**, sur `main`. Aucun agent n'y change de branche.
- **Chaque agent travaille dans son propre `git worktree`**, créé depuis `origin/main`. Une branche par lot.

| Agent | Worktrees |
|---|---|
| Claude | `.claude/worktrees/<nom>` |
| Gemini | `.gemini/worktrees/<nom>` |

Les deux dossiers sont ignorés par git (`.gitignore`). Un exemple :

```sh
git fetch origin
git worktree add .gemini/worktrees/w343 -b fix/reseau-laisse origin/main   # un nouveau lot
git worktree add .gemini/worktrees/w329 fix/audit-android-native        # un lot existant
```

- Un worktree neuf n'a pas les fichiers générés. Dans `app/` : `flutter pub get`, puis `bash scripts/build-lib.sh && bash scripts/stage-app-native.sh` pour les tests sur le vrai cœur.
- `flutter pub get` peut réécrire les fins de ligne de `app/*/flutter/generated_*` : les remettre avec `git checkout --` avant de commiter.

## 4. Les ressources partagées : un verrou par ressource

Le PC et les appareils d'essai sont partagés. Avant de s'en servir, l'agent pose un verrou ; il l'enlève dès qu'il a fini.

**Où sont les verrous :** dans le dossier git commun, hors de tout commit. Ce dossier est le même depuis tous les worktrees :

```sh
COORD="$(git rev-parse --git-common-dir)/coord"
```

**Les ressources :**

| Ressource | Quand la verrouiller |
|---|---|
| `pc-lourd` | Gradle et APK, `flutter build`, `go test ./...` complet, `-race`, un banc, un émulateur. Une seule tâche lourde à la fois : le PC a peu de mémoire, et il a déjà gelé. Après un APK : `./gradlew --stop` dans `app/android`. |
| `a57`, `tecno` | Installer, lancer ou mesurer quoi que ce soit sur le téléphone, par adb ou par SSH (Termux). |
| `appli-pc` | Lancer, fermer ou réinstaller l'application medXfer du PC. |
| `wifi` | Changer le Wi-Fi d'un appareil, créer ou rejoindre un réseau : cela coupe les essais de l'autre agent. |
| `zorin`, `pc2` | Les machines d'essai Linux et Windows joignables par SSH. |

**Poser et enlever un verrou :** le verrou est un dossier. `mkdir` échoue s'il existe déjà, donc deux agents ne peuvent pas le prendre ensemble.

```sh
mkdir -p "$COORD"
mkdir "$COORD/a57.lock" && echo "gemini | essai #329, jonction perdue | $(date '+%d/%m %H:%M') | 20 min" > "$COORD/a57.lock/qui"
# ... l'essai ...
rm -r "$COORD/a57.lock"
```

**Si la ressource est prise :**
- Lire `qui`. Attendre, ou faire autre chose, ou le dire au développeur.
- Ne jamais forcer.
- Un verrou de plus de 2 heures se signale au développeur : il peut rester d'une session qui s'est arrêtée.

## 5. Les essais réels

`AGENTS.md` (règle 8) : les essais réels se font sur les appareils du développeur, et aucun résultat ne s'invente. Un agent qui a accès à un terminal sur ce PC peut en faire la plus grande part lui-même, comme ci-dessous. Il garde pour le développeur ce que seul un humain peut faire (§5.4).

### 5.1 Joindre les appareils

- **Les adresses** ne sont pas dans le dépôt, qui est public. Elles sont dans `deploy.bat`, local et ignoré par git.
  - Elles changent avec le réseau : la passerelle du PC (`ipconfig`) est souvent le point d'accès.
  - Une nouvelle adresse ne s'accepte qu'après avoir comparé l'empreinte de sa clé d'hôte à celle déjà connue (`ssh-keyscan`, puis `ssh-keygen -lf`).
- **Termux (A57)** : `ssh -p 8022 -o BatchMode=yes -o ConnectTimeout=6 <utilisateur>@<adresse>`. La connexion se fait par clé, jamais par mot de passe. Si `sshd` refuse, demander au développeur de lancer `sshd` dans Termux.
- **adb** : `%LOCALAPPDATA%\Android\Sdk\platform-tools\adb.exe`, qui n'est pas dans le PATH.
  - `adb devices -l` liste les appareils ; `-s <série>` en choisit un.
  - L'A57 passe souvent par adb sans fil : `adb mdns services`, puis `adb connect`.
- **Zorin et PC2** : SSH. Pas d'application graphique par SSH.

### 5.2 Préparer un essai

**Un dossier de configuration à part pour chaque essai** : `MEDXFER_CONFIG_DIR=<dossier temporaire>`. Les identités et les listes de confiance du développeur ne se touchent jamais.

**Les binaires :**

| Cible | Commande |
|---|---|
| Termux, arm64 | `GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build -o xfer ./cmd/xfer` |
| TECNO, 32 bits | `GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -o xfer ./cmd/xfer`. `android/arm` exige cgo ; le binaire Linux statique se lance par adb dans `/data/local/tmp`, avec `HOME` et `MEDXFER_CONFIG_DIR` posés. |
| L'application | `scripts/build-lib.ps1 -Targets android-arm64` (ou `android-arm` pour le TECNO), `scripts/stage-app-native.ps1`, puis `flutter build apk --debug` ou `--profile` (mesures d'images). Installer avec `adb install -r -d` : un numéro de version plus haut est souvent déjà installé. Le paquet de debug est `dev.medxfer.app.debug`. |

**Les pièges connus :**
- **Git Bash :** il réécrit les chemins Android (`/data/local/tmp` devient `C:/Program Files/Git/data/…`). Préfixer `MSYS_NO_PATHCONV=1` à la commande adb.
- **Pare-feu du PC :** un nouvel exécutable qui écoute sur `0.0.0.0` ouvre une invite. Préférer que le PC se connecte vers l'appareil, ou écouter sur `127.0.0.1` pour un essai sur le PC seul. Une règle créée par un essai ne se retire qu'en administrateur : la signaler au développeur.
- **Commandes longues :** les lancer en arrière-plan, et toujours avec `timeout`, pour qu'un émetteur ne reste pas en attente sans fin.
- **Tâche détachée par SSH :** `nohup … > log 2>&1 < /dev/null &`, seule sur sa ligne. Placée après une chaîne `&&`, c'est toute la chaîne qui part en arrière-plan, et elle garde la session ouverte.
- **`pkill -f motif` dans `ssh hôte '…'`** tue aussi la session, dont la ligne de commande contient le motif. Écrire le motif avec des crochets : `pgrep -f "[x]fer node"`.
- **`xfer node` sans terminal :** lire ses commandes dans une fifo gardée ouverte, `exec 3<>fifo` puis `xfer node <&3`. Écrire ensuite dans la fifo.

### 5.3 Mener l'essai, puis ranger

- **Avant et après, sur les mêmes appareils :** `main`, puis la branche, dans les mêmes conditions. Les sorties brutes vont dans `bench-results/`, avec une section du README.
- **Une instrumentation temporaire** (une ligne qui affiche les étapes, par exemple) se construit depuis une copie, ne se commite jamais, et se signale dans le résultat.
- **Gestes par adb** (`input tap`, `input swipe`) : seulement quand `dev.medxfer.app.debug` est au premier plan (`dumpsys window | grep mCurrentFocus`). Jamais de « retour » à l'aveugle.
- **Le contenu des téléphones** (photos, fichiers) peut être regardé pour vérifier un essai. Il ne sort jamais : ni Internet, ni service externe, ni autre appareil que ceux de l'essai.
- **Ranger à la fin de chaque essai :**
  - les dossiers d'essai sur les téléphones (`~/…` dans Termux, `/data/local/tmp/…`) ;
  - ceux du PC (`M:\medxfer-g2a-tmp\<essai>` ; ne jamais toucher au reste de `M:`) ;
  - les binaires temporaires.
  Garder seulement ce qui resservira, et dire quoi.
- **Le résultat** va dans la PR (section « Essais réels »), avec les heures et les chiffres. Ce qui n'a pas été vérifié se dit tel quel.

### 5.4 Ce qui demande le développeur, et comment le lui demander

**Seul le développeur peut :**
- faire les gestes physiques : allumer ou couper le Wi-Fi ou le Bluetooth d'un téléphone, l'écran, un câble, éloigner un appareil. Couper le Wi-Fi par adb peut aussi couper adb ;
- cliquer dans l'application du PC, ou sur un écran sans adb ;
- répondre aux invites du système : pare-feu, administrateur (UAC), permissions et installations Android, code d'appairage d'adb sans fil ;
- donner un avis qu'aucune mesure ne remplace : la fluidité, l'aspect.

**Comment le demander :** un seul message court, en français. Il dit quoi faire, sur quel appareil, à quel moment, et ce qu'il faut répondre. Par exemple : « Sur l'A57, coupe le Bluetooth maintenant, puis dis-moi “fait” ». L'agent vérifie ensuite lui-même le résultat (journal, mesure, capture), et n'affirme rien qu'il n'a pas constaté.

## 6. La relecture

- **Le développeur relit les PR et les fusionne.** Le travail avance sans attendre l'autre agent.
- **Un agent relit une PR de l'autre seulement quand le développeur le lui demande.** Sa relecture est un commentaire sur la PR, qui contient :
  - la compilation ;
  - les essais réels faits ;
  - les défauts trouvés, chacun avec le scénario qui le montre et la correction proposée ;
  - un avis.
- La décision reste au développeur.
- Une PR qui touche `pkg/securechan`, `pkg/pairing`, `pkg/identity`, `pkg/webshare`, `pkg/engine/safepath.go` ou `pkg/store` a en plus sa revue de sécurité (`AGENTS.md`).
- Le code natif (Kotlin, C++) n'a pas de test automatique, et la CI ne compile que Linux. Sa relecture doit donc dire s'il a été compilé (`flutter build apk`, `flutter build windows`) et essayé sur un appareil.

## 7. La communication

- **GitHub est le seul canal partagé :** issues, PR, commentaires.
  - Le dépôt est public : ni adresse, ni nom de machine, ni lien de session, ni contenu des téléphones.
  - Chaque agent a sa propre mémoire. Ce que l'autre doit savoir va dans une issue, une PR ou un document de `docs/`.
- **Le développeur relaie** quand un agent a besoin d'une décision ou d'une action de l'autre.
- **En fin de session,** un agent laisse son lot dans un état lisible :
  - branche poussée ou commits décrits ;
  - verrous enlevés ;
  - un commentaire sur l'issue, qui dit où il en est et ce qui reste.
