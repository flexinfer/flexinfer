import assert from "node:assert/strict";
import net from "node:net";
import { EventEmitter } from "node:events";
import test from "node:test";
import { createGodotClient } from "../src/godot-client.js";

function createFakeSocket() {
  const socket = new EventEmitter();
  socket.writes = [];
  socket.write = (payload) => {
    socket.writes.push(payload);
    return true;
  };
  return socket;
}

test("callCommand resolves with parsed Godot response", async () => {
  const originalCreateConnection = net.createConnection;
  const socket = createFakeSocket();

  socket.write = (payload) => {
    socket.writes.push(payload);
    setImmediate(() => {
      socket.emit("data", Buffer.from('{"ok":true,"result":"pong"}\n', "utf8"));
    });
    return true;
  };

  net.createConnection = (_options, onConnect) => {
    setImmediate(() => onConnect?.());
    return socket;
  };

  try {
    const client = createGodotClient({
      host: "127.0.0.1",
      port: 6550,
      autoConnect: true,
      reconnectMs: 10,
    });

    const response = await client.callCommand({ cmd: "ping" });
    assert.deepEqual(response, { ok: true, result: "pong" });
    assert.match(socket.writes[0], /"cmd":"ping"/);
  } finally {
    net.createConnection = originalCreateConnection;
  }
});

test("callCommand rejects when response contains malformed JSON", async () => {
  const originalCreateConnection = net.createConnection;
  const socket = createFakeSocket();

  socket.write = () => {
    setImmediate(() => {
      socket.emit("data", Buffer.from("{invalid-json}\n", "utf8"));
    });
    return true;
  };

  net.createConnection = (_options, onConnect) => {
    setImmediate(() => onConnect?.());
    return socket;
  };

  try {
    const client = createGodotClient({
      host: "127.0.0.1",
      port: 6550,
      autoConnect: true,
      reconnectMs: 10,
    });

    await assert.rejects(client.callCommand({ cmd: "ping" }));
  } finally {
    net.createConnection = originalCreateConnection;
  }
});

test("callCommand rejects when client is not connected", async () => {
  const client = createGodotClient({
    host: "127.0.0.1",
    port: 6550,
    autoConnect: false,
  });

  await assert.rejects(client.callCommand({ cmd: "ping" }), /not connected/i);
});
