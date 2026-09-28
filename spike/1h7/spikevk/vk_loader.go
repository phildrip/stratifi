package spikevk

/*
#include <stdint.h>
void free(void*);
void* spike_dlopen(const char* n);
void* spike_dlsym(void* h, const char* n);
int   spike_dlclose(void* h);
void* spike_gipa(void* g, uint64_t inst, const char* n);
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

// Loader holds a dlopen handle and vkGetInstanceProcAddr.
type Loader struct {
	mu     sync.Mutex
	handle unsafe.Pointer
	opened bool
	gipa   unsafe.Pointer // raw PFN_vkGetInstanceProcAddr
}

// candidateLibraries are tried in order; the first that dlopens wins.
var candidateLibraries = []string{"libvulkan.so.1", "libvulkan.so"}

var (
	sharedLoader *Loader
	loaderMu     sync.Mutex
)

// AcquireLoader dlopens the Vulkan loader. Idempotent. An error means the
// system has no usable Vulkan loader: the compositor takes the software path.
func AcquireLoader() (*Loader, error) {
	loaderMu.Lock()
	defer loaderMu.Unlock()
	if sharedLoader != nil && sharedLoader.opened {
		return sharedLoader, nil
	}
	l, err := openLibrary(candidateLibraries)
	if err != nil {
		return nil, err
	}
	sharedLoader = l
	return l, nil
}

// loaderFrom is AcquireLoader with injected names, so the absent-loader
// fallback is a plain unit test instead of an uninstall-vulkan exercise.
// It deliberately bypasses the shared cache: a cached real loader must not
// mask a missing one.
func loaderFrom(names []string) (*Loader, error) {
	return openLibrary(names)
}

func openLibrary(names []string) (*Loader, error) {
	var lastErr error
	for _, name := range names {
		cname := C.CString(name)
		h := C.spike_dlopen(cname)
		C.free(unsafe.Pointer(cname))
		if h == nil {
			lastErr = fmt.Errorf("%s: not found", name)
			continue
		}
		gipaName := C.CString("vkGetInstanceProcAddr")
		gipa := C.spike_dlsym(h, gipaName)
		C.free(unsafe.Pointer(gipaName))
		if gipa == nil {
			lastErr = errors.New(name + ": no vkGetInstanceProcAddr")
			C.spike_dlclose(h)
			continue
		}
		return &Loader{handle: h, opened: true, gipa: gipa}, nil
	}
	return nil, fmt.Errorf("no vulkan loader: %w", lastErr)
}

// Resolve looks up an entry point by name for the given instance.
// instance == 0 resolves global functions (vkCreateInstance). A nil result
// means the loader does not know the symbol: the ONLY legal response is an
// error carrying the name, never a call.
func (l *Loader) Resolve(instance Instance, name string) unsafe.Pointer {
	return C.spike_gipa(l.gipa, C.uint64_t(uint64(instance)), cBytes(append([]byte(name), 0)))
}

// Close releases the dlopen handle. Not safe while handles are in use.
func (l *Loader) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.opened && l.handle != nil {
		C.spike_dlclose(l.handle)
		l.opened = false
	}
}

// cBytes converts a NUL-terminated Go byte slice to *C.char.
func cBytes(name []byte) *C.char {
	return (*C.char)(unsafe.Pointer(unsafe.SliceData(name)))
}

// acByte builds a NUL-terminated byte slice from a string.
func acByte(s string) []byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return b
}
