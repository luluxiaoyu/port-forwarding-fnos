package main

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"port-forwarding-fnos/internal/forward"
)

const prefix = "/app/port-forwarding-fnos"
const api = prefix + "/api/rules"

func newHandler(rules *forward.Manager, web fs.FS, index []byte) http.Handler {
	mux := http.NewServeMux()
	serveIndex := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(index)
	}
	mux.HandleFunc("GET "+prefix, serveIndex)
	mux.HandleFunc("GET "+prefix+"/{$}", serveIndex)
	mux.Handle("GET "+prefix+"/assets/", http.StripPrefix(prefix, http.FileServerFS(web)))
	mux.HandleFunc("GET "+api, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, rules.List())
	})
	mux.HandleFunc("POST "+api, func(w http.ResponseWriter, r *http.Request) {
		var input forward.Endpoints
		if !decode(w, r, &input) {
			return
		}
		state, err := rules.Create(input)
		writeState(w, http.StatusCreated, state, err)
	})
	mux.HandleFunc("PUT "+api+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		var input forward.Endpoints
		if !decode(w, r, &input) {
			return
		}
		state, err := rules.Edit(r.PathValue("id"), input)
		writeState(w, http.StatusOK, state, err)
	})
	mux.HandleFunc("PUT "+api+"/{id}/enabled", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		if !decode(w, r, &input) {
			return
		}
		if input.Enabled == nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "缺少启用状态")
			return
		}
		state, err := rules.SetEnabled(r.PathValue("id"), *input.Enabled)
		writeState(w, http.StatusOK, state, err)
	})
	mux.HandleFunc("DELETE "+api+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := rules.Delete(r.PathValue("id")); err != nil {
			handleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		isAPI := r.URL.Path == api || strings.HasPrefix(r.URL.Path, api+"/")
		if isAPI {
			w.Header().Set("Cache-Control", "no-store")
			uid := r.Header.Values("X-Trim-Userid")
			admin := r.Header.Values("X-Trim-Isadmin")
			if len(uid) != 1 {
				writeError(w, http.StatusUnauthorized, "unauthenticated", "请先登录飞牛")
				return
			}
			if _, err := strconv.ParseUint(uid[0], 10, 32); err != nil {
				writeError(w, http.StatusUnauthorized, "unauthenticated", "请先登录飞牛")
				return
			}
			if len(admin) != 1 || admin[0] != "true" {
				writeError(w, http.StatusForbidden, "forbidden", "需要飞牛管理员权限")
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead &&
				(r.Header.Get("X-Port-Forwarding-Request") != "1" ||
					r.Header.Get("Sec-Fetch-Site") == "cross-site" || r.Header.Get("Sec-Fetch-Site") == "same-site") {
				writeError(w, http.StatusForbidden, "forbidden", "请求须来自应用页面")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_request", "请求内容必须是 JSON")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil || decoder.Decode(new(any)) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request", "请求数据格式有误")
		return false
	}
	return true
}

func writeState(w http.ResponseWriter, status int, state forward.State, err error) {
	if err != nil {
		handleError(w, err)
		return
	}
	writeJSON(w, status, state)
}

func handleError(w http.ResponseWriter, err error) {
	var ruleErr *forward.Error
	if !errors.As(err, &ruleErr) {
		log.Printf("保存或读取规则失败: %v", err)
		writeError(w, http.StatusInternalServerError, "storage_error", "无法保存规则，请检查应用日志")
		return
	}
	status := http.StatusBadRequest
	switch ruleErr.Code {
	case "not_found":
		status = http.StatusNotFound
	case "listen_failed", "rule_enabled":
		status = http.StatusConflict
	case "unavailable":
		status = http.StatusServiceUnavailable
	}
	writeError(w, status, ruleErr.Code, ruleErr.Message)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}
