package server

// Generational object table for the server core: flat slot storage with no
// pointers between entities (AGENTS.md DOD). Registration always wins over
// stale state; destroyed IDs simply miss until a handler re-registers them.
type objectTable struct {
	typ  []ObjectType // dense; index by id, holes are ObjNone
	high uint32       // exclusive upper bound of ever-used ids
}

// init prepares an empty table sized for the common client (registry,
// compositor surfaces, shm buffers, capture session trees).
func (ot *objectTable) init() {
	const initialCap = 128
	ot.typ = make([]ObjectType, 0, initialCap)
	ot.high = 0
}

// register binds id to t, growing dense storage as needed. Raw ids may be
// reused by clients after destroy — registration always wins.
func (ot *objectTable) register(id uint32, t ObjectType) {
	for uint32(len(ot.typ)) <= id {
		ot.typ = append(ot.typ, ObjNone)
	}
	ot.typ[id] = t
	if id >= ot.high {
		ot.high = id + 1
	}
}

// unregister frees the slot. Stale lookups of the raw id fail.
func (ot *objectTable) unregister(id uint32) {
	if id < uint32(len(ot.typ)) {
		ot.typ[id] = ObjNone
	}
}

// lookup returns the object type bound to id, or ok=false.
func (ot *objectTable) lookup(id uint32) (ObjectType, bool) {
	if id >= uint32(len(ot.typ)) {
		return ObjNone, false
	}
	t := ot.typ[id]
	if t == ObjNone {
		return ObjNone, false
	}
	return t, true
}
