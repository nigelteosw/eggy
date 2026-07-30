# Eggy documentation

The documentation is a static Astro application published at:

`https://nigelteosw.github.io/eggy/docs/`

## Develop

```sh
cd docs
bun install
bun run dev
```

Astro serves the project with the production base path at
`http://localhost:4321/eggy/docs/`.

## Verify

```sh
bun test
bun run check
bun run build
bun run validate
```

## Package for GitHub Pages

```sh
bun run build:pages
```

The normal Astro output is `docs/dist/`. The Pages command validates that
build, then copies it into `docs/pages-root/docs/` with a root `.nojekyll`.
GitHub Pages supplies the repository prefix `/eggy`, so the nested artifact is
served from `/eggy/docs/`.
