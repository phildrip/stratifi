package main

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"bloxwm/internal/backend/nested"
	"bloxwm/internal/input"
	"bloxwm/internal/layout"
	"bloxwm/internal/render"
	"bloxwm/internal/server"
	"golang.org/x/sys/unix"
	"time"
)

type WindowMapping struct {
	WinID        layout.WindowID
	ClientID     uint32
	SurfaceID    uint32
	XdgSurfaceID uint32
	ToplevelID   uint32
	Title        string
	AppID        string
	Rect         layout.Rect
}

func main() {
	fmt.Println("=================================================================")
	fmt.Println("                     Starting bloxwm                             ")
	fmt.Println("       Data-Oriented Wayland Window Manager & Compositor        ")
	fmt.Println("=================================================================")

	outputW := int32(1280)
	outputH := int32(720)

	var mu sync.Mutex
	windows := make(map[layout.WindowID]*WindowMapping)
	surfaceToWin := make(map[uint32]layout.WindowID)
	var activeWin layout.WindowID

	// 1. Initialize DOD Layout Engine
	engine := layout.NewEngine(layout.LayoutConfig{
		Gap:            8,
		BorderWidth:    3,
		TitlebarHeight: 28,
	})

	outputBounds := layout.Rect{
		X: 12,
		Y: 12,
		W: outputW - 24,
		H: outputH - 24,
	}

	var backend *nested.Backend
	var srv *server.Server

	redrawLocked := func() {
		if backend == nil {
			return
		}

		pixels := backend.Pixels()
		stride := backend.Stride()

		// Background: Charcoal 0xFF181825
		render.Clear(pixels, 0xFF181825)

		// Draw each window
		for _, m := range windows {
			client := srv.GetClient(m.ClientID)
			if client == nil {
				continue
			}

			rect := m.Rect
			borderCol := uint32(0xFF45475A) // Dim gray border for inactive
			if m.WinID == activeWin {
				borderCol = uint32(0xFF89B4FA) // Cyan/Blue border for active
			}

			// 1. Draw window container background
			render.FillRect(pixels, stride, rect.X, rect.Y, rect.W, rect.H, outputW, outputH, 0xFF1E1E2E)

			// 2. Draw border
			render.DrawBorder(pixels, stride, rect.X, rect.Y, rect.W, rect.H, engine.Config.BorderWidth, outputW, outputH, borderCol)

			// 3. Draw titlebar
			titlebarH := engine.Config.TitlebarHeight
			titlebarCol := uint32(0xFF313244)
			if m.WinID == activeWin {
				titlebarCol = uint32(0xFF45475A)
			}
			render.FillRect(pixels, stride, rect.X+engine.Config.BorderWidth, rect.Y+engine.Config.BorderWidth, rect.W-2*engine.Config.BorderWidth, titlebarH, outputW, outputH, titlebarCol)

			// 4. Blit client pixels beneath titlebar
			bodyX := rect.X + engine.Config.BorderWidth
			bodyY := rect.Y + titlebarH + engine.Config.BorderWidth
			bodyW := rect.W - 2*engine.Config.BorderWidth
			bodyH := rect.H - titlebarH - 2*engine.Config.BorderWidth

			// Retrieve surface buffer
			cBuf, cW, cH, cStride, ok := client.GetSurfaceBuffer(m.SurfaceID)
			if ok && len(cBuf) > 0 {
				fmt.Printf("[+] Blitting client surface: %dx%d (stride=%d) to (%d, %d)\n", cW, cH, cStride, bodyX, bodyY)
				render.Blit(pixels, stride, bodyX, bodyY, outputW, outputH, cBuf, cStride, cW, cH)
			} else {
				// Empty placeholder if not yet committed
				render.FillRect(pixels, stride, bodyX, bodyY, bodyW, bodyH, outputW, outputH, 0xFF11111B)
			}
		}

		backend.Damage(0, 0, outputW, outputH)
		_ = backend.Present()
	}

	redrawAll := func() {
		mu.Lock()
		redrawLocked()
		mu.Unlock()
	}

	// 2. Initialize zero-allocation Input RingBuffer and Router
	ringBuf := input.NewRingBuffer(2048)
	var router *input.Router

	router = input.NewRouter(
		func(x, y float64) (input.WindowInfo, bool) {
			mu.Lock()
			defer mu.Unlock()
			hit := engine.HitTest(layout.Point{X: int32(x), Y: int32(y)})
			if hit.Type != layout.HitNone {
				if m, ok := windows[hit.WindowID]; ok {
					return input.WindowInfo{
						WinID:     m.WinID,
						ClientID:  m.ClientID,
						SurfaceID: m.SurfaceID,
						Rect:      m.Rect,
					}, true
				}
			}
			return input.WindowInfo{}, false
		},
		func(clientID uint32) *server.Client {
			if srv == nil {
				return nil
			}
			return srv.GetClient(clientID)
		},
		func(oldWin, newWin layout.WindowID) {
			mu.Lock()
			activeWin = newWin
			mu.Unlock()
			redrawAll()
		},
	)

	// 3. Initialize Nested Backend connecting to parent Sway
	cb := nested.Callbacks{
		OnPointerMotion: func(x, y float64) {
			ringBuf.Push(input.InputEvent{
				Type:     input.EventPointerMotionAbsolute,
				TimeUsec: uint64(time.Now().UnixMicro()),
				X:        x,
				Y:        y,
			})
		},
		OnPointerButton: func(button, state uint32) {
			ringBuf.Push(input.InputEvent{
				Type:     input.EventPointerButton,
				TimeUsec: uint64(time.Now().UnixMicro()),
				Code:     button,
				State:    state,
			})
		},
		OnKey: func(key, state uint32) {
			ringBuf.Push(input.InputEvent{
				Type:     input.EventKey,
				TimeUsec: uint64(time.Now().UnixMicro()),
				Code:     key,
				State:    state,
			})
		},
		OnModifiers: func(depressed, latched, locked, group uint32) {
			router.HandleKeyboardModifiers(depressed, latched, locked, group)
		},
		OnClose: func() {
			fmt.Println("[*] Parent window closed. Exiting.")
			os.Exit(0)
		},
	}

	var err error
	backend, err = nested.NewBackend(outputW, outputH, cb)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] Failed to initialize nested backend: %v\n", err)
		os.Exit(1)
	}
	defer backend.Close()

	go backend.RunEventLoop()

	// 4. Start high-throughput Input Consumer Loop
	go func() {
		var batch [32]input.InputEvent
		for {
			n := ringBuf.Drain(batch[:])
			if n == 0 {
				time.Sleep(1 * time.Millisecond)
				continue
			}
			for i := 0; i < n; i++ {
				router.ProcessEvent(batch[i])
			}
		}
	}()

	// Initial screen paint
	redrawAll()

	// 5. Initialize bloxwm Wayland Server
	socketName := "wayland-blox-0"
	srv, err = server.NewServer(socketName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] Failed to start bloxwm server: %v\n", err)
		os.Exit(1)
	}
	defer srv.Close()

	if kmFD, kmSize, err := server.DefaultXKBKeymap(); err == nil {
		srv.SetKeymap(kmFD, kmSize)
		defer unix.Close(kmFD)
	}

	// Handle new toplevel window
	srv.SetOnToplevel(func(c *server.Client, toplevel *server.XdgToplevel, xdgSurfaceID uint32) {
		mu.Lock()
		defer mu.Unlock()

		winID := engine.SlotMap.Allocate()
		_ = engine.AddWindow(winID)

		winIDs, rects := engine.Compute(outputBounds)

		// Update all window rects
		for i, id := range winIDs {
			if m, ok := windows[id]; ok {
				m.Rect = rects[i]
				targetClient := srv.GetClient(m.ClientID)
				if targetClient != nil {
					bodyW := m.Rect.W - 2*engine.Config.BorderWidth
					bodyH := m.Rect.H - engine.Config.TitlebarHeight - 2*engine.Config.BorderWidth
					targetClient.ConfigureToplevel(m.XdgSurfaceID, m.ToplevelID, bodyW, bodyH, nil)
				}
			}
		}

		// Find rect for this new window
		var assignedRect layout.Rect
		for i, id := range winIDs {
			if id == winID {
				assignedRect = rects[i]
				break
			}
		}

		surfaceID := c.GetSurfaceIDForXdgSurface(xdgSurfaceID)
		mapping := &WindowMapping{
			WinID:        winID,
			ClientID:     c.ID(),
			SurfaceID:    surfaceID,
			XdgSurfaceID: xdgSurfaceID,
			ToplevelID:   toplevel.ID,
			Title:        toplevel.Title,
			AppID:        toplevel.AppID,
			Rect:         assignedRect,
		}
		windows[winID] = mapping
		surfaceToWin[surfaceID] = winID
		surfaceToWin[xdgSurfaceID] = winID
		activeWin = winID

		bodyW := assignedRect.W - 2*engine.Config.BorderWidth
		bodyH := assignedRect.H - engine.Config.TitlebarHeight - 2*engine.Config.BorderWidth
		c.ConfigureToplevel(xdgSurfaceID, toplevel.ID, bodyW, bodyH, nil)
		fmt.Printf("[+] New window registered: WinID=%d Title=%q AppID=%q (size: %dx%d)\n", winID, toplevel.Title, toplevel.AppID, bodyW, bodyH)
	})

	// Handle surface commit
	srv.SetOnCommit(func(s *server.Surface) {
		mu.Lock()
		if winID, ok := surfaceToWin[s.ID]; ok {
			if m, exists := windows[winID]; exists {
				m.SurfaceID = s.ID
			}
		}
		redrawLocked()
		mu.Unlock()
	})

	// Handle client disconnect
	srv.SetOnClientDestroy(func(clientID uint32) {
		mu.Lock()
		defer mu.Unlock()

		for winID, m := range windows {
			if m.ClientID == clientID {
				delete(windows, winID)
				delete(surfaceToWin, m.SurfaceID)
				delete(surfaceToWin, m.XdgSurfaceID)
				_ = engine.RemoveWindow(winID)
				if activeWin == winID {
					activeWin = 0
				}
				fmt.Printf("[-] Window closed: WinID=%d ClientID=%d\n", winID, clientID)
			}
		}

		// Recompute layout for remaining windows
		winIDs, rects := engine.Compute(outputBounds)
		for i, id := range winIDs {
			if m, ok := windows[id]; ok {
				m.Rect = rects[i]
				targetClient := srv.GetClient(m.ClientID)
				if targetClient != nil {
					bodyW := m.Rect.W - 2*engine.Config.BorderWidth
					bodyH := m.Rect.H - engine.Config.TitlebarHeight - 2*engine.Config.BorderWidth
					targetClient.ConfigureToplevel(m.XdgSurfaceID, m.ToplevelID, bodyW, bodyH, nil)
				}
			}
		}
		redrawLocked()
	})

	fmt.Printf("[+] bloxwm is active on socket: %s\n", srv.SocketPath())
	fmt.Printf("[+] Run clients with: WAYLAND_DISPLAY=%s foot\n", socketName)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := srv.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "[-] Server run error: %v\n", err)
		}
	}()

	<-sigCh
	fmt.Println("[*] Shutting down bloxwm...")
}
