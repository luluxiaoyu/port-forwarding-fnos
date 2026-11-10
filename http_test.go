package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"port-forwarding-fnos/internal/forward"
)

func TestManagementAPI(t *testing.T) {
	m, err := forward.Open(filepath.Join(t.TempDir(), "rules.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	assets := fstest.MapFS{"app.js": {Data: []byte("export {}")}}
	handler := newHandler(m, assets, []byte("<html></html>"))
	request := func(method, path, user, admin, marker, site, body string, want int) string {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if user != "" {
			r.Header.Set("X-Trim-Userid", user)
			r.Header.Set("X-Trim-Isadmin", admin)
		}
		if marker != "" {
			r.Header.Set("X-Port-Forwarding-Request", marker)
		}
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("management endpoint unexpectedly opened CORS")
		}
		return w.Body.String()
	}

	request("GET", prefix, "", "", "", "", "", http.StatusOK)
	request("GET", prefix+"/", "", "", "", "", "", http.StatusOK)
	request("GET", prefix+"/assets/app.js", "", "", "", "", "", http.StatusOK)
	request("GET", api, "", "", "", "", "", http.StatusUnauthorized)
	request("GET", prefix+"/%61pi/rules", "", "", "", "", "", http.StatusUnauthorized)
	request("GET", api, "1001", "false", "", "", "", http.StatusForbidden)
	request("POST", api, "1000", "true", "", "", "{}", http.StatusForbidden)
	request("POST", api, "1000", "true", "1", "cross-site", "{}", http.StatusForbidden)
	request("POST", api, "1000", "true", "1", "same-origin", "{} {}", http.StatusBadRequest)
	if list := request("GET", api, "1000", "true", "", "", "", http.StatusOK); list != "[]\n" {
		t.Fatalf("empty list = %s", list)
	}

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	body := fmt.Sprintf(`{"listenAddress":"127.0.0.1","listenPort":%d,"targetAddress":"127.0.0.1","targetPort":1}`, port)
	request("POST", api, "1000", "true", "1", "same-origin", body, http.StatusCreated)
	request("POST", api, "1000", "true", "1", "same-origin", body, http.StatusConflict)
	rulePath := api + "/" + m.List()[0].ID
	request("PUT", rulePath, "1000", "true", "1", "", body, http.StatusConflict)
	request("PUT", rulePath+"/enabled", "1000", "true", "1", "", `{"enabled":false}`, http.StatusOK)
	request("PUT", rulePath, "1000", "true", "1", "", body, http.StatusOK)
	request("DELETE", rulePath, "1001", "false", "1", "", "", http.StatusForbidden)
	request("DELETE", rulePath, "1000", "true", "1", "", "", http.StatusNoContent)
}

func TestStaticAssetsStayInsideAssetRoot(t *testing.T) {
	dir := t.TempDir()
	assetDir := filepath.Join(dir, "assets")
	if err := os.Mkdir(assetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "private.txt"), []byte("outside asset root"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../private.txt", filepath.Join(assetDir, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	assets, err := os.OpenRoot(assetDir)
	if err != nil {
		t.Fatal(err)
	}
	defer assets.Close()
	handler := newHandler(nil, assets.FS(), nil)
	for _, name := range []string{"escape.txt", "%2e%2e/private.txt", "%2e%2e%2fprivate.txt"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", prefix+"/assets/"+name, nil))
		if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "outside asset root") {
			t.Fatalf("%s escaped assets: status %d, %s", name, w.Code, w.Body.String())
		}
	}
}

func TestManagementSocketPermissions(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "forward-socket-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "app.sock")
	listener, err := openSocket(socket, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if info, err := os.Stat(socket); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("socket permissions = %v, %v", info, err)
	}
	if other, err := openSocket(socket, dir); err == nil {
		other.Close()
		t.Fatal("second service took over the socket")
	}
}
