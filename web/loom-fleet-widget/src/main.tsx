import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { FleetOverview } from "./FleetOverview";
import "./styles.css";

const container = document.getElementById("root");
if (!container) {
  // The widget host always provides #root; this guard exists only so
  // the bundle is robust to being loaded outside a Skybridge/Apps SDK
  // host (for example when the MCP server smoke-test pipes the HTML
  // through a plain browser).
  throw new Error("loom-fleet-widget: #root element not found in host document");
}

createRoot(container).render(
  <StrictMode>
    <FleetOverview />
  </StrictMode>
);
