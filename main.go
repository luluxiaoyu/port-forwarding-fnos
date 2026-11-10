package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"port-forwarding-fnos/internal/forward"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()

	appDir, configDir, dataDir := os.Getenv("TRIM_APPDEST"), os.Getenv("TRIM_PKGETC"), os.Getenv("TRIM_PKGVAR")
	if appDir == "" || configDir == "" || dataDir == "" {
		return errors.New("缺少 TRIM_APPDEST、TRIM_PKGETC 或 TRIM_PKGVAR")
	}
	web, err := os.OpenRoot(filepath.Join(appDir, "www"))
	if err != nil {
		return err
	}
	defer web.Close()
	index, err := web.ReadFile("index.html")
	if err != nil {
		return err
	}
	assets, err := web.OpenRoot("assets")
	if err != nil {
		return err
	}
	defer assets.Close()
	socket := filepath.Join(appDir, "app.sock")
	listener, err := openSocket(socket, configDir)
	if err != nil {
		return err
	}
	defer listener.Close()
	rules, err := forward.Open(filepath.Join(configDir, "rules.json"))
	if err != nil {
		return err
	}
	defer rules.Close()

	srv := &http.Server{
		Handler: newHandler(rules, assets.FS(), index), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second,
		IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10,
	}
	result := make(chan error, 1)
	go func() { result <- srv.Serve(listener) }()
	defer srv.Close()
	ready := filepath.Join(dataDir, "server.ready")
	if err := os.WriteFile(ready, []byte(strconv.Itoa(os.Getpid())+"\n"), 0600); err != nil {
		return err
	}
	defer os.Remove(ready)
	log.Print("端口转发服务已启动")
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		timeout, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		return srv.Shutdown(timeout)
	}
}

func openSocket(path, configDir string) (*net.UnixListener, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("管理 Socket 路径已被占用：%s", path)
		}
		conn, err := net.DialTimeout("unix", path, time.Second)
		if err == nil {
			conn.Close()
			return nil, errors.New("管理 Socket 已有服务监听")
		}
		if !errors.Is(err, syscall.ECONNREFUSED) {
			return nil, err
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	mode := os.FileMode(0600)
	gid := -1
	if data, err := os.ReadFile(filepath.Join(configDir, "gateway-gid")); err == nil {
		value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 31)
		if err != nil {
			return nil, errors.New("gateway-gid 必须是飞牛网关专用组的数字 GID")
		}
		mode, gid = 0660, int(value)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	// Close the socket to other users from the instant it is created.
	mask := syscall.Umask(0177)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	syscall.Umask(mask)
	if err != nil {
		return nil, err
	}
	if gid >= 0 {
		err = os.Chown(path, -1, gid)
		if err == nil {
			err = os.Chmod(path, mode)
		}
	}
	if err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}
