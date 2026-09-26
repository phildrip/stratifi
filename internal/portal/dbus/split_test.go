package dbus

import (
	"net"
	"path/filepath"
	"testing"
)

// TestRecvReplySplitFrames drives the client against a stub that sends the
// method return in two chunks — the first splitting the message mid-header,
// the second carrying the rest plus an unrelated interleaved message.
func TestRecvReplySplitAndInterleaved(t *testing.T) {
	// Build the reply we want (serial 7) and an unrelated signal.
	var bw Writer
	bw.WriteString("the.reply.value")
	reply := buildMsg(t, &Msg{
		Type:        TypeMethodReturn,
		Serial:      99,
		ReplySerial: 7,
		Body:        bw.Bytes(),
		BodySig:     "s",
	})

	var sw Writer
	sw.WriteString("noise")
	inter := buildMsg(t, &Msg{
		Type:      TypeSignal,
		Serial:    100,
		Interface: "test.iface",
		Member:    "Noise",
		Path:      "/noise",
		Body:      sw.Bytes(),
		BodySig:   "s",
	})

	// Split: first 30 bytes of reply (mid-header), then interleave, then rest.
	sockPath := filepath.Join(t.TempDir(), "split-bus")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.AcceptUnix()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 256)
		if _, err := c.Read(buf); err != nil {
			return
		}
		if _, err := c.Write([]byte("OK 1\r\n")); err != nil {
			return
		}
		if _, err := c.Read(buf); err != nil {
			return
		}
		// First message is Hello: reply so the handshake completes.
		var hw Writer
		hw.WriteString("org.freedesktop.DBus.UniqueName:1-1")
		helloReply := buildMsg(t, &Msg{
			Type:        TypeMethodReturn,
			Serial:      1,
			ReplySerial: 1,
			Body:        hw.Bytes(),
			BodySig:     "s",
		})
		if _, err := c.Write(helloReply); err != nil {
			return
		}
		if _, err := c.Read(buf); err != nil {
			return
		}
		// Second message: the tracked call. Interleave a complete unrelated
		// signal first, then split the reply across two reads (mid-header).
		_, _ = c.Write(inter)
		_, _ = c.Write(reply[:30])
		_, _ = c.Write(reply[30:])
	}()

	c, err := Connect("unix:path=" + sockPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.nc.Close()

	// Send the call the stub is going to answer in split frames.
	if err := c.send(&Msg{
		Type:        TypeMethodCall,
		Serial:      7,
		Interface:   "test.iface",
		Member:      "Get",
		Path:        "/test",
		Destination: "test.dest",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	m, err := c.recvReply(7)
	if err != nil {
		t.Fatalf("recvReply: %v", err)
	}
	if m.ReplySerial != 7 || m.Type != TypeMethodReturn {
		t.Fatalf("msg = type %d serial %d", m.Type, m.ReplySerial)
	}
	r := NewReader(m.Body)
	v, err := r.ReadString()
	if err != nil || v != "the.reply.value" {
		t.Errorf("body = %q err=%v", v, err)
	}
}

// buildMsg serialises m with a fresh writer (test helper).
func buildMsg(t *testing.T, m *Msg) []byte {
	t.Helper()
	var w Writer
	w.Build(m)
	return w.Bytes()
}

// TestMessageTotalSpan pins framing math used by the accumulator.
func TestMessageTotalSpan(t *testing.T) {
	var bw Writer
	bw.WriteString("abc")
	var w Writer
	w.Build(&Msg{
		Type:        TypeMethodReturn,
		Serial:      1,
		ReplySerial: 1,
		Body:        bw.Bytes(),
		BodySig:     "s",
	})
	span, err := MessageTotalSpan(w.Bytes())
	if err != nil {
		t.Fatalf("span: %v", err)
	}
	if span != len(w.Bytes()) {
		t.Errorf("span = %d, want %d", span, len(w.Bytes()))
	}
	if _, err := MessageTotalSpan([]byte{0}); err == nil {
		t.Error("expected error for short buffer")
	}
}
