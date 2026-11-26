//go:build darwin && cgo

// The standard macOS menu bar for the Kendra window. Without an Edit menu a
// WKWebView gets no Cmd+C / Cmd+V / Cmd+A at all (the shortcuts are menu items in
// AppKit), and without an app menu there is no Cmd+Q.
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

static NSMenuItem *item(NSString *title, SEL action, NSString *key) {
	return [[NSMenuItem alloc] initWithTitle:title action:action keyEquivalent:key];
}

static NSString *kndVersion = @"";
static NSString *kndTagline = @"";

@interface KNDMenuTarget : NSObject
@end
@implementation KNDMenuTarget
- (void)about:(id)sender {
	[NSApp activateIgnoringOtherApps:YES];
	NSMutableParagraphStyle *ps = [NSMutableParagraphStyle new];
	ps.alignment = NSTextAlignmentCenter;
	NSAttributedString *credits = [[NSAttributedString alloc] initWithString:kndTagline attributes:@{
		NSFontAttributeName: [NSFont systemFontOfSize:11], NSForegroundColorAttributeName: NSColor.secondaryLabelColor,
		NSParagraphStyleAttributeName: ps}];
	[NSApp orderFrontStandardAboutPanelWithOptions:@{
		NSAboutPanelOptionApplicationVersion: kndVersion,
		NSAboutPanelOptionVersion: @"",
		NSAboutPanelOptionCredits: credits,
	}];
}
static WKWebView *findWebView(NSView *v) {
	if ([v isKindOfClass:[WKWebView class]]) return (WKWebView *)v;
	for (NSView *s in v.subviews) { WKWebView *w = findWebView(s); if (w) return w; }
	return nil;
}
- (void)zoom:(NSMenuItem *)sender {
	NSWindow *win = NSApp.keyWindow ?: NSApp.mainWindow ?: NSApp.windows.firstObject;
	WKWebView *wv = findWebView(win.contentView);
	if (!wv) return;
	CGFloat z = wv.pageZoom;
	if (sender.tag == 0) z = 1.0;
	else if (sender.tag > 0) z = MIN(2.0, z + 0.1);
	else z = MAX(0.6, z - 0.1);
	wv.pageZoom = z;
}
- (void)reload:(id)sender {
	NSWindow *win = NSApp.keyWindow ?: NSApp.mainWindow ?: NSApp.windows.firstObject;
	[findWebView(win.contentView) reload];
}
@end
static KNDMenuTarget *kndTarget;

static NSMenu *menuNamed(NSMenu *bar, NSString *title) {
	NSMenuItem *top = [[NSMenuItem alloc] init];
	NSMenu *m = [[NSMenu alloc] initWithTitle:title];
	top.submenu = m;
	[bar addItem:top];
	return m;
}
#define CMD NSEventModifierFlagCommand
#define OPT NSEventModifierFlagOption
#define CTRL NSEventModifierFlagControl

void kndInstallMenu(const char *appName, const char *version, const char *tagline) {
	NSString *name = [NSString stringWithUTF8String:appName];
	kndVersion = [NSString stringWithUTF8String:version];
	kndTagline = [NSString stringWithUTF8String:tagline];
	kndTarget = [KNDMenuTarget new];
	NSWindow.allowsAutomaticWindowTabbing = NO;
	NSMenu *bar = [[NSMenu alloc] init];

	NSMenu *app = menuNamed(bar, name);
	NSMenuItem *about = item([@"About " stringByAppendingString:name], @selector(about:), @"");
	about.target = kndTarget;
	[app addItem:about];
	[app addItem:[NSMenuItem separatorItem]];
	[app addItem:item([@"Hide " stringByAppendingString:name], @selector(hide:), @"h")];
	NSMenuItem *others = item(@"Hide Others", @selector(hideOtherApplications:), @"h");
	others.keyEquivalentModifierMask = CMD | OPT;
	[app addItem:others];
	[app addItem:item(@"Show All", @selector(unhideAllApplications:), @"")];
	[app addItem:[NSMenuItem separatorItem]];
	[app addItem:item([@"Quit " stringByAppendingString:name], @selector(terminate:), @"q")];

	NSMenu *edit = menuNamed(bar, @"Edit");
	[edit addItem:item(@"Undo", @selector(undo:), @"z")];
	NSMenuItem *redo = item(@"Redo", @selector(redo:), @"z");
	redo.keyEquivalentModifierMask = CMD | NSEventModifierFlagShift;
	[edit addItem:redo];
	[edit addItem:[NSMenuItem separatorItem]];
	[edit addItem:item(@"Cut", @selector(cut:), @"x")];
	[edit addItem:item(@"Copy", @selector(copy:), @"c")];
	[edit addItem:item(@"Paste", @selector(paste:), @"v")];
	[edit addItem:item(@"Select All", @selector(selectAll:), @"a")];

	NSMenu *view = menuNamed(bar, @"View");
	NSMenuItem *rl = item(@"Reload", @selector(reload:), @"r"); rl.target = kndTarget;
	[view addItem:rl];
	[view addItem:[NSMenuItem separatorItem]];
	NSMenuItem *z0 = item(@"Actual Size", @selector(zoom:), @"0"); z0.target = kndTarget; z0.tag = 0;
	NSMenuItem *z1 = item(@"Zoom In", @selector(zoom:), @"+"); z1.target = kndTarget; z1.tag = 1;
	NSMenuItem *z2 = item(@"Zoom Out", @selector(zoom:), @"-"); z2.target = kndTarget; z2.tag = -1;
	[view addItem:z0]; [view addItem:z1]; [view addItem:z2];
	[view addItem:[NSMenuItem separatorItem]];
	NSMenuItem *fs = item(@"Toggle Full Screen", @selector(toggleFullScreen:), @"f");
	fs.keyEquivalentModifierMask = CMD | CTRL;
	[view addItem:fs];

	NSMenu *win = menuNamed(bar, @"Window");
	[win addItem:item(@"Close Window", @selector(performClose:), @"w")];
	[win addItem:item(@"Minimize", @selector(performMiniaturize:), @"m")];
	[win addItem:item(@"Zoom", @selector(performZoom:), @"")];
	NSApp.windowsMenu = win;

	NSApp.mainMenu = bar;
}

// The window remembers its size and position between launches, like any Mac app.
void kndRememberFrame(void *window) {
	NSWindow *w = (__bridge NSWindow *)window;
	[w setFrameAutosaveName:@"KendraMain"];
	w.minSize = NSMakeSize(900, 560);
	w.appearance = nil; // follow the system appearance
	NSApp.appearance = nil;
}

// kndSaveFile shows the standard macOS Save panel and returns the chosen path (caller
// frees), or NULL when cancelled. It runs on the main thread, as AppKit requires, and
// waits there; the caller is a server goroutine.
char *kndSaveFile(const char *name) {
	__block char *result = NULL;
	NSString *n = name ? [[NSString stringWithUTF8String:name] lastPathComponent] : @"export.txt";
	void (^run)(void) = ^{
		[NSApp activateIgnoringOtherApps:YES];
		NSSavePanel *p = [NSSavePanel savePanel];
		p.nameFieldStringValue = n;
		p.canCreateDirectories = YES;
		p.directoryURL = [NSURL fileURLWithPath:[NSHomeDirectory() stringByAppendingPathComponent:@"Downloads"]];
		if ([p runModal] == NSModalResponseOK && p.URL.path) result = strdup([p.URL.path UTF8String]);
	};
	if ([NSThread isMainThread]) run(); else dispatch_sync(dispatch_get_main_queue(), run);
	return result;
}
