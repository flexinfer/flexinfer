#!/usr/bin/env node
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import fs from "fs";
import path from "path";
import { fileURLToPath } from "url";
import { createGodotClient } from "./src/godot-client.js";
import { createLogReader } from "./src/log-reader.js";
import { godotTools } from "./src/tools/godot.js";
import { logTools } from "./src/tools/logs.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

function loadConfig() {
  const configPath = path.join(__dirname, "config.json");
  let fileConfig = {};
  if (fs.existsSync(configPath)) {
    try {
      fileConfig = JSON.parse(fs.readFileSync(configPath, "utf8"));
    } catch (err) {
      console.error("Failed to parse config.json", err);
      process.exit(1);
    }
  }

  return {
    godot_host: process.env.GODOT_HOST || fileConfig.godot_host || "127.0.0.1",
    godot_port: Number(process.env.GODOT_PORT || fileConfig.godot_port || 6550),
    log_path:
      process.env.GODOT_LOG_PATH || fileConfig.log_path || path.join(process.env.HOME || "~", "Library/Application Support/Godot"),
    reconnect_interval: Number(
      process.env.GODOT_RECONNECT_MS || fileConfig.reconnect_interval || 5000,
    ),
    auto_connect:
      (process.env.GODOT_AUTO_CONNECT || String(fileConfig.auto_connect) || "true") ===
      "true",
  };
}

async function main() {
  const config = loadConfig();
  const server = new McpServer({
    name: "godot-debug",
    version: "0.1.0",
  });

  const transport = new StdioServerTransport();
  const logReader = createLogReader(config.log_path);
  const godotClient = createGodotClient({
    host: config.godot_host,
    port: config.godot_port,
    reconnectMs: config.reconnect_interval,
    autoConnect: config.auto_connect,
  });

  logTools(server, { logReader });
  godotTools(server, { godotClient });

  // Add empty resource handlers to satisfy some clients
  server.server.setRequestHandler(
    { method: "resources/list" },
    async () => ({ resources: [] })
  );
  server.server.setRequestHandler(
    { method: "resources/templates/list" },
    async () => ({ resourceTemplates: [] })
  );

  await server.connect(transport);
}

main().catch((err) => {
  console.error("godot-debug MCP server failed", err);
  process.exit(1);
});
