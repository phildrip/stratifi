package server

// Typed ID-indexed stores for the client's protocol entities (stratifi-aos
// phase 2). Each store is a flat slice with an id->index map, giving O(1)
// lookup/insert/destroy while keeping data contiguous for enumeration.
// Mutations are guarded by the owning Client's mutex (same discipline as
// the object table); the stores themselves are not independently
// thread-safe by design.

// bufferStore holds wl_buffer descriptions keyed by object id.
type bufferStore struct {
	items   []ShmBuffer
	indexOf map[uint32]int
}

func (s *bufferStore) init() {
	s.items = make([]ShmBuffer, 0, 32)
	s.indexOf = make(map[uint32]int, 32)
}

// add inserts or replaces the buffer bound to b.ID.
func (s *bufferStore) add(b ShmBuffer) {
	if s.indexOf == nil {
		s.init()
	}
	if i, ok := s.indexOf[b.ID]; ok {
		s.items[i] = b
		return
	}
	s.indexOf[b.ID] = len(s.items)
	s.items = append(s.items, b)
}

func (s *bufferStore) get(id uint32) (ShmBuffer, bool) {
	if s.indexOf == nil {
		return ShmBuffer{}, false
	}
	i, ok := s.indexOf[id]
	if !ok {
		return ShmBuffer{}, false
	}
	return s.items[i], true
}

func (s *bufferStore) remove(id uint32) bool {
	if s.indexOf == nil {
		return false
	}
	i, ok := s.indexOf[id]
	if !ok {
		return false
	}
	// swap-remove keeps the slice dense; fix the moved entry's index.
	last := len(s.items) - 1
	if i != last {
		s.items[i] = s.items[last]
		s.indexOf[s.items[i].ID] = i
	}
	s.items = s.items[:last]
	delete(s.indexOf, id)
	return true
}

// each visits all buffers in stable order.
func (s *bufferStore) each(fn func(b *ShmBuffer)) {
	for i := range s.items {
		fn(&s.items[i])
	}
}

// poolStore holds mapped wl_shm pools keyed by object id. Pools own an
// mmap and fd, so handlers take a stable pointer for CPU writes.
type poolStore struct {
	items   []ShmPool
	indexOf map[uint32]int
}

func (s *poolStore) init() {
	s.items = make([]ShmPool, 0, 16)
	s.indexOf = make(map[uint32]int, 16)
}

// add inserts or replaces; the replacement does NOT close the old pool's
// resources — callers manage lifecycle via Close.
func (s *poolStore) add(p ShmPool) *ShmPool {
	if s.indexOf == nil {
		s.init()
	}
	if i, ok := s.indexOf[p.ID]; ok {
		s.items[i] = p
		return &s.items[i]
	}
	s.indexOf[p.ID] = len(s.items)
	s.items = append(s.items, p)
	return &s.items[len(s.items)-1]
}

func (s *poolStore) get(id uint32) (*ShmPool, bool) {
	if s.indexOf == nil {
		return nil, false
	}
	i, ok := s.indexOf[id]
	if !ok {
		return nil, false
	}
	return &s.items[i], true
}

func (s *poolStore) remove(id uint32) bool {
	if s.indexOf == nil {
		return false
	}
	i, ok := s.indexOf[id]
	if !ok {
		return false
	}
	last := len(s.items) - 1
	if i != last {
		s.items[i] = s.items[last]
		s.indexOf[s.items[i].ID] = i
	}
	s.items = s.items[:last]
	delete(s.indexOf, id)
	return true
}

// surfaceStore holds wl_surface state keyed by object id. Hot fields
// (geometry/damage) live inline in the AoS entry; the commit path scans
// this slice contiguously.
type surfaceStore struct {
	items   []Surface
	indexOf map[uint32]int
}

func (s *surfaceStore) init() {
	s.items = make([]Surface, 0, 16)
	s.indexOf = make(map[uint32]int, 16)
}

func (s *surfaceStore) add(sf Surface) {
	if s.indexOf == nil {
		s.init()
	}
	if i, ok := s.indexOf[sf.ID]; ok {
		s.items[i] = sf
		return
	}
	s.indexOf[sf.ID] = len(s.items)
	s.items = append(s.items, sf)
}

func (s *surfaceStore) get(id uint32) (Surface, bool) {
	if s.indexOf == nil {
		return Surface{}, false
	}
	i, ok := s.indexOf[id]
	if !ok {
		return Surface{}, false
	}
	return s.items[i], true
}

// mutate gives the handler stable access to the entry for in-place edits.
func (s *surfaceStore) mutate(id uint32, fn func(sf *Surface)) bool {
	if s.indexOf == nil {
		return false
	}
	i, ok := s.indexOf[id]
	if !ok {
		return false
	}
	fn(&s.items[i])
	return true
}

func (s *surfaceStore) remove(id uint32) bool {
	if s.indexOf == nil {
		return false
	}
	i, ok := s.indexOf[id]
	if !ok {
		return false
	}
	last := len(s.items) - 1
	if i != last {
		s.items[i] = s.items[last]
		s.indexOf[s.items[i].ID] = i
	}
	s.items = s.items[:last]
	delete(s.indexOf, id)
	return true
}

// each visits all surfaces in stable order (commit flush path).
func (s *surfaceStore) each(fn func(sf *Surface)) {
	for i := range s.items {
		fn(&s.items[i])
	}
}

// xdgStore is the generic store for xdg_surface/xdg_toplevel records.
// Entries carry their id so swap-remove works generically. Mutation gives
// handlers stable access to the entry under the client mutex (transient
// pointers only).
type entry[T any] struct {
	id  uint32
	rec T
}

type xdgStore[T any] struct {
	items   []entry[T]
	indexOf map[uint32]int
}

func (s *xdgStore[T]) init() {
	s.items = make([]entry[T], 0, 16)
	s.indexOf = make(map[uint32]int, 16)
}

// add inserts or replaces and returns a stable pointer to the record.
func (s *xdgStore[T]) add(id uint32, rec T) *T {
	if s.indexOf == nil {
		s.init()
	}
	if i, ok := s.indexOf[id]; ok {
		s.items[i].rec = rec
		return &s.items[i].rec
	}
	s.indexOf[id] = len(s.items)
	s.items = append(s.items, entry[T]{id: id, rec: rec})
	return &s.items[len(s.items)-1].rec
}

func (s *xdgStore[T]) get(id uint32) (*T, bool) {
	if s.indexOf == nil {
		return nil, false
	}
	i, ok := s.indexOf[id]
	if !ok {
		return nil, false
	}
	return &s.items[i].rec, true
}

func (s *xdgStore[T]) mutate(id uint32, fn func(rec *T)) bool {
	if s.indexOf == nil {
		return false
	}
	i, ok := s.indexOf[id]
	if !ok {
		return false
	}
	fn(&s.items[i].rec)
	return true
}

func (s *xdgStore[T]) remove(id uint32) bool {
	if s.indexOf == nil {
		return false
	}
	i, ok := s.indexOf[id]
	if !ok {
		return false
	}
	last := len(s.items) - 1
	if i != last {
		s.items[i] = s.items[last]
		s.indexOf[s.items[i].id] = i
	}
	s.items = s.items[:last]
	delete(s.indexOf, id)
	return true
}
