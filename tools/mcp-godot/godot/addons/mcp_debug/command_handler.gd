extends Node

const MAX_TREE_DEPTH := 3
const MAX_CHILDREN_PER_NODE := 50
const DEFAULT_SCREENSHOT_PATH := "user://mcp_debug.png"

var log_buffer: Array = [] # Optional in-memory log history for "logs" command
var subscribers: Array = []

func handle(command: Dictionary) -> Dictionary:
    var cmd := command.get("cmd", "")
    match cmd:
        "logs":
            return _handle_logs(command)
        "scene_tree":
            return _handle_scene_tree(command)
        "inspect":
            return _handle_inspect(command)
        "call":
            return _handle_call(command)
        "signal":
            return _handle_signal(command)
        "eval":
            return _handle_eval(command)
        "set":
            return _handle_set(command)
        "screenshot":
            return _handle_screenshot(command)
        "subscribe":
            return _handle_subscribe(command)
        "unsubscribe":
            return _handle_unsubscribe(command)
        _:
            return { "ok": false, "error": "Unknown cmd: %s" % cmd }

func _handle_logs(command: Dictionary) -> Dictionary:
    var lines: int = int(command.get("lines", 100))
    var filter: String = command.get("filter", "")
    var entries: Array = log_buffer
    if filter != "":
        entries = entries.filter(func(item): return String(item).find(filter) != -1)
    if lines > 0:
        entries = entries.slice(max(entries.size() - lines, 0), entries.size())
    return { "ok": true, "data": entries }

func _handle_scene_tree(command: Dictionary) -> Dictionary:
    var target := command.get("path", "/root")
    var node = _resolve_node(target)
    if node == null:
        return { "ok": false, "error": "Node not found: %s" % target }
    return { "ok": true, "data": _serialize_tree(node, target, MAX_TREE_DEPTH) }

func _handle_inspect(command: Dictionary) -> Dictionary:
    var target := command.get("path", command.get("node_path", ""))
    var node = _resolve_node(target)
    if node == null:
        return { "ok": false, "error": "Node not found: %s" % target }
    return { "ok": true, "data": _serialize_node(node) }

func _handle_call(command: Dictionary) -> Dictionary:
    var target := command.get("path", command.get("node_path", ""))
    var method := command.get("method", "")
    var args := command.get("args", [])
    var node = _resolve_node(target)
    if node == null:
        return { "ok": false, "error": "Node not found: %s" % target }
    if not node.has_method(method):
        return { "ok": false, "error": "Method not found: %s" % method }
    var result = node.callv(method, args)
    return { "ok": true, "data": result }

func _handle_signal(command: Dictionary) -> Dictionary:
    var target := command.get("path", command.get("node_path", ""))
    var signal_name := command.get("signal", "")
    var args := command.get("args", [])
    var node = _resolve_node(target)
    if node == null:
        return { "ok": false, "error": "Node not found: %s" % target }
    if not node.has_signal(signal_name):
        return { "ok": false, "error": "Signal not found: %s" % signal_name }
    node.emit_signal(signal_name, args)
    return { "ok": true, "data": "emitted" }

func _handle_eval(command: Dictionary) -> Dictionary:
    var expression := command.get("expr", command.get("expression", ""))
    if expression == "":
        return { "ok": false, "error": "Missing expression" }
    var expr := Expression.new()
    var parse_err := expr.parse(expression, [])
    if parse_err != OK:
        return { "ok": false, "error": "Parse error for expression" }
    var value = expr.execute([], self, true)
    if expr.has_execute_failed():
        return { "ok": false, "error": "Evaluation failed" }
    return { "ok": true, "data": value }

func _handle_set(command: Dictionary) -> Dictionary:
    var target := command.get("path", command.get("node_path", ""))
    var prop := command.get("prop", command.get("property", ""))
    var value = command.get("value", null)
    var node = _resolve_node(target)
    if node == null:
        return { "ok": false, "error": "Node not found: %s" % target }
    if not node.has_method("set"):
        return { "ok": false, "error": "Set not supported on node" }
    node.set(prop, value)
    return { "ok": true, "data": "updated" }

func _handle_screenshot(command: Dictionary) -> Dictionary:
    var save_path := command.get("save_path", DEFAULT_SCREENSHOT_PATH)
    var viewport := get_viewport()
    if viewport == null:
        return { "ok": false, "error": "No viewport available" }
    var img := viewport.get_texture().get_image()
    var err := img.save_png(save_path)
    if err != OK:
        return { "ok": false, "error": "Failed to save screenshot" }
    return { "ok": true, "data": { "path": save_path } }

func _handle_subscribe(command: Dictionary) -> Dictionary:
    var channel := command.get("channel", "")
    if channel != "" and not subscribers.has(channel):
        subscribers.append(channel)
    return { "ok": true, "data": "subscribed" }

func _handle_unsubscribe(command: Dictionary) -> Dictionary:
    var channel := command.get("channel", "")
    subscribers.erase(channel)
    return { "ok": true, "data": "unsubscribed" }

func _resolve_node(path: String):
    if path == "/root" or path == "/":
        return get_tree().get_root()
    if path.begins_with("/root/"):
        var relative := path.substr(6)
        return get_tree().get_root().get_node_or_null(relative)
    return get_tree().get_root().get_node_or_null(path)

func _serialize_tree(node: Node, path: String, depth: int) -> Dictionary:
    var data := {
        "name": node.name,
        "type": node.get_class(),
        "path": path,
        "children": []
    }
    if depth <= 0:
        return data
    var count := 0
    for child in node.get_children():
        if count >= MAX_CHILDREN_PER_NODE:
            break
        var child_path := path.rstrip("/") + "/" + String(child.name)
        data["children"].append(_serialize_tree(child, child_path, depth - 1))
        count += 1
    return data

func _serialize_node(node: Node) -> Dictionary:
    var info := {
        "name": node.name,
        "type": node.get_class(),
        "owner": node.owner if node.owner != null else null,
        "properties": {}
    }
    for prop_info in node.get_property_list():
        var prop_name: String = prop_info.get("name", "")
        if prop_name == "":
            continue
        var value = node.get(prop_name)
        info["properties"][prop_name] = _safe_value(value)
    return info

func _safe_value(value):
    if typeof(value) in [TYPE_INT, TYPE_FLOAT, TYPE_BOOL, TYPE_STRING, TYPE_NIL]:
        return value
    if value is Vector2 or value is Vector3 or value is Vector4:
        return value
    if value is Array:
        return value
    if value is Dictionary:
        return value
    if value is Node:
        return { "node": value.name }
    return str(value)
