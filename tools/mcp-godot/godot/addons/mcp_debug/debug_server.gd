extends Node

var port := 6550
var host := "127.0.0.1"
var server := TCPServer.new()
var connections: Array = [] # { peer: StreamPeerTCP, buffer: String }
var command_handler := preload("res://addons/mcp_debug/command_handler.gd").new()

func _ready():
    var err = server.listen(port, host)
    if err != OK:
        push_error("[MCP Debug] Failed to listen on %s:%s (err=%s)" % [host, port, err])
        return
    print("[MCP Debug] Listening on %s:%s" % [host, port])
    set_process(true)

func _process(_delta):
    _accept_new_connections()
    _poll_connections()

func _accept_new_connections():
    while server.is_connection_available():
        var peer: StreamPeerTCP = server.take_connection()
        connections.append({ "peer": peer, "buffer": "" })
        print("[MCP Debug] Client connected from %s" % peer.get_connected_host())

func _poll_connections():
    var i = 0
    while i < connections.size():
        var entry = connections[i]
        var peer: StreamPeerTCP = entry["peer"]

        if peer.get_status() != StreamPeerTCP.STATUS_CONNECTED:
            connections.remove_at(i)
            continue

        var available := peer.get_available_bytes()
        if available > 0:
            var chunk := peer.get_utf8_string(available)
            entry["buffer"] += chunk
            _drain_buffer(peer, entry)
        i += 1

func _drain_buffer(peer: StreamPeerTCP, entry: Dictionary):
    var buffer: String = entry["buffer"]
    var newline := buffer.find("\n")
    while newline != -1:
        var line := buffer.substr(0, newline).strip_edges()
        buffer = buffer.substr(newline + 1)
        if line != "":
            _handle_line(peer, line)
        newline = buffer.find("\n")
    entry["buffer"] = buffer

func _handle_line(peer: StreamPeerTCP, line: String):
    var parsed = JSON.parse_string(line)
    if parsed == null:
        _send_error(peer, "Invalid JSON")
        return

    var response: Dictionary = command_handler.handle(parsed)
    _send(peer, response)

func _send(peer: StreamPeerTCP, payload: Dictionary):
    var text := JSON.stringify(payload)
    peer.put_utf8_string(text + "\n")
    peer.flush()

func _send_error(peer: StreamPeerTCP, message: String):
    _send(peer, { "ok": false, "error": message })
