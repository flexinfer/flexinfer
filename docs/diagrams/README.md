# Loom Core Diagrams

This folder contains Mermaid diagram sources used by `docs/ARCHITECTURE.md`.

## Hand-authored diagrams

- `component.mmd`: high-level component diagram
- `tool-call-sequence.mmd`: tool-call sequence flow
- `config-flow.mmd`: registry -> generate -> sync -> reload flow

## Auto-generated diagrams (`py-diagram-gen`)

Generated from `libs/py-diagram-gen`:

- `internal-modules.mmd`: imports/dependencies for `internal/`
- `pkg-modules.mmd`: imports/dependencies for `pkg/`

## Regeneration

From the monorepo root (`~/workspace` style layout):

```bash
cd libs/py-diagram-gen

uv run diagram-gen modules ../../services/loom-core/internal \
  -f mermaid -d LR -t light \
  -o ../../services/loom-core/docs/diagrams/internal-modules.mmd

uv run diagram-gen modules ../../services/loom-core/pkg \
  -f mermaid -d LR -t light \
  -o ../../services/loom-core/docs/diagrams/pkg-modules.mmd
```

From the `services/loom-core` repo directly:

```bash
mkdir -p docs/diagrams
cd ../../libs/py-diagram-gen

uv run diagram-gen modules ../../services/loom-core/internal \
  -f mermaid -d LR -t light \
  -o ../../services/loom-core/docs/diagrams/internal-modules.mmd

uv run diagram-gen modules ../../services/loom-core/pkg \
  -f mermaid -d LR -t light \
  -o ../../services/loom-core/docs/diagrams/pkg-modules.mmd
```

For SVG output, install Mermaid CLI (`mmdc`) and rerun with `--render-svg`.
