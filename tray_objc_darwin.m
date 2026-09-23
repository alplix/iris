// A macOS menu bar tray built directly on NSStatusBar/NSStatusItem, instead
// of getlantern/systray: that library (like Wails itself) registers its own
// Cocoa NSApplicationDelegate-conforming class, and the two crash the
// process when linked into the same binary (see tray_darwin.go's own
// comment, and README's roadmap history). IrisTrayController below is a
// plain NSObject with a handful of action methods — it never touches
// NSApp's delegate at all, so it has nothing to collide with.
#import <Cocoa/Cocoa.h>
#include "tray_objc_darwin.h"

// Implemented in Go, see tray_darwin.go's //export goTrayAction.
extern void goTrayAction(int action);

@interface IrisTrayController : NSObject
@end

@implementation IrisTrayController
- (void)onShow:(id)sender {
    goTrayAction(0);
}
- (void)onRefresh:(id)sender {
    goTrayAction(1);
}
- (void)onHide:(id)sender {
    goTrayAction(2);
}
- (void)onQuit:(id)sender {
    goTrayAction(3);
}
@end

static NSStatusItem *irisStatusItem;
static IrisTrayController *irisTrayController;
static NSMenuItem *irisShowItem;
static NSMenuItem *irisRefreshItem;
static NSMenuItem *irisHideItem;
static NSMenuItem *irisQuitItem;

static NSString *irisNSStr(const char *s) {
    if (s == NULL) {
        return @"";
    }
    return [NSString stringWithUTF8String:s];
}

void iris_tray_start(const void *iconBytes, long iconLen,
                      const char *tooltip,
                      const char *showLabel, const char *showTip,
                      const char *refreshLabel, const char *refreshTip,
                      const char *hideLabel, const char *hideTip,
                      const char *quitLabel, const char *quitTip) {
    // Idempotent: the same singleton Wails itself will use once wails.Run
    // starts the real event loop.
    [NSApplication sharedApplication];

    irisTrayController = [[IrisTrayController alloc] init];

    irisStatusItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
    NSData *data = [NSData dataWithBytes:iconBytes length:(NSUInteger)iconLen];
    NSImage *image = [[NSImage alloc] initWithData:data];
    [image setSize:NSMakeSize(18, 18)];
    irisStatusItem.button.image = image;
    irisStatusItem.button.toolTip = irisNSStr(tooltip);

    NSMenu *menu = [[NSMenu alloc] init];

    irisShowItem = [[NSMenuItem alloc] initWithTitle:irisNSStr(showLabel) action:@selector(onShow:) keyEquivalent:@""];
    irisShowItem.target = irisTrayController;
    irisShowItem.toolTip = irisNSStr(showTip);
    irisShowItem.enabled = NO; // enabled once trayWindowReady fires, matching tray.go's non-Darwin behavior
    [menu addItem:irisShowItem];

    irisRefreshItem = [[NSMenuItem alloc] initWithTitle:irisNSStr(refreshLabel) action:@selector(onRefresh:) keyEquivalent:@""];
    irisRefreshItem.target = irisTrayController;
    irisRefreshItem.toolTip = irisNSStr(refreshTip);
    [menu addItem:irisRefreshItem];

    irisHideItem = [[NSMenuItem alloc] initWithTitle:irisNSStr(hideLabel) action:@selector(onHide:) keyEquivalent:@""];
    irisHideItem.target = irisTrayController;
    irisHideItem.toolTip = irisNSStr(hideTip);
    irisHideItem.enabled = NO;
    [menu addItem:irisHideItem];

    [menu addItem:[NSMenuItem separatorItem]];

    irisQuitItem = [[NSMenuItem alloc] initWithTitle:irisNSStr(quitLabel) action:@selector(onQuit:) keyEquivalent:@""];
    irisQuitItem.target = irisTrayController;
    irisQuitItem.toolTip = irisNSStr(quitTip);
    [menu addItem:irisQuitItem];

    irisStatusItem.menu = menu;
}

void iris_tray_stop(void) {
    if (irisStatusItem != nil) {
        [[NSStatusBar systemStatusBar] removeStatusItem:irisStatusItem];
        irisStatusItem = nil;
    }
}

void iris_tray_set_window_up(int up) {
    if (irisShowItem == nil) {
        return;
    }
    irisShowItem.enabled = up ? YES : NO;
    irisHideItem.enabled = up ? YES : NO;
}

void iris_tray_relabel(const char *tooltip,
                        const char *showLabel, const char *showTip,
                        const char *refreshLabel, const char *refreshTip,
                        const char *hideLabel, const char *hideTip,
                        const char *quitLabel, const char *quitTip) {
    if (irisStatusItem == nil) {
        return;
    }
    irisStatusItem.button.toolTip = irisNSStr(tooltip);
    irisShowItem.title = irisNSStr(showLabel);
    irisShowItem.toolTip = irisNSStr(showTip);
    irisRefreshItem.title = irisNSStr(refreshLabel);
    irisRefreshItem.toolTip = irisNSStr(refreshTip);
    irisHideItem.title = irisNSStr(hideLabel);
    irisHideItem.toolTip = irisNSStr(hideTip);
    irisQuitItem.title = irisNSStr(quitLabel);
    irisQuitItem.toolTip = irisNSStr(quitTip);
}
