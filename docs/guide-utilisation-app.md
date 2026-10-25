# medXfer : installer l'application

> **Version de développement.** Ces paquets servent aux essais. La version publiée, avec son installateur, sa clé de publication et le Play Store, vient avec J8. Ce guide sera complété en WP-B.8 (utilisation de l'application).

Les paquets se construisent avec les scripts du dossier `scripts/` : `package-windows.ps1`, `package-linux.sh`, `package-android.ps1`.

## 1. Windows

Un dossier portable, zippé : `medxfer-<version>-windows-x64.zip`. Rien à installer, aucun droit d'administrateur.

1. Décompressez l'archive où vous voulez, par exemple dans `Documents`.
2. Lancez `medxfer.exe`.

Le dossier contient tout ce dont l'application a besoin : le cœur (`medxfer.dll`) et le runtime Visual C++ (`msvcp140.dll`, `vcruntime140.dll`, `vcruntime140_1.dll`). Windows 10 ou 11, 64 bits.

### Le pare-feu

Au premier lancement, Windows demande s'il faut autoriser medXfer sur le réseau : cochez **les deux** cases (réseaux privés et publics). Sans cela, les autres appareils ne voient pas le PC et ne peuvent pas recevoir ce qu'il envoie.

Si la fenêtre n'est pas apparue, ou si la case a été décochée, posez les règles une fois pour toutes dans un PowerShell lancé **en administrateur** :

```powershell
New-NetFirewallRule -DisplayName "medXfer" -Direction Inbound -Protocol TCP -LocalPort 18887 -Action Allow -Profile Private,Public
New-NetFirewallRule -DisplayName "medXfer mDNS" -Direction Inbound -Protocol UDP -LocalPort 5353 -Action Allow -Profile Private,Public
New-NetFirewallRule -DisplayName "medXfer découverte" -Direction Inbound -Protocol UDP -LocalPort 19998 -Action Allow -Profile Private,Public
```

| Port | Rôle |
|---|---|
| TCP 18887 | Appairage et transferts : c'est toujours le receveur qui se connecte à l'émetteur |
| UDP 5353 | Découverte des appareils (mDNS) |
| UDP 19998 | Découverte des appareils (balise de medXfer) |

L'installateur de la version publiée posera ces règles lui-même.

## 2. Linux

Une archive : `medxfer-<version>-linux-x64.tar.gz`.

1. Décompressez-la : `tar -xzf medxfer-<version>-linux-x64.tar.gz`.
2. Installez les bibliothèques qu'elle demande. Le fichier `DEPENDANCES.txt` les liste, avec la version de glibc nécessaire et, pour Debian et Ubuntu, la commande `apt install` à taper. Sur un bureau GNOME, KDE ou Xfce, elles sont presque toujours déjà là. Sur un système minimal, n'oubliez pas OpenGL (`libegl1`, `libgl1`, `libgles2`, `libgl1-mesa-dri`) : l'application le charge au lancement, et s'arrête aussitôt sans lui.
3. Lancez `./medxfer` depuis le dossier décompressé.

L'archive est construite sur Ubuntu 22.04 : elle demande glibc 2.35 ou plus récente, comme Ubuntu 22.04, Debian 12, Zorin OS 17, Linux Mint 21 et les versions suivantes. La CI l'essaie sur une Ubuntu 22.04 et une Ubuntu 24.04 neuves, avec les seuls paquets de `DEPENDANCES.txt`. Sur une autre distribution, les noms des paquets peuvent changer ; les bibliothèques, elles, sont les mêmes.

## 3. Android

Un APK pour les téléphones arm64 (presque tous les téléphones récents) : `medxfer-<version>-android-arm64.apk`. Android 11 ou plus récent.

1. Copiez l'APK sur le téléphone et ouvrez-le.
2. Android demande d'autoriser l'installation depuis cette source (le gestionnaire de fichiers, le navigateur) : acceptez, pour cette fois.

L'APK est signé avec la **clé de développement** de medXfer. Android n'accepte une mise à jour que si elle est signée avec la même clé que l'application installée. Pour passer d'une version installée autrement (depuis Android Studio ou `flutter run`, signée avec la clé de débogage) à ce paquet, il faut donc désinstaller l'application d'abord. L'historique, les réglages et les appareils de confiance sont alors perdus.
