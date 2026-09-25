package layout

import (
	"fmt"
)

// WindowID is a 32-bit generational handle.
// High 16 bits = generation count, Low 16 bits = slot index.
type WindowID uint32

// MakeWindowID constructs a WindowID from index and generation.
func MakeWindowID(index uint16, gen uint16) WindowID {
	return WindowID((uint32(gen) << 16) | uint32(index))
}

// Index returns the slot index.
func (id WindowID) Index() uint16 {
	return uint16(id & 0xffff)
}

// Generation returns the generational counter.
func (id WindowID) Generation() uint16 {
	return uint16(id >> 16)
}

// Slot represents an entry in the SlotMap.
type Slot struct {
	Generation uint16
	Active     bool
}

// SlotMap provides safe, contiguous generational storage with zero pointer chasing.
type SlotMap struct {
	slots []Slot
	free  []uint16
}

// NewSlotMap creates a pre-allocated SlotMap with the given initial capacity.
func NewSlotMap(capacity int) *SlotMap {
	sm := &SlotMap{
		slots: make([]Slot, 0, capacity),
		free:  make([]uint16, 0, capacity),
	}
	return sm
}

// Allocate reserves a new slot and returns its generational WindowID.
func (sm *SlotMap) Allocate() WindowID {
	var idx uint16
	if len(sm.free) > 0 {
		idx = sm.free[len(sm.free)-1]
		sm.free = sm.free[:len(sm.free)-1]
		sm.slots[idx].Active = true
		return MakeWindowID(idx, sm.slots[idx].Generation)
	}

	idx = uint16(len(sm.slots))
	sm.slots = append(sm.slots, Slot{
		Generation: 1,
		Active:     true,
	})
	return MakeWindowID(idx, 1)
}

// Validate returns true if the WindowID is currently valid and active.
func (sm *SlotMap) Validate(id WindowID) bool {
	idx := id.Index()
	if int(idx) >= len(sm.slots) {
		return false
	}
	slot := sm.slots[idx]
	return slot.Active && slot.Generation == id.Generation()
}

// Free releases a slot, incrementing its generation to invalidate old handles.
func (sm *SlotMap) Free(id WindowID) error {
	if !sm.Validate(id) {
		return fmt.Errorf("invalid or stale WindowID: %d", id)
	}
	idx := id.Index()
	sm.slots[idx].Active = false
	sm.slots[idx].Generation++
	sm.free = append(sm.free, idx)
	return nil
}

// Len returns the number of active slots.
func (sm *SlotMap) Len() int {
	count := 0
	for i := range sm.slots {
		if sm.slots[i].Active {
			count++
		}
	}
	return count
}
