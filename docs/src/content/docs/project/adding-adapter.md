---
title: Adding an adapter
description: Extend Eggy with a provider package and one bootstrap wiring change while keeping the kernel closed to provider details.
eyebrow: Project
---

A new provider should add one package under `plugins/<category>/<provider>/` plus composition-root wiring. It should not require edits to an existing adapter or provider-specific imports in the kernel.

## 1. Choose a port

Start in `internal/ports/ports.go`. Reuse the smallest suitable interface, such as `Model`, `Channel`, `ContextStore`, `StateStore`, `Scheduler`, `Runner`, `RepositoryCheckout`, `RepositoryReader`, or `Tool`.

Do not change an existing method signature to fit one provider. If the capability is genuinely new, add one narrow provider-neutral interface instead of widening a broad port.

## 2. Implement the provider package

Keep wire types, HTTP or CLI calls, error translation, and credentials inside the new plugin package.

```text
plugins/
  models/
    yourprovider/
      model.go
      model_test.go
```

Test against a fake transport or subprocess. Test fakes stay local to their package; a fake is not public API.

## 3. Add configuration

Put new non-secret fields and parsing in `internal/config`. Secrets should be named by configuration and loaded from environment variables.

For model backends, branch on `ProviderConfig.Adapter`; do not hardcode another unconditional constructor.

## 4. Wire in bootstrap

Construct the adapter in `internal/bootstrap` and hand it to an existing service or register its `ports.Tool`. Bootstrap is the only package allowed to know every adapter exists.

Add fake-adapter wiring when the capability participates in integration or Docker smoke tests.

## Safety constraints

Keep repository mutation and shipping out of generic tools and Telegram selections. Preserve path, environment, timeout, output, and process-group restrictions.

Calendar tools may be consolidated internally, but each mutation retains one independent approval action and one executor.
