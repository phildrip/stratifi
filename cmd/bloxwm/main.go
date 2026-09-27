package main

import (
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"bloxwm/internal/backend/nested"
	"bloxwm/internal/compositor"
	"bloxwm/internal/input"
	"bloxwm/internal/layout"
	"bloxwm/internal/server"
	"golang.org/x/sys/unix"
)

// WindowMapping ties a layout slot to its client's Wayland objects.
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

// objRef scopes object lookups: Wayland object ids are per-client — two
// clients both have surface id 3, so bare-id keys silently collide.
type objRef struct {
	clientID uint32
	objID    uint32
}

// evKind enumerates the shell's event queue payload kinds (plain data;
// the owner goroutine applies them sequentially — single-owner state).
type evKind uint8

const (
	evWindowAdded evKind = iota
	evClientGone
	evCommitted
	evFocusMoved
	evQuit
)

// shellEvent is one message to the owner goroutine.
type shellEvent struct {
	kind         evKind
	clientID     uint32
	surfaceID    uint32
	xdgSurfaceID uint32
	toplevelID   uint32
	winID        layout.WindowID
	title        string
	appID        string
}

func main() {
	fmt.Println("=================================================================")
	fmt.Println("                     Starting bloxwm                             ")
	fmt.Println("       Data-Oriented Wayland Window Manager & Compositor        ")
	fmt.Println("=================================================================")

	outputW := int32(1280)
	outputH := int32(720)

	// OWNER-MODEL SHELL: no shared mutex. All scene state below is owned
	// by the actor goroutine in runActor; server callbacks and the input
	// pump communicate through the events channel (plain-data sends).
	events := make(chan shellEvent, 128)

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

	// 2. Router: reads a lock-free rect snapshot (atomic.Pointer) instead
	// of touching owner state directly; focus moves queue through its own
	// channel.
	var sceneInfo atomic.Pointer[[]input.WindowInfo]
	var srv *server.Server
	var frameLoop *compositor.FrameLoop

	ringBuf := input.NewRingBuffer(2048)
	var router *input.Router

	router = input.NewRouter(
		func(x, y float64) (input.WindowInfo, bool) {
			if list := sceneInfo.Load(); list != nil {
				for _, info := range *list {
					r := info.Rect
					if x >= float64(r.X) && x < float64(r.X+r.W) &&
						y >= float64(r.Y) && y < float64(r.Y+r.H) {
						return info, true
					}
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
			select {
			case events <- shellEvent{kind: evFocusMoved, winID: newWin}:
			default:
			}
		},
	)

	// 3. Initialize Nested Backend connecting to parent Sway
	exitCh := make(chan struct{}, 1)
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
			// Modifiers route through the router's own state; the input
			// consumer goroutine applies them.
			go router.HandleKeyboardModifiers(depressed, latched, locked, group)
		},
		OnClose: func() {
			// Graceful: signal the actor so deferred cleanup runs.
			fmt.Println("[*] Parent window closed. Shutting down.")
			select {
			case exitCh <- struct{}{}:
			default:
			}
		},
	}

	var backend *nested.Backend
	var err error
	backend, err = nested.NewBackend(outputW, outputH, cb)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] Failed to initialize nested backend: %v\n", err)
		os.Exit(1)
	}
	defer backend.Close()

	frameLoop = compositor.NewFrameLoop(backend, nil, nil)
	frameLoop.SetConfig(compositor.FrameConfig{
		BorderWidth:    engine.Config.BorderWidth,
		TitlebarHeight: engine.Config.TitlebarHeight,
	})
	if os.Getenv("BLOXWM_GPU") == "1" {
		if gpuExec, err := compositor.NewVulkanExecutor(outputW, outputH); err == nil {
			// GPU composites each frame; BridgeTo copies into the parent
			// shm surface after every render (KMS scanout removes this).
			frameLoop.SetExecutor(compositor.BridgeExecutor{Inner: gpuExec, Out: backend})
			defer gpuExec.Close()
			fmt.Println("[+] GPU compositing enabled (Vulkan executor)")
		} else {
			fmt.Printf("[-] GPU executor unavailable (%v); using shm blitter\n", err)
		}
	}

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

	// Server callbacks: channel sends only — the actor applies them.
	srv.SetOnToplevel(func(c *server.Client, toplevel *server.XdgToplevel, xdgSurfaceID uint32) {
		events <- shellEvent{
			kind:         evWindowAdded,
			clientID:     c.ID(),
			surfaceID:    c.GetSurfaceIDForXdgSurface(xdgSurfaceID),
			xdgSurfaceID: xdgSurfaceID,
			toplevelID:   toplevel.ID,
			title:        toplevel.Title,
			appID:        toplevel.AppID,
		}
	})

	srv.SetOnCommit(func(s *server.Surface) {
		select {
		case events <- shellEvent{kind: evCommitted, clientID: s.ClientID, surfaceID: s.ID}:
		default:
			// Commits coalesce: a newer one supersedes the dropped.
		}
	})

	srv.SetOnClientDestroy(func(clientID uint32) {
		events <- shellEvent{kind: evClientGone, clientID: clientID}
	})

	fmt.Printf("[+] bloxwm is active on socket: %s\n", srv.SocketPath())
	fmt.Printf("[+] Run clients with: WAYLAND_DISPLAY=%s foot\n", socketName)

	// ACTOR GOROUTINE: owns all scene state (windows, surfaceToWin,
	// activeWin) and renders. No other goroutine touches these.
	var (
		windows      = make(map[layout.WindowID]*WindowMapping)
		surfaceToWin = make(map[objRef]layout.WindowID)
		activeWin    layout.WindowID
	)

	renderTick := make(chan struct{}, 1) // coalescing render requests

	// focusActive points keyboard focus at the active window. The router
	// owns focus state, so this queues a request (applied on the input
	// consumer goroutine). Called when a window appears and again once its
	// surface is mapped, so the client has a keyboard to receive events on.
	focusActive := func() {
		m, ok := windows[activeWin]
		if !ok || m.SurfaceID == 0 {
			return
		}
		router.RequestFocus(activeWin, m.ClientID, m.SurfaceID)
	}

	// publishScene pushes a lock-free WindowInfo list for the router's
	// hit-testing (atomic.Pointer swap: readers never block).
	publishScene := func() {
		list := make([]input.WindowInfo, 0, len(windows))
		for _, m := range windows {
			list = append(list, input.WindowInfo{
				WinID:     m.WinID,
				ClientID:  m.ClientID,
				SurfaceID: m.SurfaceID,
				Rect:      m.Rect,
			})
		}
		sceneInfo.Store(&list)
	}

	// The frame loop renders from closures that run ON the actor
	// goroutine — single-owner discipline, no locks needed.
	frameLoop.SetScene(func(dst []compositor.SceneWindow) []compositor.SceneWindow {
		for _, m := range windows {
			dst = append(dst, compositor.SceneWindow{
				ClientID:  m.ClientID,
				SurfaceID: m.SurfaceID,
				Rect: compositor.Rect{
					X: m.Rect.X, Y: m.Rect.Y, W: m.Rect.W, H: m.Rect.H,
				},
				Active: m.WinID == activeWin,
			})
		}
		return dst
	})
	frameLoop.SetSnapshot(func(clientID, surfaceID uint32, dst []byte) (int, int32, int32, int32, bool) {
		client := srv.GetClient(clientID)
		if client == nil {
			return 0, 0, 0, 0, false
		}
		return client.SnapshotSurfaceBuffer(surfaceID, dst)
	})
	frameLoop.SetInfo(func(clientID, surfaceID uint32) (int32, int32, int32, bool) {
		client := srv.GetClient(clientID)
		if client == nil {
			return 0, 0, 0, false
		}
		return client.SurfaceBufferInfo(surfaceID)
	})

	// Server run loop.
	go func() {
		if err := srv.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "[-] Server run error: %v\n", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// The actor: drains events (state mutations), renders on request.
	applyEvent := func(ev shellEvent) {
		switch ev.kind {
		case evWindowAdded:
			winID := engine.SlotMap.Allocate()
			_ = engine.AddWindow(winID)

			winIDs, rects := engine.Compute(outputBounds)
			for i, id := range winIDs {
				if m, ok := windows[id]; ok {
					m.Rect = rects[i]
					if targetClient := srv.GetClient(m.ClientID); targetClient != nil {
						bodyW := m.Rect.W - 2*engine.Config.BorderWidth
						bodyH := m.Rect.H - engine.Config.TitlebarHeight - 2*engine.Config.BorderWidth
						targetClient.ConfigureToplevel(m.XdgSurfaceID, m.ToplevelID, bodyW, bodyH, nil)
					}
				}
			}

			surfaceID := srv.GetClient(ev.clientID).GetSurfaceIDForXdgSurface(ev.xdgSurfaceID)
			assignedRect := layout.Rect{}
			for i, id := range winIDs {
				if id == winID {
					assignedRect = rects[i]
					break
				}
			}
			mapping := &WindowMapping{
				WinID:        winID,
				ClientID:     ev.clientID,
				SurfaceID:    surfaceID,
				XdgSurfaceID: ev.xdgSurfaceID,
				ToplevelID:   ev.toplevelID,
				Title:        ev.title,
				AppID:        ev.appID,
				Rect:         assignedRect,
			}
			windows[winID] = mapping
			surfaceToWin[objRef{ev.clientID, surfaceID}] = winID
			surfaceToWin[objRef{ev.clientID, ev.xdgSurfaceID}] = winID
			activeWin = winID

			bodyW := assignedRect.W - 2*engine.Config.BorderWidth
			bodyH := assignedRect.H - engine.Config.TitlebarHeight - 2*engine.Config.BorderWidth
			if targetClient := srv.GetClient(ev.clientID); targetClient != nil {
				targetClient.ConfigureToplevel(ev.xdgSurfaceID, ev.toplevelID, bodyW, bodyH, nil)
			}
			fmt.Printf("[+] New window registered: WinID=%d Title=%q AppID=%q (size: %dx%d)\n",
				winID, ev.title, ev.appID, bodyW, bodyH)
			focusActive()

		case evCommitted:
			if winID, ok := surfaceToWin[objRef{ev.clientID, ev.surfaceID}]; ok {
				if m, exists := windows[winID]; exists {
					m.SurfaceID = ev.surfaceID
					// Re-assert focus once the surface is mapped: the
					// initial focus request may precede the client's
					// wl_keyboard, and SetFocus is a no-op if unchanged.
					if winID == activeWin {
						focusActive()
					}
				}
			}

		case evClientGone:
			for winID, m := range windows {
				if m.ClientID == ev.clientID {
					delete(windows, winID)
					delete(surfaceToWin, objRef{m.ClientID, m.SurfaceID})
					delete(surfaceToWin, objRef{m.ClientID, m.XdgSurfaceID})
					_ = engine.RemoveWindow(winID)
					if activeWin == winID {
						activeWin = 0
					}
					fmt.Printf("[-] Window closed: WinID=%d ClientID=%d\n", winID, ev.clientID)
				}
			}

			// Recompute layout for the survivors and reconfigure them.
			winIDs, rects := engine.Compute(outputBounds)
			for i, id := range winIDs {
				if m, ok := windows[id]; ok {
					m.Rect = rects[i]
					if targetClient := srv.GetClient(m.ClientID); targetClient != nil {
						bodyW := m.Rect.W - 2*engine.Config.BorderWidth
						bodyH := m.Rect.H - engine.Config.TitlebarHeight - 2*engine.Config.BorderWidth
						targetClient.ConfigureToplevel(m.XdgSurfaceID, m.ToplevelID, bodyW, bodyH, nil)
					}
				}
			}

			// Focus falls to the first remaining window: a plain channel
			// command the router applies on its own loop — no cross-
			// goroutine method calls, no lock-order contracts.
			if len(winIDs) > 0 {
				if m, ok := windows[winIDs[0]]; ok {
					activeWin = winIDs[0]
					router.RequestFocus(winIDs[0], m.ClientID, m.SurfaceID)
				}
			}

			publishScene()
		}
	}

	publishScene()

	for {
		select {
		case ev := <-events:
			applyEvent(ev)
			// State changed: coalesce a render.
			select {
			case renderTick <- struct{}{}:
			default:
			}

		case <-renderTick:
			frameLoop.RenderOnce() // owner goroutine: single-owner discipline

		case <-exitCh:
			fmt.Println("[*] Shutting down bloxwm...")
			return

		case <-sigCh:
			fmt.Println("[*] Shutting down bloxwm...")
			return
		}
	}
}
