//go:build darwin && cgo

package session

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>

static CFMutableDictionaryRef sessionQuery(void) {
    CFMutableDictionaryRef q = CFDictionaryCreateMutable(NULL, 0,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
    CFDictionarySetValue(q, kSecAttrService, CFSTR("io.github.kongesque.line-cli"));
    CFDictionarySetValue(q, kSecAttrAccount, CFSTR("default"));
    return q;
}

typedef struct { OSStatus status; void *data; int length; } sessionResult;

static sessionResult readSession(void) {
    sessionResult result = {0, NULL, 0};
    CFMutableDictionaryRef q = sessionQuery();
    CFDictionarySetValue(q, kSecReturnData, kCFBooleanTrue);
    CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitOne);
    CFTypeRef value = NULL;
    result.status = SecItemCopyMatching(q, &value);
    CFRelease(q);
    if (result.status == errSecSuccess) {
        CFDataRef data = (CFDataRef)value;
        result.length = (int)CFDataGetLength(data);
        result.data = malloc(result.length ? result.length : 1);
        if (!result.data) result.status = errSecAllocate;
        else if (result.length) memcpy(result.data, CFDataGetBytePtr(data), result.length);
        CFRelease(value);
    }
    return result;
}

static OSStatus writeSession(const void *bytes, int length) {
    CFMutableDictionaryRef q = sessionQuery();
    CFDataRef data = CFDataCreate(NULL, bytes, length);
    CFMutableDictionaryRef attrs = CFDictionaryCreateMutable(NULL, 0,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFDictionarySetValue(attrs, kSecValueData, data);
    OSStatus status = SecItemUpdate(q, attrs);
    if (status == errSecItemNotFound) {
        CFDictionarySetValue(q, kSecValueData, data);
        status = SecItemAdd(q, NULL);
    }
    CFRelease(attrs);
    CFRelease(data);
    CFRelease(q);
    return status;
}

static OSStatus deleteSession(void) {
    CFMutableDictionaryRef q = sessionQuery();
    OSStatus status = SecItemDelete(q);
    CFRelease(q);
    return status;
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
)

func (KeychainStore) Load() (*State, error) {
	r := C.readSession()
	if r.data != nil {
		defer C.free(r.data)
	}
	if r.status == C.errSecItemNotFound {
		return nil, ErrNotFound
	}
	if r.status != C.errSecSuccess {
		return nil, fmt.Errorf("read macOS Keychain (status %d)", r.status)
	}
	var s State
	if err := json.Unmarshal(C.GoBytes(r.data, r.length), &s); err != nil {
		return nil, fmt.Errorf("saved session is invalid; run line login")
	}
	if s.Version != 1 || s.AccessToken == "" || s.MID == "" {
		return nil, fmt.Errorf("saved session is incomplete or unsupported; run line login")
	}
	return &s, nil
}

func (KeychainStore) Save(s *State) error {
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("encode session for Keychain")
	}
	bytes := C.CBytes(data)
	defer C.free(bytes)
	if status := C.writeSession(bytes, C.int(len(data))); status != C.errSecSuccess {
		return fmt.Errorf("save macOS Keychain (status %d)", status)
	}
	return nil
}

func (KeychainStore) Delete() error {
	if status := C.deleteSession(); status != C.errSecSuccess && status != C.errSecItemNotFound {
		return fmt.Errorf("delete macOS Keychain session (status %d)", status)
	}
	return nil
}
