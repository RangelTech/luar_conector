// Hub do conector de computadores (spec 11 §26): recebe as conexões de saída dos computadores,
// mantém a presença (online/offline) e repassa bytes entre o ssh do ambiente da empresa e o
// servidor SSH embutido no computador. O hub não conhece segredos de SSH: só encaminha.
package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/RangelTech/luar_conector/internal/wsconn"
)

const (
	pingEvery    = 15 * time.Second
	offlineAfter = 45 * time.Second
	codeTTL      = 10 * time.Minute
	maxHosts     = 50 // teto antiabuso por tenant (spec 11 §26.7)
)

type host struct {
	HostID    string    `json:"host_id"`
	TenantID  string    `json:"tenant_id"`
	Name      string    `json:"name"`
	TokenHash string    `json:"token_hash,omitempty"`
	ConnPub   string    `json:"conn_pub"`
	OS        string    `json:"os,omitempty"`
	Arch      string    `json:"arch,omitempty"`
	Version   string    `json:"version,omitempty"`
	LastSeen  time.Time `json:"last_seen"`
	Paired    bool      `json:"paired"`
}

type pairing struct {
	HostID  string
	Expires time.Time
}

type hub struct {
	mu       sync.Mutex
	hosts    map[string]*host
	codes    map[string]pairing
	ctrl     map[string]*ctrlConn
	waiting  map[string]chan io.ReadWriteCloser
	dataDir  string
	internal string
	secret   []byte
	upgrader websocket.Upgrader
}

type ctrlConn struct {
	ws  *websocket.Conn
	wmu sync.Mutex
}

func (c *ctrlConn) send(v any) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.ws.WriteJSON(v)
}

func newHub() *hub {
	h := &hub{
		hosts: map[string]*host{}, codes: map[string]pairing{}, ctrl: map[string]*ctrlConn{},
		waiting: map[string]chan io.ReadWriteCloser{},
		dataDir: env("HUB_DATA", "./data"), internal: os.Getenv("HUB_INTERNAL_TOKEN"),
		secret:   []byte(os.Getenv("HUB_RELAY_SECRET")),
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }, ReadBufferSize: 32 << 10, WriteBufferSize: 32 << 10},
	}
	_ = os.MkdirAll(h.dataDir, 0o700)
	if b, err := os.ReadFile(filepath.Join(h.dataDir, "hub.json")); err == nil {
		_ = json.Unmarshal(b, &h.hosts)
	}
	return h
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func (h *hub) save() {
	b, _ := json.Marshal(h.hosts)
	tmp := filepath.Join(h.dataDir, "hub.json.tmp")
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, filepath.Join(h.dataDir, "hub.json"))
	}
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func hashTok(t string) string { s := sha256.Sum256([]byte(t)); return hex.EncodeToString(s[:]) }

func (h *hub) auth(r *http.Request) bool {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return h.internal != "" && subtle.ConstantTimeCompare([]byte(got), []byte(h.internal)) == 1
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// ---- interno (engine) ------------------------------------------------------------------

func (h *hub) handlePair(w http.ResponseWriter, r *http.Request) {
	if !h.auth(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	var in struct {
		HostID, TenantID, Name, ConnPub string `json:"-"`
	}
	var body map[string]string
	if json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	in.HostID, in.TenantID, in.Name, in.ConnPub = body["host_id"], body["tenant_id"], body["name"], body["conn_pub"]
	if in.HostID == "" || in.TenantID == "" || in.ConnPub == "" {
		http.Error(w, "bad request", 400)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	count := 0
	for _, x := range h.hosts {
		if x.TenantID == in.TenantID && x.HostID != in.HostID {
			count++
		}
	}
	if count >= maxHosts {
		http.Error(w, "limite de computadores do tenant", 409)
		return
	}
	ex := h.hosts[in.HostID]
	if ex == nil {
		ex = &host{HostID: in.HostID, TenantID: in.TenantID}
		h.hosts[in.HostID] = ex
	}
	ex.Name, ex.ConnPub, ex.TokenHash, ex.Paired = in.Name, in.ConnPub, "", false // repareamento invalida o token antigo
	code := strings.ToUpper(randHex(4))
	h.codes[code] = pairing{HostID: in.HostID, Expires: time.Now().Add(codeTTL)}
	h.save()
	writeJSON(w, 200, map[string]any{"code": code, "expires_in": int(codeTTL.Seconds())})
}

func (h *hub) status(x *host) map[string]any {
	_, live := h.ctrl[x.HostID]
	online := live && time.Since(x.LastSeen) < offlineAfter
	return map[string]any{"host_id": x.HostID, "tenant_id": x.TenantID, "name": x.Name, "paired": x.Paired,
		"online": online, "last_seen": x.LastSeen, "os": x.OS, "arch": x.Arch, "version": x.Version}
}

func (h *hub) handleHosts(w http.ResponseWriter, r *http.Request) {
	if !h.auth(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	tenant := r.URL.Query().Get("tenant_id")
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []map[string]any{}
	for _, x := range h.hosts {
		if tenant == "" || x.TenantID == tenant {
			out = append(out, h.status(x))
		}
	}
	writeJSON(w, 200, out)
}

func (h *hub) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if !h.auth(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/internal/hosts/")
	h.mu.Lock()
	delete(h.hosts, id)
	if c := h.ctrl[id]; c != nil {
		_ = c.ws.Close()
		delete(h.ctrl, id)
	}
	h.save()
	h.mu.Unlock()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (h *hub) signRelay(hostID string, ttl time.Duration) string {
	payload := hostID + "|" + strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)
	m := hmac.New(sha256.New, h.secret)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(m.Sum(nil))
}

func (h *hub) verifyRelay(tok string) (string, bool) {
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 || len(h.secret) == 0 {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	m := hmac.New(sha256.New, h.secret)
	m.Write(raw)
	if !hmac.Equal([]byte(hex.EncodeToString(m.Sum(nil))), []byte(parts[1])) {
		return "", false
	}
	f := strings.SplitN(string(raw), "|", 2)
	exp, _ := strconv.ParseInt(f[len(f)-1], 10, 64)
	if len(f) != 2 || time.Now().Unix() > exp {
		return "", false
	}
	return f[0], true
}

func (h *hub) handleRelayToken(w http.ResponseWriter, r *http.Request) {
	if !h.auth(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	var body map[string]any
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body)
	id, _ := body["host_id"].(string)
	ttl := 3600.0
	if v, ok := body["ttl"].(float64); ok && v > 0 && v <= 86400 {
		ttl = v
	}
	h.mu.Lock()
	x := h.hosts[id]
	h.mu.Unlock()
	if x == nil || !x.Paired {
		http.Error(w, "host not found", 404)
		return
	}
	writeJSON(w, 200, map[string]any{"token": h.signRelay(id, time.Duration(ttl)*time.Second)})
}

// ---- computador ------------------------------------------------------------------------

func (h *hub) handleAgentPair(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	code := strings.ToUpper(strings.TrimSpace(body["code"]))
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.codes[code]
	if !ok || time.Now().After(p.Expires) {
		delete(h.codes, code)
		http.Error(w, "codigo invalido ou vencido", 404)
		return
	}
	delete(h.codes, code) // uso unico
	x := h.hosts[p.HostID]
	if x == nil {
		http.Error(w, "host not found", 404)
		return
	}
	tok := randHex(32)
	x.TokenHash, x.Paired = hashTok(tok), true
	x.OS, x.Arch, x.Version = body["os"], body["arch"], body["version"]
	x.LastSeen = time.Now()
	h.save()
	writeJSON(w, 200, map[string]any{"host_id": x.HostID, "device_token": tok, "conn_pub": x.ConnPub, "name": x.Name})
}

func (h *hub) agentHost(r *http.Request) *host {
	id := r.Header.Get("X-Host-Id")
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	h.mu.Lock()
	defer h.mu.Unlock()
	x := h.hosts[id]
	if x == nil || !x.Paired || subtle.ConstantTimeCompare([]byte(x.TokenHash), []byte(hashTok(tok))) != 1 {
		return nil
	}
	return x
}

func (h *hub) handleCtrl(w http.ResponseWriter, r *http.Request) {
	x := h.agentHost(r)
	if x == nil {
		http.Error(w, "forbidden", 403)
		return
	}
	ws, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &ctrlConn{ws: ws}
	h.mu.Lock()
	if old := h.ctrl[x.HostID]; old != nil {
		_ = old.ws.Close()
	}
	h.ctrl[x.HostID] = c
	x.LastSeen = time.Now()
	if v := r.Header.Get("X-Version"); v != "" {
		x.Version = v
	}
	h.mu.Unlock()
	log.Printf("ctrl online host=%s name=%s", x.HostID, x.Name)
	ws.SetPongHandler(func(string) error {
		h.mu.Lock()
		x.LastSeen = time.Now()
		h.mu.Unlock()
		return ws.SetReadDeadline(time.Now().Add(offlineAfter))
	})
	_ = ws.SetReadDeadline(time.Now().Add(offlineAfter))
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(pingEvery)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				c.wmu.Lock()
				err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
				c.wmu.Unlock()
				if err != nil {
					return
				}
			case <-stop:
				return
			}
		}
	}()
	for { // o computador só manda pongs/mensagens leves
		if _, _, err := ws.ReadMessage(); err != nil {
			break
		}
		h.mu.Lock()
		x.LastSeen = time.Now()
		h.mu.Unlock()
	}
	close(stop)
	h.mu.Lock()
	if h.ctrl[x.HostID] == c {
		delete(h.ctrl, x.HostID)
	}
	h.save()
	h.mu.Unlock()
	_ = ws.Close()
	log.Printf("ctrl offline host=%s", x.HostID)
}

func (h *hub) handleData(w http.ResponseWriter, r *http.Request) {
	x := h.agentHost(r)
	if x == nil {
		http.Error(w, "forbidden", 403)
		return
	}
	sid := r.URL.Query().Get("sid")
	h.mu.Lock()
	ch := h.waiting[sid]
	h.mu.Unlock()
	if ch == nil {
		http.Error(w, "unknown session", 404)
		return
	}
	ws, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ch <- wsconn.New(ws)
}

// dial pede ao computador que abra uma conexão de dados e devolve o lado do hub.
func (h *hub) dial(hostID string) (io.ReadWriteCloser, error) {
	h.mu.Lock()
	c := h.ctrl[hostID]
	h.mu.Unlock()
	if c == nil {
		return nil, errors.New("computador offline")
	}
	sid := randHex(12)
	ch := make(chan io.ReadWriteCloser, 1)
	h.mu.Lock()
	h.waiting[sid] = ch
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.waiting, sid); h.mu.Unlock() }()
	if err := c.send(map[string]string{"op": "dial", "sid": sid}); err != nil {
		return nil, err
	}
	select {
	case conn := <-ch:
		return conn, nil
	case <-time.After(15 * time.Second):
		return nil, errors.New("o computador nao respondeu")
	}
}

// handleRelayWS é o relay por WebSocket seguro (porta 443), que o ambiente da empresa consegue
// alcançar (o firewall do ambiente só libera 80/443). Autenticação pelo mesmo token HMAC curto.
func (h *hub) handleRelayWS(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("token")
	if tok == "" {
		tok = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	hostID, ok := h.verifyRelay(tok)
	if !ok {
		http.Error(w, "token invalido", 403)
		return
	}
	dst, err := h.dial(hostID)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	ws, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		_ = dst.Close()
		return
	}
	wsconn.Pipe(wsconn.New(ws), dst)
}

// ---- relay TCP interno (ambiente da empresa -> hub) -------------------------------------

func (h *hub) serveRelay(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
			line, err := readLine(c)
			if err != nil || !strings.HasPrefix(line, "RELAY ") {
				return
			}
			hostID, ok := h.verifyRelay(strings.TrimSpace(strings.TrimPrefix(line, "RELAY ")))
			if !ok {
				_, _ = c.Write([]byte("ERR token invalido\n"))
				return
			}
			dst, err := h.dial(hostID)
			if err != nil {
				_, _ = c.Write([]byte("ERR " + err.Error() + "\n"))
				return
			}
			_ = c.SetReadDeadline(time.Time{})
			_, _ = c.Write([]byte("OK\n"))
			wsconn.Pipe(c, dst)
		}()
	}
}

func readLine(c net.Conn) (string, error) {
	var b []byte
	one := make([]byte, 1)
	for len(b) < 4096 {
		if _, err := c.Read(one); err != nil {
			return "", err
		}
		if one[0] == '\n' {
			return string(b), nil
		}
		b = append(b, one[0])
	}
	return "", errors.New("linha longa")
}

// ---- OTA --------------------------------------------------------------------------------

func (h *hub) handleVersion(w http.ResponseWriter, r *http.Request) {
	dir := env("HUB_RELEASES", filepath.Join(h.dataDir, "releases"))
	b, err := os.ReadFile(filepath.Join(dir, "latest.json"))
	if err != nil {
		writeJSON(w, 200, map[string]string{"version": ""})
		return
	}
	var l struct {
		Version string                       `json:"version"`
		Files   map[string]map[string]string `json:"files"`
	}
	if json.Unmarshal(b, &l) != nil {
		writeJSON(w, 200, map[string]string{"version": ""})
		return
	}
	base := strings.TrimRight(env("HUB_PUBLIC_URL", ""), "/")
	for _, f := range l.Files {
		f["url"] = base + "/dl/" + f["path"]
	}
	writeJSON(w, 200, l)
}

func main() {
	h := newHub()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") })
	mux.HandleFunc("/internal/pair", h.handlePair)
	mux.HandleFunc("/internal/hosts", h.handleHosts)
	mux.HandleFunc("/internal/hosts/", h.handleRevoke)
	mux.HandleFunc("/internal/relay-token", h.handleRelayToken)
	mux.HandleFunc("/relay", h.handleRelayWS)
	mux.HandleFunc("/agent/pair", h.handleAgentPair)
	mux.HandleFunc("/agent/ctrl", h.handleCtrl)
	mux.HandleFunc("/agent/data", h.handleData)
	mux.HandleFunc("/agent/version", h.handleVersion)
	mux.Handle("/dl/", http.StripPrefix("/dl/", http.FileServer(http.Dir(env("HUB_RELEASES", filepath.Join(h.dataDir, "releases"))))))
	ln, err := net.Listen("tcp", env("HUB_RELAY_ADDR", ":2222"))
	if err != nil {
		log.Fatal(err)
	}
	go h.serveRelay(ln)
	srv := &http.Server{Addr: env("HUB_ADDR", ":8080"), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("hub em %s, relay em %s", srv.Addr, ln.Addr())
	log.Fatal(srv.ListenAndServe())
}
