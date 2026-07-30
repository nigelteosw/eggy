---
title: Testing and releases
description: Run Eggy's required checks, understand CI coverage, and distinguish code verification from Docker smoke.
eyebrow: Project
---

Run the focused package test while developing, then execute the complete repository gate.

## Required verification

```sh
make fmt vet test race build
```

This formats Go packages, vets and tests the repository, runs the race detector, builds the embedded web application, and compiles `eggyd`.

If the default Go cache is unwritable in your environment:

```sh
GOCACHE=/tmp/eggy-go-cache make vet test race build
```

## Docker smoke

```sh
make smoke
```

The smoke script builds the container and exercises its runtime surface. Run it when Docker is available. Report an unavailable daemon separately rather than treating the skipped smoke as success.

## Continuous integration

CI checks Go formatting, vet, tests, the race detector, the embedded website build, and Docker smoke. The documentation job separately installs Bun dependencies, validates docs, and produces a static build.

## Persistence compatibility

Preserve `/data/state.json` schema compatibility. If a change cannot remain compatible, add an explicit migration and schema-version change. Apply the same care to readable cron files, encrypted OAuth data, and the SQLite schema.

## Release boundary

The container is the deployable artifact. Railway runs one `eggyd` process and persists the Eggy home on a mounted volume.
