package main

/*
#include <dlfcn.h>
static void* go_dlopen(const char* n) { return dlopen(n, RTLD_NOW); }
*/
import "C"
import "fmt"

func main() {
	h := C.go_dlopen(C.CString("libvulkan.so.999")) // simulate absent loader
	if h == nil {
		fmt.Println("dlopen nil -> fallback to CPU renderer (no crash, binary alive)")
		return
	}
	C.dlclose(h)
	fmt.Println("unexpectedly loaded")
}
