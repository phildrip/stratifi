package server

import (
	"stratifi/internal/protocol/foreigntoplevel"
	"stratifi/internal/wire"
)

// Toplevel is one window's identity, advertised through
// ext-foreign-toplevel-list-v1 (used by the screen-share window picker, and
// by taskbars).
type Toplevel struct {
	Key        uint32 // stable per-window id (the layout WindowID)
	Title      string
	AppID      string
	Identifier string // opaque, unique for the window's lifetime
}

// toplevelList is one client's ext_foreign_toplevel_list_v1 object and the
// handle objects it has been given, keyed by toplevel.
type toplevelList struct {
	id      uint32
	handles map[uint32]uint32 // toplevel key -> handle object id
	active  bool
}

// SetToplevelProvider registers a reader for the current window list. The
// server calls it when a client binds the list, so the client receives the
// windows that already exist (the compositor owns the window state).
func (s *Server) SetToplevelProvider(fn func() []Toplevel) { s.toplevelProvider = fn }

func (s *Server) toplevelSnapshot() []Toplevel {
	if s.toplevelProvider == nil {
		return nil
	}
	return s.toplevelProvider()
}

// addToplevelList registers a new list object and emits the existing windows.
// Called from HandleWlRegistryBind with c.mu held.
func (c *Client) addToplevelList(id uint32) {
	if c.toplevelLists == nil {
		c.toplevelLists = make(map[uint32]*toplevelList)
		c.toplevelKeys = make(map[uint32]uint32)
	}
	l := &toplevelList{id: id, handles: make(map[uint32]uint32), active: true}
	c.toplevelLists[id] = l
	w := c.conn.WriterRef()
	for _, t := range c.server.toplevelSnapshot() {
		c.sendToplevelLocked(w, l, t)
	}
	_ = foreigntoplevel.SendExtForeignToplevelListV1Finished(w, id)
	_ = c.conn.Flush()
}

// sendToplevelLocked emits a handle for t on list l (c.mu held). The handle
// object id is taken above the client's high-water mark so it can never
// collide with a client-chosen id.
func (c *Client) sendToplevelLocked(w *wire.Writer, l *toplevelList, t Toplevel) {
	hid := c.objects.high
	c.registerObject(hid, ObjForeignToplevelHandle)
	l.handles[t.Key] = hid
	c.toplevelKeys[hid] = t.Key
	_ = foreigntoplevel.SendExtForeignToplevelListV1Toplevel(w, l.id, hid)
	c.sendHandleMetaLocked(w, hid, t)
}

// sendHandleMetaLocked sends the metadata triple followed by done (c.mu held).
func (c *Client) sendHandleMetaLocked(w *wire.Writer, hid uint32, t Toplevel) {
	_ = foreigntoplevel.SendExtForeignToplevelHandleV1Title(w, hid, t.Title)
	_ = foreigntoplevel.SendExtForeignToplevelHandleV1AppId(w, hid, t.AppID)
	_ = foreigntoplevel.SendExtForeignToplevelHandleV1Identifier(w, hid, t.Identifier)
	_ = foreigntoplevel.SendExtForeignToplevelHandleV1Done(w, hid)
}

// --- handlers ---

func (c *Client) HandleExtForeignToplevelListV1Stop(senderID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if l, ok := c.toplevelLists[senderID]; ok {
		l.active = false // no further toplevel events, object stays until destroy
	}
	return nil
}

func (c *Client) HandleExtForeignToplevelListV1Destroy(senderID uint32) error {
	c.mu.Lock()
	c.removeToplevelListLocked(senderID)
	c.unregisterObject(senderID)
	c.mu.Unlock()
	return nil
}

func (c *Client) HandleExtForeignToplevelHandleV1Destroy(senderID uint32) error {
	c.mu.Lock()
	c.removeHandleLocked(senderID)
	c.unregisterObject(senderID)
	c.mu.Unlock()
	return nil
}

func (c *Client) removeToplevelListLocked(id uint32) {
	l, ok := c.toplevelLists[id]
	if !ok {
		return
	}
	for _, hid := range l.handles {
		delete(c.toplevelKeys, hid)
	}
	delete(c.toplevelLists, id)
}

func (c *Client) removeHandleLocked(hid uint32) {
	key, ok := c.toplevelKeys[hid]
	if !ok {
		return
	}
	delete(c.toplevelKeys, hid)
	for _, l := range c.toplevelLists {
		if l.handles[key] == hid {
			delete(l.handles, key)
		}
	}
}

// --- notifications from the compositor ---

// NotifyToplevelOpened announces a new window to every listening client.
func (s *Server) NotifyToplevelOpened(t Toplevel) {
	for _, c := range s.clientsSnapshot() {
		c.mu.Lock()
		if len(c.toplevelLists) > 0 {
			w := c.conn.WriterRef()
			for _, l := range c.toplevelLists {
				if l.active {
					c.sendToplevelLocked(w, l, t)
				}
			}
			_ = c.conn.Flush()
		}
		c.mu.Unlock()
	}
}

// NotifyToplevelChanged updates a window's title/app-id (and done).
func (s *Server) NotifyToplevelChanged(t Toplevel) {
	for _, c := range s.clientsSnapshot() {
		c.mu.Lock()
		w := c.conn.WriterRef()
		sent := false
		for _, l := range c.toplevelLists {
			if !l.active {
				continue
			}
			if hid, ok := l.handles[t.Key]; ok {
				c.sendHandleMetaLocked(w, hid, t)
				sent = true
			}
		}
		if sent {
			_ = c.conn.Flush()
		}
		c.mu.Unlock()
	}
}

// NotifyToplevelClosed tells clients a window is gone; the handle is dropped.
func (s *Server) NotifyToplevelClosed(key uint32) {
	for _, c := range s.clientsSnapshot() {
		c.mu.Lock()
		w := c.conn.WriterRef()
		sent := false
		for _, l := range c.toplevelLists {
			if !l.active {
				continue
			}
			if hid, ok := l.handles[key]; ok {
				_ = foreigntoplevel.SendExtForeignToplevelHandleV1Closed(w, hid)
				delete(l.handles, key)
				delete(c.toplevelKeys, hid)
				sent = true
			}
		}
		if sent {
			_ = c.conn.Flush()
		}
		c.mu.Unlock()
	}
}

// clientsSnapshot copies the client set (safe to iterate without clientMu).
func (s *Server) clientsSnapshot() []*Client {
	s.clientMu.RLock()
	defer s.clientMu.RUnlock()
	out := make([]*Client, 0, len(s.clients))
	for _, c := range s.clients {
		out = append(out, c)
	}
	return out
}
