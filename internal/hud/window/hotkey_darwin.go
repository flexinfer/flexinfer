//go:build darwin

package window

/*
#cgo CFLAGS: -x objective-c -fmodules
#cgo LDFLAGS: -framework Carbon -framework Cocoa

#import <Carbon/Carbon.h>
#import <Cocoa/Cocoa.h>

// Hotkey reference stored as a static global.
static EventHotKeyRef hotkeyRef = NULL;

// C-callable callback that will be invoked from the Carbon event handler.
// Defined in Go via //export.
extern void goHotkeyCallback(void);

// Carbon event handler for hotkey events.
static OSStatus hotkeyHandler(EventHandlerCallRef nextHandler, EventRef event, void *userData) {
    (void)nextHandler;
    (void)event;
    (void)userData;
    goHotkeyCallback();
    return noErr;
}

// registerCarbonHotkey registers Cmd+Shift+L as a global hotkey.
// Returns 0 on success, non-zero on error.
static int registerCarbonHotkey(void) {
    // Install the hotkey event handler.
    EventTypeSpec eventType;
    eventType.eventClass = kEventClassKeyboard;
    eventType.eventKind  = kEventHotKeyPressed;

    OSStatus status = InstallApplicationEventHandler(
        &hotkeyHandler,
        1,
        &eventType,
        NULL,
        NULL
    );
    if (status != noErr) {
        return (int)status;
    }

    // Register Cmd+Shift+L.
    // 'L' key code is 0x25 (kVK_ANSI_L).
    // Modifiers: cmdKey | shiftKey.
    EventHotKeyID hotkeyID;
    hotkeyID.signature = 'LOOM';
    hotkeyID.id        = 1;

    status = RegisterEventHotKey(
        kVK_ANSI_L,
        cmdKey | shiftKey,
        hotkeyID,
        GetApplicationEventTarget(),
        0,
        &hotkeyRef
    );
    return (int)status;
}

// unregisterCarbonHotkey removes the registered global hotkey.
// Returns 0 on success, non-zero on error.
static int unregisterCarbonHotkey(void) {
    if (hotkeyRef == NULL) {
        return 0;
    }
    OSStatus status = UnregisterEventHotKey(hotkeyRef);
    hotkeyRef = NULL;
    return (int)status;
}
*/
import "C"
import (
	"fmt"
	"sync"
)

var (
	hotkeyMu       sync.Mutex
	hotkeyCallback func()
	hotkeyActive   bool
)

//export goHotkeyCallback
func goHotkeyCallback() {
	hotkeyMu.Lock()
	cb := hotkeyCallback
	hotkeyMu.Unlock()

	if cb != nil {
		cb()
	}
}

// RegisterHotkey registers Cmd+Shift+L as a system-wide global hotkey.
// When pressed, the provided callback function is invoked. The callback is
// called from a Carbon event handler context; keep it fast and non-blocking.
// Only one hotkey can be registered at a time; call UnregisterHotkey first
// to change the callback.
func RegisterHotkey(callback func()) error {
	if callback == nil {
		return fmt.Errorf("callback must not be nil")
	}

	hotkeyMu.Lock()
	defer hotkeyMu.Unlock()

	if hotkeyActive {
		return fmt.Errorf("hotkey already registered; call UnregisterHotkey first")
	}

	hotkeyCallback = callback

	rc := C.registerCarbonHotkey()
	if rc != 0 {
		hotkeyCallback = nil
		return fmt.Errorf("RegisterEventHotKey failed with status %d", int(rc))
	}

	hotkeyActive = true
	return nil
}

// UnregisterHotkey removes the previously registered global hotkey.
func UnregisterHotkey() error {
	hotkeyMu.Lock()
	defer hotkeyMu.Unlock()

	if !hotkeyActive {
		return nil
	}

	rc := C.unregisterCarbonHotkey()
	if rc != 0 {
		return fmt.Errorf("UnregisterEventHotKey failed with status %d", int(rc))
	}

	hotkeyCallback = nil
	hotkeyActive = false
	return nil
}
