extends EditorPlugin

func _enter_tree():
    add_autoload_singleton("MCPDebugServer", "res://addons/mcp_debug/debug_server.gd")

func _exit_tree():
    remove_autoload_singleton("MCPDebugServer")
