// FleetOverview is the entry component for the loom fleet widget. In
// slice 1b-β this is a static placeholder that exercises the full
// build pipeline (Vite → single-file HTML → Go embed → MCP Apps host).
// Live data wiring against /api/mobile/v1/dashboard via the MCP server
// proxy lands in slice 1b-γ.
export function FleetOverview() {
  return (
    <div className="card">
      <h1>
        <span className="dot" aria-hidden="true" />
        Loom Fleet
      </h1>
      <p className="sub">
        Slice 1b-β placeholder — React widget bundled via Vite into a single
        HTML resource, embedded in the <code>mcp-loom-widget</code> server.
      </p>
      <Row label="Source" value="mcp-loom-widget" />
      <Row label="Bundler" value="Vite + react + vite-plugin-singlefile" />
      <Row label="Wire format" value="MCP Apps (ui:// resource)" />
      <Row label="Next slice" value="1b-γ wire live HUD data" />
      <p className="placeholder">
        When 1b-γ lands, this widget will fetch live data from{" "}
        <code>/api/mobile/v1/dashboard</code> via the MCP server proxy so the
        bearer token never enters the LLM context.
      </p>
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="row">
      <span className="label">{label}</span>
      <span className="value">{value}</span>
    </div>
  );
}
