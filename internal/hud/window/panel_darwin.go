//go:build darwin

// Package window provides macOS native overlay panel management via CGo.
package window

/*
#cgo CFLAGS: -x objective-c -fmodules
#cgo LDFLAGS: -framework Cocoa -framework Carbon -framework WebKit

#include <stdlib.h>
#import <Cocoa/Cocoa.h>
#import <Carbon/Carbon.h>
#import <WebKit/WebKit.h>

// Static globals for the overlay panel and web view.
static NSPanel *overlayPanel = nil;
static WKWebView *overlayWebView = nil;

void createOverlayPanel(int x, int y, int width, int height, const char* url) {
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

        // Load the URL.
        NSString *urlString = [NSString stringWithUTF8String:url];
        NSURL *nsURL = [NSURL URLWithString:urlString];
        if (nsURL != nil) {
            NSURLRequest *request = [NSURLRequest requestWithURL:nsURL];
            [overlayWebView loadRequest:request];
        }

        [overlayPanel makeKeyAndOrderFront:nil];
    });
}

void showPanel(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (overlayPanel != nil) {
            [overlayPanel makeKeyAndOrderFront:nil];
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
