//go:build darwin && !ios

package browse

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework UniformTypeIdentifiers

#import <Cocoa/Cocoa.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
	bool save, folder, multiple;
	const char* title;
	const char* dir;
	const char* name;
	const char* exts;  // comma separated, empty for any file
	uintptr_t parent;  // NSWindow*
} request;

static NSSavePanel* makePanel(request r) {
	NSSavePanel* panel;
	if (r.save) {
		panel = [NSSavePanel savePanel];
		if (*r.name) panel.nameFieldStringValue = @(r.name);
	} else {
		NSOpenPanel* open = [NSOpenPanel openPanel];
		open.canChooseFiles = !r.folder;
		open.canChooseDirectories = r.folder;
		open.allowsMultipleSelection = r.multiple;
		panel = open;
	}
	panel.canCreateDirectories = YES;
	if (*r.title) panel.message = @(r.title);
	if (*r.dir) panel.directoryURL = [NSURL fileURLWithPath:@(r.dir) isDirectory:YES];

	NSMutableArray<UTType*>* types = [NSMutableArray array];
	for (NSString* ext in [@(r.exts) componentsSeparatedByString:@","]) {
		UTType* type = ext.length ? [UTType typeWithFilenameExtension:ext] : nil;
		if (type) [types addObject:type];
	}
	if (types.count) panel.allowedContentTypes = types;
	return panel;
}

// panelPaths returns the chosen paths as a malloc'd JSON array.
static char* panelPaths(NSSavePanel* panel) {
	NSArray<NSURL*>* urls = [panel isKindOfClass:[NSOpenPanel class]] ? ((NSOpenPanel*)panel).URLs : @[panel.URL];
	NSData* json = [NSJSONSerialization dataWithJSONObject:[urls valueForKey:@"path"] options:0 error:nil];
	return strndup(json.bytes, json.length);
}

// runPanel blocks until the panel is closed and returns NULL if it was
// cancelled. AppKit only runs on the main thread, so from any other thread the
// panel is opened on the main queue, as a sheet if it has a parent, and the
// event loop of the program keeps running while we wait. On the main thread
// itself the panel runs modal.
static char* runPanel(request r) {
	@autoreleasepool {
		if ([NSThread isMainThread]) {
			[NSApplication sharedApplication];
			if (NSApp.activationPolicy == NSApplicationActivationPolicyProhibited) {
				// A program without a user interface has to become one to get the keyboard.
				[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
				[[NSRunningApplication currentApplication] activateWithOptions:NSApplicationActivateAllWindows];
			}
			NSSavePanel* panel = makePanel(r);
			return [panel runModal] == NSModalResponseOK ? panelPaths(panel) : NULL;
		}

		__block char* result = NULL;
		dispatch_semaphore_t done = dispatch_semaphore_create(0);
		dispatch_async(dispatch_get_main_queue(), ^{
			NSSavePanel* panel = makePanel(r);
			void (^closed)(NSModalResponse) = ^(NSModalResponse response) {
				if (response == NSModalResponseOK) result = panelPaths(panel);
				dispatch_semaphore_signal(done);
			};
			NSWindow* parent = (__bridge NSWindow*)(void*)r.parent;
			if (parent) {
				[panel beginSheetModalForWindow:parent completionHandler:closed];
			} else {
				[panel beginWithCompletionHandler:closed];
			}
		});
		dispatch_semaphore_wait(done, DISPATCH_TIME_FOREVER);
		return result;
	}
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"strings"
	"unsafe"
)

func show(m mode, o Options) ([]string, error) {
	// The panels have no type selector, so they accept the extensions of all
	// filters, or any file as soon as one filter does.
	var exts []string
	for _, f := range o.Filters {
		e := f.extensions()
		if e == nil {
			exts = nil
			break
		}
		exts = append(exts, e...)
	}

	r := C.request{
		save:     C.bool(m == modeSave),
		folder:   C.bool(m == modeFolder),
		multiple: C.bool(m == modeOpenMultiple),
		title:    C.CString(o.Title),
		dir:      C.CString(o.Dir),
		name:     C.CString(o.Name),
		exts:     C.CString(strings.Join(exts, ",")),
		parent:   C.uintptr_t(o.Parent),
	}
	defer C.free(unsafe.Pointer(r.title))
	defer C.free(unsafe.Pointer(r.dir))
	defer C.free(unsafe.Pointer(r.name))
	defer C.free(unsafe.Pointer(r.exts))

	res := C.runPanel(r)
	if res == nil {
		return nil, ErrCancelled
	}
	defer C.free(unsafe.Pointer(res))

	var paths []string
	if err := json.Unmarshal([]byte(C.GoString(res)), &paths); err != nil {
		return nil, fmt.Errorf("browse: %w", err)
	}
	return paths, nil
}
