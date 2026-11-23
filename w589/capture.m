// comment-checker-disable-file
// Loaded into the test app with DYLD_INSERT_LIBRARIES. Every ~0.1 s, while $W589_FRAMES/go exists and until
// $W589_FRAMES/stop does, it saves the app's largest window as the window server composited it (falling back to
// cacheDisplay when that is unavailable), at 1x, as JPEG.
#import <AppKit/AppKit.h>
#import <dlfcn.h>

typedef CGImageRef (*W589ListImage)(CGRect, uint32_t, uint32_t, uint32_t);

static void w589_note(NSString *dir, NSString *line) {
    NSString *path = [dir stringByAppendingPathComponent:@"capture.log"];
    NSFileHandle *handle = [NSFileHandle fileHandleForWritingAtPath:path];
    if (!handle) {
        [[NSFileManager defaultManager] createFileAtPath:path contents:nil attributes:nil];
        handle = [NSFileHandle fileHandleForWritingAtPath:path];
    }
    [handle seekToEndOfFile];
    [handle writeData:[[line stringByAppendingString:@"\n"] dataUsingEncoding:NSUTF8StringEncoding]];
    [handle closeFile];
}

__attribute__((constructor)) static void w589_start(void) {
    NSString *dir = NSProcessInfo.processInfo.environment[@"W589_FRAMES"];
    if (dir.length == 0) return;
    dispatch_async(dispatch_get_main_queue(), ^{
        void *graphics = dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", RTLD_LAZY);
        W589ListImage listImage = graphics ? (W589ListImage)dlsym(graphics, "CGWindowListCreateImage") : NULL;
        __block int frame = 0;
        __block NSString *method = nil;
        NSFileManager *files = [NSFileManager defaultManager];
        NSTimer *timer = [NSTimer timerWithTimeInterval:0.1 repeats:YES block:^(NSTimer *t) {
            if ([files fileExistsAtPath:[dir stringByAppendingPathComponent:@"stop"]]) {
                w589_note(dir, [NSString stringWithFormat:@"stopped after %d frames", frame]);
                [t invalidate];
                return;
            }
            if (![files fileExistsAtPath:[dir stringByAppendingPathComponent:@"go"]]) return;
            NSWindow *best = nil;
            for (NSWindow *window in NSApp.windows) {
                if (!window.isVisible || window.frame.size.height < 300) continue;
                if (!best || window.frame.size.width * window.frame.size.height > best.frame.size.width * best.frame.size.height) best = window;
            }
            if (!best) return;
            NSBitmapImageRep *rep = nil;
            CGImageRef image = listImage ? listImage(CGRectNull, 1 << 3, (uint32_t)best.windowNumber, (1 << 0) | (1 << 4)) : NULL;
            if (image && CGImageGetWidth(image) > 1) {
                rep = [[NSBitmapImageRep alloc] initWithCGImage:image];
                if (!method) method = @"window server image";
            }
            if (image) CGImageRelease(image);
            if (!rep) {
                NSView *view = best.contentView;
                rep = [view bitmapImageRepForCachingDisplayInRect:view.bounds];
                [view cacheDisplayInRect:view.bounds toBitmapImageRep:rep];
                if (!method) method = @"cacheDisplay (window server image unavailable)";
            }
            if (frame == 0) w589_note(dir, [NSString stringWithFormat:@"capturing window %ld (%.0fx%.0f) by %@", (long)best.windowNumber, best.frame.size.width, best.frame.size.height, method]);
            NSData *jpeg = [rep representationUsingType:NSBitmapImageFileTypeJPEG properties:@{NSImageCompressionFactor: @0.7}];
            [jpeg writeToFile:[dir stringByAppendingPathComponent:[NSString stringWithFormat:@"%05d.jpg", frame]] atomically:NO];
            frame += 1;
        }];
        [[NSRunLoop mainRunLoop] addTimer:timer forMode:NSRunLoopCommonModes];
    });
}
