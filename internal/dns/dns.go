// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package dns is the tiny DNS server behind Covo's .test domains. macOS
// sends it queries for the zones listed in /etc/resolver; it answers every
// name in those zones with the loopback addresses and refuses anything else.
// It listens on 127.0.0.1 only, on a high port, as the user.
package dns

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	ttl        = 60
	maxUDP     = 512
	maxTCP     = 4096
	tcpTimeout = 5 * time.Second

	typeA    = 1
	typeAAAA = 28
	classIN  = 1

	rcodeFormErr = 1
	rcodeNotImp  = 4
	rcodeRefused = 5
)

// Server answers queries for Zones.
type Server struct {
	Zones []string // e.g. "test"
}

func (s Server) inZone(name string) bool {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	for _, z := range s.Zones {
		if name == z || strings.HasSuffix(name, "."+z) {
			return true
		}
	}
	return false
}

// Handle answers one DNS message. It returns nil when the message is too
// broken to answer at all.
func (s Server) Handle(msg []byte) []byte {
	if len(msg) < 12 {
		return nil
	}
	id := msg[0:2]
	flags := binary.BigEndian.Uint16(msg[2:4])
	if flags&0x8000 != 0 { // a response, not a query
		return nil
	}
	opcode := (flags >> 11) & 0xF
	rd := flags & 0x0100
	qd := binary.BigEndian.Uint16(msg[4:6])

	reply := func(rcode uint16, question []byte, answers ...[]byte) []byte {
		out := make([]byte, 12, 12+len(question)+64)
		copy(out[0:2], id)
		f := 0x8000 | opcode<<11 | rd | rcode
		if rcode == 0 {
			f |= 0x0400 // authoritative
		}
		binary.BigEndian.PutUint16(out[2:4], f)
		if question != nil {
			binary.BigEndian.PutUint16(out[4:6], 1)
		}
		binary.BigEndian.PutUint16(out[6:8], uint16(len(answers)))
		out = append(out, question...)
		for _, a := range answers {
			out = append(out, a...)
		}
		return out
	}

	if opcode != 0 {
		return reply(rcodeNotImp, nil)
	}
	if qd != 1 {
		return reply(rcodeFormErr, nil)
	}
	name, end, err := parseName(msg, 12)
	if err != nil || len(msg) < end+4 {
		return reply(rcodeFormErr, nil)
	}
	question := msg[12 : end+4]
	qtype := binary.BigEndian.Uint16(msg[end : end+2])
	qclass := binary.BigEndian.Uint16(msg[end+2 : end+4])
	if qclass != classIN || !s.inZone(name) {
		return reply(rcodeRefused, question)
	}
	switch qtype {
	case typeA:
		return reply(0, question, answer(typeA, net.IPv4(127, 0, 0, 1).To4()))
	case typeAAAA:
		return reply(0, question, answer(typeAAAA, net.IPv6loopback))
	default:
		return reply(0, question) // the name exists, no record of that type
	}
}

// answer is a resource record whose name points back at the question.
func answer(rtype uint16, ip net.IP) []byte {
	rr := []byte{0xC0, 12} // pointer to the name at offset 12
	rr = binary.BigEndian.AppendUint16(rr, rtype)
	rr = binary.BigEndian.AppendUint16(rr, classIN)
	rr = binary.BigEndian.AppendUint32(rr, ttl)
	rr = binary.BigEndian.AppendUint16(rr, uint16(len(ip)))
	return append(rr, ip...)
}

// parseName reads an uncompressed name at off; queries carry no pointers.
func parseName(msg []byte, off int) (string, int, error) {
	var labels []string
	total := 0
	for {
		if off >= len(msg) {
			return "", 0, errors.New("truncated name")
		}
		n := int(msg[off])
		off++
		if n == 0 {
			break
		}
		if n > 63 {
			return "", 0, errors.New("bad label")
		}
		if off+n > len(msg) {
			return "", 0, errors.New("truncated label")
		}
		total += n + 1
		if total > 255 {
			return "", 0, errors.New("name too long")
		}
		labels = append(labels, string(msg[off:off+n]))
		off += n
	}
	return strings.Join(labels, "."), off, nil
}

// ListenAndServe serves UDP and TCP on addr until ctx is done.
func (s Server) ListenAndServe(ctx context.Context, addr string) error {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("listen udp %s: %w", addr, err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		_ = pc.Close()
		return fmt.Errorf("listen tcp %s: %w", addr, err)
	}
	return s.Serve(ctx, pc, ln)
}

// Serve answers on pc and ln until ctx is done, then closes them.
func (s Server) Serve(ctx context.Context, pc net.PacketConn, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = pc.Close()
		_ = ln.Close()
	}()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.serveUDP(pc) }()
	go func() { defer wg.Done(); s.serveTCP(ln) }()
	wg.Wait()
	return fmt.Errorf("dns server: %w", ctx.Err())
}

func (s Server) serveUDP(pc net.PacketConn) {
	buf := make([]byte, maxUDP)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		if out := s.Handle(buf[:n]); out != nil {
			_, _ = pc.WriteTo(out, from)
		}
	}
}

func (s Server) serveTCP(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go s.serveConn(conn)
	}
}

func (s Server) serveConn(conn net.Conn) {
	defer conn.Close()
	for {
		_ = conn.SetDeadline(time.Now().Add(tcpTimeout))
		var size [2]byte
		if _, err := io.ReadFull(conn, size[:]); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(size[:]))
		if n > maxTCP {
			return
		}
		msg := make([]byte, n)
		if _, err := io.ReadFull(conn, msg); err != nil {
			return
		}
		out := s.Handle(msg)
		if out == nil {
			return
		}
		if _, err := conn.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(out))), out...)); err != nil {
			return
		}
	}
}
