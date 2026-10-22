package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"

	"github.com/RangelTech/luar_conector/internal/wsconn"
)

func wsURL(hub, path string) string {
	u, _ := url.Parse(hub)
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = path
	return u.String()
}

func doPair(hub, code string) error {
	if code == "" {
		return errors.New("informe o código de pareamento (--code)")
	}
	body, _ := json.Marshal(map[string]string{"code": code, "os": runtime.GOOS, "arch": runtime.GOARCH, "version": Version})
	resp, err := http.Post(hub+"/agent/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("o hub recusou: %s", strings.TrimSpace(string(b)))
	}
	var out struct {
		HostID      string `json:"host_id"`
		DeviceToken string `json:"device_token"`
		ConnPub     string `json:"conn_pub"`
		Name        string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	c := &config{Hub: hub, HostID: out.HostID, DeviceToken: out.DeviceToken, ConnPub: out.ConnPub, Name: out.Name}
	if err := saveConfig(c); err != nil {
		return err
	}
	fmt.Printf("Computador %q pareado. Rode: luar-conector install\n", c.Name)
	return nil
}

func header(c *config) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+c.DeviceToken)
	h.Set("X-Host-Id", c.HostID)
	h.Set("X-Version", Version)
	return h
}

// runForever mantém a conexão de saída com o hub, reconectando com espera crescente.
func runForever(c *config) {
	cfg, err := serverConfig(configDir(), c.ConnPub)
	if err != nil {
		log.Fatalf("configuração SSH: %v", err)
	}
	go updateLoop(c)
	wait := time.Second
	for {
		started := time.Now()
		err := session(c, cfg)
		log.Printf("conexão encerrada: %v", err)
		if time.Since(started) > time.Minute {
			wait = time.Second
		}
		time.Sleep(wait)
		if wait < 60*time.Second {
			wait *= 2
		}
	}
}

func session(c *config, cfg *ssh.ServerConfig) error {
	d := websocket.Dialer{HandshakeTimeout: 15 * time.Second, Proxy: http.ProxyFromEnvironment}
	ws, resp, err := d.Dial(wsURL(c.Hub, "/agent/ctrl"), header(c))
	if err != nil {
		if resp != nil && (resp.StatusCode == 403 || resp.StatusCode == 401) {
			return errors.New("o hub recusou este computador (removido ou repareado)")
		}
		return err
	}
	defer ws.Close()
	log.Printf("conectado ao hub como %s", c.Name)
	markHealthy()
	ws.SetPingHandler(func(string) error { // responde aos pings do hub
		return ws.WriteControl(websocket.PongMessage, nil, time.Now().Add(5*time.Second))
	})
	for {
		var msg struct {
			Op  string `json:"op"`
			Sid string `json:"sid"`
		}
		if err := ws.ReadJSON(&msg); err != nil {
			return err
		}
		if msg.Op == "dial" && msg.Sid != "" {
			go serveDial(c, cfg, msg.Sid)
		}
	}
}

func serveDial(c *config, cfg *ssh.ServerConfig, sid string) {
	d := websocket.Dialer{HandshakeTimeout: 15 * time.Second, Proxy: http.ProxyFromEnvironment}
	ws, _, err := d.Dial(wsURL(c.Hub, "/agent/data")+"?sid="+url.QueryEscape(sid), header(c))
	if err != nil {
		log.Printf("dial data: %v", err)
		return
	}
	serveSSH(wsconn.New(ws), cfg)
}
