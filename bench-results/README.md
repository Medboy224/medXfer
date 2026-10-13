# Mesures archivées (`bench-results/`)

Les mesures sur appareils réels (bible §17.4, DEV-21) sont gardées ici pour comparer les appareils et les versions. Chaque fichier `matrix-<appareil>-<système>-<date>.json` est la sortie de `xfer bench matrix` côté **receveur**.

## 13 octobre 2026 — valeurs par défaut (WP-4.9, D8 du plan J4b)

**Conditions de mesure :**
- PC Windows (« Naruto ») et Samsung Galaxy A57 sous Termux (Android 16), reliés par le hotspot direct du téléphone (environ 6 à 13 MB/s) ;
- marche R6 : le transfert complet, avec offre, journal, `fsync`, `digest` et renommage ;
- 256 Mio par passage.

| Fichier | Sens | Passages par case | Débits médians | Dispersion maximale |
|---|---|---|---|---|
| `matrix-Naruto-windows-20261013-115050.json` | téléphone → PC | 1 | 11,5 à 13,0 MB/s | inconnue (1 passage) |
| `matrix-localhost-android-20261013-121346.json` | PC → téléphone | 3 | 6,8 à 12,3 MB/s | jusqu'à ×3 dans une même case |

**Dossier de 2 000 petits fichiers** (`small2000`, PC → téléphone, `xfer recv --workers N`) :

| Flux | 1 | 2 | 4 | 8 |
|---|---|---|---|---|
| Durée | 16,1 s / 16,7 s | 9,0 s / 6,9 s | 4,6 s | 4,3 s / 4,8 s |

**Décision : on garde 4 flux × 4 Mio.**

- **Le lien limite tout.** L'étape limitante est `net_wait` partout. Côté PC, les 16 combinaisons sont à ±6 % les unes des autres.
- **La règle tombe dans le bruit.** Elle propose 1 × 1 Mio dans un sens et 2 × 2 Mio dans l'autre. Mais les écarts entre cases sont plus petits que la dispersion entre passages d'une même case, qui dépasse de loin la tolérance de 5 %.
- **Moins de 4 flux est nettement plus lent.** C'est le cas pour un dossier de petits fichiers (1 flux : ×3,5 ; 2 flux : ×1,5 à ×2) comme en réception sur le téléphone (1 flux : 7,7 à 10,5 MB/s, contre 10,9 à 12,3 MB/s avec 4 flux).
- **Au-delà, rien ne change.** 8 flux n'apportent rien par rapport à 4, et aucune taille de chunk ne se distingue.

Depuis ces mesures, `bench matrix` indique la dispersion de chaque case et déclare la recommandation **non fiable** si un seul passage a été fait, ou si la dispersion dépasse la tolérance. Il faudra refaire la matrice sur un lien plus rapide et plus stable (Wi-Fi 5 GHz, Ethernet), avec `--repeat 3`, avant de changer ces valeurs.
