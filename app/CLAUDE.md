# L'application Flutter : consignes pour l'IA

Ce fichier complète le `CLAUDE.md` de la racine, qui s'applique aussi ici : flux de travail, langues, commentaires, zones sensibles. Le plan qui fait foi pour l'application est `docs/medxfer-plan-j8.md` (refonte « Version 4 », lecteurs, bêta) ; l'architecture vient de `docs/medxfer-plan-j6b.md`, §1.

## Organisation

| Dossier | Rôle |
|---|---|
| `lib/core/` | Le cœur vu par l'interface (`core.dart`) ; le vrai adaptateur sur la bibliothèque C (`ffi_core.dart`) ; le faux, qui rejoue les scénarios (`fake_core.dart`, `DEV-17`) |
| `lib/platform/` | Ce que fait le système (réseau, point d'accès, BLE, pare-feu, stockage, sélecteur, mise en veille…). **Une interface par sujet, un adaptateur par bibliothèque ou par canal natif** |
| `lib/connection/` | Se connecter : lien BLE, câble, pare-feu, lien d'appairage |
| `lib/library/`, `lib/transfer/` | Bibliothèque de fichiers et sélection ; transferts, historique, Web Share |
| `lib/features/` | Un dossier par écran |
| `lib/l10n/` | `app_fr.arb` et `app_en.arb` : les Dart sont générés par `flutter pub get` |
| `lib/theme.dart` | L'identité « Version 4 » (couleurs, accent, police Archivo) |
| `android/app/src/main/kotlin/dev/medxfer/app/` | Les adaptateurs Android (BLE, Wi-Fi Direct, point d'accès, rejoindre un réseau, stockage, bibliothèque, service de premier plan) |
| `windows/runner/` | Les adaptateurs Windows en C++ (BLE, pare-feu, point d'accès, rejoindre un réseau, câble) |

Le Dart parle au natif par des `MethodChannel` nommés `dev.medxfer.app/<sujet>`, un par sujet.

## Règles propres à l'application

- **La frontière avec le cœur suit la bible §10.4.**
  - Les événements sont lus par un isolat de fond, par lots (B8, B10), et l'arrêt passe par la sentinelle (B9).
  - Tout `char*` est rendu à `medxfer_free` (B1).
  - `coreSchema` (`lib/core/core.dart`) suit `SchemaVersion` (`pkg/core/core.go`).
- **Une décision vit dans le Go ou le Dart**, pas dans le Kotlin ou le C++ : quel réseau, quelle bande, quel chemin, quand réessayer. Le natif exécute et rapporte, sans choisir.
  - Ces quelque 6 000 lignes n'ont aucun test automatique ; ce qui y décide n'est vérifié que par les essais réels.
- **Aucun paquet sans décision.** Chaque dépendance se tient derrière une interface de `lib/core` ou `lib/platform` (commentaire de `pubspec.yaml`), et un nouveau paquet est proposé au développeur avant d'être ajouté.
  - Ce qui est retenu : libre, maintenu, rien de propriétaire (décisions C8 du plan J6c et D8 du plan J8).
- **Chaque texte affiché existe dans les deux `.arb`**, français et anglais.
- **Les mesures d'images** (fluidité, à-coups) se font sur une version profile, jamais debug (`lib/perf.dart`).
- **Le périmètre d'un lot d'interface se fixe à l'avance.** Les finitions visuelles n'ont pas de fin : ce qui dépasse le lot devient une issue, et la mesure (WP-8.4) passe avant le polissage.

## Commandes

Depuis la racine, pour que les tests utilisent aussi le vrai cœur :
```sh
bash scripts/build-lib.sh && bash scripts/stage-app-native.sh
```

Puis, dans `app/` :
```sh
flutter pub get
flutter analyze          # doit être propre
flutter test             # le faux cœur ; le vrai aussi quand build/lib contient la bibliothèque
flutter build linux --release
```

- **Windows :** `scripts\build-lib.ps1`, `scripts\stage-app-native.ps1`, puis `flutter build windows --release` (le mode développeur doit être activé).
- **Android :** `flutter build apk --release`.
- **La CI** (job `app`) lance `flutter analyze`, `flutter test` et la construction Linux, avec Flutter 3.47.6.

## Tests

- **Les tests de widgets tournent sur `FakeCore`.** Scripte ses réponses avec `FakeCore.on`, ou rejoue un scénario de `cmd/xfer/testdata/scenarios/`.
- **Un nouveau parcours entre le cœur et l'interface s'enregistre comme scénario :** il est alors vérifié contre le vrai cœur par `xfer ctl`, et contre l'interface par le faux (`DEV-17`).
- **Les adaptateurs de `lib/platform` se testent avec un canal simulé** (`test/platform/hotspot_test.dart`). Ce qui s'appuie sur eux se teste avec une fausse implémentation de leur interface.
- **Ce qui ne se teste qu'en vrai** (radio, réseau, caméra) donne un protocole d'essai réel pour le développeur : étapes, ce qu'il faut relever, ce qui ferait échouer. Ne déclare jamais un essai réel comme fait.
