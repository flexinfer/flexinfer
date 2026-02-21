export function logTools(server, { logReader }) {
  server.tool("godot_logs", {
    description: "Tail recent Godot game/editor logs from the local log file.",
    inputSchema: {
      type: "object",
      properties: {
        lines: { type: "number", minimum: 1, maximum: 500, default: 50 },
        filter: { type: "string" },
      },
    },
  }, async (args) => {
    const lines = logReader.readRecent({ lines: args.lines, filter: args.filter });
    return {
      content: [
        {
          type: "text",
          text: lines.join("\n"),
        },
      ],
    };
  });

  server.tool("godot_logs_stream", {
    description: "Stream Godot logs in real time (polls log file).",
    inputSchema: {
      type: "object",
      properties: {
        duration: { type: "number", minimum: 1, maximum: 300, default: 60 },
        filter: { type: "string" },
      },
    },
  }, async (args) => {
    const durationMs = (args.duration || 60) * 1000;
    const lines = await logReader.tailStream({ durationMs, filter: args.filter });
    return {
      content: [
        {
          type: "text",
          text: lines.join("\n"),
        },
      ],
    };
  });
}
