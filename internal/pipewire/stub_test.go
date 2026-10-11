package pipewire

import (
	"net"
	"path/filepath"
	"testing"
	"time"

	"stratifi/internal/pipewire/spa"
)

// stubDaemon is a deterministic PipeWire server stub implementing exactly
// the client connect path, replicating the empirical behaviors observed on
// the live daemon (see stratifi-d2f):
//   - protocol v3 framing (opcode in word 2 top byte, size in low 24 bits)
//   - Hello demarshal = Struct{Int(PW_VERSION_CORE=4)}
//   - client.update_properties accepted on PW_ID_CLIENT=1
//   - get_registry: proxy ids must be sequential (gap → -ENOSPC error event)
//   - zero registry globals until the client sends properties
type stubDaemon struct {
	t        *testing.T
	ln       *net.UnixListener
	globals  int  // how many globals to advertise after props
	gotProps bool // client update received?
}

func startStubDaemon(t *testing.T, globals int) (*stubDaemon, string) {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "pipewire-stub-0")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		t.Fatalf("stub listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	sd := &stubDaemon{t: t, globals: globals}
	go sd.serve(ln)
	return sd, sockPath
}

func (sd *stubDaemon) send(c *net.UnixConn, id, opcode uint32, pod []byte) {
	sd.t.Helper()
	var w Writer
	w.Begin(id, opcode)
	w.PutBytes(pod)
	w.Finish()
	if _, err := c.Write(w.Bytes()); err != nil {
		sd.t.Logf("stub send: %v", err)
	}
}

// coreInfoPod builds Struct{Int id, Int cookie, String x4, Long mask, dict}.
func coreInfoPod(cookie uint32) []byte {
	var b spa.Builder
	b.BeginStruct()
	b.Int(0)
	b.Int(int32(cookie))
	b.String("stub")
	b.String("stub-host")
	b.String("1.6.2")
	b.String("pipewire-0")
	b.Long(1)
	b.BeginStruct()
	b.Int(0)
	b.End()
	b.End()
	return b.Bytes()
}

// donePod builds Struct{Int id, Int seq}.
func donePod(id, seq uint32) []byte {
	var b spa.Builder
	b.BeginStruct()
	b.Int(int32(id))
	b.Int(int32(seq))
	b.End()
	return b.Bytes()
}

// errorPod builds Struct{Int id, Int seq, Int res, String error}.
func errorPod(id, seq uint32, res int32, msg string) []byte {
	var b spa.Builder
	b.BeginStruct()
	b.Int(int32(id))
	b.Int(int32(seq))
	b.Int(res)
	b.String(msg)
	b.End()
	return b.Bytes()
}

// globalPod builds Struct{Int id, Int perms, String type, Int version, dict}.
func globalPod(id uint32, typ string, version uint32) []byte {
	var b spa.Builder
	b.BeginStruct()
	b.Int(int32(id))
	b.Int(0x148)
	b.String(typ)
	b.Int(int32(version))
	b.BeginStruct()
	b.Int(0)
	b.End()
	b.End()
	return b.Bytes()
}

func (sd *stubDaemon) serve(ln *net.UnixListener) {
	for {
		c, err := ln.AcceptUnix()
		if err != nil {
			return
		}
		go sd.handle(c)
	}
}

func (sd *stubDaemon) handle(c *net.UnixConn) {
	sd.t.Helper()
	buf := make([]byte, 4096)
	var pending []byte
	mapSize := uint32(2) // core(0) + client(1) after hello, matching the daemon

	for {
		n, err := c.Read(buf)
		if err != nil {
			return
		}
		pending = append(pending, buf[:n]...)
		for len(pending) >= HeaderSize {
			h, total, err := DecodeHeader(pending)
			if err != nil {
				return
			}
			if len(pending) < total {
				break
			}
			body := pending[HeaderSize:total]
			pending = pending[total:]

			switch {
			case h.ID == 0 && h.Opcode == CoreMethodHello:
				// Struct{Int(version)}: expect 4.
				pod, _, perr := spa.ReadPod(body)
				if perr != nil || pod.Type != spa.TypeStruct {
					return
				}
				ipod, _, perr := spa.ReadPod(pod.Body)
				if perr != nil || ipod.Type != spa.TypeInt || leU32(ipod.Body) != PWVersionCore {
					sd.send(c, 0, CoreEventError, errorPod(0, h.Seq, -71, "bad hello"))
					return
				}
				sd.send(c, 0, CoreEventInfo, coreInfoPod(0x07c0ffee))
				// client resource at PW_ID_CLIENT=1.
				sd.send(c, 1, 0, globalPod(1, "PipeWire:Interface:Client", 3))

			case h.ID == PWIDClient && h.Opcode == ClientMethodUpdateProperties:
				sd.gotProps = true

			case h.ID == 0 && h.Opcode == CoreMethodGetRegistry:
				// Struct{Int version, Int new_id}: new_id must be == mapSize
				// (sequential proxy ids; gaps are -ENOSPC on the real daemon).
				pod, _, perr := spa.ReadPod(body)
				if perr != nil {
					return
				}
				it := spa.NewIter(pod.Body)
				_, _, _ = it.Next() // version
				idPod, ok, _ := it.Next()
				if !ok {
					return
				}
				newID := leU32(idPod.Body)
				if newID != mapSize {
					sd.send(c, 0, CoreEventError,
						errorPod(0, h.Seq, -28, "can't add id: No space left on device"))
					continue
				}
				mapSize++
				if sd.gotProps {
					for i := uint32(1); i <= uint32(sd.globals); i++ {
						sd.send(c, newID, RegistryEventGlobal,
							globalPod(100+i, TypeFactory, 3))
					}
				}
				// no props → zero globals (access module never granted).

			case h.ID == 0 && h.Opcode == CoreMethodSync:
				pod, _, perr := spa.ReadPod(body)
				if perr != nil {
					return
				}
				it := spa.NewIter(pod.Body)
				idPod, ok, _ := it.Next()
				seqPod, ok2, _ := it.Next()
				if !ok || !ok2 {
					return
				}
				sd.send(c, 0, CoreEventDone, donePod(leU32(idPod.Body), leU32(seqPod.Body)))
			}
		}
	}
}

// TestStubDaemonHandshakeAndRegistry drives the real client against the
// stub and asserts the empirical protocol behaviors deterministically.
func TestStubDaemonHandshakeAndRegistry(t *testing.T) {
	sd, sockPath := startStubDaemon(t, 5)

	c, err := Connect(sockPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.nc.Close()

	if err := c.Hello(); err != nil {
		t.Fatalf("Hello: %v", err)
	}
	if err := c.UpdateClientProperties(); err != nil {
		t.Fatalf("UpdateClientProperties: %v", err)
	}
	registryID, err := c.GetRegistry()
	if err != nil {
		t.Fatalf("GetRegistry: %v", err)
	}
	c.registryID = registryID
	if err := c.Sync(0, 1); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	deadline := time.After(2 * time.Second)
	count := 0
	for count < sd.globals {
		h, body, _, err := c.recvMsg()
		if err != nil {
			select {
			case <-deadline:
				t.Fatalf("got %d/%d globals before deadline", count, sd.globals)
			default:
			}
			continue
		}
		if h.ID == registryID && h.Opcode == RegistryEventGlobal {
			g, err := parseGlobal(body)
			if err != nil {
				t.Fatalf("global parse: %v", err)
			}
			if g.Type != TypeFactory {
				t.Errorf("global type = %s", g.Type)
			}
			count++
		}
	}
	if count != sd.globals {
		t.Fatalf("globals = %d, want %d", count, sd.globals)
	}
}

// TestStubDaemonRejectsIDGap pins the sequential proxy id rule: a registry
// proxy id that skips a map slot is rejected with an error event.
func TestStubDaemonRejectsIDGap(t *testing.T) {
	_, sockPath := startStubDaemon(t, 1)

	c, err := Connect(sockPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.nc.Close()

	if err := c.Hello(); err != nil {
		t.Fatal(err)
	}
	if err := c.Sync(0, 1); err != nil {
		t.Fatal(err)
	}

	// Burn proxy id 2 so the next allocation (3) would leave a gap.
	_, err = c.GetRegistry() // takes id 2
	if err != nil {
		t.Fatalf("first GetRegistry: %v", err)
	}
	if err := c.Sync(0, 2); err != nil {
		t.Fatal(err)
	}

	// Now delete the registry from the server map? The stub's mapSize grew
	// to 3; a second GetRegistry gets id 3 = valid. Simulate a gap by
	// having the client request an explicit larger id through the raw path.
	var b spa.Builder
	b.BeginStruct()
	b.Int(int32(PWVersionRegistry))
	b.Int(9) // gap: mapSize is 3 now
	b.End()

	var w Writer
	var fdSlots [4]int
	w.Init(make([]byte, 0, 128), &fdSlots)
	w.Begin(0, CoreMethodGetRegistry)
	w.PutBytes(b.Bytes())
	w.Finish()
	if _, err := c.nc.Write(w.Bytes()); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(2 * time.Second)
	for {
		h, body, _, err := c.recvMsg()
		if err != nil {
			select {
			case <-deadline:
				t.Fatal("no error event for id gap")
			default:
			}
			continue
		}
		if h.ID == 0 && h.Opcode == CoreEventError {
			pod, _, err := spa.ReadPod(body)
			if err != nil {
				t.Fatalf("error event pod: %v", err)
			}
			if pod.Type != spa.TypeStruct {
				t.Fatalf("error event not a Struct: %x", pod.Type)
			}
			it := spa.NewIter(pod.Body)
			_, _, _ = it.Next()
			_, _, _ = it.Next()
			resPod, ok, _ := it.Next()
			if !ok || resPod.Type != spa.TypeInt {
				t.Fatal("error event missing res")
			}
			if got := leU32(resPod.Body); got != 0xFFFFFFE4 { // -28 ENOSPC
				t.Errorf("res = %d, want -28", int32(got))
			}
			return
		}
	}
}
