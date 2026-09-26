package server

import "testing"

// The stores must support the exact access patterns the protocol handlers
// use: create-by-id, lookup-by-id, destroy-by-id, enumerate-all (commit
// flush / shutdown close), and replace-on-rebind (create_pool then destroy
// then recreate the same id).

func TestBufferStoreCRUD(t *testing.T) {
	var s bufferStore
	s.init()

	s.add(ShmBuffer{ID: 10, Width: 64, Height: 32})
	buf, ok := s.get(10)
	if !ok || buf.Width != 64 || buf.Height != 32 {
		t.Fatalf("get(10) = %+v/%v", buf, ok)
	}

	// replace-on-rebind: registering the same id overwrites.
	s.add(ShmBuffer{ID: 10, Width: 128, Height: 64})
	buf, _ = s.get(10)
	if buf.Width != 128 {
		t.Errorf("rebind did not replace: %+v", buf)
	}

	// destroy
	if !s.remove(10) {
		t.Error("remove(10) should report success")
	}
	if !s.remove(10) {
		// second remove of an unknown id is a no-op, but must not panic
		t.Log("double remove returns false")
	}
	if _, ok := s.get(10); ok {
		t.Error("destroyed buffer still resolvable")
	}
}

func TestBufferStoreEnumerate(t *testing.T) {
	var s bufferStore
	s.init()
	for i := uint32(1); i <= 5; i++ {
		s.add(ShmBuffer{ID: i, PoolID: 100 + i})
	}
	n := 0
	s.each(func(b *ShmBuffer) {
		n++
		if b.PoolID != 100+b.ID {
			t.Errorf("buffer %d pool mismatch", b.ID)
		}
	})
	if n != 5 {
		t.Errorf("enumerate saw %d buffers, want 5", n)
	}
}

func TestPoolStoreOwnershipAndClose(t *testing.T) {
	var s poolStore
	s.init()

	// add returns a stable pointer the handler writes pixels into.
	p := s.add(ShmPool{ID: 7, Size: 4096})
	if p == nil || p.Size != 4096 {
		t.Fatalf("add(7) = %+v", p)
	}
	p2, ok := s.get(7)
	if !ok || p2 != p {
		t.Error("get(7) must return the same backing pointer")
	}

	if !s.remove(7) {
		t.Error("remove(7) should report success")
	}
	if _, ok := s.get(7); ok {
		t.Error("removed pool still resolvable")
	}
}

func TestSurfaceStoreCRUD(t *testing.T) {
	var s surfaceStore
	s.init()

	s.add(Surface{ID: 3, Active: true})
	surf, ok := s.get(3)
	if !ok || !surf.Active {
		t.Fatalf("get(3) = %+v/%v", surf, ok)
	}

	// handlers mutate through the pointer
	s.mutate(3, func(sf *Surface) { sf.Current.DamageW = 42 })
	surf, _ = s.get(3)
	if surf.Current.DamageW != 42 {
		t.Errorf("mutate lost write: %+v", surf)
	}

	s.remove(3)
	if _, ok := s.get(3); ok {
		t.Error("destroyed surface still resolvable")
	}
}

func TestStoreZeroValueSafe(t *testing.T) {
	// A zero-value store must not panic on any operation.
	var b bufferStore
	if _, ok := b.get(1); ok {
		t.Error("zero bufferStore must be empty")
	}
	var p poolStore
	if _, ok := p.get(1); ok {
		t.Error("zero poolStore must be empty")
	}
	var s surfaceStore
	if _, ok := s.get(1); ok {
		t.Error("zero surfaceStore must be empty")
	}
}
