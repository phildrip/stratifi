package vulkan

import (
	"debug/elf"
	"os"
	"strings"
	"testing"
)

// TestBinaryLinksZeroLibvulkan verifies that binaries importing
// internal/render/vulkan have zero build-time DT_NEEDED dependency on
// libvulkan.so (all symbols must be resolved at runtime via dlopen/dlsym).
func TestBinaryLinksZeroLibvulkan(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	f, err := elf.Open(exe)
	if err != nil {
		t.Fatalf("elf.Open(%s): %v", exe, err)
	}
	defer f.Close()

	libs, err := f.ImportedLibraries()
	if err != nil {
		t.Fatalf("ImportedLibraries: %v", err)
	}
	for _, lib := range libs {
		if strings.Contains(strings.ToLower(lib), "vulkan") {
			t.Fatalf("binary has build-time DT_NEEDED dependency on %q; want runtime dlopen only (0 libvulkan DT_NEEDED)", lib)
		}
	}
}

// TestMissingVulkanLoaderFallback verifies that when libvulkan.so is absent on
// the host, NewDeviceFromLibraries returns a clean error so the compositor
// falls back to CPUExecutor instead of failing at process startup.
func TestMissingVulkanLoaderFallback(t *testing.T) {
	_, err := newDeviceFromLibraries([]string{"libvulkan-definitely-missing-stratifi.so.999"})
	if err == nil {
		t.Fatal("expected error when Vulkan loader shared library is missing")
	}

	// A library that exists (libc.so.6) but lacks vkGetInstanceProcAddr must also fail cleanly.
	_, err = newDeviceFromLibraries([]string{"libc.so.6"})
	if err == nil || !strings.Contains(err.Error(), "vkGetInstanceProcAddr") {
		t.Fatalf("expected missing vkGetInstanceProcAddr error for libc.so.6, got: %v", err)
	}
}
