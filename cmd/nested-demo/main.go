package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"bloxwm/internal/protocol/wayland"
	"bloxwm/internal/wire"
	"golang.org/x/sys/unix"
)

var nextID uint32 = 1

func allocID() uint32 {
	id := nextID
	nextID++
	return id
}

func main() {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	waylandDisplay := os.Getenv("WAYLAND_DISPLAY")
	if waylandDisplay == "" {
		waylandDisplay = "wayland-1"
	}
	sockPath := fmt.Sprintf("%s/%s", runtimeDir, waylandDisplay)
	fmt.Printf("[+] Connecting to parent Sway session at: %s\n", sockPath)

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] Failed to connect to Sway: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	rawConn, err := conn.(*net.UnixConn).SyscallConn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] SyscallConn error: %v\n", err)
		os.Exit(1)
	}

	var rawFD int
	_ = rawConn.Control(func(fd uintptr) {
		rawFD = int(fd)
	})

	var sendBuf [8192]byte
	var sendFDs [4]int
	var w wire.Writer
	w.Init(sendBuf[:], sendFDs[:])

	// 1. Display is object 1
	displayID := allocID() // 1

	// 2. Get registry
	registryID := allocID() // 2
	start := w.BeginMessage(displayID, 1) // wl_display.get_registry
	_ = w.WriteUint32(registryID)
	_ = w.EndMessage(start, displayID, 1)

	// 3. Sync
	syncID := allocID() // 3
	start = w.BeginMessage(displayID, 0) // wl_display.sync
	_ = w.WriteUint32(syncID)
	_ = w.EndMessage(start, displayID, 0)

	_ = unix.Sendmsg(rawFD, w.Bytes(), nil, nil, 0)

	// Read globals from Sway
	var inBuf [16384]byte
	var oobBuf [256]byte
	pfd := []unix.PollFd{{Fd: int32(rawFD), Events: unix.POLLIN}}

	var compName, shmName, xdgName uint32
	var r wire.Reader
	doneSync := false

	for !doneSync {
		_, _ = unix.Poll(pfd, 1000)
		n, _, _, _, err := unix.Recvmsg(rawFD, inBuf[:], oobBuf[:], 0)
		if err != nil || n == 0 {
			fmt.Fprintf(os.Stderr, "[-] Recvmsg failed: %v\n", err)
			os.Exit(1)
		}

		r.Init(inBuf[:n], nil)
		for r.Remaining() >= wire.HeaderSize {
			hdr, err := r.ReadHeader()
			if err != nil {
				break
			}
			if hdr.ObjectID == registryID && hdr.Opcode == 0 { // wl_registry.global
				name, _ := r.ReadUint32()
				iface, _ := r.ReadString()
				ver, _ := r.ReadUint32()
				if iface == "wl_compositor" && compName == 0 {
					compName = name
					fmt.Printf("    Found wl_compositor (name %d, v%d)\n", name, ver)
				} else if iface == "wl_shm" && shmName == 0 {
					shmName = name
					fmt.Printf("    Found wl_shm (name %d, v%d)\n", name, ver)
				} else if iface == "xdg_wm_base" && xdgName == 0 {
					xdgName = name
					fmt.Printf("    Found xdg_wm_base (name %d, v%d)\n", name, ver)
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
		fmt.Fprintf(os.Stderr, "[-] Missing required globals: comp=%d, shm=%d, xdg=%d\n", compName, shmName, xdgName)
		os.Exit(1)
	}

	w.Reset()

	// 4. Bind wl_compositor (id = 4)
	compID := allocID() // 4
	start = w.BeginMessage(registryID, 0)
	_ = w.WriteUint32(compName)
	_ = w.WriteString("wl_compositor")
	_ = w.WriteUint32(4)
	_ = w.WriteUint32(compID)
	_ = w.EndMessage(start, registryID, 0)

	// 5. Bind wl_shm (id = 5)
	shmID := allocID() // 5
	start = w.BeginMessage(registryID, 0)
	_ = w.WriteUint32(shmName)
	_ = w.WriteString("wl_shm")
	_ = w.WriteUint32(1)
	_ = w.WriteUint32(shmID)
	_ = w.EndMessage(start, registryID, 0)

	// 6. Bind xdg_wm_base (id = 6)
	xdgWmBaseID := allocID() // 6
	start = w.BeginMessage(registryID, 0)
	_ = w.WriteUint32(xdgName)
	_ = w.WriteString("xdg_wm_base")
	_ = w.WriteUint32(1)
	_ = w.WriteUint32(xdgWmBaseID)
	_ = w.EndMessage(start, registryID, 0)

	// 7. Create wl_surface (id = 7)
	surfaceID := allocID() // 7
	start = w.BeginMessage(compID, 0) // wl_compositor.create_surface
	_ = w.WriteUint32(surfaceID)
	_ = w.EndMessage(start, compID, 0)

	// 8. Create xdg_surface (id = 8)
	xdgSurfaceID := allocID() // 8
	start = w.BeginMessage(xdgWmBaseID, 2) // xdg_wm_base.get_xdg_surface
	_ = w.WriteUint32(xdgSurfaceID)
	_ = w.WriteUint32(surfaceID)
	_ = w.EndMessage(start, xdgWmBaseID, 2)

	// 9. Create xdg_toplevel (id = 9)
	xdgToplevelID := allocID() // 9
	start = w.BeginMessage(xdgSurfaceID, 1) // xdg_surface.get_toplevel
	_ = w.WriteUint32(xdgToplevelID)
	_ = w.EndMessage(start, xdgSurfaceID, 1)

	// Set title
	start = w.BeginMessage(xdgToplevelID, 2) // xdg_toplevel.set_title
	_ = w.WriteString("bloxwm nested compositor spike")
	_ = w.EndMessage(start, xdgToplevelID, 2)

	// Set app_id
	start = w.BeginMessage(xdgToplevelID, 3) // xdg_toplevel.set_app_id
	_ = w.WriteString("bloxwm")
	_ = w.EndMessage(start, xdgToplevelID, 3)

	// Commit initial state to trigger configure
	start = w.BeginMessage(surfaceID, 6) // wl_surface.commit
	_ = w.EndMessage(start, surfaceID, 6)

	fmt.Printf("[+] Sending setup requests (%d bytes)...\n", len(w.Bytes()))
	if err := unix.Sendmsg(rawFD, w.Bytes(), nil, nil, 0); err != nil {
		fmt.Fprintf(os.Stderr, "[-] Sendmsg failed: %v\n", err)
		os.Exit(1)
	}

	// Wait for xdg_surface.configure
	var confSerial uint32
	haveConfig := false

	for !haveConfig {
		_, _ = unix.Poll(pfd, 1000)
		n, _, _, _, err := unix.Recvmsg(rawFD, inBuf[:], oobBuf[:], 0)
		if err != nil || n == 0 {
			fmt.Fprintf(os.Stderr, "[-] Recvmsg failed: %v\n", err)
			os.Exit(1)
		}
		r.Init(inBuf[:n], nil)
		for r.Remaining() >= wire.HeaderSize {
			hdr, err := r.ReadHeader()
			if err != nil {
				break
			}
			fmt.Printf("[SWAY EVENT] obj=%d op=%d size=%d\n", hdr.ObjectID, hdr.Opcode, hdr.Size)
			if hdr.ObjectID == displayID && hdr.Opcode == 0 { // wl_display.error
				objID, _ := r.ReadUint32()
				code, _ := r.ReadUint32()
				msg, _ := r.ReadString()
				fmt.Fprintf(os.Stderr, "[-] Sway Protocol Error: obj=%d code=%d msg=%s\n", objID, code, msg)
				os.Exit(1)
			} else if hdr.ObjectID == xdgWmBaseID && hdr.Opcode == 0 { // ping
				serial, _ := r.ReadUint32()
				w.Reset()
				start := w.BeginMessage(xdgWmBaseID, 3) // pong
				_ = w.WriteUint32(serial)
				_ = w.EndMessage(start, xdgWmBaseID, 3)
				_ = unix.Sendmsg(rawFD, w.Bytes(), nil, nil, 0)
			} else if hdr.ObjectID == xdgSurfaceID && hdr.Opcode == 0 { // xdg_surface.configure
				confSerial, _ = r.ReadUint32()
				fmt.Printf("[+] Received xdg_surface.configure serial=%d\n", confSerial)
				haveConfig = true
			} else {
				payloadLen := int(hdr.Size) - wire.HeaderSize
				for i := 0; i < payloadLen; i += 4 {
					_, _ = r.ReadUint32()
				}
			}
		}
	}

	// Prepare buffer frame: 960x640 ARGB8888
	width := int32(960)
	height := int32(640)
	stride := width * 4
	poolSize := int(stride * height)

	memFD, err := unix.MemfdCreate("bloxwm-nested-frame", 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] MemfdCreate failed: %v\n", err)
		os.Exit(1)
	}
	defer unix.Close(memFD)
	_ = unix.Ftruncate(memFD, int64(poolSize))

	pixelData, err := unix.Mmap(memFD, 0, poolSize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] Mmap failed: %v\n", err)
		os.Exit(1)
	}
	defer unix.Munmap(pixelData)

	// Render Data-Oriented "Blox" layout mockup:
	// Dark theme with tiled blocks and colored borders
	for y := int32(0); y < height; y++ {
		for x := int32(0); x < width; x++ {
			offset := int((y*width + x) * 4)
			// Dark charcoal background
			r, g, b := byte(0x18), byte(0x18), byte(0x25)

			// Block 1 (Left Column): x in [20, 460], y in [20, 620]
			if x >= 20 && x <= 460 && y >= 20 && y <= 620 {
				r, g, b = 0x24, 0x27, 0x3a
				// Cyan border
				if x <= 22 || x >= 458 || y <= 22 || y >= 618 {
					r, g, b = 0x89, 0xb4, 0xfa
				}
				// Titlebar
				if y <= 55 {
					r, g, b = 0x36, 0x3a, 0x4f
				}
			}

			// Block 2 (Right Top): x in [480, 940], y in [20, 310]
			if x >= 480 && x <= 940 && y >= 20 && y <= 310 {
				r, g, b = 0x24, 0x27, 0x3a
				// Green border
				if x <= 482 || x >= 938 || y <= 22 || y >= 308 {
					r, g, b = 0xa6, 0xe3, 0xa1
				}
				if y <= 55 {
					r, g, b = 0x36, 0x3a, 0x4f
				}
			}

			// Block 3 (Right Bottom): x in [480, 940], y in [330, 620]
			if x >= 480 && x <= 940 && y >= 330 && y <= 620 {
				r, g, b = 0x24, 0x27, 0x3a
				// Pink border
				if x <= 482 || x >= 938 || y <= 332 || y >= 618 {
					r, g, b = 0xf3, 0x8b, 0xa8
				}
				if y <= 365 {
					r, g, b = 0x36, 0x3a, 0x4f
				}
			}

			// Wayland ARGB8888 Little-Endian: [B, G, R, A]
			pixelData[offset+0] = b
			pixelData[offset+1] = g
			pixelData[offset+2] = r
			pixelData[offset+3] = 0xFF
		}
	}

	w.Reset()

	// Ack configure
	start = w.BeginMessage(xdgSurfaceID, 4) // xdg_surface.ack_configure
	_ = w.WriteUint32(confSerial)
	_ = w.EndMessage(start, xdgSurfaceID, 4)

	// 10. Create shm pool (id = 10)
	poolID := allocID() // 10
	start = w.BeginMessage(shmID, 0) // wl_shm.create_pool
	_ = w.WriteUint32(poolID)
	_ = w.WriteInt32(int32(poolSize))
	_ = w.EndMessage(start, shmID, 0)

	// 11. Create buffer (id = 11)
	bufferID := allocID() // 11
	start = w.BeginMessage(poolID, 0) // wl_shm_pool.create_buffer
	_ = w.WriteUint32(bufferID)
	_ = w.WriteInt32(0) // offset
	_ = w.WriteInt32(width)
	_ = w.WriteInt32(height)
	_ = w.WriteInt32(stride)
	_ = w.WriteUint32(uint32(wayland.WlShmFormatArgb8888))
	_ = w.EndMessage(start, poolID, 0)

	// Attach buffer to surface
	start = w.BeginMessage(surfaceID, 1) // wl_surface.attach
	_ = w.WriteUint32(bufferID)
	_ = w.WriteInt32(0)
	_ = w.WriteInt32(0)
	_ = w.EndMessage(start, surfaceID, 1)

	// Damage surface
	start = w.BeginMessage(surfaceID, 2) // wl_surface.damage
	_ = w.WriteInt32(0)
	_ = w.WriteInt32(0)
	_ = w.WriteInt32(width)
	_ = w.WriteInt32(height)
	_ = w.EndMessage(start, surfaceID, 2)

	// Commit surface
	start = w.BeginMessage(surfaceID, 6) // wl_surface.commit
	_ = w.EndMessage(start, surfaceID, 6)

	// Send buffer creation and commit with memFD
	oob := unix.UnixRights(memFD)
	if err := unix.Sendmsg(rawFD, w.Bytes(), oob, nil, 0); err != nil {
		fmt.Fprintf(os.Stderr, "[-] Sendmsg with FD failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=================================================================")
	fmt.Println("  SUCCESS! BLOXWM NESTED WINDOW IS LIVE ON YOUR SWAY DESKTOP!   ")
	fmt.Println("  Look at your screen: A 960x640 window titled                   ")
	fmt.Println("  'bloxwm nested compositor spike' is showing our Blox layout!    ")
	fmt.Println("=================================================================")
	fmt.Println("[*] Running event loop (responds to pings). Press Ctrl+C when done.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		for {
			n, _, _, _, err := unix.Recvmsg(rawFD, inBuf[:], oobBuf[:], 0)
			if err != nil || n == 0 {
				return
			}
			r.Init(inBuf[:n], nil)
			for r.Remaining() >= wire.HeaderSize {
				hdr, err := r.ReadHeader()
				if err != nil {
					break
				}
				if hdr.ObjectID == xdgWmBaseID && hdr.Opcode == 0 { // ping
					serial, _ := r.ReadUint32()
					w.Reset()
					start := w.BeginMessage(xdgWmBaseID, 3) // pong
					_ = w.WriteUint32(serial)
					_ = w.EndMessage(start, xdgWmBaseID, 3)
					_ = unix.Sendmsg(rawFD, w.Bytes(), nil, nil, 0)
				} else {
					payloadLen := int(hdr.Size) - wire.HeaderSize
					for i := 0; i < payloadLen; i += 4 {
						_, _ = r.ReadUint32()
					}
				}
			}
		}
	}()

	<-sigCh
	fmt.Println("[*] Window closed cleanly.")
}
