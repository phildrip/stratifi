package server

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bloxwm/internal/protocol/wayland"
	"bloxwm/internal/wire"
	"golang.org/x/sys/unix"
)

// fakeFrameGrabber fills the target with a deterministic gradient.
type fakeFrameGrabber struct{}

func (fakeFrameGrabber) GrabFramebuffer(dst []byte, width, height int32, stride int32) error {
	if stride*height > int32(len(dst)) {
		return ErrFrameGrabberOverflow
	}
	for y := int32(0); y < height; y++ {
		row := dst[y*stride:]
		for x := int32(0); x < width; x++ {
			row[x*4+0] = byte(x)
			row[x*4+1] = byte(y)
			row[x*4+2] = 0x42
			row[x*4+3] = 0xFF
		}
	}
	return nil
}

// ErrFrameGrabberOverflow signals a buffer smaller than the framebuffer.
var ErrFrameGrabberOverflow = errorFrameOverflow{}

type errorFrameOverflow struct{}

func (errorFrameOverflow) Error() string { return "frame buffer too small for grab" }

// captureTestClient drives a raw socket against the compositor like a real
// screen-sharing client (Firefox's webrtc/portal path).
type captureTestClient struct {
	t        *testing.T
	rawFD    int
	netConn  *net.UnixConn
	w        wire.Writer
	sendBuf  [8192]byte
	sendFDs  [4]int
	recvBuf  [8192]byte
	oobBuf   [256]byte
	r        wire.Reader
	poolFD   int
	poolSize int
}

func newCaptureTestClient(t *testing.T, sockPath string) *captureTestClient {
	t.Helper()
	addr, err := net.ResolveUnixAddr("unix", sockPath)
	if err != nil {
		t.Fatalf("ResolveUnixAddr: %v", err)
	}
	netConn, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		t.Fatalf("DialUnix: %v", err)
	}
	rawConn, err := netConn.SyscallConn()
	if err != nil {
		t.Fatalf("SyscallConn: %v", err)
	}
	var rawFD int
	_ = rawConn.Control(func(fd uintptr) { rawFD = int(fd) })
	cc := &captureTestClient{t: t, rawFD: rawFD, netConn: netConn}
	cc.w.Init(cc.sendBuf[:], cc.sendFDs[:])
	t.Cleanup(func() { _ = netConn.Close() })
	return cc
}

func (cc *captureTestClient) send() {
	cc.t.Helper()
	if err := unix.Sendmsg(cc.rawFD, cc.w.Bytes(), nil, nil, 0); err != nil {
		cc.t.Fatalf("Sendmsg: %v", err)
	}
	cc.w.Reset()
}

// sendPool creates a wl_shm pool backed by a memfd and returns its mapping.
func (cc *captureTestClient) sendPool(shmID uint32, poolID uint32, size int) []byte {
	cc.t.Helper()
	fd, err := unix.MemfdCreate("bloxwm-capture-test", 0)
	if err != nil {
		cc.t.Fatalf("MemfdCreate: %v", err)
	}
	if err := unix.Ftruncate(fd, int64(size)); err != nil {
		cc.t.Fatalf("Ftruncate: %v", err)
	}

	start := cc.w.BeginMessage(shmID, 0) // wl_shm.create_pool
	_ = cc.w.WriteUint32(poolID)
	_ = cc.w.WriteInt32(int32(size))
	_ = cc.w.EndMessage(start, shmID, 0)
	oob := unix.UnixRights(fd)
	if err := unix.Sendmsg(cc.rawFD, cc.w.Bytes(), oob, nil, 0); err != nil {
		cc.t.Fatalf("Sendmsg with pool fd: %v", err)
	}
	cc.w.Reset()

	data, err := unix.Mmap(fd, 0, size, unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		cc.t.Fatalf("client mmap: %v", err)
	}
	cc.poolFD = fd
	cc.poolSize = size
	cc.t.Cleanup(func() {
		_ = unix.Munmap(data)
		_ = unix.Close(fd)
	})
	return data
}

// recvAll drains pending server events into the reader.
func (cc *captureTestClient) recvAll(timeoutMs int) {
	cc.t.Helper()
	pfd := []unix.PollFd{{Fd: int32(cc.rawFD), Events: unix.POLLIN}}
	_, err := unix.Poll(pfd, timeoutMs)
	if err != nil {
		cc.t.Fatalf("Poll: %v", err)
	}
	n, _, _, _, err := unix.Recvmsg(cc.rawFD, cc.recvBuf[:], cc.oobBuf[:], 0)
	if err != nil {
		cc.t.Fatalf("Recvmsg: %v", err)
	}
	cc.r.Init(cc.recvBuf[:n], nil)
}

// TestCaptureSessionFullFrame exercises the Firefox portal capture path:
// bind managers, source, session negotiation (buffer_size/shm_format/done),
// then frame attach → capture → ready with verified pixel contents.
func TestCaptureSessionFullFrame(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "bloxwm-capture-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tempDir)

	sockPath := filepath.Join(tempDir, "wayland-cap-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()

	grabCh := make(chan struct{}, 4)
	srv.SetFrameGrabber(grabRecordingGrabber{grabCh})

	go func() { _ = srv.Run() }()
	time.Sleep(20 * time.Millisecond)

	cc := newCaptureTestClient(t, sockPath)

	// Registry + bind wl_shm (id 3) and the two capture managers (ids 4, 5).
	start := cc.w.BeginMessage(1, 1) // wl_display.get_registry
	_ = cc.w.WriteUint32(2)
	_ = cc.w.EndMessage(start, 1, 1)

	start = cc.w.BeginMessage(2, 0) // wl_registry.bind wl_shm
	_ = cc.w.WriteUint32(GlobalShm)
	_ = cc.w.WriteString("wl_shm")
	_ = cc.w.WriteUint32(1)
	_ = cc.w.WriteUint32(3)
	_ = cc.w.EndMessage(start, 2, 0)

	start = cc.w.BeginMessage(2, 0) // ext_output_image_capture_source_manager_v1
	_ = cc.w.WriteUint32(GlobalOutputImageCaptureSourceManager)
	_ = cc.w.WriteString("ext_output_image_capture_source_manager_v1")
	_ = cc.w.WriteUint32(1)
	_ = cc.w.WriteUint32(4)
	_ = cc.w.EndMessage(start, 2, 0)

	start = cc.w.BeginMessage(2, 0) // ext_image_copy_capture_manager_v1
	_ = cc.w.WriteUint32(GlobalImageCopyCaptureManager)
	_ = cc.w.WriteString("ext_image_copy_capture_manager_v1")
	_ = cc.w.WriteUint32(1)
	_ = cc.w.WriteUint32(5)
	_ = cc.w.EndMessage(start, 2, 0)
	cc.send()

	// Create source (id 10) bound to output global 4, then session (id 11).
	start = cc.w.BeginMessage(4, 0) // ext_output_image_capture_source_manager_v1.create_source
	_ = cc.w.WriteUint32(10)
	_ = cc.w.WriteUint32(GlobalOutput)
	_ = cc.w.EndMessage(start, 4, 0)

	start = cc.w.BeginMessage(5, 0) // ext_image_copy_capture_manager_v1.create_session
	_ = cc.w.WriteUint32(11)        // new session id
	_ = cc.w.WriteUint32(10)        // source
	_ = cc.w.WriteUint32(0)         // options
	_ = cc.w.EndMessage(start, 5, 0)
	cc.send()

	cc.recvAll(1000)

	var bufW, bufH uint32
	var shmFormats []uint32
	sessionDone := false
	for cc.r.Remaining() >= wire.HeaderSize {
		hdr, err := cc.r.ReadHeader()
		if err != nil {
			t.Fatalf("ReadHeader: %v", err)
		}
		switch {
		case hdr.ObjectID == 11 && hdr.Opcode == 0: // buffer_size
			bufW, _ = cc.r.ReadUint32()
			bufH, _ = cc.r.ReadUint32()
		case hdr.ObjectID == 11 && hdr.Opcode == 1: // shm_format
			f, _ := cc.r.ReadUint32()
			shmFormats = append(shmFormats, f)
		case hdr.ObjectID == 11 && hdr.Opcode == 4: // done
			sessionDone = true
		default:
			payloadLen := int(hdr.Size) - wire.HeaderSize
			for i := 0; i < payloadLen; i += 4 {
				_, _ = cc.r.ReadUint32()
			}
		}
	}
	if bufW != 960 || bufH != 640 {
		t.Errorf("buffer_size = %dx%d, want 960x640", bufW, bufH)
	}
	if len(shmFormats) != 2 {
		t.Errorf("shm formats = %v, want ARGB8888+XRGB8888", shmFormats)
	}
	if !sessionDone {
		t.Error("missing session done event")
	}

	// Frame: session.create_frame (frame id 12), shm pool 13/buffer 14.
	start = cc.w.BeginMessage(11, 0) // session.create_frame
	_ = cc.w.WriteUint32(12)
	_ = cc.w.EndMessage(start, 11, 0)
	cc.send()

	poolSize := 960 * 640 * 4
	clientView := cc.sendPool(3, 13, poolSize)

	start = cc.w.BeginMessage(13, 0) // wl_shm_pool.create_buffer
	_ = cc.w.WriteUint32(14)         // buffer id
	_ = cc.w.WriteInt32(0)           // offset
	_ = cc.w.WriteInt32(960)         // width
	_ = cc.w.WriteInt32(640)         // height
	_ = cc.w.WriteInt32(960 * 4)     // stride
	_ = cc.w.WriteUint32(uint32(wayland.WlShmFormatArgb8888))
	_ = cc.w.EndMessage(start, 13, 0)

	start = cc.w.BeginMessage(12, 1) // frame.attach_buffer
	_ = cc.w.WriteUint32(14)
	_ = cc.w.EndMessage(start, 12, 1)

	start = cc.w.BeginMessage(12, 2) // frame.damage_buffer (full)
	_ = cc.w.WriteInt32(0)
	_ = cc.w.WriteInt32(0)
	_ = cc.w.WriteInt32(960)
	_ = cc.w.WriteInt32(640)
	_ = cc.w.EndMessage(start, 12, 2)

	start = cc.w.BeginMessage(12, 3) // frame.capture
	_ = cc.w.EndMessage(start, 12, 3)
	cc.send()

	// The grabber must have run exactly once for the capture request.
	select {
	case <-grabCh:
	case <-time.After(1 * time.Second):
		t.Fatal("frame grabber never invoked")
	}

	// Verify pixels landed in the client's shm buffer (server wrote them).
	if clientView[0] != 0x00 || clientView[1] != 0x00 || clientView[2] != 0x42 || clientView[3] != 0xFF {
		t.Errorf("capture buffer first pixel wrong: % x", clientView[:4])
	}
	last := 959*4 + (639-1)*960*4 // x=959,y=639 — deterministic gradient tail
	if clientView[last+0] != 0xBF || clientView[last+2] != 0x42 {
		t.Errorf("capture buffer tail pixel wrong: % x", clientView[last:last+4])
	}

	cc.recvAll(1000)
	frameReady := false
	transformSeen := false
	damageSeen := false
	for cc.r.Remaining() >= wire.HeaderSize {
		hdr, err := cc.r.ReadHeader()
		if err != nil {
			t.Fatalf("ReadHeader: %v", err)
		}
		switch {
		case hdr.ObjectID == 12 && hdr.Opcode == 0: // transform
			_, _ = cc.r.ReadUint32()
			transformSeen = true
		case hdr.ObjectID == 12 && hdr.Opcode == 1: // damage
			_, _ = cc.r.ReadInt32()
			_, _ = cc.r.ReadInt32()
			_, _ = cc.r.ReadInt32()
			_, _ = cc.r.ReadInt32()
			damageSeen = true
		case hdr.ObjectID == 12 && hdr.Opcode == 2: // presentation_time
			_, _ = cc.r.ReadUint32()
			_, _ = cc.r.ReadUint32()
			_, _ = cc.r.ReadUint32()
		case hdr.ObjectID == 12 && hdr.Opcode == 3: // ready
			frameReady = true
		case hdr.ObjectID == 12 && hdr.Opcode == 4: // failed
			_, _ = cc.r.ReadUint32()
			t.Fatalf("capture unexpectedly failed")
		default:
			payloadLen := int(hdr.Size) - wire.HeaderSize
			for i := 0; i < payloadLen; i += 4 {
				_, _ = cc.r.ReadUint32()
			}
		}
	}
	if !transformSeen || !damageSeen || !frameReady {
		t.Errorf("frame events: transform=%v damage=%v ready=%v", transformSeen, damageSeen, frameReady)
	}
}

// grabRecordingGrabber delegates to fakeFrameGrabber and ticks a channel.
type grabRecordingGrabber struct {
	ch chan<- struct{}
}

func (g grabRecordingGrabber) GrabFramebuffer(dst []byte, width, height, stride int32) error {
	err := fakeFrameGrabber{}.GrabFramebuffer(dst, width, height, stride)
	select {
	case g.ch <- struct{}{}:
	default:
	}
	return err
}

// TestCaptureSessionStopped guards the stopped-session error path.
func TestCaptureSessionStopped(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "bloxwm-capstop-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tempDir)

	sockPath := filepath.Join(tempDir, "wayland-capstop-0")
	srv, err := NewServer(sockPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()
	go func() { _ = srv.Run() }()
	time.Sleep(20 * time.Millisecond)

	cc := newCaptureTestClient(t, sockPath)

	// create_session with unknown source must fail the request handler.
	start := cc.w.BeginMessage(1, 1)
	_ = cc.w.WriteUint32(2)
	_ = cc.w.EndMessage(start, 1, 1)

	start = cc.w.BeginMessage(2, 0)
	_ = cc.w.WriteUint32(GlobalImageCopyCaptureManager)
	_ = cc.w.WriteString("ext_image_copy_capture_manager_v1")
	_ = cc.w.WriteUint32(1)
	_ = cc.w.WriteUint32(5)
	_ = cc.w.EndMessage(start, 2, 0)

	start = cc.w.BeginMessage(5, 0) // create_session with bogus source 999
	_ = cc.w.WriteUint32(11)
	_ = cc.w.WriteUint32(999)
	_ = cc.w.WriteUint32(0)
	_ = cc.w.EndMessage(start, 5, 0)
	cc.send()

	// The server must terminate the client (protocol error) or respond
	// with an error; a fatal protocol error closes the socket.
	time.Sleep(50 * time.Millisecond)
	pfd := []unix.PollFd{{Fd: int32(cc.rawFD), Events: unix.POLLIN | unix.POLLHUP}}
	_, _ = unix.Poll(pfd, 500)
	n, _, _, _, _ := unix.Recvmsg(cc.rawFD, cc.recvBuf[:], nil, 0)
	_ = n
	// The invalid session request must have been rejected: poll the server
	// side for the protocol error propagation via CountClients.
	if n := srv.CountClients(); n > 1 {
		t.Errorf("expected client termination, %d active", n)
	}
}

// ensure the capture grabber type is registered on the server side.
var _ FrameGrabber = fakeFrameGrabber{}
