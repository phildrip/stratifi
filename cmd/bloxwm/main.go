package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"bloxwm/internal/backend/nested"
	"bloxwm/internal/compositor"
	"bloxwm/internal/config"
	"bloxwm/internal/cursor"
	"bloxwm/internal/input"
	"bloxwm/internal/layout"
	"bloxwm/internal/server"
	"bloxwm/internal/session"
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
	evCloseActive
	evFocusNext
	evFocusPrev
	evPopupAdded
	evSubsurfaceAdded
	evSubsurfaceGone
	evCursorSet
	evFullscreen
	evFocusWindowUp
	evFocusWindowDown
	evMoveWindowUp
	evMoveWindowDown
	evMoveColLeft
	evMoveColRight
	evWorkspaceSwitch
	evWorkspaceMove
	evColumnNext
	evColumnPrev
	evWorkspaceNext
	evWorkspacePrev
)

// shellEvent is one message to the owner goroutine.
type shellEvent struct {
	kind            evKind
	popupID         uint32
	px, py, pw, ph  int32
	ssOrder         int32
	cursorOn        bool
	wsIndex         int
	clientID        uint32
	surfaceID       uint32
	parentSurfaceID uint32
	xdgSurfaceID    uint32
	toplevelID      uint32
	winID           layout.WindowID
	title           string
	appID           string
}

func main() {
	fmt.Println("=================================================================")
	fmt.Println("                     Starting bloxwm                             ")
	fmt.Println("       Data-Oriented Wayland Window Manager & Compositor        ")
	fmt.Println("=================================================================")

	outputW := int32(1280)
	outputH := int32(720)

	// The Wayland socket we serve; spawned clients are pointed at it.
	const socketName = "wayland-blox-0"

	// Bare-metal output: BLOXWM_KMS=1 opens the DRM card directly instead of
	// nesting in a parent compositor. Requires DRM master (run from a VT).
	// Output dimensions then come from the connector's preferred mode, and
	// input arrives from evdev rather than parent Wayland events.
	useKMS := os.Getenv("BLOXWM_KMS") == "1"
	var kmsDisp *kmsDisplay
	if useKMS {
		var err error
		kmsDisp, err = setupKMSDisplay()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[-] %v\n", err)
			os.Exit(1)
		}
		defer kmsDisp.Close()
		outputW = kmsDisp.Width()
		outputH = kmsDisp.Height()
	}

	// OWNER-MODEL SHELL: no shared mutex. All scene state below is owned
	// by the actor goroutine in runActor; server callbacks and the input
	// pump communicate through the events channel (plain-data sends).
	events := make(chan shellEvent, 128)

	// 1. Initialize DOD Layout Engine (metrics from config; hard
	// defaults apply when no config file exists).
	cfgPath := config.FindConfigPath()
	cfg, err := config.LoadFile(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] Bad config %s: %v\n", cfgPath, err)
		os.Exit(1)
	}
	if cfgPath != "" {
		fmt.Printf("[+] Config: %s (%d bindings)\n", cfgPath, len(cfg.Bindings))
	}
	workspaces := layout.NewWorkspaces(layout.LayoutConfig{
		Gap:            cfg.Gap,
		BorderWidth:    cfg.BorderWidth,
		TitlebarHeight: cfg.TitlebarHeight,
		ColumnWidth:    cfg.ColumnWidth,
	})
	engine := workspaces.Engine()

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

	// Keybindings from config. A bad binding exits loudly: a typo'd combo
	// that silently does nothing is the failure this strictness prevents.
	// Duplicate combos and unknown actions are rejected here, not at fire
	// time, for the same reason.
	var bindings []input.Binding
	seenCombo := make(map[[2]uint32]bool)
	for _, b := range cfg.Bindings {
		pb, err := input.ParseBinding(b.Combo, b.Action, b.Args)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[-] Bad binding: %v\n", err)
			os.Exit(1)
		}
		key := [2]uint32{pb.Mods, pb.Code}
		if seenCombo[key] {
			fmt.Fprintf(os.Stderr, "[-] Duplicate binding for combo %q\n", b.Combo)
			os.Exit(1)
		}
		seenCombo[key] = true
		switch pb.Action {
		case "close", "quit", "spawn", "focus_next", "focus_prev",
			"workspace", "move_to_workspace", "focus_col_next", "focus_col_prev",
			"workspace_next", "workspace_prev", "column_next", "column_prev", "overview",
			"fullscreen", "focus_window_up", "focus_window_down",
			"move_window_up", "move_window_down", "move_col_left", "move_col_right":
		default:
			fmt.Fprintf(os.Stderr, "[-] Unknown action %q (combo %q)\n", pb.Action, b.Combo)
			os.Exit(1)
		}
		bindings = append(bindings, pb)
	}
	router.SetBindings(input.NewMatcher(bindings))
	handleAction := func(action string, args []string) {
		emit := func(ev shellEvent) {
			select {
			case events <- ev:
			default:
			}
		}
		switch action {
		case "close":
			emit(shellEvent{kind: evCloseActive})
		case "quit":
			select {
			case exitCh <- struct{}{}:
			default:
			}
		case "spawn":
			if len(args) == 0 {
				return
			}
			// When supervised, ask the session supervisor to spawn: the
			// child becomes ITS child, so a compositor restart does not
			// kill it. Otherwise exec directly (standalone behaviour).
			if sock := os.Getenv("BLOXWM_SESSION_SOCKET"); sock != "" {
				if _, err := session.SendSpawn(sock, args); err == nil {
					return
				}
				fmt.Fprintf(os.Stderr, "[-] session spawn failed; exec'ing directly\n")
			}
			cmd := exec.Command(args[0], args[1:]...)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			// Children are bloxwm clients, not the parent's: point them at
			// our socket explicitly. Replace (not append) WAYLAND_DISPLAY —
			// a duplicate leaves the old value winning.
			env := os.Environ()
			display := "WAYLAND_DISPLAY=" + socketName
			replaced := false
			for i, kv := range env {
				if len(kv) >= 16 && kv[:16] == "WAYLAND_DISPLAY=" {
					env[i] = display
					replaced = true
				}
			}
			if !replaced {
				env = append(env, display)
			}
			cmd.Env = env
			if err := cmd.Start(); err == nil {
				_ = cmd.Process.Release() // fully detach: no zombies, survives us
			}
		case "focus_next":
			emit(shellEvent{kind: evFocusNext})
		case "focus_prev":
			emit(shellEvent{kind: evFocusPrev})
		case "focus_col_next":
			emit(shellEvent{kind: evColumnNext})
		case "focus_col_prev":
			emit(shellEvent{kind: evColumnPrev})
		case "workspace_next":
			emit(shellEvent{kind: evWorkspaceNext})
		case "workspace_prev":
			emit(shellEvent{kind: evWorkspacePrev})
		case "column_next":
			emit(shellEvent{kind: evColumnNext})
		case "column_prev":
			emit(shellEvent{kind: evColumnPrev})
		case "fullscreen":
			emit(shellEvent{kind: evFullscreen})
		case "focus_window_up":
			emit(shellEvent{kind: evFocusWindowUp})
		case "focus_window_down":
			emit(shellEvent{kind: evFocusWindowDown})
		case "move_window_up":
			emit(shellEvent{kind: evMoveWindowUp})
		case "move_window_down":
			emit(shellEvent{kind: evMoveWindowDown})
		case "move_col_left":
			emit(shellEvent{kind: evMoveColLeft})
		case "move_col_right":
			emit(shellEvent{kind: evMoveColRight})
		case "overview":
			// Overview is not implemented yet; recognized and ignored so a
			// bound gesture is not reported as an unknown action.
			_ = args
		case "workspace":
			if n, ok := parseWorkspaceArg(args); ok {
				emit(shellEvent{kind: evWorkspaceSwitch, wsIndex: n})
			}
		case "move_to_workspace":
			if n, ok := parseWorkspaceArg(args); ok {
				emit(shellEvent{kind: evWorkspaceMove, wsIndex: n})
			}
		default:
			fmt.Printf("[-] Unknown action %q\n", action)
		}
	}
	router.SetActionHandler(handleAction)

	// Gesture router: config-driven gesture bindings dispatch through the
	// same action handler as keybindings. Fed from evdev MT events in KMS
	// mode; nested mode has no touchpad source.
	var gestureRouter *input.GestureRouter
	{
		var gb []input.GestureBinding
		for _, g := range cfg.Gestures {
			b, err := input.ParseGestureBinding(g.Type, g.Direction, g.Fingers, g.Action)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[-] Bad gesture binding: %v\n", err)
				os.Exit(1)
			}
			gb = append(gb, b)
		}
		if len(gb) > 0 {
			gestureRouter = input.NewGestureRouter(gb, func(a input.GestureAction) {
				handleAction(string(a), nil)
			})
		}
	}

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
		OnDisconnect: func() {
			// The parent vanished (socket EOF/error) without a close
			// request. A nested compositor has nothing to present to, so
			// shut down rather than linger with a dead connection.
			fmt.Println("[-] Parent compositor disconnected. Shutting down.")
			select {
			case exitCh <- struct{}{}:
			default:
			}
		},
	}

	var backend *nested.Backend
	var output compositor.Output
	if useKMS {
		output = kmsDisp
	} else {
		var err error
		backend, err = nested.NewBackend(outputW, outputH, cb)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[-] Failed to initialize nested backend: %v\n", err)
			os.Exit(1)
		}
		defer backend.Close()
		output = backend
	}

	frameLoop = compositor.NewFrameLoop(output, nil, nil)
	frameLoop.SetConfig(compositor.FrameConfig{
		BorderWidth:    engine.Config.BorderWidth,
		TitlebarHeight: engine.Config.TitlebarHeight,
	})
	if os.Getenv("BLOXWM_GPU") == "1" {
		if useKMS {
			// GPU renders straight into scanout memory; flip presents it.
			// No bridge, no readback, no CPU pixels on this path.
			if fd, err := kmsDisp.PrimeFD(); err != nil {
				fmt.Fprintf(os.Stderr, "[-] KMS scanout export: %v\n", err)
				os.Exit(1)
			} else if gpuExec, err := compositor.NewVulkanExecutorOnDmaBuf(
				fd, uint32(outputW), uint32(outputH), uint32(kmsDisp.Stride()), outputW, outputH,
			); err != nil {
				fmt.Fprintf(os.Stderr, "[-] GPU scanout executor: %v\n", err)
				os.Exit(1)
			} else {
				frameLoop.SetExecutor(compositor.ScanoutExecutor{Inner: gpuExec, Flip: kmsDisp.Present})
				defer gpuExec.Close()
				fmt.Println("[+] GPU+KMS direct scanout enabled (Vulkan executor)")
			}
		} else if gpuExec, err := compositor.NewVulkanExecutor(outputW, outputH); err == nil {
			// GPU composites each frame; BridgeTo copies into the parent
			// shm surface after every render (KMS scanout removes this).
			frameLoop.SetExecutor(compositor.BridgeExecutor{Inner: gpuExec, Out: backend})
			defer gpuExec.Close()
			fmt.Println("[+] GPU compositing enabled (Vulkan executor)")
		} else {
			fmt.Printf("[-] GPU executor unavailable (%v); using shm blitter\n", err)
		}
	}

	if useKMS {
		// Bare metal: pump evdev directly; no parent compositor exists.
		if _, err := startEvdevInput(ringBuf, gestureRouter); err != nil {
			fmt.Fprintf(os.Stderr, "[-] %v\n", err)
			os.Exit(1)
		}
		fmt.Println("[+] evdev input enabled")
	} else {
		go backend.RunEventLoop()
	}

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

	srv.SetOnPopupMapped(func(clientID, popupID, surfaceID, parentXdgSurfaceID uint32, x, y, w, h int32) {
		events <- shellEvent{
			kind:         evPopupAdded,
			clientID:     clientID,
			surfaceID:    surfaceID,
			popupID:      popupID,
			xdgSurfaceID: parentXdgSurfaceID,
			px:           x,
			py:           y,
			pw:           w,
			ph:           h,
		}
	})

	srv.SetOnSubsurface(func(clientID, surfaceID, parentSurfaceID uint32, x, y, w, h, order int32) {
		events <- shellEvent{
			kind:            evSubsurfaceAdded,
			clientID:        clientID,
			surfaceID:       surfaceID,
			parentSurfaceID: parentSurfaceID,
			px:              x,
			py:              y,
			pw:              w,
			ph:              h,
			ssOrder:         order,
		}
	})

	srv.SetOnSubsurfaceGone(func(clientID, surfaceID uint32) {
		events <- shellEvent{kind: evSubsurfaceGone, clientID: clientID, surfaceID: surfaceID}
	})

	srv.SetOnCursor(func(clientID, surfaceID uint32, hx, hy, w, h int32, active bool) {
		events <- shellEvent{
			kind:      evCursorSet,
			clientID:  clientID,
			surfaceID: surfaceID,
			px:        hx,
			py:        hy,
			pw:        w,
			ph:        h,
			cursorOn:  active,
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
	renderTick := make(chan struct{}, 1) // coalescing render requests

	// The actor's scene state. It owns windows/surfaceToWin/activeWin;
	// client lookup and focus requests are injected so the shell is
	// testable without a live server (see shell_test.go).
	// Compositor-owned cursor. A reserved client id (0, which real clients
	// never get) routes the default cursor image through the SAME blit path
	// as client surfaces, so no renderer change is needed.
	const cursorClientID uint32 = 0
	const defaultCursorSurfaceID uint32 = 1
	defaultCursor, cursorErr := cursor.Load("left_ptr", cursor.DefaultSize())
	if cursorErr != nil {
		fmt.Printf("[!] default cursor unavailable (%v): pointer invisible over chrome\n", cursorErr)
	}

	shell := newShellState(
		workspaces, outputBounds, &sceneInfo,
		func(clientID uint32) shellClient {
			if srv == nil {
				return nil
			}
			if c := srv.GetClient(clientID); c != nil {
				return c
			}
			return nil
		},
		func(winID layout.WindowID, clientID, surfaceID, toplevelID uint32) {
			router.RequestFocus(winID, clientID, surfaceID, toplevelID)
		},
	)

	// The frame loop renders from closures that run ON the actor
	// goroutine — single-owner discipline, no locks needed.
	frameLoop.SetScene(func(dst []compositor.SceneWindow) []compositor.SceneWindow {
		fs := shell.fullscreenActive()
		for _, m := range shell.windows {
			if !shell.sceneVisible(m.WinID) {
				continue // hidden on another workspace, or behind fullscreen
			}
			if m.WinID == fs {
				// Fullscreen: borderless, filling the whole output.
				dst = append(dst, compositor.SceneWindow{
					ClientID:  m.ClientID,
					SurfaceID: m.SurfaceID,
					Rect: compositor.Rect{
						X: shell.output.X, Y: shell.output.Y,
						W: shell.output.W, H: shell.output.H,
					},
					Popup:  true,
					Active: true,
				})
				continue
			}
			dst = append(dst, compositor.SceneWindow{
				ClientID:  m.ClientID,
				SurfaceID: m.SurfaceID,
				Rect: compositor.Rect{
					X: m.Rect.X, Y: m.Rect.Y, W: m.Rect.W, H: m.Rect.H,
				},
				Active: m.WinID == shell.activeWin,
			})
		}
		// Subsurfaces render above their parent window and below popups,
		// borderless, at offsets from the parent's body origin.
		for _, ss := range shell.visibleSubsurfaces() {
			dst = append(dst, compositor.SceneWindow{
				ClientID:  ss.clientID,
				SurfaceID: ss.surfaceID,
				Rect: compositor.Rect{
					X: ss.rect.X, Y: ss.rect.Y, W: ss.rect.W, H: ss.rect.H,
				},
				Popup: true, // borderless overlay, same draw path as popups
			})
		}
		// Popups render above windows, borderless, at their placed rects.
		for _, p := range shell.popups {
			if p.w < 2 || p.h < 2 {
				continue
			}
			dst = append(dst, compositor.SceneWindow{
				ClientID:  p.clientID,
				SurfaceID: p.surfaceID,
				Rect: compositor.Rect{
					X: p.x, Y: p.y, W: p.w, H: p.h,
				},
				Popup: true,
			})
		}
		// Cursor is drawn topmost: the hovered client's shape if it set
		// one, else the compositor default (a themed arrow).
		if defaultCursor != nil || len(shell.cursors) > 0 {
			cx, cy := router.CursorPosition()
			srcClient, srcSurface := cursorClientID, defaultCursorSurfaceID
			var hx, hy, w, h int32
			if defaultCursor != nil {
				hx, hy, w, h = defaultCursor.HotX, defaultCursor.HotY, defaultCursor.W, defaultCursor.H
			}
			if hc := router.HoverClient(); hc != 0 {
				if ce, ok := shell.cursors[hc]; ok && ce.active && ce.w > 0 {
					srcClient, srcSurface = ce.clientID, ce.surfaceID
					hx, hy, w, h = ce.hx, ce.hy, ce.w, ce.h
				}
			}
			if w > 0 && h > 0 {
				dst = append(dst, compositor.SceneWindow{
					ClientID:  srcClient,
					SurfaceID: srcSurface,
					Rect: compositor.Rect{
						X: int32(cx) - hx, Y: int32(cy) - hy, W: w, H: h,
					},
					Popup: true,
				})
			}
		}
		return dst
	})
	frameLoop.SetSnapshot(func(clientID, surfaceID uint32, dst []byte) (int, int32, int32, int32, bool) {
		if clientID == cursorClientID {
			if defaultCursor == nil {
				return 0, 0, 0, 0, false
			}
			n := copy(dst, defaultCursor.Pixels)
			return n, defaultCursor.W, defaultCursor.H, defaultCursor.Stride(), n > 0
		}
		client := srv.GetClient(clientID)
		if client == nil {
			return 0, 0, 0, 0, false
		}
		return client.SnapshotSurfaceBuffer(surfaceID, dst)
	})
	frameLoop.SetInfo(func(clientID, surfaceID uint32) (int32, int32, int32, bool) {
		if clientID == cursorClientID {
			if defaultCursor == nil {
				return 0, 0, 0, false
			}
			return defaultCursor.W, defaultCursor.H, defaultCursor.Stride(), true
		}
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

	publishScene := shell.publishScene
	applyEvent := shell.applyEvent

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

// parseWorkspaceArg reads a 1-based workspace number from a binding arg.
func parseWorkspaceArg(args []string) (int, bool) {
	if len(args) != 1 {
		return 0, false
	}
	n := 0
	for _, c := range args[0] {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, n > 0
}
