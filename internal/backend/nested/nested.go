package nested

import (
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"

	"bloxwm/internal/protocol/wayland"
	"bloxwm/internal/wire"
	"golang.org/x/sys/unix"
)

// Callbacks for input and window events from the parent compositor.
type Callbacks struct {
	OnPointerMotion func(x, y float64)
	OnPointerButton func(button uint32, state uint32)
	OnPointerLeave  func()
	OnKey           func(key uint32, state uint32)
	OnModifiers     func(depressed, latched, locked, group uint32)
	OnClose         func()

	// OnDisconnect fires when the parent connection ends without a close
	// request (socket EOF or read error). The compositor cannot run
	// without its parent, so this must trigger shutdown.
	OnDisconnect func()
}

// Backend represents a nested compositor output window running inside a parent Wayland compositor.
type Backend struct {
	conn   net.Conn
	rawFD  int
	width  int32
	height int32
	stride int32
	size   int

	memFD     int
	pixelData []byte

	// Wayland protocol object IDs on the parent connection
	nextID       uint32
	displayID    uint32
	registryID   uint32
	compID       uint32
	shmID        uint32
	xdgWmBaseID  uint32
	seatID       uint32
	pointerID    uint32
	keyboardID   uint32
	surfaceID    uint32
	xdgSurfaceID uint32
	toplevelID   uint32
	poolID       uint32
	bufferID     uint32

	// Wire codec buffers for parent communication
	wBuf    [8192]byte
	wFDs    [4]int
	writer  wire.Writer
	writeMu sync.Mutex

	callbacks Callbacks
	closed    atomic.Bool
}

// NewBackend initializes a connection to the parent Wayland compositor and maps an output framebuffer.
func NewBackend(width, height int32, cb Callbacks) (*Backend, error) {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	waylandDisplay := os.Getenv("WAYLAND_DISPLAY")
	if waylandDisplay == "" {
		waylandDisplay = "wayland-1"
	}
	sockPath := fmt.Sprintf("%s/%s", runtimeDir, waylandDisplay)

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("failed connecting to parent Wayland session at %s: %w", sockPath, err)
	}

	rawConn, err := conn.(*net.UnixConn).SyscallConn()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to get syscall conn: %w", err)
	}

	var rawFD int
	_ = rawConn.Control(func(fd uintptr) {
		rawFD = int(fd)
	})

	b := &Backend{
		conn:      conn,
		rawFD:     rawFD,
		width:     width,
		height:    height,
		stride:    width * 4,
		size:      int(width * 4 * height),
		callbacks: cb,
	}
	b.writer.Init(b.wBuf[:], b.wFDs[:])

	if err := b.initProtocol(); err != nil {
		_ = b.Close()
		return nil, err
	}

	return b, nil
}

func (b *Backend) allocID() uint32 {
	b.nextID++
	return b.nextID
}

func (b *Backend) initProtocol() error {
	// 1. Display is object 1
	b.displayID = b.allocID() // 1

	// 2. Get registry
	b.registryID = b.allocID() // 2
	start := b.writer.BeginMessage(b.displayID, 1)
	_ = b.writer.WriteUint32(b.registryID)
	_ = b.writer.EndMessage(start, b.displayID, 1)

	// 3. Sync
	syncID := b.allocID() // 3
	start = b.writer.BeginMessage(b.displayID, 0)
	_ = b.writer.WriteUint32(syncID)
	_ = b.writer.EndMessage(start, b.displayID, 0)

	if err := unix.Sendmsg(b.rawFD, b.writer.Bytes(), nil, nil, 0); err != nil {
		return fmt.Errorf("failed sending get_registry: %w", err)
	}

	// Read globals from parent
	var inBuf [16384]byte
	var oobBuf [256]byte
	pfd := []unix.PollFd{{Fd: int32(b.rawFD), Events: unix.POLLIN}}

	var compName, shmName, xdgName, seatName uint32
	var r wire.Reader
	doneSync := false

	for !doneSync {
		_, _ = unix.Poll(pfd, 1000)
		n, _, _, _, err := unix.Recvmsg(b.rawFD, inBuf[:], oobBuf[:], 0)
		if err != nil || n == 0 {
			return fmt.Errorf("recvmsg failed reading globals: %w", err)
		}

		r.Init(inBuf[:n], nil)
		for r.Remaining() >= wire.HeaderSize {
			hdr, err := r.ReadHeader()
			if err != nil {
				break
			}
			if hdr.ObjectID == b.registryID && hdr.Opcode == 0 { // wl_registry.global
				name, _ := r.ReadUint32()
				iface, _ := r.ReadString()
				_, _ = r.ReadUint32() // version
				switch iface {
				case "wl_compositor":
					if compName == 0 {
						compName = name
					}
				case "wl_shm":
					if shmName == 0 {
						shmName = name
					}
				case "xdg_wm_base":
					if xdgName == 0 {
						xdgName = name
					}
				case "wl_seat":
					if seatName == 0 {
						seatName = name
					}
				}
			} else if hdr.ObjectID == syncID && hdr.Opcode == 0 { // wl_callback.done
				_, _ = r.ReadUint32()
				doneSync = true
			} else {
				payloadLen := int(hdr.Size) - wire.HeaderSize
				for i := 0; i < payloadLen; i += 4 {
					_, _ = r.ReadUint32()
				}
			}
		}
	}

	if compName == 0 || shmName == 0 || xdgName == 0 {
		return fmt.Errorf("parent compositor missing required globals: comp=%d, shm=%d, xdg=%d", compName, shmName, xdgName)
	}

	b.writer.Reset()

	// 4. Bind wl_compositor (id = 4)
	b.compID = b.allocID()
	start = b.writer.BeginMessage(b.registryID, 0)
	_ = b.writer.WriteUint32(compName)
	_ = b.writer.WriteString("wl_compositor")
	_ = b.writer.WriteUint32(4)
	_ = b.writer.WriteUint32(b.compID)
	_ = b.writer.EndMessage(start, b.registryID, 0)

	// 5. Bind wl_shm (id = 5)
	b.shmID = b.allocID()
	start = b.writer.BeginMessage(b.registryID, 0)
	_ = b.writer.WriteUint32(shmName)
	_ = b.writer.WriteString("wl_shm")
	_ = b.writer.WriteUint32(1)
	_ = b.writer.WriteUint32(b.shmID)
	_ = b.writer.EndMessage(start, b.registryID, 0)

	// 6. Bind xdg_wm_base (id = 6)
	b.xdgWmBaseID = b.allocID()
	start = b.writer.BeginMessage(b.registryID, 0)
	_ = b.writer.WriteUint32(xdgName)
	_ = b.writer.WriteString("xdg_wm_base")
	_ = b.writer.WriteUint32(1)
	_ = b.writer.WriteUint32(b.xdgWmBaseID)
	_ = b.writer.EndMessage(start, b.registryID, 0)

	// 7. Bind wl_seat if available
	if seatName != 0 {
		b.seatID = b.allocID()
		start = b.writer.BeginMessage(b.registryID, 0)
		_ = b.writer.WriteUint32(seatName)
		_ = b.writer.WriteString("wl_seat")
		_ = b.writer.WriteUint32(1)
		_ = b.writer.WriteUint32(b.seatID)
		_ = b.writer.EndMessage(start, b.registryID, 0)

		// Get pointer
		b.pointerID = b.allocID()
		start = b.writer.BeginMessage(b.seatID, 0) // wl_seat.get_pointer
		_ = b.writer.WriteUint32(b.pointerID)
		_ = b.writer.EndMessage(start, b.seatID, 0)

		// Get keyboard
		b.keyboardID = b.allocID()
		start = b.writer.BeginMessage(b.seatID, 1) // wl_seat.get_keyboard
		_ = b.writer.WriteUint32(b.keyboardID)
		_ = b.writer.EndMessage(start, b.seatID, 1)
	}

	// Create surface
	b.surfaceID = b.allocID()
	start = b.writer.BeginMessage(b.compID, 0) // wl_compositor.create_surface
	_ = b.writer.WriteUint32(b.surfaceID)
	_ = b.writer.EndMessage(start, b.compID, 0)

	// Create xdg_surface
	b.xdgSurfaceID = b.allocID()
	start = b.writer.BeginMessage(b.xdgWmBaseID, 2) // xdg_wm_base.get_xdg_surface
	_ = b.writer.WriteUint32(b.xdgSurfaceID)
	_ = b.writer.WriteUint32(b.surfaceID)
	_ = b.writer.EndMessage(start, b.xdgWmBaseID, 2)

	// Create xdg_toplevel
	b.toplevelID = b.allocID()
	start = b.writer.BeginMessage(b.xdgSurfaceID, 1) // xdg_surface.get_toplevel
	_ = b.writer.WriteUint32(b.toplevelID)
	_ = b.writer.EndMessage(start, b.xdgSurfaceID, 1)

	// Set title & app_id
	start = b.writer.BeginMessage(b.toplevelID, 2) // set_title
	_ = b.writer.WriteString("bloxwm compositor")
	_ = b.writer.EndMessage(start, b.toplevelID, 2)

	start = b.writer.BeginMessage(b.toplevelID, 3) // set_app_id
	_ = b.writer.WriteString("bloxwm")
	_ = b.writer.EndMessage(start, b.toplevelID, 3)

	// Initial commit to request configure
	start = b.writer.BeginMessage(b.surfaceID, 6) // wl_surface.commit
	_ = b.writer.EndMessage(start, b.surfaceID, 6)

	fmt.Printf("[+] nested: sending surface creation requests (%d bytes)...\n", len(b.writer.Bytes()))
	if err := unix.Sendmsg(b.rawFD, b.writer.Bytes(), nil, nil, 0); err != nil {
		return fmt.Errorf("failed sending surface creation requests: %w", err)
	}

	fmt.Printf("[+] nested: waiting for configure...\n")
	// Wait for xdg_surface.configure
	var confSerial uint32
	haveConfig := false

	for !haveConfig {
		_, _ = unix.Poll(pfd, 1000)
		n, _, _, _, err := unix.Recvmsg(b.rawFD, inBuf[:], oobBuf[:], 0)
		if err != nil || n == 0 {
			return fmt.Errorf("recvmsg failed waiting for configure: %w", err)
		}
		r.Init(inBuf[:n], nil)
		for r.Remaining() >= wire.HeaderSize {
			hdr, err := r.ReadHeader()
			if err != nil {
				break
			}
			if hdr.ObjectID == b.displayID && hdr.Opcode == 0 { // wl_display.error
				objID, _ := r.ReadUint32()
				code, _ := r.ReadUint32()
				msg, _ := r.ReadString()
				return fmt.Errorf("parent protocol error: obj=%d code=%d msg=%s", objID, code, msg)
			} else if hdr.ObjectID == b.xdgSurfaceID && hdr.Opcode == 0 { // xdg_surface.configure
				confSerial, _ = r.ReadUint32()
				haveConfig = true
			} else {
				payloadLen := int(hdr.Size) - wire.HeaderSize
				for i := 0; i < payloadLen; i += 4 {
					_, _ = r.ReadUint32()
				}
			}
		}
	}

	// Create memfd framebuffer
	memFD, err := unix.MemfdCreate("bloxwm-framebuffer", 0)
	if err != nil {
		return fmt.Errorf("memfd create error: %w", err)
	}
	b.memFD = memFD
	_ = unix.Ftruncate(memFD, int64(b.size))

	pixelData, err := unix.Mmap(memFD, 0, b.size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("mmap error: %w", err)
	}
	b.pixelData = pixelData

	b.writer.Reset()

	// Ack configure
	start = b.writer.BeginMessage(b.xdgSurfaceID, 4) // ack_configure
	_ = b.writer.WriteUint32(confSerial)
	_ = b.writer.EndMessage(start, b.xdgSurfaceID, 4)

	// Create shm pool
	b.poolID = b.allocID()
	start = b.writer.BeginMessage(b.shmID, 0) // wl_shm.create_pool
	_ = b.writer.WriteUint32(b.poolID)
	_ = b.writer.WriteInt32(int32(b.size))
	_ = b.writer.EndMessage(start, b.shmID, 0)

	// Create buffer
	b.bufferID = b.allocID()
	start = b.writer.BeginMessage(b.poolID, 0) // wl_shm_pool.create_buffer
	_ = b.writer.WriteUint32(b.bufferID)
	_ = b.writer.WriteInt32(0) // offset
	_ = b.writer.WriteInt32(b.width)
	_ = b.writer.WriteInt32(b.height)
	_ = b.writer.WriteInt32(b.stride)
	_ = b.writer.WriteUint32(uint32(wayland.WlShmFormatArgb8888))
	_ = b.writer.EndMessage(start, b.poolID, 0)

	// Attach buffer to surface
	start = b.writer.BeginMessage(b.surfaceID, 1) // wl_surface.attach
	_ = b.writer.WriteUint32(b.bufferID)
	_ = b.writer.WriteInt32(0)
	_ = b.writer.WriteInt32(0)
	_ = b.writer.EndMessage(start, b.surfaceID, 1)

	// Damage and commit
	start = b.writer.BeginMessage(b.surfaceID, 2) // wl_surface.damage
	_ = b.writer.WriteInt32(0)
	_ = b.writer.WriteInt32(0)
	_ = b.writer.WriteInt32(b.width)
	_ = b.writer.WriteInt32(b.height)
	_ = b.writer.EndMessage(start, b.surfaceID, 2)

	start = b.writer.BeginMessage(b.surfaceID, 6) // wl_surface.commit
	_ = b.writer.EndMessage(start, b.surfaceID, 6)

	oob := unix.UnixRights(b.memFD)
	if err := unix.Sendmsg(b.rawFD, b.writer.Bytes(), oob, nil, 0); err != nil {
		return fmt.Errorf("failed sending initial buffer and commit: %w", err)
	}
	b.writer.Reset()

	return nil
}

// Width returns the output width in pixels.
func (b *Backend) Width() int32 {
	return b.width
}

// Height returns the output height in pixels.
func (b *Backend) Height() int32 {
	return b.height
}

// Stride returns the byte stride per scanline.
func (b *Backend) Stride() int32 {
	return b.stride
}

// Pixels returns the direct memory-mapped pixel slice of the output framebuffer.
func (b *Backend) Pixels() []byte {
	return b.pixelData
}

// Damage marks a region of the screen as modified.
func (b *Backend) Damage(x, y, w, h int32) {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()

	start := b.writer.BeginMessage(b.surfaceID, 2) // wl_surface.damage
	_ = b.writer.WriteInt32(x)
	_ = b.writer.WriteInt32(y)
	_ = b.writer.WriteInt32(w)
	_ = b.writer.WriteInt32(h)
	_ = b.writer.EndMessage(start, b.surfaceID, 2)
}

// Present commits the current framebuffer to the parent compositor (0 allocs/op).
func (b *Backend) Present() error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()

	// Attach existing buffer ID to notify parent of damage update
	start := b.writer.BeginMessage(b.surfaceID, 1) // wl_surface.attach
	_ = b.writer.WriteUint32(b.bufferID)
	_ = b.writer.WriteInt32(0)
	_ = b.writer.WriteInt32(0)
	_ = b.writer.EndMessage(start, b.surfaceID, 1)

	// Commit surface
	start = b.writer.BeginMessage(b.surfaceID, 6) // wl_surface.commit
	_ = b.writer.EndMessage(start, b.surfaceID, 6)

	err := unix.Sendmsg(b.rawFD, b.writer.Bytes(), nil, nil, 0)
	b.writer.Reset()
	return err
}

// RunEventLoop processes incoming messages and input from the parent compositor.
func (b *Backend) RunEventLoop() {
	var inBuf [8192]byte
	var oobBuf [256]byte
	var r wire.Reader

	for !b.closed.Load() {
		n, _, _, _, err := unix.Recvmsg(b.rawFD, inBuf[:], oobBuf[:], 0)
		if err != nil {
			// Transient interrupts must not kill the input path: the loop
			// exits only when closed or the peer disconnects.
			if b.closed.Load() {
				return
			}
			if err == unix.EINTR || err == unix.EAGAIN {
				continue
			}
			b.notifyDisconnect()
			return
		}
		if n == 0 {
			if b.closed.Load() {
				return
			}
			b.notifyDisconnect()
			return
		}

		r.Init(inBuf[:n], nil)
		for r.Remaining() >= wire.HeaderSize {
			hdr, err := r.ReadHeader()
			if err != nil {
				break
			}
			if hdr.ObjectID == b.displayID && hdr.Opcode == 0 {
				objID, _ := r.ReadUint32()
				code, _ := r.ReadUint32()
				msg, _ := r.ReadString()
				fmt.Printf("[-] PARENT ERROR IN RUN LOOP: obj=%d code=%d msg=%s\n", objID, code, msg)
			}
			if hdr.ObjectID == b.xdgWmBaseID && hdr.Opcode == 0 { // ping
				serial, _ := r.ReadUint32()
				b.writeMu.Lock()
				start := b.writer.BeginMessage(b.xdgWmBaseID, 3) // pong
				_ = b.writer.WriteUint32(serial)
				_ = b.writer.EndMessage(start, b.xdgWmBaseID, 3)
				_ = unix.Sendmsg(b.rawFD, b.writer.Bytes(), nil, nil, 0)
				b.writer.Reset()
				b.writeMu.Unlock()
			} else if hdr.ObjectID == b.pointerID && hdr.Opcode == 2 { // wl_pointer.motion
				_, _ = r.ReadUint32() // time
				xFixed, _ := r.ReadUint32()
				yFixed, _ := r.ReadUint32()
				x := float64(int32(xFixed)) / 256.0
				y := float64(int32(yFixed)) / 256.0
				if b.callbacks.OnPointerMotion != nil {
					b.callbacks.OnPointerMotion(x, y)
				}
			} else if hdr.ObjectID == b.pointerID && hdr.Opcode == 3 { // wl_pointer.button
				_, _ = r.ReadUint32() // serial
				_, _ = r.ReadUint32() // time
				button, _ := r.ReadUint32()
				state, _ := r.ReadUint32()
				if b.callbacks.OnPointerButton != nil {
					b.callbacks.OnPointerButton(button, state)
				}
			} else if hdr.ObjectID == b.pointerID && hdr.Opcode == 1 { // wl_pointer.leave
				_, _ = r.ReadUint32() // serial
				_, _ = r.ReadUint32() // surface
				if b.callbacks.OnPointerLeave != nil {
					b.callbacks.OnPointerLeave()
				}
			} else if hdr.ObjectID == b.keyboardID && hdr.Opcode == 3 { // wl_keyboard.key
				_, _ = r.ReadUint32() // serial
				_, _ = r.ReadUint32() // time
				key, _ := r.ReadUint32()
				state, _ := r.ReadUint32()
				if b.callbacks.OnKey != nil {
					b.callbacks.OnKey(key, state)
				}
			} else if hdr.ObjectID == b.keyboardID && hdr.Opcode == 4 { // wl_keyboard.modifiers
				_, _ = r.ReadUint32() // serial
				depressed, _ := r.ReadUint32()
				latched, _ := r.ReadUint32()
				locked, _ := r.ReadUint32()
				group, _ := r.ReadUint32()
				if b.callbacks.OnModifiers != nil {
					b.callbacks.OnModifiers(depressed, latched, locked, group)
				}
			} else if hdr.ObjectID == b.toplevelID && hdr.Opcode == 1 { // xdg_toplevel.close
				if b.callbacks.OnClose != nil {
					b.callbacks.OnClose()
				}
			} else {
				payloadLen := int(hdr.Size) - wire.HeaderSize
				for i := 0; i < payloadLen; i += 4 {
					_, _ = r.ReadUint32()
				}
			}
		}
	}
}

// notifyDisconnect tells the caller the parent connection is gone. It runs
// on the event-loop goroutine, which then exits.
func (b *Backend) notifyDisconnect() {
	if b.callbacks.OnDisconnect != nil {
		b.callbacks.OnDisconnect()
	}
}

// Close terminates connection and releases resources.
func (b *Backend) Close() error {
	if b.closed.Swap(true) {
		return nil
	}
	if b.pixelData != nil {
		_ = unix.Munmap(b.pixelData)
		b.pixelData = nil
	}
	if b.memFD >= 0 {
		_ = unix.Close(b.memFD)
		b.memFD = -1
	}
	return b.conn.Close()
}
