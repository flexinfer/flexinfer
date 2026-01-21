# Loom Core diagrams

This folder contains diagram sources used by `docs/ARCHITECTURE.md`.

## Hand-authored diagrams

- `component.mmd`: high-level component diagram
- `tool-call-sequence.mmd`: tool-call sequence flow
- `config-flow.mmd`: registry → generation → sync → reload flow

## Auto-generated diagrams (py-diagram-gen)

These are generated via `libs/py-diagram-gen` (CLI: `diagram-gen`).

- `internal-modules.mmd`: imports/dependencies for `services/loom-core/internal/`
- `pkg-modules.mmd`: imports/dependencies for `services/loom-core/pkg/`

Regenerate (from repo root):

```bash
mkdir -p services/loom-core/docs/diagrams
cd libs/py-diagram-gen

uv run diagram-gen modules ../../services/loom-core/internal \
  -f mermaid -d LR -t light \
  -o ../../services/loom-core/docs/diagrams/internal-modules.mmd

uv run diagram-gen modules ../../services/loom-core/pkg \
  -f mermaid -d LR -t light \
  -o ../../services/loom-core/docs/diagrams/pkg-modules.mmd
```

If you want rendered SVGs, install Mermaid CLI (`mmdc`) and rerun with `--render-svg`.

