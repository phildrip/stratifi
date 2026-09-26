package pipewire

import (
	"fmt"

	"bloxwm/internal/pipewire/spa"
)

// Registry event opcodes.
const (
	RegistryEventGlobal       uint32 = 0
	RegistryEventGlobalRemove uint32 = 1
)

// Registry method opcodes.
const (
	RegistryMethodAddListener uint32 = 0
	RegistryMethodBind        uint32 = 1
	RegistryMethodDestroy     uint32 = 2
)

// Known interface type strings (pw/type-info.h style "PipeWire:Interface:*").
const (
	TypeRegistry = "PipeWire:Interface:Registry"
	TypeClient   = "PipeWire:Interface:Client"
	TypeStream   = "PipeWire:Interface:Stream"
	TypeFactory  = "PipeWire:Interface:Factory"
	TypeNode     = "PipeWire:Interface:Node"
)

// Global is one registry advertisement.
type Global struct {
	ID          uint32
	Permissions uint32
	Type        string
	Version     uint32
}

// PWVersionRegistry is PW_VERSION_REGISTRY.
const PWVersionRegistry = 3

// Sync issues a core sync round-trip; the daemon flushes all pending
// events and answers with core.done(id, seq).
func (c *Conn) Sync(id uint32, seq uint32) error {
	var b spa.Builder
	b.Reserve(64)
	b.BeginStruct()
	b.Int(int32(id))
	b.Int(int32(seq))
	b.End()

	var w Writer
	var fdSlots [4]int
	w.Init(make([]byte, 0, 128), &fdSlots)
	w.Begin(0, CoreMethodSync)
	w.PutBytes(b.Bytes())
	w.Finish()

	if _, err := c.nc.Write(w.Bytes()); err != nil {
		return fmt.Errorf("pipewire: sync: %w", err)
	}
	return nil
}

// GetRegistry binds the registry object and returns its proxy id.
func (c *Conn) GetRegistry() (uint32, error) {
	proxyID := nextProxyID(&c.nextResID)

	var b spa.Builder
	b.Reserve(64)
	b.BeginStruct()
	b.Int(PWVersionCore)
	b.Int(int32(proxyID))
	b.End()

	var w Writer
	var fdSlots [4]int
	w.Init(make([]byte, 0, 128), &fdSlots)
	w.Begin(0, CoreMethodGetRegistry)
	w.PutBytes(b.Bytes())
	w.Finish()

	if _, err := c.nc.Write(w.Bytes()); err != nil {
		return 0, fmt.Errorf("pipewire: get_registry: %w", err)
	}
	return proxyID, nil
}

// parseGlobal decodes a registry global event: Struct{Int id, Int perms,
// String type, Int version, Object dict}.
func parseGlobal(body []byte) (*Global, error) {
	pod, _, err := spa.ReadPod(body)
	if err != nil {
		return nil, err
	}
	if pod.Type != spa.TypeStruct {
		return nil, fmt.Errorf("pipewire: global expected Struct, got %x", pod.Type)
	}
	it := spa.NewIter(pod.Body)
	g := &Global{}

	readU32 := func() (uint32, error) {
		p, ok, err := it.Next()
		if err != nil || !ok {
			return 0, spa.ErrShortPod
		}
		if p.Type != spa.TypeInt {
			return 0, fmt.Errorf("pipewire: global expected Int, got %x", p.Type)
		}
		return leU32(p.Body), nil
	}
	readStr := func() (string, error) {
		p, ok, err := it.Next()
		if err != nil || !ok {
			return "", spa.ErrShortPod
		}
		if p.Type != spa.TypeString {
			return "", fmt.Errorf("pipewire: global expected String, got %x", p.Type)
		}
		if len(p.Body) == 0 {
			return "", nil
		}
		return string(p.Body[:len(p.Body)-1]), nil
	}

	if g.ID, err = readU32(); err != nil {
		return nil, err
	}
	if g.Permissions, err = readU32(); err != nil {
		return nil, err
	}
	if g.Type, err = readStr(); err != nil {
		return nil, err
	}
	if g.Version, err = readU32(); err != nil {
		return nil, err
	}
	// The trailing dict Object pod is not needed for enumeration.
	return g, nil
}

// Bind registers a proxy for a global and sends the bind request.
func (c *Conn) Bind(globalID uint32, typ string, version uint32) (uint32, error) {
	proxyID := nextProxyID(&c.nextResID)

	var b spa.Builder
	b.Reserve(96)
	b.BeginStruct()
	b.Int(int32(globalID))
	b.String(typ)
	b.Int(int32(version))
	b.Int(int32(proxyID))
	b.End()

	var w Writer
	var fdSlots [4]int
	w.Init(make([]byte, 0, 128), &fdSlots)
	w.Begin(c.registryID, RegistryMethodBind)
	w.PutBytes(b.Bytes())
	w.Finish()

	if _, err := c.nc.Write(w.Bytes()); err != nil {
		return 0, fmt.Errorf("pipewire: bind: %w", err)
	}
	return proxyID, nil
}
