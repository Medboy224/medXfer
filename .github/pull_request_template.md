<!-- Modèle tiré de la feuille de route, §5. Une PR par lot, idéalement moins de 600 lignes modifiées hors tests et fichiers golden. -->

## Objectif

<!-- Le lot (WP-x.y), ce qu'il change, et l'issue qu'il ferme (« Closes #NNN »). -->

## Exigences de la bible

<!-- Les identifiants couverts (STO-12, WEB-06…), et le statut qui change dans la bible. -->

## Tests

<!-- Les tests ajoutés. Pour une correction : le test qui échouait avant, avec sa sortie sur l'ancien code. Un défaut non reproduit est dit « préventif ». -->

## Mesure

<!-- Si le chemin chaud est touché : avant et après, mêmes appareils, chiffres archivés dans bench-results/. Sinon : « sans objet ». -->

## Essais réels

<!-- Appareils, réseau, ce qui a été vérifié. Ou ce qui reste à essayer, et par qui. -->

## Risques

<!-- Ce qui pourrait casser, et ce qui n'a pas été vérifié. -->

## Vérifications

- [ ] CI verte sur Windows, Linux et macOS
- [ ] Aucun secret dans les journaux, les événements ou les erreurs (bible, chapitre 13)
- [ ] Aucun nouveau `os.Remove` ou `Root.Remove` hors des fichiers créés par medXfer (`STO-09` à `STO-14`)
- [ ] Bible ou plan à jour si une décision ou un statut a changé
- [ ] Revue de sécurité faite si la PR touche `securechan`, `pairing`, `identity`, `webshare`, `safepath` ou `store`
- [ ] Constats hors lot ouverts en issues, pas corrigés en passant
