package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"bloxwm/internal/server"
)

func main() {
	socketName := "wayland-blox-0"
	if len(os.Args) > 1 {
		socketName = os.Args[1]
	}

	fmt.Println("==================================================")
	fmt.Println("               bloxwm - Wayland Compositor        ")
	fmt.Println("==================================================")

	srv, err := server.NewServer(socketName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] Failed to initialize server: %v\n", err)
		os.Exit(1)
	}
	defer srv.Close()

	srv.SetOnCommit(func(s *server.Surface) {
		fmt.Printf("[+] Surface committed: ID=%d, Client=%d, BufferID=%d\n",
			s.ID, s.ClientID, s.Current.AttachedBufferID)
	})

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\n[*] Shutting down bloxwm...")
		_ = srv.Close()
		os.Exit(0)
	}()

	fmt.Printf("[+] Listening on Wayland socket: %s\n", srv.SocketPath())
	fmt.Printf("[+] Test with any client: WAYLAND_DISPLAY=%s foot (or alacritty)\n\n", socketName)

	if err := srv.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "[-] Server loop terminated: %v\n", err)
	}
}
