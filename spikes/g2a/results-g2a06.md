# G2A-06 — mesures brutes (boucle locale, application vs `xfer`)

Durée « moteur » = durée rapportée par le récepteur (`OnComplete`), du démarrage des workers au
dernier fragment reçu, sans la finalisation (fsync, renommage). Médianes ; essais alternés.

## Windows 11 (PC du projet, 8 threads, SSD) — 1 Gio, 7 essais

| | médiane | débit |
|---|---|---|
| `xfer send` + `xfer recv` | 2 034 ms | 503 Mio/s |
| application | 2 025 ms | 506 Mio/s |

Application / xfer : **100,4 %**. Hash identiques.

## Android 16, SM-A576B (arm64) — 1 Gio, 7 essais

| | médiane | débit |
|---|---|---|
| `xfer` dans Termux (en arrière-plan, via SSH) | 3 476 ms | 295 Mio/s |
| application (premier plan) | 2 446 ms | 419 Mio/s |

Application / xfer : **142 %**. Termux tourne en arrière-plan, l'application au premier plan.

## Linux, Zorin OS 17 (i5-2520M 2 cœurs / 4 threads, 3,8 Go, disque mécanique)

1 Gio, 7 essais, source régénérée à chaque essai : xfer 13 974 ms, application 15 145 ms (92,3 %),
écarts de 9,9 à 28 s : surtout le disque.

256 Mio, même fichier source pour tous, `sync` avant chaque essai (le reste de l'enquête) :

| variante | moteur (médiane) | / xfer |
|---|---|---|
| `xfer` (deux processus) | 509 ms | 100 % |
| `oneproc` : émetteur + récepteur dans un processus Go, sans C ni Flutter | 499 ms | 102 % |
| `cprobe` : `libmedxfer.so` chargée par un programme C, frontière C, modèle poll | 500 ms | **101,8 %** |
| application, départ immédiat | 671–695 ms | 74–78 % |
| application, départ après 3 s | 588–590 ms | 89–90 % |
| application, après 3 s, sans redessin | 599 ms | 88 % |
| application, modèle callback / poll | 670–700 / 560–690 ms | pas de différence |

Temps total (finalisation comprise, dominé par le disque) : application / xfer 87 à 97 %.

**Conclusions.**
- Le pont ne coûte rien : la bibliothèque `c-shared` appelée par la frontière C fait 101,8 % de `xfer`.
- Ni le modèle d'événements, ni GOMAXPROCS (4 dans les deux cas), ni l'affinité CPU, ni le
  redessin n'expliquent l'écart.
- L'écart vient du **processus hôte Flutter** sur cette machine faible : environ 15 points au
  démarrage de l'application (travail de démarrage de Flutter en parallèle), environ 11 points
  ensuite, cause non identifiée sans profileur.
- Sur Windows (8 threads) et sur le téléphone, aucun écart.
- Plafond mesuré en boucle locale : environ 500 Mio/s ; un vrai transfert est limité par le réseau
  (3 Mio/s en Wi-Fi ici, 110 Mo/s en gigabit) : à ce débit, ce coût CPU ne se voit pas.
