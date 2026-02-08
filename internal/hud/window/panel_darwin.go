//go:build darwin

// Package window provides macOS native overlay panel management via CGo.
package window

/*
#cgo CFLAGS: -x objective-c -fmodules
#cgo LDFLAGS: -framework Cocoa -framework Carbon -framework WebKit -framework QuartzCore

#include <stdlib.h>
#import <Cocoa/Cocoa.h>
#import <Carbon/Carbon.h>
#import <WebKit/WebKit.h>
#import <QuartzCore/QuartzCore.h>

// Static globals for the overlay panel and web view.
static NSPanel *overlayPanel = nil;
static WKWebView *overlayWebView = nil;

void createOverlayPanel(int x, int y, int width, int height, const char* url) {
    // Convert the URL to an NSString NOW, before dispatch_async returns and
    // the Go caller frees the C string. Blocks capture ObjC objects by
    // retaining them, so urlStr survives until the block executes.
    NSString *urlStr = [NSString stringWithUTF8String:url];

    dispatch_async(dispatch_get_main_queue(), ^{
        // If a panel already exists, destroy it first.
        if (overlayPanel != nil) {
            [overlayPanel close];
            overlayPanel = nil;
            overlayWebView = nil;
        }

        NSRect frame = NSMakeRect(x, y, width, height);

        // NSPanel with non-activating, utility, resizable style.
        NSUInteger style = NSWindowStyleMaskTitled |
                           NSWindowStyleMaskClosable |
                           NSWindowStyleMaskResizable |
                           NSWindowStyleMaskNonactivatingPanel |
                           NSWindowStyleMaskUtilityWindow;

        overlayPanel = [[NSPanel alloc] initWithContentRect:frame
                                                  styleMask:style
                                                    backing:NSBackingStoreBuffered
                                                      defer:NO];

        // Float above other windows.
        [overlayPanel setLevel:NSFloatingWindowLevel];
        [overlayPanel setHidesOnDeactivate:NO];
        [overlayPanel setCollectionBehavior:
            NSWindowCollectionBehaviorCanJoinAllSpaces |
            NSWindowCollectionBehaviorFullScreenAuxiliary];

        // Translucent background.
        [overlayPanel setOpaque:NO];
        [overlayPanel setBackgroundColor:[NSColor colorWithCalibratedWhite:0.05 alpha:0.92]];

        // Title bar customization: hidden title, transparent title bar.
        [overlayPanel setTitleVisibility:NSWindowTitleHidden];
        [overlayPanel setTitlebarAppearsTransparent:YES];

        // Visual effect view for vibrancy (behind-window blending).
        NSVisualEffectView *visualEffect = [[NSVisualEffectView alloc]
            initWithFrame:[[overlayPanel contentView] bounds]];
        [visualEffect setAutoresizingMask:NSViewWidthSizable | NSViewHeightSizable];
        [visualEffect setMaterial:NSVisualEffectMaterialUnderWindowBackground];
        [visualEffect setBlendingMode:NSVisualEffectBlendingModeBehindWindow];
        [visualEffect setState:NSVisualEffectStateActive];
        [overlayPanel setContentView:visualEffect];

        // Create WKWebView configuration.
        WKWebViewConfiguration *config = [[WKWebViewConfiguration alloc] init];
        [config.preferences setValue:@YES forKey:@"developerExtrasEnabled"];

        // Create WKWebView filling the visual effect view.
        overlayWebView = [[WKWebView alloc]
            initWithFrame:[visualEffect bounds]
            configuration:config];
        [overlayWebView setAutoresizingMask:NSViewWidthSizable | NSViewHeightSizable];

        // Transparent web view background so the vibrancy shows through.
        [overlayWebView setValue:@NO forKey:@"drawsBackground"];

        [visualEffect addSubview:overlayWebView];

        // Load the URL (urlStr was captured and retained by the block).
        NSURL *nsURL = [NSURL URLWithString:urlStr];
        if (nsURL != nil) {
            NSURLRequest *request = [NSURLRequest requestWithURL:nsURL];
            [overlayWebView loadRequest:request];
        }

        [overlayPanel makeKeyAndOrderFront:nil];

        // Activate the app so the panel can receive focus and the WebView
        // renders. Required for accessory-policy apps launched from a terminal.
        [NSApp activateIgnoringOtherApps:YES];
    });
}

void showPanel(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (overlayPanel != nil) {
            [overlayPanel makeKeyAndOrderFront:nil];
            [NSApp activateIgnoringOtherApps:YES];
        }
    });
}

void hidePanel(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (overlayPanel != nil) {
            [overlayPanel orderOut:nil];
        }
    });
}

void togglePanel(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (overlayPanel != nil) {
            if ([overlayPanel isVisible]) {
                [overlayPanel orderOut:nil];
            } else {
                [overlayPanel makeKeyAndOrderFront:nil];
                [NSApp activateIgnoringOtherApps:YES];
            }
        }
    });
}

bool isPanelVisible(void) {
    __block bool visible = false;
    // Must read AppKit state on the main thread to avoid undefined behavior.
    dispatch_sync(dispatch_get_main_queue(), ^{
        if (overlayPanel != nil) {
            visible = [overlayPanel isVisible];
        }
    });
    return visible;
}

void destroyPanel(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (overlayPanel != nil) {
            [overlayPanel close];
            overlayPanel = nil;
            overlayWebView = nil;
        }
    });
}

void setAlwaysOnTop(bool onTop) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (overlayPanel != nil) {
            [overlayPanel setLevel:onTop ? NSFloatingWindowLevel : NSNormalWindowLevel];
        }
    });
}

// initNSApp initializes NSApplication as an accessory app (no dock icon).
// Must be called before any AppKit operations like panel creation.
void initNSApp(void) {
    [NSApplication sharedApplication];
    [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
}

// runEventLoop runs the NSApplication event loop on the current thread.
// Must be called from the main thread after initNSApp(). Blocks until
// stopEventLoop() is called.
void runEventLoop(void) {
    [NSApp run];
}

// stopEventLoop stops the NSApplication event loop.
void stopEventLoop(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [NSApp stop:nil];
        // Post a dummy event to unblock [NSApp run]'s internal event poll.
        NSEvent *event = [NSEvent otherEventWithType:NSEventTypeApplicationDefined
                                            location:NSMakePoint(0, 0)
                                       modifierFlags:0
                                           timestamp:0
                                        windowNumber:0
                                             context:nil
                                             subtype:0
                                               data1:0
                                               data2:0];
        [NSApp postEvent:event atStart:YES];
    });
}

// --- Edge-anchored borderless overlay panel with slide animation ---

// Stored on/off-screen frames for animation.
static NSRect panelOnScreenFrame;
static NSRect panelOffScreenFrame;
static bool panelAnimating = false;
static bool panelEdgeIsRight = true;

// createOverlayPanelEdge creates a borderless, edge-anchored floating panel.
// edge: "right" or "left". width: panel width in points.
// opacity: background alpha 0.0-1.0. cornerRadius: corner radius in points.
void createOverlayPanelEdge(const char* edge, int width, double opacity,
                            double cornerRadius, const char* url) {
    NSString *edgeStr = [NSString stringWithUTF8String:edge];
    NSString *urlStr  = [NSString stringWithUTF8String:url];

    dispatch_async(dispatch_get_main_queue(), ^{
        // Destroy existing panel.
        if (overlayPanel != nil) {
            [overlayPanel close];
            overlayPanel = nil;
            overlayWebView = nil;
        }

        panelEdgeIsRight = ![edgeStr isEqualToString:@"left"];

        // Compute frame from the visible screen area (excludes menu bar + Dock).
        NSRect screen = [[NSScreen mainScreen] visibleFrame];
        CGFloat panelW = (CGFloat)width;
        CGFloat panelH = screen.size.height;
        CGFloat panelX;
        if (panelEdgeIsRight) {
            panelX = NSMaxX(screen) - panelW;
        } else {
            panelX = screen.origin.x;
        }
        CGFloat panelY = screen.origin.y;

        panelOnScreenFrame  = NSMakeRect(panelX, panelY, panelW, panelH);
        // Off-screen: shift by panel width in the edge direction.
        if (panelEdgeIsRight) {
            panelOffScreenFrame = NSMakeRect(panelX + panelW, panelY, panelW, panelH);
        } else {
            panelOffScreenFrame = NSMakeRect(panelX - panelW, panelY, panelW, panelH);
        }

        // Borderless, non-activating, utility panel.
        NSUInteger style = NSWindowStyleMaskBorderless |
                           NSWindowStyleMaskNonactivatingPanel |
                           NSWindowStyleMaskUtilityWindow;

        overlayPanel = [[NSPanel alloc] initWithContentRect:panelOnScreenFrame
                                                  styleMask:style
                                                    backing:NSBackingStoreBuffered
                                                      defer:NO];

        [overlayPanel setLevel:NSFloatingWindowLevel];
        [overlayPanel setHidesOnDeactivate:NO];
        [overlayPanel setCollectionBehavior:
            NSWindowCollectionBehaviorCanJoinAllSpaces |
            NSWindowCollectionBehaviorFullScreenAuxiliary];

        // Translucent background with configurable opacity.
        [overlayPanel setOpaque:NO];
        [overlayPanel setBackgroundColor:[NSColor colorWithCalibratedWhite:0.05 alpha:opacity]];

        // Draggable by window background (Svelte header uses -webkit-app-region: drag).
        [overlayPanel setMovableByWindowBackground:YES];

        // Rounded corners.
        [[overlayPanel contentView] setWantsLayer:YES];
        [[[overlayPanel contentView] layer] setCornerRadius:cornerRadius];
        [[[overlayPanel contentView] layer] setMasksToBounds:YES];

        // Visual effect view for vibrancy.
        NSVisualEffectView *visualEffect = [[NSVisualEffectView alloc]
            initWithFrame:[[overlayPanel contentView] bounds]];
        [visualEffect setAutoresizingMask:NSViewWidthSizable | NSViewHeightSizable];
        [visualEffect setMaterial:NSVisualEffectMaterialUnderWindowBackground];
        [visualEffect setBlendingMode:NSVisualEffectBlendingModeBehindWindow];
        [visualEffect setState:NSVisualEffectStateActive];
        [overlayPanel setContentView:visualEffect];

        // Preserve rounded corners on the visual effect view.
        [visualEffect setWantsLayer:YES];
        [[visualEffect layer] setCornerRadius:cornerRadius];
        [[visualEffect layer] setMasksToBounds:YES];

        // WKWebView.
        WKWebViewConfiguration *config = [[WKWebViewConfiguration alloc] init];
        [config.preferences setValue:@YES forKey:@"developerExtrasEnabled"];

        overlayWebView = [[WKWebView alloc]
            initWithFrame:[visualEffect bounds]
            configuration:config];
        [overlayWebView setAutoresizingMask:NSViewWidthSizable | NSViewHeightSizable];
        [overlayWebView setValue:@NO forKey:@"drawsBackground"];
        [visualEffect addSubview:overlayWebView];

        NSURL *nsURL = [NSURL URLWithString:urlStr];
        if (nsURL != nil) {
            NSURLRequest *request = [NSURLRequest requestWithURL:nsURL];
            [overlayWebView loadRequest:request];
        }

        [overlayPanel makeKeyAndOrderFront:nil];
        [NSApp activateIgnoringOtherApps:YES];
    });
}

// slideIn animates the panel from off-screen to its anchored position.
void slideIn(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (overlayPanel == nil || panelAnimating) return;
        panelAnimating = true;

        [overlayPanel setFrame:panelOffScreenFrame display:NO];
        [overlayPanel setAlphaValue:0.0];
        [overlayPanel makeKeyAndOrderFront:nil];
        [NSApp activateIgnoringOtherApps:YES];

        [NSAnimationContext runAnimationGroup:^(NSAnimationContext *ctx) {
            [ctx setDuration:0.25];
            [ctx setTimingFunction:[CAMediaTimingFunction functionWithName:
                kCAMediaTimingFunctionEaseOut]];
            [[overlayPanel animator] setFrame:panelOnScreenFrame display:YES];
            [[overlayPanel animator] setAlphaValue:1.0];
        } completionHandler:^{
            panelAnimating = false;
        }];
    });
}

// slideOut animates the panel off-screen and then hides it.
void slideOut(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (overlayPanel == nil || panelAnimating) return;
        panelAnimating = true;

        [NSAnimationContext runAnimationGroup:^(NSAnimationContext *ctx) {
            [ctx setDuration:0.20];
            [ctx setTimingFunction:[CAMediaTimingFunction functionWithName:
                kCAMediaTimingFunctionEaseIn]];
            [[overlayPanel animator] setFrame:panelOffScreenFrame display:YES];
            [[overlayPanel animator] setAlphaValue:0.0];
        } completionHandler:^{
            [overlayPanel orderOut:nil];
            panelAnimating = false;
        }];
    });
}

// animatedToggle slides the panel in or out with animation.
void animatedToggle(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (overlayPanel == nil || panelAnimating) return;
        if ([overlayPanel isVisible]) {
            // Call slideOut logic inline to avoid nested dispatch_async.
            panelAnimating = true;
            [NSAnimationContext runAnimationGroup:^(NSAnimationContext *ctx) {
                [ctx setDuration:0.20];
                [ctx setTimingFunction:[CAMediaTimingFunction functionWithName:
                    kCAMediaTimingFunctionEaseIn]];
                [[overlayPanel animator] setFrame:panelOffScreenFrame display:YES];
                [[overlayPanel animator] setAlphaValue:0.0];
            } completionHandler:^{
                [overlayPanel orderOut:nil];
                panelAnimating = false;
            }];
        } else {
            panelAnimating = true;
            [overlayPanel setFrame:panelOffScreenFrame display:NO];
            [overlayPanel setAlphaValue:0.0];
            [overlayPanel makeKeyAndOrderFront:nil];
            [NSApp activateIgnoringOtherApps:YES];
            [NSAnimationContext runAnimationGroup:^(NSAnimationContext *ctx) {
                [ctx setDuration:0.25];
                [ctx setTimingFunction:[CAMediaTimingFunction functionWithName:
                    kCAMediaTimingFunctionEaseOut]];
                [[overlayPanel animator] setFrame:panelOnScreenFrame display:YES];
                [[overlayPanel animator] setAlphaValue:1.0];
            } completionHandler:^{
                panelAnimating = false;
            }];
        }
    });
}
*/
import "C"
import "unsafe"

// CreatePanel creates and shows a macOS NSPanel overlay window at (x, y)
// with the given dimensions, loading the specified URL in an embedded WKWebView.
func CreatePanel(x, y, width, height int, url string) {
	curl := C.CString(url)
	defer C.free(unsafe.Pointer(curl))
	C.createOverlayPanel(C.int(x), C.int(y), C.int(width), C.int(height), curl)
}

// Show makes the overlay panel visible and brings it to front.
func Show() {
	C.showPanel()
}

// Hide hides the overlay panel without destroying it.
func Hide() {
	C.hidePanel()
}

// Toggle toggles the overlay panel's visibility.
func Toggle() {
	C.togglePanel()
}

// IsVisible returns true if the overlay panel is currently visible.
func IsVisible() bool {
	return bool(C.isPanelVisible())
}

// Destroy closes and releases the overlay panel and its web view.
func Destroy() {
	C.destroyPanel()
}

// SetAlwaysOnTop sets whether the panel floats above all other windows.
func SetAlwaysOnTop(onTop bool) {
	C.setAlwaysOnTop(C.bool(onTop))
}

// InitApp initializes NSApplication as an accessory app (no dock icon).
// Must be called on the main thread before CreatePanel or any AppKit call.
func InitApp() {
	C.initNSApp()
}

// RunApp runs the CoreFoundation event loop on the current thread.
// This blocks until StopApp is called. It MUST be called from the main
// goroutine with runtime.LockOSThread() held, because macOS requires
// AppKit operations on the process's initial thread (thread 0).
func RunApp() {
	C.runEventLoop()
}

// StopApp stops the event loop, causing RunApp to return.
func StopApp() {
	C.stopEventLoop()
}

// CreateOverlayPanel creates a borderless, edge-anchored floating panel
// configured by the given OverlayConfig. The panel spans the full visible
// screen height and anchors to the specified edge.
func CreateOverlayPanel(cfg OverlayConfig) {
	cedge := C.CString(cfg.Edge)
	defer C.free(unsafe.Pointer(cedge))
	curl := C.CString(cfg.URL)
	defer C.free(unsafe.Pointer(curl))
	C.createOverlayPanelEdge(cedge, C.int(cfg.Width), C.double(cfg.Opacity),
		C.double(cfg.CornerRadius), curl)
}

// SlideIn animates the overlay panel from off-screen to its anchored position.
func SlideIn() {
	C.slideIn()
}

// SlideOut animates the overlay panel off-screen and hides it.
func SlideOut() {
	C.slideOut()
}

// AnimatedToggle slides the overlay panel in or out with animation.
func AnimatedToggle() {
	C.animatedToggle()
}
