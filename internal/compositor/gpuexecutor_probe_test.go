package compositor

import "testing"

// TestGPUSingleQuadMapping probes the push-constant → raster path with a
// single quarter-size quad: it must land exactly at scene (0,0,64,64).
func TestGPUSingleQuadMapping(t *testing.T) {
	exe, err := NewVulkanExecutor(128, 128)
	if err != nil {
		t.Skipf("no Vulkan device: %v", err)
	}
	defer exe.Close()

	dl := NewDrawList()
	dl.Quad(0, 0, 64, 64, 0xFF00FF00)   // green background
	dl.Quad(10, 10, 60, 40, 0xFF0000FF) // blue inner
	dl.Quad(10, 10, 60, 8, 0xFFFF0000)  // red strip
	dl.Quad(10, 40, 60, 8, 0xFF00FFFF)  // yellow strip
	dl.Quad(30, 20, 20, 10, 0xFFFFFFFF) // white blob

	exe.Execute(dl,
		func(clientID, surfaceID uint32) (int32, int32, int32, bool) { return 0, 0, 0, false },
		func(clientID, surfaceID uint32, dst []byte) (int, bool) { return 0, false },
	)

	// Second frame: only the red quad — does a fresh frame's first draw work?
	dl2 := NewDrawList()
	dl2.Quad(0, 0, 64, 64, 0xFFFF0000)
	exe.Execute(dl2,
		func(clientID, surfaceID uint32) (int32, int32, int32, bool) { return 0, 0, 0, false },
		func(clientID, surfaceID uint32, dst []byte) (int, bool) { return 0, false },
	)

	data, ok := exe.ren.ReadbackTarget(exe.target)
	if !ok {
		t.Fatal("readback failed")
	}
	for _, pt := range [][2]int32{{8, 8}, {40, 8}, {70, 8}, {90, 8}, {30, 13}, {30, 44}, {38, 24}, {100, 100}} {
		off := pt[1]*512 + pt[0]*4
		t.Logf("F1 (%d,%d) = % x", pt[0], pt[1], data[off:off+4])
	}
}

func sprintfPixel(b []byte) string {
	return fmtPixel(b)
}

func fmtPixel(b []byte) string {
	const hexd = "0123456789abcdef"
	s := []byte("0x________")
	for i, v := range b {
		s[2+i*2] = hexd[v>>4]
		s[3+i*2] = hexd[v&0xF]
	}
	return string(s)
}
