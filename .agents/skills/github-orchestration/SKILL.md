---
name: github-orchestration
description: >-
  Orchestrate multi-agent work on medXfer via GitHub issues, isolated branches or worktrees,
  and pull requests matching .github/pull_request_template.md. Use when starting a work
  package, splitting an audit or feature across parallel agents, opening follow-up issues,
  or preparing a PR on GitHub.
---

# Orchestration GitHub et multi-agents

Runbook pour coordonner plusieurs agents sur `medXfer` via GitHub sans collisions de branches ni PR hors périmètre.

## 1. Reconnaissance avant action

1. Vérifier l'état local : `git branch --show-current`, `git status --short`, `git worktree list`.
2. Vérifier l'état GitHub (hors sandbox si réseau requis) : `gh issue list --state open`, `gh pr list --state open`.
3. Si la copie de travail contient déjà des modifications d'un autre lot (ou si la branche courante porte une autre PR), **demander ou créer une branche/worktree dédiée depuis `main`** avant d'éditer.

## 2. Découpage multi-agents

1. Chaque agent travaille sur un **lot borné** (`< 600` lignes modifiées hors tests et golden) et un ensemble de fichiers disjoint des lots concurrents.
2. Pour un sujet transverse (ex. correctifs Windows / Android / Dart / Go), préférer des PR séparées par couche ou par issue plutôt qu'une PR géante.
3. Tout constat hors du lot devient une **issue GitHub séparée** (jamais corrigé en passant).

## 3. Ouverture d'issue et de PR

1. Commit(s) au format `type(portée) : WP-x.y, ce qui change` (en français), avec uniquement `Co-Authored-By:` si écrit avec une IA — **aucun lien vers une session ou conversation** (dépôt public).
2. Vérifier avant PR : `gofmt -l .` vide, `go vet ./...`, `go vet -tags diag ./...`, `go test ./...`, `flutter analyze`, `flutter test`.
3. Rédiger la PR selon `.github/pull_request_template.md` (`Objectif`, `Exigences de la bible`, `Tests`, `Mesure`, `Essais réels`, `Risques`, `Vérifications`) sans cocher la CI ni inventer d'essais réels non effectués par le développeur.
4. Ne jamais fusionner ni pousser sur `main` sans accord explicite du développeur.
