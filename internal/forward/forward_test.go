package forward

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func listen(t *testing.T, port int) *net.TCPListener {
	t.Helper()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	return listener
}

func freePort(t *testing.T) int {
	t.Helper()
	listener := listen(t, 0)
	listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func dial(t *testing.T, port int) *net.TCPConn {
	t.Helper()
	conn, err := net.DialTimeout("tcp4", endpoint("127.0.0.1", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	return conn.(*net.TCPConn)
}

func open(t *testing.T, file string) *Manager {
	t.Helper()
	manager, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	return manager
}

func input(t *testing.T, target int) Endpoints {
	t.Helper()
	return Endpoints{ListenAddress: "127.0.0.1", ListenPort: freePort(t), TargetAddress: "127.0.0.1", TargetPort: target}
}

func create(t *testing.T, m *Manager, endpoints Endpoints) State {
	t.Helper()
	rule, err := m.Create(endpoints)
	if err != nil {
		t.Fatal(err)
	}
	return rule
}

func TestTCPHalfClose(t *testing.T) {
	target := listen(t, 0)
	target.SetDeadline(time.Now().Add(3 * time.Second))
	m := open(t, filepath.Join(t.TempDir(), "rules.json"))
	rule := create(t, m, input(t, target.Addr().(*net.TCPAddr).Port))

	client := dial(t, rule.ListenPort)
	remote, err := target.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	remote.SetDeadline(time.Now().Add(3 * time.Second))

	if _, err := client.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	if err := client.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if body, err := io.ReadAll(remote); err != nil || string(body) != "request" {
		t.Fatalf("target read = %q, %v", body, err)
	}
	if _, err := remote.Write([]byte("response after EOF")); err != nil {
		t.Fatal(err)
	}
	if err := remote.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if body, err := io.ReadAll(client); err != nil || string(body) != "response after EOF" {
		t.Fatalf("client read = %q, %v", body, err)
	}
}

func TestDisableClosesAndReleases(t *testing.T) {
	target := listen(t, 0)
	target.SetDeadline(time.Now().Add(3 * time.Second))
	file := filepath.Join(t.TempDir(), "rules.json")
	m := open(t, file)
	rule := create(t, m, input(t, target.Addr().(*net.TCPAddr).Port))
	client := dial(t, rule.ListenPort)
	remote, err := target.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	remote.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := m.Edit(rule.ID, rule.Endpoints); err == nil {
		t.Fatal("edited an enabled rule")
	}
	if state, err := m.SetEnabled(rule.ID, false); err != nil || state.Status != "disabled" {
		t.Fatalf("disable = %+v, %v", state, err)
	}
	for _, conn := range []*net.TCPConn{client, remote} {
		if _, err := conn.Read(make([]byte, 1)); err != io.EOF {
			t.Fatalf("expected connection to close, got %v", err)
		}
	}
	listen(t, rule.ListenPort)
	updated := rule.Endpoints
	updated.ListenPort = freePort(t)
	if _, err := m.Edit(rule.ID, updated); err != nil {
		t.Fatal(err)
	}
	m.Close()
	m = open(t, file)
	if actual := m.List()[0]; actual.Status != "disabled" || actual.Endpoints != updated {
		t.Fatalf("reloaded rule = %+v", actual)
	}
	if err := m.Delete(rule.ID); err != nil {
		t.Fatal(err)
	}
	m.Close()
	if list := open(t, file).List(); len(list) != 0 {
		t.Fatalf("deleted rule reappeared: %+v", list)
	}
}

func TestRestoreSkipsOccupiedRuleAndCanRetry(t *testing.T) {
	file := filepath.Join(t.TempDir(), "rules.json")
	m := open(t, file)
	first := create(t, m, input(t, 1))
	second := create(t, m, input(t, 1))
	if _, err := m.Create(first.Endpoints); err == nil {
		t.Fatal("created a duplicate listener")
	}
	m.Close()
	blocker := listen(t, first.ListenPort)
	m = open(t, file)
	states := m.List()
	if len(states) != 2 || states[0].Status != "failed" || states[0].ListenError == "" ||
		states[1].Status != "listening" || states[1].ID != second.ID {
		t.Fatalf("unexpected restored rules: %+v", states)
	}
	blocker.Close()
	if state, err := m.SetEnabled(first.ID, true); err != nil || state.Status != "listening" {
		t.Fatalf("retry = %+v, %v", state, err)
	}
}

func TestTargetRecoveryAndLoop(t *testing.T) {
	m := open(t, filepath.Join(t.TempDir(), "rules.json"))
	target := listen(t, 0)
	targetPort := target.Addr().(*net.TCPAddr).Port
	endpoints := input(t, targetPort)
	target.Close()
	rule := create(t, m, endpoints)
	client := dial(t, rule.ListenPort)
	if _, err := client.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("unreachable target should close connection: %v", err)
	}
	if state := m.List()[0]; state.Status != "listening" || state.TargetError == "" {
		t.Fatalf("state with unavailable target = %+v", state)
	}
	target = listen(t, targetPort)
	target.SetDeadline(time.Now().Add(3 * time.Second))
	recovered := dial(t, rule.ListenPort)
	remote, err := target.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	if _, err := remote.Write([]byte("back online")); err != nil {
		t.Fatal(err)
	}
	if err := remote.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if body, err := io.ReadAll(recovered); err != nil || string(body) != "back online" {
		t.Fatalf("recovered connection = %q, %v", body, err)
	}
	if state := m.List()[0]; state.Status != "listening" || state.TargetError != "" {
		t.Fatalf("state after target recovery = %+v", state)
	}

	port := freePort(t)
	for _, address := range []string{"127.0.0.1", "0.0.0.0"} {
		_, err := m.Create(Endpoints{ListenAddress: address, ListenPort: port, TargetAddress: "127.0.0.1", TargetPort: port})
		var failure *Error
		if !errors.As(err, &failure) || failure.Code != "invalid_rule" {
			t.Fatalf("self-forward through %s: %v", address, err)
		}
	}
	hostnameRule := create(t, m, Endpoints{ListenAddress: "127.0.0.1", ListenPort: port, TargetAddress: "localhost", TargetPort: port})
	loop := dial(t, hostnameRule.ListenPort)
	if _, err := loop.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("hostname loop did not close the connection: %v", err)
	}
	if last := m.List()[1]; last.TargetError == "" {
		t.Fatalf("hostname loop error = %+v", last)
	}
}

func TestWriteFailureLeavesRulesAndListenersUnchanged(t *testing.T) {
	dir := t.TempDir()
	m := open(t, filepath.Join(dir, "missing", "rules.json"))
	endpoints := input(t, 1)
	if _, err := m.Create(endpoints); err == nil || len(m.List()) != 0 {
		t.Fatalf("rule stored despite write failure: %v", err)
	}
	listen(t, endpoints.ListenPort)

	m = open(t, filepath.Join(dir, "rules.json"))
	rule := create(t, m, input(t, 1))
	if err := os.Remove(m.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(m.path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetEnabled(rule.ID, false); err == nil || m.List()[0].Status != "listening" {
		t.Fatalf("listener changed despite failed disable: %v", err)
	}
}
