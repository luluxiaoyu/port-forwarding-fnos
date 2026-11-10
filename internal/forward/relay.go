package forward

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"time"
)

type relay struct {
	rule      Rule
	listeners []net.Listener
	packets   []*udpListener
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	slots     chan struct{}

	mu          sync.Mutex
	listenError string
	targetError string
}

func bind(rule Rule, slots chan struct{}) (*relay, error) {
	if target, err := netip.ParseAddr(rule.TargetAddress); err == nil && pointsToListener(rule, target, rule.TargetPort) {
		return nil, &Error{"invalid_rule", "服务地址和端口指回了这条规则的入口"}
	}
	addresses := []string{rule.ListenAddress}
	if rule.ListenAddress == "*" {
		addresses = []string{"0.0.0.0", "::"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &relay{rule: rule, ctx: ctx, cancel: cancel, slots: slots}
	for _, address := range addresses {
		family := "6"
		if netip.MustParseAddr(address).Is4() {
			family = "4"
		}
		if err := run.bindSocket(rule.Protocol+family, endpoint(address, rule.ListenPort)); err != nil {
			run.abort()
			return nil, &Error{"listen_failed", fmt.Sprintf("无法监听 %s：%v", endpoint(address, rule.ListenPort), err)}
		}
	}
	return run, nil
}

func (r *relay) bindSocket(network, address string) error {
	if r.rule.Protocol == "udp" {
		local, err := net.ResolveUDPAddr(network, address)
		if err != nil {
			return err
		}
		conn, err := net.ListenUDP(network, local)
		if err != nil {
			return err
		}
		listener, err := newUDPListener(conn, local.IP.IsUnspecified(), network)
		if err != nil {
			conn.Close()
			return err
		}
		r.packets = append(r.packets, listener)
		return nil
	}
	listener, err := net.Listen(network, address)
	if err != nil {
		return err
	}
	r.listeners = append(r.listeners, listener)
	return nil
}

func pointsToListener(rule Rule, target netip.Addr, port int) bool {
	if port != rule.ListenPort {
		return false
	}
	target = target.Unmap()
	if rule.ListenAddress != "*" {
		address := netip.MustParseAddr(rule.ListenAddress)
		if address == target {
			return true
		}
		if !address.IsUnspecified() || address.Is4() != target.Is4() {
			return false
		}
	}
	if target.IsLoopback() || target.IsUnspecified() {
		return true
	}
	addrs, _ := net.InterfaceAddrs()
	for _, addr := range addrs {
		if prefix, err := netip.ParsePrefix(addr.String()); err == nil && prefix.Addr().Unmap() == target {
			return true
		}
	}
	return false
}

func (r *relay) start() {
	for _, listener := range r.listeners {
		r.wg.Go(func() { r.serveTCP(listener) })
	}
	for _, conn := range r.packets {
		r.wg.Go(func() { r.serveUDP(conn) })
	}
}

func (r *relay) serveTCP(listener net.Listener) {
	for {
		client, err := listener.Accept()
		if err != nil {
			r.failListener(err)
			return
		}
		select {
		case r.slots <- struct{}{}:
			r.wg.Go(func() {
				defer func() { <-r.slots }()
				r.forwardTCP(client.(*net.TCPConn))
			})
		default:
			client.Close()
		}
	}
}

func (r *relay) failListener(err error) {
	r.mu.Lock()
	if r.ctx.Err() == nil {
		r.listenError = fmt.Sprintf("监听中断：%v", err)
		r.abort()
	}
	r.mu.Unlock()
}

func (r *relay) errors() (string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listenError, r.targetError
}

func (r *relay) abort() {
	r.cancel()
	for _, listener := range r.listeners {
		listener.Close()
	}
	for _, conn := range r.packets {
		conn.Close()
	}
}

func (r *relay) stop() {
	r.abort()
	r.wg.Wait()
}

func (r *relay) dial(ctx context.Context) (net.Conn, error) {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	dialer.ControlContext = func(_ context.Context, _, address string, _ syscall.RawConn) error {
		target, err := netip.ParseAddrPort(address)
		if err == nil && (target.Addr().Unmap().IsUnspecified() || pointsToListener(r.rule, target.Addr(), int(target.Port()))) {
			return &Error{"invalid_rule", "服务地址解析到了当前入口或通配地址"}
		}
		return nil
	}
	return dialer.DialContext(ctx, r.rule.Protocol, endpoint(r.rule.TargetAddress, r.rule.TargetPort))
}

func (r *relay) reportTarget(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ctx.Err() != nil {
		return
	}
	r.targetError = ""
	if err != nil {
		r.targetError = fmt.Sprintf("转发到服务失败：%v", err)
	}
}

func (r *relay) forwardTCP(client *net.TCPConn) {
	defer client.Close()
	stopClient := context.AfterFunc(r.ctx, func() { client.Close() })
	defer stopClient()

	target, err := r.dial(r.ctx)
	r.reportTarget(err)
	if err != nil {
		return
	}
	remote := target.(*net.TCPConn)
	defer remote.Close()
	stopRemote := context.AfterFunc(r.ctx, func() { remote.Close() })
	defer stopRemote()

	var copyBoth sync.WaitGroup
	copyBoth.Go(func() { copyTCP(remote, client) })
	copyTCP(client, remote)
	copyBoth.Wait()
}

func copyTCP(dst, src *net.TCPConn) {
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		src.Close()
		return
	}
	dst.CloseWrite()
}
