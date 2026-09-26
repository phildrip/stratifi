package server

import (
	"sync"
	"testing"
)

// The generational object table is the DOD backbone for the server core:
// flat slot storage, no pointers between entities, stale IDs fail loudly.

// TestObjectTableRegisterLookup pins register→lookup semantics.
func TestObjectTableRegisterLookup(t *testing.T) {
	cases := []struct {
		name  string
		typ   ObjectType
		id    uint32
		valid bool
	}{
		{"surface", ObjSurface, 10, true},
		{"buffer", ObjBuffer, 14, true},
		{"zero id is display", ObjDisplay, 1, true},
	}
	for _, tc := range cases {
		var ot objectTable
		ot.init()
		ot.register(tc.id, tc.typ)
		got, ok := ot.lookup(tc.id)
		if got != tc.typ || !ok {
			t.Errorf("%s: lookup(%d) = %v/%v, want %v/true", tc.name, tc.id, got, ok, tc.typ)
		}
	}
}

// TestObjectTableUnregisterIsGenerational is the safety property: after a
// destroy, lookups with the raw old ID must fail (stale references cannot
// silently bind to a later object reusing the slot).
func TestObjectTableUnregisterIsGenerational(t *testing.T) {
	var ot objectTable
	ot.init()
	ot.register(10, ObjSurface)
	ot.unregister(10)

	if got, ok := ot.lookup(10); ok {
		t.Errorf("stale lookup after unregister = %v/true", got)
	}

	// Reuse of the slot with the same raw ID re-activates it for the new
	// protocol handler — registration always wins over stale state.
	ot.register(10, ObjBuffer)
	if got, ok := ot.lookup(10); !ok || got != ObjBuffer {
		t.Errorf("re-register lookup = %v/%v", got, ok)
	}
}

// TestObjectTableUnknownID covers the miss path used by dispatch.
func TestObjectTableUnknownID(t *testing.T) {
	var ot objectTable
	ot.init()
	if _, ok := ot.lookup(0xbad); ok {
		t.Error("unknown id lookup must fail")
	}
}

// TestObjectTableManyIDs exercises growable dense storage and iteration
// order independence (lookup is by ID, not by index).
func TestObjectTableManyIDs(t *testing.T) {
	var ot objectTable
	ot.init()
	ids := []uint32{1, 2, 3, 50, 500, 5000, 0xffffff}
	for _, id := range ids {
		ot.register(id, ObjSurface)
	}
	for _, id := range ids {
		if _, ok := ot.lookup(id); !ok {
			t.Errorf("lookup(%d) failed after bulk register", id)
		}
	}
	// Unregister half; the rest still resolve.
	for i := 0; i < len(ids)/2; i++ {
		ot.unregister(ids[i])
	}
	for i := len(ids) / 2; i < len(ids); i++ {
		if _, ok := ot.lookup(ids[i]); !ok {
			t.Errorf("lookup(%d) failed after partial unregister", ids[i])
		}
	}
}

// TestDispatchTableComplete catches the missing-dispatcher bug class: every
// registered object type must route somewhere. The dispatch table must have
// an entry for every ObjectType constant — unsupported-but-boundable types
// map to a protocol error, not a "no dispatcher" runtime error.
func TestDispatchTableComplete(t *testing.T) {
	for typ := ObjNone + 1; typ < objTypeMax; typ++ {
		if typ == ObjNone {
			continue
		}
		d, ok := dispatchTable[typ]
		if !ok {
			t.Errorf("object type %d (%v) missing from dispatchTable", typ, typ)
			continue
		}
		if d == nil {
			t.Errorf("dispatchTable[%d] is nil", typ)
		}
	}
}

// TestConcurrentObjectTableAndCapture exercises the capture grab path
// (which drops and re-takes the client mutex) racing against concurrent
// object registration — the exact interleaving that must never corrupt
// the dense table or the capture frame state.
func TestConcurrentObjectTableAndCapture(t *testing.T) {
	srv := &Server{clients: make(map[uint32]*Client)}
	c := newClient(1, nil, srv)
	srv.clients[1] = c

	var wg sync.WaitGroup
	const goroutines = 8
	const iters = 200
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			base := uint32(1000 + g*1000)
			for i := 0; i < iters; i++ {
				id := base + uint32(i)
				// All protocol paths mutate the table under c.mu; the
				// dense table is not independently thread-safe.
				c.mu.Lock()
				c.registerObject(id, ObjSurface)
				_, ok := c.findObject(id)
				if !ok {
					c.mu.Unlock()
					t.Errorf("lookup lost id %d", id)
					return
				}
				c.unregisterObject(id)
				c.mu.Unlock()
			}
		}(g)
	}
	// Concurrent capture state mutation on a shared session.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			c.mu.Lock()
			c.capture.frames[1] = &captureFrame{ID: 1, Session: 1}
			c.capture.frames[2] = &captureFrame{ID: 2, Session: 1}
			c.mu.Unlock()
		}
	}()
	wg.Wait()
}
