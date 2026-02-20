import net from "net";
import EventEmitter from "events";

export function createGodotClient({ host, port, reconnectMs = 5000, autoConnect = true }) {
  const emitter = new EventEmitter();
  let socket = null;
  let reconnectTimer = null;
  let buffer = "";

  function scheduleReconnect() {
    if (!autoConnect) return;
    if (reconnectTimer) return;
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null;
      connect();
    }, reconnectMs);
  }

  function handleData(data) {
    buffer += data.toString("utf8");
    let newlineIndex = buffer.indexOf("\n");
    while (newlineIndex !== -1) {
      const chunk = buffer.slice(0, newlineIndex).trim();
      buffer = buffer.slice(newlineIndex + 1);
      if (chunk) {
        try {
          const parsed = JSON.parse(chunk);
          emitter.emit("message", parsed);
        } catch (err) {
          emitter.emit("error", err);
        }
      }
      newlineIndex = buffer.indexOf("\n");
    }
  }

  function connect() {
    if (socket) return;
    socket = net.createConnection({ host, port }, () => {
      emitter.emit("connected");
    });

    socket.on("data", handleData);
    socket.on("error", (err) => {
      emitter.emit("error", err);
    });
    socket.on("close", () => {
      emitter.emit("disconnected");
      socket = null;
      scheduleReconnect();
    });
  }

  function send(payload) {
    if (!socket) {
      throw new Error("Godot client not connected");
    }
    socket.write(`${JSON.stringify(payload)}\n`);
  }

  function callCommand(command) {
    return new Promise((resolve, reject) => {
      const handler = (msg) => {
        emitter.off("error", errorHandler);
        emitter.off("message", handler);
        resolve(msg);
      };
      const errorHandler = (err) => {
        emitter.off("message", handler);
        emitter.off("error", errorHandler);
        reject(err);
      };
      emitter.on("message", handler);
      emitter.on("error", errorHandler);
      try {
        send(command);
      } catch (err) {
        emitter.off("message", handler);
        emitter.off("error", errorHandler);
        reject(err);
      }
    });
  }

  if (autoConnect) {
    connect();
  }

  return {
    on: emitter.on.bind(emitter),
    callCommand,
    send,
    connect,
  };
}
