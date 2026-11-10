package forward

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func listenUDP(t *testing.T, network string, address *net.UDPAddr) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP(network, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(4 * time.Second))
	return conn
}

func requireIPv6UDP(t *testing.T) {
	t.Helper()
	probe, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback})
	if err != nil {
		t.Skipf("IPv6 unavailable: %v", err)
	}
	probe.Close()
}

func TestDualStackUDPClientsAndPersistence(t *testing.T) {
	requireIPv6UDP(t)
	file := filepath.Join(t.TempDir(), "rules.json")
	m := open(t, file)
	target := listenUDP(t, "udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	spare := listenUDP(t, "udp4", &net.UDPAddr{IP: net.IPv4zero})
	port := spare.LocalAddr().(*net.UDPAddr).Port
	spare.Close()
	endpoints := Endpoints{
		ListenAddress: "*", ListenPort: port, TargetAddress: "127.0.0.1",
		TargetPort: target.LocalAddr().(*net.UDPAddr).Port, Protocol: "udp", Remark: " 游戏服务 ",
	}
	rule := create(t, m, endpoints)
	tcp := endpoints
	tcp.Protocol = "tcp"
	create(t, m, tcp) // The TCP listener can use the same port.

	type peer struct {
		network string
		ip      net.IP
		payload byte
	}
	peers := []peer{
		{"udp4", net.IPv4(127, 0, 0, 1), 'a'},
		{"udp4", net.IPv4(127, 0, 0, 1), 'b'},
		{"udp6", net.IPv6loopback, 'c'},
	}
	if runtime.GOOS == "linux" {
		// Linux routes 127/8 to loopback. A connected client to this alias
		// accepts the reply only when it comes from the destination it used.
		peers = append(peers, peer{"udp4", net.IPv4(127, 0, 0, 2), 'd'})
	}
	upstreams := make(map[byte]string)
	for _, p := range peers {
		client, err := net.DialUDP(p.network, nil, &net.UDPAddr{IP: p.ip, Port: port})
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		client.SetDeadline(time.Now().Add(4 * time.Second))
		if _, err := client.Write([]byte{p.payload}); err != nil {
			t.Fatal(err)
		}
		data := make([]byte, 16)
		n, upstream, err := target.ReadFromUDP(data)
		if err != nil || n != 1 || data[0] != p.payload {
			t.Fatalf("target datagram = %q, %v", data[:n], err)
		}
		for _, previous := range upstreams {
			if upstream.String() == previous {
				t.Fatalf("clients shared upstream address %s", upstream)
			}
		}
		upstreams[p.payload] = upstream.String()
		if _, err := target.WriteToUDP([]byte{'r', p.payload}, upstream); err != nil {
			t.Fatal(err)
		}
		if n, err := client.Read(data); err != nil || string(data[:n]) != "r"+string(p.payload) {
			t.Fatalf("%s reply = %q, %v", p.network, data[:n], err)
		}
	}
	m.Close()
	m = open(t, file)
	list := m.List()
	if len(list) != 2 || list[0].ID != rule.ID || list[0].Protocol != "udp" || list[0].Remark != "游戏服务" ||
		list[0].Status != "listening" || list[1].Status != "listening" {
		t.Fatalf("restored rules = %+v", list)
	}
	if _, err := m.SetEnabled(rule.ID, false); err != nil {
		t.Fatal(err)
	}
	listenUDP(t, "udp4", &net.UDPAddr{IP: net.IPv4zero, Port: port})
	listenUDP(t, "udp6", &net.UDPAddr{IP: net.IPv6zero, Port: port})
}

func TestDualStackRollback(t *testing.T) {
	requireIPv6UDP(t)
	m := open(t, filepath.Join(t.TempDir(), "rules.json"))
	blocker := listenUDP(t, "udp6", &net.UDPAddr{IP: net.IPv6zero})
	port := blocker.LocalAddr().(*net.UDPAddr).Port
	_, err := m.Create(Endpoints{ListenAddress: "*", ListenPort: port, TargetAddress: "127.0.0.1", TargetPort: 1, Protocol: "udp"})
	if err == nil || len(m.List()) != 0 {
		t.Fatalf("partial rule created: %v", err)
	}
	listenUDP(t, "udp4", &net.UDPAddr{IP: net.IPv4zero, Port: port})
}

func TestLegacyRuleDefaultsToTCP(t *testing.T) {
	file := filepath.Join(t.TempDir(), "rules.json")
	const old = `[{"id":"legacy","listenAddress":"127.0.0.1","listenPort":5000,"targetAddress":"localhost","targetPort":6000,"enabled":false}]`
	if err := os.WriteFile(file, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	list := open(t, file).List()
	if len(list) != 1 || list[0].Protocol != "tcp" || list[0].Remark != "" || list[0].Enabled {
		t.Fatalf("legacy rule = %+v", list)
	}
}
