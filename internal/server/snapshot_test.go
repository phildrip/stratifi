package server

import (
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

// makeSnapshotClient builds a client with one pool/buffer/surface wired the
// way HandleWlShm* handlers do, for teardown-race testing.
func makeSnapshotClient(t testing.TB) (*Client, *ShmPool) {
	t.Helper()
	c := &Client{id: 1}
	c.objects.init()
	c.surfaces.init()
	c.buffers.init()
	c.pools.init()

	// Real memfd-backed pool so the munmap path is exercised for real.
	fd, err := unix.MemfdCreate("bloxwm-snapshot-test", 0)
	if err != nil {
		t.Fatalf("MemfdCreate: %v", err)
	}
	size := 64 * 64 * 4
	if err := unix.Ftruncate(fd, int64(size)); err != nil {
		t.Fatalf("Ftruncate: %v", err)
	}
	data, err := unix.Mmap(fd, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		t.Fatalf("Mmap: %v", err)
	}
	t.Cleanup(func() {
		_ = unix.Munmap(data)
		_ = unix.Close(fd)
	})

	pool := c.pools.add(ShmPool{ID: 7, FD: fd, Size: size, Data: data})
	c.buffers.add(ShmBuffer{ID: 10, PoolID: 7, Width: 64, Height: 64, Stride: 256})
	c.surfaces.add(Surface{ID: 3, Active: true, Current: SurfaceState{AttachedBufferID: 10}})
	return c, pool
}

// TestSnapshotAfterPoolDestroy pins the teardown race: if the pool is
// destroyed between SurfaceBufferInfo and SnapshotSurfaceBuffer, the
// snapshot must report not-ok — never fault on the unmapped mapping.
func TestSnapshotAfterPoolDestroy(t *testing.T) {
	c, pool := makeSnapshotClient(t)

	// Learn geometry (the compositor's scratch-sizing call).
	if _, _, _, ok := c.SurfaceBufferInfo(3); !ok {
		t.Fatal("expected valid buffer info")
	}

	// Simulate client teardown racing the blit: unmap + drop the pool
	// exactly as HandleWlShmPoolDestroy/Close does.
	_ = unix.Munmap(pool.Data)
	pool.Data = nil
	c.pools.remove(7)

	// The snapshot must not fault and must report failure cleanly.
	scratch := make([]byte, 64*64*4)
	_, _, _, _, ok := c.SnapshotSurfaceBuffer(3, scratch)
	if ok {
		t.Error("snapshot must fail after pool teardown")
	}
}

// TestConcurrentSnapshots exercises concurrent snapshot + teardown across
// many goroutines with -race: the client mutex must make teardown vs blit
// safe (no use-after-unmap, no torn reads).
func TestConcurrentSnapshotVsTeardown(t *testing.T) {
	c, pool := makeSnapshotClient(t)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Blit side: snapshots continuously.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scratch := make([]byte, 64*64*4)
			for {
				select {
				case <-stop:
					return
				default:
				}
				c.SnapshotSurfaceBuffer(3, scratch)
			}
		}()
	}

	// Teardown side: mirror the real HandleWlShmPoolDestroy sequence —
	// munmap happens UNDER the client mutex, so no snapshot can observe a
	// mapping mid-teardown.
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.mu.Lock()
		_ = unix.Munmap(pool.Data)
		pool.Data = nil
		c.pools.remove(7)
		c.mu.Unlock()
		close(stop)
	}()

	wg.Wait()
	// A snapshot after teardown must fail cleanly (not fault).
	_, _, _, _, ok := c.SnapshotSurfaceBuffer(3, make([]byte, 64*64*4))
	if ok {
		t.Error("snapshot after teardown must fail")
	}
}
