function notReady(text) {
  return {
    content: [
      {
        type: "text",
        text,
      },
    ],
  };
}

export function godotTools(server, { godotClient }) {
  server.tool("godot_scene_tree", {
    description: "Fetch the current scene tree from Godot (plugin must be running).",
    inputSchema: {
      type: "object",
      properties: {
        path: { type: "string" },
      },
    },
  }, async (args) => {
    try {
      const response = await godotClient.callCommand({ cmd: "scene_tree", path: args.path || "/root" });
      return { content: [{ type: "json", json: response }] };
    } catch (err) {
      return notReady(`scene_tree failed: ${err.message}`);
    }
  });

  server.tool("godot_inspect", {
    description: "Inspect a node by path (plugin must be running).",
    inputSchema: {
      type: "object",
      required: ["node_path"],
      properties: {
        node_path: { type: "string" },
      },
    },
  }, async (args) => {
    try {
      const response = await godotClient.callCommand({ cmd: "inspect", path: args.node_path });
      return { content: [{ type: "json", json: response }] };
    } catch (err) {
      return notReady(`inspect failed: ${err.message}`);
    }
  });

  server.tool("godot_call", {
    description: "Call a method on a node (plugin must be running).",
    inputSchema: {
      type: "object",
      required: ["node_path", "method"],
      properties: {
        node_path: { type: "string" },
        method: { type: "string" },
        args: { type: "array" },
      },
    },
  }, async (args) => {
    try {
      const response = await godotClient.callCommand({
        cmd: "call",
        path: args.node_path,
        method: args.method,
        args: args.args || [],
      });
      return { content: [{ type: "json", json: response }] };
    } catch (err) {
      return notReady(`call failed: ${err.message}`);
    }
  });

  server.tool("godot_signal", {
    description: "Emit a signal on a node (plugin must be running).",
    inputSchema: {
      type: "object",
      required: ["node_path", "signal"],
      properties: {
        node_path: { type: "string" },
        signal: { type: "string" },
        args: { type: "array" },
      },
    },
  }, async (args) => {
    try {
      const response = await godotClient.callCommand({
        cmd: "signal",
        path: args.node_path,
        signal: args.signal,
        args: args.args || [],
      });
        return { content: [{ type: "json", json: response }] };
    } catch (err) {
      return notReady(`signal failed: ${err.message}`);
    }
  });

  server.tool("godot_eval", {
    description: "Evaluate a GDScript expression (plugin must be running; consider sandboxing).",
    inputSchema: {
      type: "object",
      required: ["expression"],
      properties: {
        expression: { type: "string" },
      },
    },
  }, async (args) => {
    try {
      const response = await godotClient.callCommand({ cmd: "eval", expr: args.expression });
      return { content: [{ type: "json", json: response }] };
    } catch (err) {
      return notReady(`eval failed: ${err.message}`);
    }
  });

  server.tool("godot_set", {
    description: "Set a property on a node (plugin must be running).",
    inputSchema: {
      type: "object",
      required: ["node_path", "property", "value"],
      properties: {
        node_path: { type: "string" },
        property: { type: "string" },
        value: {},
      },
    },
  }, async (args) => {
    try {
      const response = await godotClient.callCommand({
        cmd: "set",
        path: args.node_path,
        prop: args.property,
        value: args.value,
      });
      return { content: [{ type: "json", json: response }] };
    } catch (err) {
      return notReady(`set failed: ${err.message}`);
    }
  });

  server.tool("godot_screenshot", {
    description: "Capture a screenshot (plugin must be running).",
    inputSchema: {
      type: "object",
      properties: {
        save_path: { type: "string" },
      },
    },
  }, async (args) => {
    try {
      const response = await godotClient.callCommand({ cmd: "screenshot", save_path: args.save_path });
      return { content: [{ type: "json", json: response }] };
    } catch (err) {
      return notReady(`screenshot failed: ${err.message}`);
    }
  });
}
