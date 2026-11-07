package pipewire

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
	"stratifi/internal/alignio"
	"stratifi/internal/pipewire/spa"
)

// Conn is a PipeWire native-protocol connection to the pw daemon.
type Conn struct {
	nc *net.UnixConn
	fd int

	mu          sync.Mutex
	acc         alignio.Accumulator // accumulated stream bytes awaiting a full packet
	inFDStorage [16]int
	inFDs       alignio.FDQueue // fds received but not yet consumed by a message
	nextSeq     uint32
	recvFDs     []int
	nextResID   atomic.Uint32

	CoreID     uint32 // core proxy id (0 for the first core)
	Version    string
	cookie     uint32
	registryID uint32 // bound registry proxy id
}

// nextProxyID allocates the next sequential client proxy id. Proxy id 1 is
// reserved for the server's client resource (it carries client_info events);
// client-allocated proxies start at 2. The daemon's object map is a
// sequential array — ids must never skip (pw_map_insert_at rejects gaps
// with -ENOSPC).
func nextProxyID(seq *atomic.Uint32) uint32 {
	v := seq.Add(1)
	if v < 2 {
		return seq.Add(1) // consume slot 1, return 2
	}
	return v
}

// DefaultSocketPath is the user PipeWire daemon socket.
const DefaultSocketPath = "/run/user/1000/pipewire-0"

// Connect dials the PipeWire daemon at the given socket path.
func Connect(path string) (*Conn, error) {
	nc, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("pipewire: dial %s: %w", path, err)
	}
	var rawFD int
	if rc, err := nc.SyscallConn(); err == nil {
		_ = rc.Control(func(fd uintptr) { rawFD = int(fd) })
	}
	c := &Conn{nc: nc, fd: rawFD}
	c.inFDs.Init(c.inFDStorage[:])
	return c, nil
}

// ConnectDefault dials the user daemon or $PIPEWIRE_RUNTIME_DIR override.
func ConnectDefault() (*Conn, error) {
	path := os.Getenv("PIPEWIRE_RUNTIME_DIR")
	if path == "" {
		path = "/run/user/1000"
	}
	return Connect(path + "/pipewire-0")
}

// Cookie returns the core cookie from the last core_info event.
func (c *Conn) Cookie() uint32 { return c.cookie }

// PWVersionCore is PW_VERSION_CORE (spa/pod protocol ABI level).
const PWVersionCore = 4

// Core event opcodes (pw/core.h).
const (
	CoreEventInfo      uint32 = 0
	CoreEventDone      uint32 = 1
	CoreEventPing      uint32 = 2
	CoreEventError     uint32 = 3
	CoreEventRemoveID  uint32 = 4
	CoreEventBoundID   uint32 = 5
	CoreEventAddMem    uint32 = 6
	CoreEventRemoveMem uint32 = 7
)

// Core method opcodes (pw/core.h).
const (
	CoreMethodHello        uint32 = 1
	CoreMethodSync         uint32 = 2
	CoreMethodPong         uint32 = 3
	CoreMethodGetRegistry  uint32 = 5
	CoreMethodCreateObject uint32 = 6
	CoreMethodDestroy      uint32 = 7
)

// Client method opcodes (pw/client.h).
const (
	ClientMethodUpdateProperties uint32 = 2
)

// ClientIdentity describes the compositor to the pw daemon. The access
// module grants registry permissions when these properties are announced,
// so they must reflect the running process, not hardcoded strings.
type ClientIdentity struct {
	ApplicationName string
	Binary          string
	User            string
	Host            string
	Language        string
	CoreName        string
}

// identityFromEnv derives the client identity from process state.
func identityFromEnv() ClientIdentity {
	userName := "unknown"
	if u, err := user.Current(); err == nil {
		userName = u.Username
	}
	host := "unknown"
	var uts unix.Utsname
	if err := unix.Uname(&uts); err == nil {
		host = nulString(uts.Nodename[:])
	}
	lang := os.Getenv("LANG")
	if lang == "" {
		lang = "C"
	}
	return ClientIdentity{
		ApplicationName: "stratifi",
		Binary:          filepath.Base(os.Args[0]),
		User:            userName,
		Host:            host,
		Language:        lang,
		CoreName:        "pipewire-0",
	}
}

// nulString trims a NUL-terminated fixed byte array.
func nulString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// UpdateClientProperties announces the compositor's identity on the client
// proxy (PW_ID_CLIENT=1). The access module grants registry permissions on
// this update — without it get_registry returns zero globals.
func (c *Conn) UpdateClientProperties() error {
	return c.UpdateClientPropertiesWith(identityFromEnv())
}

// UpdateClientPropertiesWith sends the given identity; the dict n_items is
// derived from the encoded pairs so the count can never drift.
func (c *Conn) UpdateClientPropertiesWith(id ClientIdentity) error {
	type kv struct{ k, v string }
	pairs := []kv{
		{"application.name", id.ApplicationName},
		{"application.process.binary", id.Binary},
		{"application.process.user", id.User},
		{"application.process.host", id.Host},
		{"application.language", id.Language},
		{"core.name", id.CoreName},
	}

	var b spa.Builder
	b.Reserve(512)
	b.BeginStruct() // outer struct {dict}
	b.BeginStruct() // dict
	b.Int(int32(len(pairs)))
	for _, p := range pairs {
		b.String(p.k)
		b.String(p.v)
	}
	b.End()
	b.End()

	var w Writer
	var fdSlots [4]int
	w.Init(make([]byte, 0, 512), &fdSlots)
	w.Begin(PWIDClient, ClientMethodUpdateProperties)
	w.PutBytes(b.Bytes())
	w.Finish()

	if _, err := c.nc.Write(w.Bytes()); err != nil {
		return fmt.Errorf("pipewire: client update: %w", err)
	}
	return nil
}

// PWIDClient is the client's own proxy id (PW_ID_CLIENT).
const PWIDClient uint32 = 1

// Hello performs the version handshake: Struct{Int(PW_VERSION_CORE)}.
func (c *Conn) Hello() error {
	var b spa.Builder
	b.Reserve(64)
	b.BeginStruct()
	b.Int(PWVersionCore)
	b.End()

	var w Writer
	var fdSlots [4]int
	w.Init(make([]byte, 0, 128), &fdSlots)
	w.Begin(0, CoreMethodHello)
	w.PutBytes(b.Bytes())
	w.Finish()

	if _, err := c.nc.Write(w.Bytes()); err != nil {
		return fmt.Errorf("pipewire: hello: %w", err)
	}
	return nil
}

// CoreInfo is the decoded pw_core_info event payload.
type CoreInfo struct {
	ID       uint32
	Cookie   uint32
	UserName string
	HostName string
	Version  string
	Name     string
}

// parseCoreInfo decodes Struct{Int id, Int cookie, String user, String host,
// String version, String name, Long change_mask, Object dict}.
func parseCoreInfo(body []byte) (*CoreInfo, error) {
	pod, total, err := spa.ReadPod(body)
	if err != nil {
		return nil, fmt.Errorf("pipewire: core_info pod: %w", err)
	}
	if pod.Type != spa.TypeStruct {
		return nil, fmt.Errorf("pipewire: core_info expected Struct, got %x", pod.Type)
	}
	it := spa.NewIter(body[:total])
	_, _, _ = it.Next() // consume the outer Struct pod; walk its Body
	it = spa.NewIter(pod.Body)
	info := &CoreInfo{}

	readU32 := func() (uint32, error) {
		p, ok, err := it.Next()
		if err != nil || !ok {
			return 0, fmt.Errorf("pipewire: core_info struct short: %v", err)
		}
		if p.Type != spa.TypeInt {
			return 0, fmt.Errorf("pipewire: core_info expected Int, got %x", p.Type)
		}
		if len(p.Body) < 4 {
			return 0, spa.ErrShortPod
		}
		return leU32(p.Body), nil
	}
	readStr := func() (string, error) {
		p, ok, err := it.Next()
		if err != nil || !ok {
			return "", fmt.Errorf("pipewire: core_info struct short: %v", err)
		}
		if p.Type != spa.TypeString {
			return "", fmt.Errorf("pipewire: core_info expected String, got %x", p.Type)
		}
		if len(p.Body) == 0 {
			return "", nil
		}
		return string(p.Body[:len(p.Body)-1]), nil // strip NUL
	}

	if info.ID, err = readU32(); err != nil {
		return nil, err
	}
	if info.Cookie, err = readU32(); err != nil {
		return nil, err
	}
	if info.UserName, err = readStr(); err != nil {
		return nil, err
	}
	if info.HostName, err = readStr(); err != nil {
		return nil, err
	}
	if info.Version, err = readStr(); err != nil {
		return nil, err
	}
	if info.Name, err = readStr(); err != nil {
		return nil, err
	}
	return info, nil
}

// recvMsg reads one complete pw message from the stream socket, using a
// rolling accumulation buffer (stream framing: one read may return partial
// or multiple packets). Returns header, body, and ancillary fds.
func (c *Conn) recvMsg() (Header, []byte, []int, error) {
	var oob [64]byte
	for {
		raw, ok, err := c.acc.Next(HeaderSize, MessageTotalSpan)
		if err != nil {
			return Header{}, nil, nil, err
		}
		if ok {
			h, total, err := DecodeHeader(raw)
			if err != nil {
				return Header{}, nil, nil, err
			}
			activeFDs := c.inFDs.Active()
			take := int(h.NFDs)
			if take == 0 || take > len(activeFDs) {
				take = len(activeFDs)
			}
			gotFDs := append(c.recvFDs[:0], activeFDs[:take]...)
			c.inFDs.Consume(take)
			return h, raw[HeaderSize:total], gotFDs, nil
		}

		var chunk [512]byte
		n, oobn, _, _, rerr := unix.Recvmsg(c.fd, chunk[:], oob[:], 0)
		if rerr != nil {
			return Header{}, nil, nil, rerr
		}
		if n > 0 {
			c.acc.Append(chunk[:n])
		}
		if oobn > 0 {
			if err := c.inFDs.RecvOOB(oob[:oobn]); err != nil {
				return Header{}, nil, nil, err
			}
		}
	}
}
