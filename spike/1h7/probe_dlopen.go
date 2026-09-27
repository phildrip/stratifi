package main

// Can a CGO_ENABLED=0 pure-Go binary call dlopen at all? The documented Go
// answer: no — unless we resolve libc's dlopen address ourselves. But there
// is a supported path: glibc's dlopen lives in libc.so.6; on Linux the
// dynamic linker exports dlopen via _dlfcn... Let me empirically try the
// simplest thing: syscall.SYS_... does not exist for dlopen.
//
// Alternative used by real projects (e.g. moby's, tini's): the pure-Go way
// to load a shared object without cgo is to parse and invoke ld.so directly,
// which nobody does. The PRACTICAL pure-Go path is:
//   syscall.Syscall on SYS_finit_module? no.
//
// So empirically test: can we find dlopen by calling syscall via libc base
// address from /proc/self/maps and calling the ELF entry? Too fragile.
//
// CONCLUSION TO TEST INSTEAD: keep cgo ONLY for dlopen/dlsym (no Vulkan
// headers, no libvulkan at link time). Then the binary links libc (already
// does) but NOT libvulkan. Check: does that binary run when libvulkan is
// absent?
import "fmt"

// #include <dlfcn.h>
// #include <stdlib.h>
// static void* go_dlopen(const char* n) { return dlopen(n, RTLD_NOW | RTLD_GLOBAL); }
// static void* go_dlsym(void* h, const char* n) { return dlsym(h, n); }
import "C"

func main() {
	h := C.go_dlopen(C.CString("libvulkan.so.1"))
	fmt.Println("handle:", h)
	if h == nil {
		fmt.Println("libvulkan NOT present -> graceful fallback possible")
		return
	}
	sym := C.go_dlsym(h, C.CString("vkGetInstanceProcAddr"))
	fmt.Println("vkGetInstanceProcAddr:", sym)
}
