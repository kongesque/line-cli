//go:build darwin && cgo

package session

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation -framework Foundation -framework LocalAuthentication
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>
void *lineNoInteractionContext(void);
void lineReleaseContext(void *context);

static CFMutableDictionaryRef sessionQuery(const char *account) {
    CFMutableDictionaryRef q = CFDictionaryCreateMutable(NULL, 0,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
    CFDictionarySetValue(q, kSecAttrService, CFSTR("io.github.kongesque.line-cli"));
    CFStringRef accountValue = CFStringCreateWithCString(NULL, account, kCFStringEncodingUTF8);
    CFDictionarySetValue(q, kSecAttrAccount, accountValue);
    CFRelease(accountValue);
    return q;
}

typedef struct { OSStatus status; void *data; int length; } sessionResult;

static sessionResult readSession(const char *account, int noPrompt) {
    sessionResult result = {0, NULL, 0};
    CFMutableDictionaryRef q = sessionQuery(account);
    if (noPrompt) {
        void *context = lineNoInteractionContext();
        CFDictionarySetValue(q, kSecUseAuthenticationContext, context);
        lineReleaseContext(context);
    }
    CFDictionarySetValue(q, kSecReturnData, kCFBooleanTrue);
    CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitOne);
    CFTypeRef value = NULL;
    result.status = SecItemCopyMatching(q, &value);
    CFRelease(q);
    if (result.status == errSecSuccess) {
        CFDataRef data = (CFDataRef)value;
        CFIndex length = CFDataGetLength(data);
        if (length > (4 << 20)) {
            result.status = errSecDecode;
            CFRelease(value);
            return result;
        }
        result.length = (int)length;
        result.data = malloc(result.length ? result.length : 1);
        if (!result.data) result.status = errSecAllocate;
        else if (result.length) memcpy(result.data, CFDataGetBytePtr(data), result.length);
        CFRelease(value);
    }
    return result;
}

static OSStatus writeSession(const char *account, const void *bytes, int length) {
    CFMutableDictionaryRef q = sessionQuery(account);
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

static OSStatus deleteSession(const char *account) {
    CFMutableDictionaryRef q = sessionQuery(account);
    OSStatus status = SecItemDelete(q);
    CFRelease(q);
    return status;
}
*/
import "C"

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"unsafe"
)

type macKeychainStore struct{ account string }

func (KeychainStore) Load() (*State, error) { return (macKeychainStore{"default"}).Load() }
func (KeychainStore) Save(s *State) error   { return (macKeychainStore{"default"}).Save(s) }
func (KeychainStore) Delete() error         { return (macKeychainStore{"default"}).Delete() }

func prepareNativeStorage() error {
	if err := checkSavedStorage(macKeychainStore{"default"}); err != nil {
		return err
	}
	probe := macKeychainStore{"storage-probe-" + rand.Text()}
	return exerciseStorage(probe, func() error { return removeStorageProbe(probe) })
}

func (s macKeychainStore) Load() (*State, error) {
	return s.load(false)
}

func (s macKeychainStore) load(noPrompt bool) (*State, error) {
	account := C.CString(s.account)
	defer C.free(unsafe.Pointer(account))
	var silent C.int
	if noPrompt {
		silent = 1
	}
	r := C.readSession(account, silent)
	if r.data != nil {
		defer C.free(r.data)
		defer C.memset(r.data, 0, C.size_t(r.length))
	}
	if r.status == C.errSecItemNotFound {
		return nil, ErrNotFound
	}
	if r.status != C.errSecSuccess {
		if noPrompt {
			return nil, ErrStorageUnavailable
		}
		return nil, fmt.Errorf("read macOS Keychain (status %d)", r.status)
	}
	data := C.GoBytes(r.data, r.length)
	defer clear(data)
	return decodeSession(data)
}

type macStatusStore struct{ macKeychainStore }

func (s macStatusStore) Load() (*State, error) { return s.macKeychainStore.load(true) }
func platformStorageStatus(check bool) (StorageStatus, error) {
	return nativeStorageStatus(macStatusStore{macKeychainStore{"default"}}, check, func() error { return ErrReadOnlyStatus })
}

func (store macKeychainStore) Save(s *State) error {
	data, err := json.Marshal(s)
	if err != nil || len(data) > maxSessionBytes-1024 {
		return fmt.Errorf("encode session for Keychain")
	}
	defer clear(data)
	account := C.CString(store.account)
	defer C.free(unsafe.Pointer(account))
	bytes := C.CBytes(data)
	defer C.free(bytes)
	defer C.memset(bytes, 0, C.size_t(len(data)))
	if status := C.writeSession(account, bytes, C.int(len(data))); status != C.errSecSuccess {
		return fmt.Errorf("save macOS Keychain (status %d)", status)
	}
	return nil
}

func (store macKeychainStore) Delete() error {
	account := C.CString(store.account)
	defer C.free(unsafe.Pointer(account))
	if status := C.deleteSession(account); status != C.errSecSuccess && status != C.errSecItemNotFound {
		return fmt.Errorf("delete macOS Keychain session (status %d)", status)
	}
	return nil
}
