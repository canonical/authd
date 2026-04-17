# authd docs variant demo — stable vs. edge side by side

This guide shows how to build and serve the `stable-docs` and `edge-docs`
variants of the authd documentation locally, using only raw `sphinx-build`
and `python3 -m http.server` commands. No Makefile changes are required.

## Why two variants?

Read the Docs hosts two separate projects for authd:

| RTD project | URL | `READTHEDOCS_VERSION` value |
|---|---|---|
| stable | https://documentation.ubuntu.com/authd/stable-docs/ | `stable-docs` |
| edge | https://documentation.ubuntu.com/authd/edge-docs/ | `edge-docs` |

RTD injects `READTHEDOCS_VERSION` automatically. `docs/conf.py` reads it to:
- set the correct `og:url` meta tag (edge vs. stable URL)
- register the matching Sphinx build tag (`edge` or `stable`) so that
  `{only}` directives in `.md` source files include/exclude content correctly

## Prerequisites

The virtualenv must exist. If you have not set it up yet:

```bash
cd docs
make install   # sets up .sphinx/venv — only needed once
```

`make install` is the original upstream target; no Makefile changes are involved.

## Build both variants

Run these two commands from the `docs/` directory. Each writes to its own
output directory so the builds do not overwrite each other.

```bash
# Stable variant → _build-stable/
READTHEDOCS_VERSION=stable-docs \
  .sphinx/venv/bin/sphinx-build \
  --fail-on-warning --keep-going -b dirhtml \
  -c . -d .sphinx/.doctrees-stable -j auto \
  . _build-stable

# Edge variant → _build-edge/
READTHEDOCS_VERSION=edge-docs \
  .sphinx/venv/bin/sphinx-build \
  --fail-on-warning --keep-going -b dirhtml \
  -c . -d .sphinx/.doctrees-edge -j auto \
  . _build-edge
```

## Serve both variants simultaneously

Open two terminals, both from the `docs/` directory:

**Terminal 1 — stable on port 8000:**
```bash
cd _build-stable && python3 -m http.server --bind 127.0.0.1 8000
```

**Terminal 2 — edge on port 8001:**
```bash
cd _build-edge && python3 -m http.server --bind 127.0.0.1 8001
```

Then open both in a browser, ideally side by side:

| | Stable | Edge |
|---|---|---|
| URL | http://127.0.0.1:8000/ | http://127.0.0.1:8001/ |
| Header banner | — | "This is the **edge** version…" |
| `og:url` meta | `.../stable-docs` | `.../edge-docs` |
| `{only} edge` blocks | hidden | visible |
| `{only} stable` blocks | visible | hidden |

## Best page to compare

Open the install guide in both browsers:

- http://127.0.0.1:8000/howto/install-authd/
- http://127.0.0.1:8001/howto/install-authd/

Any `{only} edge` admonition you add to that page will appear only on the
right-hand (edge) browser tab.

## Clean up build artefacts

```bash
# From docs/
rm -rf _build-stable _build-edge .sphinx/.doctrees-stable .sphinx/.doctrees-edge
```

## How to write variant-specific content

In any `.md` source file, use the `{only}` directive with the `edge` or `stable` tag.

### Basic usage — paragraphs, lists, admonitions

````markdown
::::{only} edge
This paragraph is visible only in the edge-docs build.
::::

::::{only} stable
This paragraph is visible only in the stable-docs build.
::::
````

### Nesting directives — the outer fence must use more characters

When wrapping an existing directive (e.g. an admonition), the outer `{only}`
fence must have **more** colon/backtick characters than the inner one:

````markdown
:::::{only} edge
:::{admonition} Edge-only note
:class: important
This admonition appears only in edge builds.
:::
:::::
````

### ⚠️ Headings cannot go inside `{only}` — a key Sphinx limitation

**Section headings (`##`, `###`, …) must never be placed inside an `{only}`
directive.** Sphinx promotes headings out of the block during document-structure
parsing, which happens *before* tag evaluation. The result is that the heading
appears in **every** build regardless of the tag — only the body content is filtered.

**Wrong** — heading leaks into all builds:

````markdown
::::{only} stable
## System requirements

* Ubuntu 24.04 LTS or later
::::
````

**Correct** — heading always present, only the body is conditional:

````markdown
## System requirements

::::{only} stable
* Ubuntu 24.04 LTS or later
::::
````

The `edge` and `stable` Sphinx tags are registered in `docs/conf.py` from
`READTHEDOCS_VERSION`. On RTD the value is injected automatically; locally
the raw `sphinx-build` commands above set it explicitly.
