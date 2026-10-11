// Package portal bridges the compositor to xdg-desktop-portal's ScreenCast
// interface, the Firefox/WebRTC screen-sharing path (stratifi-bro).
//
// Portal flow (xdg-desktop-portal ScreenCast v1):
//  1. CreateSession(a{sv}) → o request_handle; session appears as an object
//     whose Response signal carries the session_handle.
//  2. SelectSources(o session_handle, a{sv}) → o request_handle.
//  3. Start(o session_handle, s parent_window, a{sv}) → Response with
//     a(ua{sv}) streams; each stream's node_id identifies the PipeWire node
//     the compositor must feed.
package portal

import (
	"fmt"
	"strconv"

	dbus "stratifi/internal/portal/dbus"
)

// Portal drives ScreenCast sessions on one bus connection.
type Portal struct {
	conn    *dbus.Conn
	counter int
}

// NewPortal wraps an existing bus connection.
func NewPortal(conn *dbus.Conn) *Portal { return &Portal{conn: conn} }

// StreamInfo describes one portal-selected capture stream.
type StreamInfo struct {
	NodeID uint32
}

// PortalError wraps a portal Response code (0 success, 1 denied, 2 cancelled).
type PortalError struct{ Code uint32 }

func (e *PortalError) Error() string {
	switch e.Code {
	case 1:
		return "portal: source selection denied"
	case 2:
		return "portal: selection cancelled"
	default:
		return fmt.Sprintf("portal: response code %d", e.Code)
	}
}

// CreateSession opens a new ScreenCast session and returns its object path.
func (p *Portal) CreateSession() (string, error) {
	p.counter++
	token := "stratifi_session_" + strconv.Itoa(p.counter)

	msg := &dbus.Msg{
		Type:        dbus.TypeMethodCall,
		BodySig:     "a{sv}",
		Path:        dbus.PathPortal,
		Interface:   dbus.IfaceScreenCast,
		Member:      "CreateSession",
		Destination: dbus.DestPortal,
	}

	var w dbus.Writer
	start := w.BeginDict()
	w.DictEntry("handle_token", token)
	w.FinishDict(start)
	msg.Body = w.Bytes()

	reply, err := p.conn.Call(msg)
	if err != nil {
		return "", err
	}
	r := dbus.NewReader(reply.Body)
	path, err := r.ReadString() // signature "o"
	if err != nil {
		return "", err
	}
	return path, nil
}

// SelectSources restricts the session to monitor capture with cursors
// embedded in the frame (metadata=0, cursor_mode=1).
func (p *Portal) SelectSources(sessionHandle string) (string, error) {
	p.counter++
	token := "stratifi_select_" + strconv.Itoa(p.counter)

	msg := &dbus.Msg{
		Type:        dbus.TypeMethodCall,
		BodySig:     "oa{sv}",
		Path:        sessionHandle,
		Interface:   dbus.IfaceScreenCast,
		Member:      "SelectSources",
		Destination: dbus.DestPortal,
	}

	var w dbus.Writer
	w.WriteString(sessionHandle)
	start := w.BeginDict()
	w.DictEntry("handle_token", token)
	w.DictEntryUint("types", 1)       // 1 = monitors
	w.DictEntryUint("cursor_mode", 1) // 1 = hidden (compositor draws)
	w.FinishDict(start)
	msg.Body = w.Bytes()

	reply, err := p.conn.Call(msg)
	if err != nil {
		return "", err
	}
	r := dbus.NewReader(reply.Body)
	path, err := r.ReadString() // signature "o"
	if err != nil {
		return "", err
	}
	return path, nil
}

// Start arms the session. The portal answers via a Request Response signal
// carrying the stream list; this call returns the request handle that the
// compositor listens on.
func (p *Portal) Start(sessionHandle, parentWindow string) (string, error) {
	p.counter++
	token := "stratifi_start_" + strconv.Itoa(p.counter)

	msg := &dbus.Msg{
		Type:        dbus.TypeMethodCall,
		BodySig:     "ssa{sv}",
		Path:        sessionHandle,
		Interface:   dbus.IfaceScreenCast,
		Member:      "Start",
		Destination: dbus.DestPortal,
	}

	var w dbus.Writer
	w.WriteString(sessionHandle)
	w.WriteString(parentWindow)
	start := w.BeginDict()
	w.DictEntry("handle_token", token)
	w.FinishDict(start)
	msg.Body = w.Bytes()

	reply, err := p.conn.Call(msg)
	if err != nil {
		return "", err
	}
	r := dbus.NewReader(reply.Body)
	return r.ReadString() // signature "o" — request handle
}

// ParseStartResults decodes a Response signal body (u code, a{sv} results)
// into stream infos. The "streams" variant holds a(ua{sv}).
func ParseStartResults(body []byte) (uint32, []StreamInfo, error) {
	r := dbus.NewReader(body)
	code, err := r.ReadUint32()
	if err != nil {
		return 0, nil, err
	}
	if code != 0 {
		return code, nil, &PortalError{Code: code}
	}

	// results a{sv}: u32 length (8-aligned), then entries {s, v}.
	for r.Remaining() > 0 {
		for r.Offset()%8 != 0 {
			skip := 8 - r.Offset()%8
			if skip > r.Remaining() {
				break
			}
			_, _ = r.Skip(skip)
		}
		if r.Remaining() < 4 {
			break
		}
		key, err := r.ReadString()
		if err != nil {
			return code, nil, err
		}
		// variant: 4-align, sig g, boxed value
		for r.Offset()%4 != 0 {
			if r.Remaining() == 0 {
				break
			}
			_, _ = r.Skip(1)
		}
		sig, err := r.ReadSignature()
		if err != nil {
			return code, nil, err
		}
		if key != "streams" {
			continue
		}
		if sig != "a(ua{sv})" {
			return code, nil, fmt.Errorf("portal: unexpected streams sig %q", sig)
		}
		streams, err := parseStreams(r)
		if err != nil {
			return code, nil, err
		}
		return code, streams, nil
	}
	return code, nil, nil
}

func parseStreams(r *dbus.Reader) ([]StreamInfo, error) {
	// a(ua{sv}): u32 len, then structs (u node_id, a{sv} props).
	if _, err := r.ReadUint32(); err != nil {
		return nil, err
	}
	var out []StreamInfo
	for r.Remaining() >= 8 {
		id, err := r.ReadUint32()
		if err != nil {
			return out, err
		}
		out = append(out, StreamInfo{NodeID: id})
		// props a{sv}: length + contents; we skip since node_id is all we need.
		l, err := r.ReadUint32()
		if err != nil {
			return out, err
		}
		if int(l) > r.Remaining() {
			return out, nil
		}
		_, _ = r.Skip(int(l))
	}
	return out, nil
}
