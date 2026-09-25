package vulkan

import (
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Linux DRM ioctl definitions for allocating test PRIME DMA-BUF buffers.
const (
	drmIoctlModeCreateDumb  = 0xc02064b2 // DRM_IOCTL_MODE_CREATE_DUMB
	drmIoctlPrimeHandleToFd = 0xc00c642d // DRM_IOCTL_PRIME_HANDLE_TO_FD
)

type drmModeCreateDumb struct {
	height uint32
	width  uint32
	bpp    uint32
	flags  uint32
	handle uint32
	pitch  uint32
	size   uint64
}

type drmPrimeHandle struct {
	handle uint32
	flags  uint32
	fd     int32
}

func openDrmDevice() (int, error) {
	paths := []string{"/dev/dri/card1", "/dev/dri/card0"}
	for _, p := range paths {
		if fd, err := unix.Open(p, unix.O_RDWR|unix.O_CLOEXEC, 0); err == nil {
			return fd, nil
		}
	}
	return -1, os.ErrNotExist
}

func createDrmDmaBuf(drmFD int, width, height uint32) (int, uint32, error) {
	dumb := drmModeCreateDumb{
		width:  width,
		height: height,
		bpp:    32,
	}

	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		uintptr(drmFD),
		uintptr(drmIoctlModeCreateDumb),
		uintptr(unsafe.Pointer(&dumb)),
	)
	if errno != 0 {
		return -1, 0, errno
	}

	prime := drmPrimeHandle{
		handle: dumb.handle,
		flags:  unix.O_RDWR | unix.O_CLOEXEC,
		fd:     -1,
	}

	_, _, errno = unix.Syscall(
		unix.SYS_IOCTL,
		uintptr(drmFD),
		uintptr(drmIoctlPrimeHandleToFd),
		uintptr(unsafe.Pointer(&prime)),
	)
	if errno != 0 {
		return -1, 0, errno
	}

	return int(prime.fd), dumb.pitch, nil
}

func TestVulkanDeviceCreation(t *testing.T) {
	dev, err := NewDevice()
	if err != nil {
		t.Fatalf("NewDevice failed: %v", err)
	}
	defer dev.Close()

	name := dev.DeviceName()
	if len(name) == 0 {
		t.Fatalf("expected non-empty device name")
	}
	t.Logf("Initialized Vulkan GPU Device: %s", name)
}

func TestVulkanDmaBufImport(t *testing.T) {
	drmFD, err := openDrmDevice()
	if err != nil {
		t.Skipf("skipping DMA-BUF test: no accessible DRM device (%v)", err)
	}
	defer unix.Close(drmFD)

	// Allocate a real Linux kernel dumb buffer and export to PRIME DMA-BUF FD
	width := uint32(64)
	height := uint32(64)
	dmaBufFD, pitch, err := createDrmDmaBuf(drmFD, width, height)
	if err != nil {
		t.Skipf("skipping DMA-BUF test: dumb buffer / PRIME export unsupported (%v)", err)
	}
	defer unix.Close(dmaBufFD)

	dev, err := NewDevice()
	if err != nil {
		t.Fatalf("NewDevice failed: %v", err)
	}
	defer dev.Close()

	// Import DMA-BUF FD into Vulkan device memory
	imported, err := dev.ImportDmaBuf(dmaBufFD, width, height, pitch, 0)
	if err != nil {
		t.Fatalf("ImportDmaBuf failed: %v", err)
	}
	defer dev.DestroyImportedBuffer(imported)

	if imported.Width != width || imported.Height != height || imported.Stride != pitch {
		t.Fatalf("unexpected imported dimensions: %+v", imported)
	}
	t.Logf("Successfully imported 64x64 DMA-BUF (stride=%d) into Vulkan device memory!", pitch)
}

func BenchmarkVulkanDmaBufImport(b *testing.B) {
	drmFD, err := openDrmDevice()
	if err != nil {
		b.Skipf("skipping benchmark: no DRM device (%v)", err)
	}
	defer unix.Close(drmFD)

	width := uint32(64)
	height := uint32(64)
	dmaBufFD, pitch, err := createDrmDmaBuf(drmFD, width, height)
	if err != nil {
		b.Skipf("skipping benchmark: dumb buffer unsupported (%v)", err)
	}
	defer unix.Close(dmaBufFD)

	dev, err := NewDevice()
	if err != nil {
		b.Fatalf("NewDevice failed: %v", err)
	}
	defer dev.Close()

	var imported ImportedBuffer
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := dev.ImportDmaBufInto(&imported, dmaBufFD, width, height, pitch, 0); err != nil {
			b.Fatalf("ImportDmaBufInto failed: %v", err)
		}
		dev.DestroyImportedBuffer(&imported)
	}
}

func TestVulkanRendererCreation(t *testing.T) {
	dev, err := NewDevice()
	if err != nil {
		t.Fatalf("NewDevice failed: %v", err)
	}
	defer dev.Close()

	ren, err := NewRenderer(dev)
	if err != nil {
		t.Fatalf("NewRenderer failed: %v", err)
	}
	defer ren.Close()
	t.Logf("Successfully created Vulkan compositor renderer")
}

func TestVulkanRendererFrame(t *testing.T) {
	dev, err := NewDevice()
	if err != nil {
		t.Fatalf("NewDevice failed: %v", err)
	}
	defer dev.Close()

	ren, err := NewRenderer(dev)
	if err != nil {
		t.Fatalf("NewRenderer failed: %v", err)
	}
	defer ren.Close()

	target, err := ren.CreateTarget(1280, 720)
	if err != nil {
		t.Fatalf("CreateTarget failed: %v", err)
	}
	defer ren.DestroyTarget(target)

	// Record a frame with clear background and solid color tiles/dividers
	if err := ren.BeginFrame(target, 0.1, 0.1, 0.12, 1.0); err != nil {
		t.Fatalf("BeginFrame failed: %v", err)
	}

	// Draw left tile
	ren.DrawColorQuad(10, 10, 620, 700, 0.2, 0.3, 0.4, 1.0)
	// Draw divider
	ren.DrawColorQuad(635, 0, 10, 720, 0.8, 0.8, 0.8, 1.0)
	// Draw right tile
	ren.DrawColorQuad(650, 10, 620, 700, 0.15, 0.25, 0.35, 1.0)

	if err := ren.EndFrame(); err != nil {
		t.Fatalf("EndFrame failed: %v", err)
	}

	if err := ren.WaitFrame(1000000000); err != nil {
		t.Fatalf("WaitFrame failed: %v", err)
	}
	t.Logf("Successfully submitted and executed Vulkan frame with solid color quads")
}

func TestVulkanRendererDmaBufBlit(t *testing.T) {
	drmFD, err := openDrmDevice()
	if err != nil {
		t.Skipf("skipping DMA-BUF blit test: no DRM device (%v)", err)
	}
	defer unix.Close(drmFD)

	width := uint32(64)
	height := uint32(64)
	dmaBufFD, pitch, err := createDrmDmaBuf(drmFD, width, height)
	if err != nil {
		t.Skipf("skipping DMA-BUF blit test: dumb buffer unsupported (%v)", err)
	}
	defer unix.Close(dmaBufFD)

	dev, err := NewDevice()
	if err != nil {
		t.Fatalf("NewDevice failed: %v", err)
	}
	defer dev.Close()

	ren, err := NewRenderer(dev)
	if err != nil {
		t.Fatalf("NewRenderer failed: %v", err)
	}
	defer ren.Close()

	target, err := ren.CreateTarget(640, 480)
	if err != nil {
		t.Fatalf("CreateTarget failed: %v", err)
	}
	defer ren.DestroyTarget(target)

	imported, err := dev.ImportDmaBuf(dmaBufFD, width, height, pitch, 0)
	if err != nil {
		t.Fatalf("ImportDmaBuf failed: %v", err)
	}
	defer dev.DestroyImportedBuffer(imported)

	if err := ren.BeginFrame(target, 0.05, 0.05, 0.05, 1.0); err != nil {
		t.Fatalf("BeginFrame failed: %v", err)
	}

	// Blit imported DMA-BUF texture to destination rect
	ren.DrawTextureQuad(imported, 50, 50, 200, 200, 0.0, 0.0, 1.0, 1.0, 1.0)

	if err := ren.EndFrame(); err != nil {
		t.Fatalf("EndFrame failed: %v", err)
	}

	if err := ren.WaitFrame(1000000000); err != nil {
		t.Fatalf("WaitFrame failed: %v", err)
	}
	t.Logf("Successfully composited client DMA-BUF texture quad with Vulkan!")
}

func TestVulkanCloseIdempotent(t *testing.T) {
	dev, err := NewDevice()
	if err != nil {
		t.Fatalf("NewDevice failed: %v", err)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("second Close must be nil, got: %v", err)
	}
}

func TestVulkanImportIntoClosedDevice(t *testing.T) {
	dev, err := NewDevice()
	if err != nil {
		t.Fatalf("NewDevice failed: %v", err)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if err := dev.ImportDmaBufInto(&ImportedBuffer{}, -1, 1, 1, 4, 0); err == nil {
		t.Fatal("expected error importing into closed device")
	}
}

func TestVulkanImportInvalidFd(t *testing.T) {
	dev, err := NewDevice()
	if err != nil {
		t.Fatalf("NewDevice failed: %v", err)
	}
	defer dev.Close()

	latinFD, err := unix.Open("/dev/zero", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Skipf("skipping: cannot open /dev/zero: %v", err)
	}
	defer unix.Close(latinFD)

	if _, err := dev.ImportDmaBuf(latinFD, 64, 64, 256, 0); err == nil {
		t.Fatal("expected error importing non-DMA-BUF file descriptor")
	}
}

func TestVulkanCreateTargetZeroSize(t *testing.T) {
	dev, err := NewDevice()
	if err != nil {
		t.Fatalf("NewDevice failed: %v", err)
	}
	defer dev.Close()

	ren, err := NewRenderer(dev)
	if err != nil {
		t.Fatalf("NewRenderer failed: %v", err)
	}
	defer ren.Close()

	for _, size := range [][2]uint32{{0, 0}, {0, 720}, {1280, 0}} {
		if _, err := ren.CreateTarget(size[0], size[1]); err == nil {
			t.Fatalf("expected error creating zero-size target %v", size)
		}
	}
}

func TestVulkanDrawOutsidePassNoPanic(t *testing.T) {
	dev, err := NewDevice()
	if err != nil {
		t.Fatalf("NewDevice failed: %v", err)
	}
	defer dev.Close()

	ren, err := NewRenderer(dev)
	if err != nil {
		t.Fatalf("NewRenderer failed: %v", err)
	}
	defer ren.Close()

	target, err := ren.CreateTarget(320, 240)
	if err != nil {
		t.Fatalf("CreateTarget failed: %v", err)
	}
	defer ren.DestroyTarget(target)

	// Draws outside an active render pass must be silently ignored, not panic.
	ren.DrawColorQuad(0, 0, 10, 10, 1, 1, 1, 1)
	ren.DrawTextureQuad(nil, 0, 0, 10, 10, 0, 0, 1, 1, 1)

	if err := ren.EndFrame(); err != nil {
		t.Fatalf("EndFrame without pass must be no-op success, got: %v", err)
	}
}

func TestVulkanDestroyImportedBufferIdempotent(t *testing.T) {
	drmFD, err := openDrmDevice()
	if err != nil {
		t.Skipf("skipping: no DRM device (%v)", err)
	}
	defer unix.Close(drmFD)

	dmaBufFD, pitch, err := createDrmDmaBuf(drmFD, 16, 16)
	if err != nil {
		t.Skipf("skipping: dumb buffer unsupported (%v)", err)
	}
	defer unix.Close(dmaBufFD)

	dev, err := NewDevice()
	if err != nil {
		t.Fatalf("NewDevice failed: %v", err)
	}
	defer dev.Close()

	buf, err := dev.ImportDmaBuf(dmaBufFD, 16, 16, pitch, 0)
	if err != nil {
		t.Fatalf("ImportDmaBuf failed: %v", err)
	}

	dev.DestroyImportedBuffer(buf)
	dev.DestroyImportedBuffer(buf) // second destroy must be safe
	dev.DestroyImportedBuffer(nil) // nil must be safe
}
