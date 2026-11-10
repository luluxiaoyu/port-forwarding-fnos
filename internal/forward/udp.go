package forward

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

const (
	udpIdleTimeout = 60 * time.Second
	udpBufferSize  = 64 * 1024
	udpQueueSize   = 8
)

type udpRoute struct {
	client netip.AddrPort
	local  netip.Addr
	zone   int
}

type udpListener struct {
	*net.UDPConn
	v4 *ipv4.PacketConn
	v6 *ipv6.PacketConn
}

func newUDPListener(conn *net.UDPConn, wildcard bool, network string) (*udpListener, error) {
	l := &udpListener{UDPConn: conn}
	if !wildcard {
		return l, nil
	}
	if network == "udp4" {
		l.v4 = ipv4.NewPacketConn(conn)
		return l, l.v4.SetControlMessage(ipv4.FlagDst, true)
	}
	l.v6 = ipv6.NewPacketConn(conn)
	return l, l.v6.SetControlMessage(ipv6.FlagDst|ipv6.FlagInterface, true)
}

// Remember the NAS address the client used so replies from wildcard rules
// still come from that address on a NAS with multiple IPs.
func (l *udpListener) read(buf []byte) (int, udpRoute, error) {
	switch {
	case l.v4 != nil:
		n, info, addr, err := l.v4.ReadFrom(buf)
		if err != nil {
			return 0, udpRoute{}, err
		}
		route := udpRoute{client: addr.(*net.UDPAddr).AddrPort()}
		if info != nil {
			ip, _ := netip.AddrFromSlice(info.Dst)
			route.local = ip.Unmap()
		}
		return n, route, nil
	case l.v6 != nil:
		n, info, addr, err := l.v6.ReadFrom(buf)
		if err != nil {
			return 0, udpRoute{}, err
		}
		route := udpRoute{client: addr.(*net.UDPAddr).AddrPort()}
		if info != nil {
			route.local, _ = netip.AddrFromSlice(info.Dst)
			if route.local.IsLinkLocalUnicast() {
				route.zone = info.IfIndex
			}
		}
		return n, route, nil
	default:
		n, addr, err := l.ReadFromUDPAddrPort(buf)
		return n, udpRoute{client: addr}, err
	}
}

func (l *udpListener) reply(buf []byte, route udpRoute) {
	switch {
	case !route.local.IsValid() || route.local.IsMulticast():
		l.WriteToUDPAddrPort(buf, route.client)
	case l.v4 != nil:
		l.v4.WriteTo(buf, &ipv4.ControlMessage{Src: route.local.AsSlice()}, net.UDPAddrFromAddrPort(route.client))
	case l.v6 != nil:
		l.v6.WriteTo(buf, &ipv6.ControlMessage{Src: route.local.AsSlice(), IfIndex: route.zone}, net.UDPAddrFromAddrPort(route.client))
	}
}

func (r *relay) serveUDP(listener *udpListener) {
	var mu sync.Mutex
	peers := make(map[udpRoute]chan []byte)
	buffer := make([]byte, udpBufferSize)
	for {
		n, route, err := listener.read(buffer)
		if err != nil {
			if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EHOSTUNREACH) ||
				errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.ENOBUFS) {
				continue
			}
			r.failListener(err)
			return
		}
		mu.Lock()
		inbox := peers[route]
		if inbox == nil {
			select {
			case r.slots <- struct{}{}:
				inbox = make(chan []byte, udpQueueSize)
				peers[route] = inbox
				r.wg.Go(func() {
					defer func() {
						mu.Lock()
						delete(peers, route)
						<-r.slots
						mu.Unlock()
					}()
					r.forwardUDP(listener, route, inbox)
				})
			default:
			}
		}
		if inbox != nil && len(inbox) < cap(inbox) {
			inbox <- bytes.Clone(buffer[:n])
		}
		mu.Unlock()
	}
}

func (r *relay) forwardUDP(listener *udpListener, route udpRoute, inbox <-chan []byte) {
	ctx, cancel := context.WithCancel(r.ctx)
	target, err := r.dial(ctx)
	if err != nil {
		cancel()
		r.reportTarget(err)
		return
	}
	stopRemote := context.AfterFunc(ctx, func() { target.Close() })
	target.SetReadDeadline(time.Now().Add(udpIdleTimeout))

	var writer sync.WaitGroup
	writer.Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case packet := <-inbox:
				target.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if _, err := target.Write(packet); err != nil {
					if ctx.Err() == nil {
						r.reportTarget(err)
					}
					cancel()
					return
				}
				r.reportTarget(nil)
				target.SetReadDeadline(time.Now().Add(udpIdleTimeout))
			}
		}
	})
	defer func() {
		stopRemote()
		cancel()
		target.Close()
		writer.Wait()
	}()

	buffer := make([]byte, udpBufferSize)
	for {
		n, err := target.Read(buffer)
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, os.ErrDeadlineExceeded) {
				r.reportTarget(err)
			}
			return
		}
		r.reportTarget(nil)
		target.SetReadDeadline(time.Now().Add(udpIdleTimeout))
		// A single stale client must not interrupt other clients on this listener.
		listener.reply(buffer[:n], route)
	}
}
