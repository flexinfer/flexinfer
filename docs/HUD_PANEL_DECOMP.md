# HUD Panel Decomposition Pattern

How to break a multi-thousand-line `*Panel.svelte` monolith into composable
pieces that other panels can copy. Established by Slice B1 (FleetPanel,
1777 → <300 lines) per `.loom/117-implementation-plan-hud-ux-overhaul-2026-05-15.md`.

## Layout

```
internal/hud/frontend/src/lib/
├── components/
│   └── <panel>/
│       ├── <Section>Card.svelte        # one card / visual zone per file
│       ├── <Detail>.svelte             # drawer body, stays out of the panel
│       └── ...
├── stores/
│   └── <panel>.svelte.ts               # owns ALL filter/sort/select $state
└── utils/
    └── <panel>Rows.ts                  # pure row builders + typed row contract
```

The panel root file (`<Panel>Panel.svelte`) becomes a composition shell that
imports from the three buckets above. Target: under 300 lines including
markup and styles. If you need more, extract another card.

## Store contract

Every panel store exposes interactive state as `$state` and computed views
as `$derived`. Components read getters; mutators are explicit methods, not
prop drilling.

```ts
// lib/stores/<panel>.svelte.ts
class PanelStore {
  // Interactive state — UI mutates these directly via methods.
  filter = $state<string>('');
  search = $state<string>('');
  sortKey = $state<string>('default');
  sortDir = $state<'asc' | 'desc'>('asc');
  selected = $state<Set<string>>(new Set());

  // Mutators
  setSort(key: string, dir: 'asc' | 'desc') { this.sortKey = key; this.sortDir = dir; }
  setSearch(s: string) { this.search = s; }

  // Derived views — components subscribe via getters.
  get filtered() { /* derive from raw data + filter/search */ }
  get visible()  { /* derive from filtered + sort + pagination */ }
}
export const panelStore = new PanelStore();
```

## Pure row builders

Cross-store joins (e.g. fleet rows that need spawn lookups + claim expiry +
agent metadata) live in `lib/utils/<panel>Rows.ts`. They take their inputs
as parameters and return typed rows. This keeps the store free of cross-
store coupling and gives a single seam to test row shape.

```ts
// lib/utils/fleetRows.ts
export interface FleetRow { ... }
export function buildFleetRows(
  agents: UnifiedAgent[],
  sessions: Session[],
  spawnByAgentId: Map<string, SpawnState>,
  options: { sortKey: string; sortDir: 'asc' | 'desc'; groupByRootSession: boolean },
): FleetRow[] { ... }
```

The panel passes the joined inputs in a `$derived.by` and forwards the
result to the table component. Selectors stay pure and node-testable.

## Composition shell

```svelte
<!-- <Panel>Panel.svelte -->
<script>
  import { panelStore } from '../stores/<panel>.svelte.ts';
  import HeaderCard from './<panel>/HeaderCard.svelte';
  import MainTable from './<panel>/MainTable.svelte';
  import DetailDrawer from './<panel>/DetailDrawer.svelte';

  $effect(() => {
    panelStore.startPolling();
    return () => panelStore.stopPolling();
  });
</script>

<div class="panel">
  <HeaderCard />
  <MainTable />
  <DetailDrawer />
</div>
```

The shell is allowed minimal local `$state` for cross-cutting concerns (the
detail drawer's open/close ID usually lives in `router.detail`, not in the
store). Anything else belongs in the store.

## Validation per panel

- `wc -l internal/hud/frontend/src/lib/components/<Panel>Panel.svelte` < 300
- `pnpm build` clean, no new warnings on touched files
- `go test ./internal/hud/...` green
- Bookmark URL still resolves (`#<group>/<view>`); drill (`#<group>/<view>/<id>`)
  still opens the drawer
- Manual smoke against live daemon: filter / sort / drill / close drawer
- Side-by-side screenshot if behavior is at all suspicious

## Order of decomp

When applied to a panel:

1. **Move state into the store first.** Land the store contract change in
   isolation; the panel still works because it now reads via getters.
2. **Extract pure row builders.** Test them via a minimal smoke harness if
   the join logic is non-trivial.
3. **Extract one card at a time.** Start with the biggest. After each,
   verify the panel still renders identically.
4. **Extract the detail drawer last.** It's the most complex contract
   (stats chips + lineage + nested data) and benefits from the panel
   already being slim.

A canary panel (B1 = FleetPanel) establishes the pattern; B2 panels
(Spawn, Servers, Tasks, Sandbox, Graph) reuse it without re-deciding.
