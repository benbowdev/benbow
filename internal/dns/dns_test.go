// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package dns

import (
	"context"
	"encoding/binary"
	"net"
	"slices"
	"testing"
	"time"
)

func startServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = Server{Zones: []string{"test"}}.Serve(ctx, pc, ln); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return addr
}

func resolver(addr, network string) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}
}

func TestLookup(t *testing.T) {
	addr := startServer(t)
	for _, network := range []string{"udp", "tcp"} {
		r := resolver(addr, network)
		for _, name := range []string{"demo.test", "demo.covo.test", "DEMO.Covo.Test", "a.b.c.d.test"} {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			ips, err := r.LookupHost(ctx, name)
			cancel()
			if err != nil {
				t.Fatalf("%s %s: %v", network, name, err)
			}
			slices.Sort(ips)
			if !slices.Equal(ips, []string{"127.0.0.1", "::1"}) {
				t.Fatalf("%s %s = %v", network, name, ips)
			}
		}
		for _, name := range []string{"example.com", "test.example.com", "demo.localhost.", "testx"} {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if ips, err := r.LookupHost(ctx, name); err == nil {
				t.Fatalf("%s %s resolved to %v, want refused", network, name, ips)
			}
			cancel()
		}
	}
}

func query(name string, qtype uint16) []byte {
	msg := []byte{0x12, 0x34, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, l := range splitLabels(name) {
		msg = append(msg, byte(len(l)))
		msg = append(msg, l...)
	}
	msg = append(msg, 0)
	msg = binary.BigEndian.AppendUint16(msg, qtype)
	return binary.BigEndian.AppendUint16(msg, classIN)
}

func splitLabels(name string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(name); i++ {
		if i == len(name) || name[i] == '.' {
			out = append(out, name[start:i])
			start = i + 1
		}
	}
	return out
}

func TestHandle(t *testing.T) {
	s := Server{Zones: []string{"test"}}
	tests := []struct {
		name    string
		msg     []byte
		rcode   uint16
		answers uint16
	}{
		{"A", query("demo.test", typeA), 0, 1},
		{"AAAA", query("demo.test", typeAAAA), 0, 1},
		{"MX in zone", query("demo.test", 15), 0, 0},
		{"out of zone", query("example.com", typeA), rcodeRefused, 0},
		{"zone apex", query("test", typeA), 0, 1},
		{"look-alike", query("contest", typeA), rcodeRefused, 0},
		{"notify opcode", append([]byte{0, 1, 0x20, 0}, query("demo.test", typeA)[4:]...), rcodeNotImp, 0},
		{"two questions", append(append([]byte{0, 1, 1, 0, 0, 2}, query("demo.test", typeA)[6:]...), 0), rcodeFormErr, 0},
		{"pointer in question", []byte{0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0xC0, 12, 0, 1, 0, 1}, rcodeFormErr, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := s.Handle(tt.msg)
			if out == nil {
				t.Fatal("no reply")
			}
			if out[0] != tt.msg[0] || out[1] != tt.msg[1] {
				t.Error("id not echoed")
			}
			if rc := binary.BigEndian.Uint16(out[2:4]) & 0xF; rc != tt.rcode {
				t.Errorf("rcode = %d, want %d", rc, tt.rcode)
			}
			if an := binary.BigEndian.Uint16(out[6:8]); an != tt.answers {
				t.Errorf("answers = %d, want %d", an, tt.answers)
			}
		})
	}
	if s.Handle([]byte{1, 2, 3}) != nil {
		t.Error("a short message must be dropped")
	}
	resp := query("demo.test", typeA)
	resp[2] |= 0x80
	if s.Handle(resp) != nil {
		t.Error("a response must not be answered")
	}
}

func FuzzHandle(f *testing.F) {
	f.Add(query("demo.test", typeA))
	f.Add(query("example.com", typeAAAA))
	f.Add([]byte{0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 63})
	s := Server{Zones: []string{"test"}}
	f.Fuzz(func(t *testing.T, msg []byte) {
		out := s.Handle(msg)
		if out == nil {
			return
		}
		if len(out) < 12 || out[0] != msg[0] || out[1] != msg[1] || out[2]&0x80 == 0 {
			t.Fatalf("malformed reply %x to %x", out, msg)
		}
		if len(out) > maxUDP {
			t.Fatalf("reply of %d bytes exceeds UDP size", len(out))
		}
	})
}
