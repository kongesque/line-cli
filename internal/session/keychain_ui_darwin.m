//go:build darwin && cgo

#import <LocalAuthentication/LocalAuthentication.h>

// A per-query context avoids changing process-global Keychain UI settings.
void *lineNoInteractionContext(void) {
    LAContext *context = [[LAContext alloc] init];
    context.interactionNotAllowed = YES;
    return context;
}

void lineReleaseContext(void *context) {
    [(LAContext *)context release];
}
