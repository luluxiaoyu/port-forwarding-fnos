package forward

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

type Endpoints struct {
	ListenAddress string `json:"listenAddress"`
	ListenPort    int    `json:"listenPort"`
	TargetAddress string `json:"targetAddress"`
	TargetPort    int    `json:"targetPort"`
	Protocol      string `json:"protocol"`
	Remark        string `json:"remark,omitempty"`
}

type Rule struct {
	ID string `json:"id"`
	Endpoints
	Enabled bool `json:"enabled"`
}

type State struct {
	Rule
	Status      string `json:"status"`
	ListenError string `json:"listenError,omitempty"`
	TargetError string `json:"targetError,omitempty"`
}

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

type Manager struct {
	mu       sync.Mutex
	path     string
	rules    []Rule
	running  map[string]*relay
	failures map[string]string
	slots    map[string]chan struct{}
	closed   bool
}

func Open(path string) (*Manager, error) {
	m := &Manager{
		path: path, rules: []Rule{}, running: make(map[string]*relay),
		failures: make(map[string]string),
		slots:    map[string]chan struct{}{"tcp": make(chan struct{}, 256), "udp": make(chan struct{}, 256)},
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &m.rules); err != nil {
		return nil, fmt.Errorf("读取规则配置: %w", err)
	}
	ids := make(map[string]bool)
	for i, rule := range m.rules {
		if rule.ID == "" || ids[rule.ID] {
			return nil, fmt.Errorf("规则 ID 缺失或重复: %q", rule.ID)
		}
		ids[rule.ID] = true
		endpoints, err := normalize(rule.Endpoints)
		if err != nil {
			return nil, fmt.Errorf("规则 %s: %w", rule.ID, err)
		}
		m.rules[i].Endpoints = endpoints
	}
	for _, rule := range m.rules {
		if !rule.Enabled {
			continue
		}
		run, err := bind(rule, m.slots[rule.Protocol])
		if err != nil {
			m.failures[rule.ID] = err.Error()
			log.Printf("恢复规则 %s 失败: %v", rule.ID, err)
			continue
		}
		m.running[rule.ID] = run
		run.start()
	}
	return m, nil
}

func (m *Manager) List() []State {
	m.mu.Lock()
	defer m.mu.Unlock()
	states := make([]State, 0, len(m.rules))
	for _, rule := range m.rules {
		states = append(states, m.state(rule))
	}
	return states
}

func (m *Manager) state(rule Rule) State {
	state := State{Rule: rule, Status: "disabled"}
	if rule.Enabled {
		state.Status = "failed"
		state.ListenError = m.failures[rule.ID]
		if run := m.running[rule.ID]; run != nil {
			state.ListenError, state.TargetError = run.errors()
			if state.ListenError == "" {
				state.Status = "listening"
			}
		}
	}
	return state
}

func (m *Manager) Create(input Endpoints) (State, error) {
	endpoints, err := normalize(input)
	if err != nil {
		return State{}, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return State{}, err
	}
	rule := Rule{ID: hex.EncodeToString(id[:]), Endpoints: endpoints, Enabled: true}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enable(rule, append(slices.Clone(m.rules), rule))
}

func (m *Manager) Edit(id string, input Endpoints) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index, err := m.find(id)
	if err != nil {
		return State{}, err
	}
	rule := m.rules[index]
	if rule.Enabled {
		return State{}, &Error{"rule_enabled", "请先停用这条规则"}
	}
	endpoints, err := normalize(input)
	if err != nil {
		return State{}, err
	}
	rule.Endpoints = endpoints
	next := slices.Clone(m.rules)
	next[index] = rule
	if err := m.commit(next); err != nil {
		return State{}, err
	}
	return m.state(rule), nil
}

func (m *Manager) SetEnabled(id string, enabled bool) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index, err := m.find(id)
	if err != nil {
		return State{}, err
	}
	rule := m.rules[index]
	if rule.Enabled == enabled && (!enabled || m.state(rule).Status == "listening") {
		return m.state(rule), nil
	}
	rule.Enabled = enabled
	next := slices.Clone(m.rules)
	next[index] = rule
	if enabled {
		return m.enable(rule, next)
	}
	if err := m.commit(next); err != nil {
		return State{}, err
	}
	m.stop(rule.ID)
	return m.state(rule), nil
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	index, err := m.find(id)
	if err != nil {
		return err
	}
	next := slices.Delete(slices.Clone(m.rules), index, index+1)
	if err := m.commit(next); err != nil {
		return err
	}
	m.stop(id)
	return nil
}

func (m *Manager) find(id string) (int, error) {
	if m.closed {
		return -1, &Error{"unavailable", "应用正在停止"}
	}
	for i, rule := range m.rules {
		if rule.ID == id {
			return i, nil
		}
	}
	return -1, &Error{"not_found", "这条规则已不存在"}
}

func (m *Manager) enable(rule Rule, next []Rule) (State, error) {
	if m.closed {
		return State{}, &Error{"unavailable", "应用正在停止"}
	}
	run, err := bind(rule, m.slots[rule.Protocol])
	if err != nil {
		return State{}, err
	}
	if err := m.commit(next); err != nil {
		run.stop()
		return State{}, err
	}
	m.stop(rule.ID)
	m.running[rule.ID] = run
	run.start()
	return m.state(rule), nil
}

func (m *Manager) stop(id string) {
	if run := m.running[id]; run != nil {
		run.stop()
		delete(m.running, id)
	}
	delete(m.failures, id)
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for id := range m.running {
		m.stop(id)
	}
}

func (m *Manager) commit(next []Rule) error {
	file, err := os.CreateTemp(filepath.Dir(m.path), ".rules-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(next); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), m.path); err != nil {
		return err
	}
	m.rules = next
	dir, err := os.Open(filepath.Dir(m.path))
	if err == nil {
		err = dir.Sync()
		dir.Close()
	}
	if err != nil {
		log.Printf("同步规则目录失败: %v", err)
	}
	return nil
}

func normalize(input Endpoints) (Endpoints, error) {
	input.Protocol = strings.ToLower(strings.TrimSpace(input.Protocol))
	switch input.Protocol {
	case "":
		input.Protocol = "tcp"
	case "tcp", "udp":
	default:
		return Endpoints{}, &Error{"invalid_rule", "协议请选择 TCP 或 UDP"}
	}
	input.Remark = strings.TrimSpace(input.Remark)
	if utf8.RuneCountInString(input.Remark) > 120 {
		return Endpoints{}, &Error{"invalid_rule", "备注不能超过 120 个字符"}
	}
	input.ListenAddress = strings.TrimSpace(input.ListenAddress)
	if input.ListenAddress != "*" {
		address, err := netip.ParseAddr(input.ListenAddress)
		if err != nil || address.Zone() != "" {
			return Endpoints{}, &Error{"invalid_rule", "入口地址请输入 IPv4 或 IPv6 地址，或选择全部地址"}
		}
		input.ListenAddress = address.Unmap().String()
	}
	if input.ListenPort < 1 || input.ListenPort > 65535 || input.TargetPort < 1 || input.TargetPort > 65535 {
		return Endpoints{}, &Error{"invalid_rule", "端口必须是 1–65535 的整数"}
	}
	target := strings.ToLower(strings.TrimSpace(input.TargetAddress))
	if ip, err := netip.ParseAddr(target); err == nil {
		ip = ip.Unmap()
		if ip.IsUnspecified() || ip.Zone() != "" {
			return Endpoints{}, &Error{"invalid_rule", "服务地址请输入可连接的 IPv4、IPv6 或主机名"}
		}
		input.TargetAddress = ip.String()
	} else if validHostname(target) {
		input.TargetAddress = target
	} else {
		return Endpoints{}, &Error{"invalid_rule", "服务地址请输入 IPv4、IPv6 或主机名"}
	}
	return input, nil
}

func validHostname(value string) bool {
	host := strings.TrimSuffix(value, ".")
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
				return false
			}
		}
	}
	return true
}

func endpoint(address string, port int) string {
	return net.JoinHostPort(address, strconv.Itoa(port))
}
