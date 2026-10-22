package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// UpdatePubKey (base64 de 32 bytes ed25519) é definida no build; vazia = só confere o hash.
var UpdatePubKey = ""

type latest struct {
	Version string `json:"version"`
	Files   map[string]struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
		Sig    string `json:"sig"`
	} `json:"files"`
}

func pendingPath() string { return filepath.Join(configDir(), "update.pending") }

// markHealthy confirma que a versão atual conectou: apaga o marcador de atualização pendente.
func markHealthy() { _ = os.Remove(pendingPath()) }

// rollbackIfNeeded roda no início: se uma atualização ficou pendente e a versão nova não conectou
// dentro do prazo, volta para o binário anterior.
func rollbackWatch() {
	b, err := os.ReadFile(pendingPath())
	if err != nil {
		return
	}
	prev := string(b)
	go func() {
		time.Sleep(2 * time.Minute)
		if _, err := os.Stat(pendingPath()); err != nil {
			return // conectou: saudável
		}
		self, _ := os.Executable()
		log.Printf("atualização não conectou; voltando para a versão anterior")
		_ = os.Remove(pendingPath())
		if err := swap(prev, self); err == nil {
			restart(self)
		}
	}()
}

func swap(from, to string) error {
	if runtime.GOOS == "windows" {
		_ = os.Remove(to + ".old")
		if err := os.Rename(to, to+".old"); err != nil {
			return err
		}
	}
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	tmp := to + ".new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, to)
}

func restart(self string) {
	os.Exit(0) // o supervisor (processo pai) sobe a versão nova
}

func updateLoop(c *config) {
	rollbackWatch()
	time.Sleep(time.Minute) // primeira conferência logo depois de conectar; depois a cada 6 horas
	for {
		if err := checkUpdate(c); err != nil {
			log.Printf("atualização: %v", err)
		}
		time.Sleep(6 * time.Hour)
	}
}

func verifySig(data []byte, sigB64 string) bool {
	if UpdatePubKey == "" {
		return true
	}
	pub, err := base64.StdEncoding.DecodeString(UpdatePubKey)
	sig, err2 := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || err2 != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(pub, data, sig)
}

func checkUpdate(c *config) error {
	resp, err := http.Get(c.Hub + "/agent/version")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var l latest
	if err := json.NewDecoder(resp.Body).Decode(&l); err != nil || l.Version == "" || l.Version == Version {
		return nil
	}
	f, ok := l.Files[runtime.GOOS+"-"+runtime.GOARCH]
	if !ok {
		return nil
	}
	r2, err := http.Get(f.URL)
	if err != nil {
		return err
	}
	defer r2.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r2.Body, 200<<20))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != f.SHA256 || !verifySig(data, f.Sig) {
		return fmt.Errorf("pacote da versão %s recusado (hash ou assinatura)", l.Version)
	}
	self, _ := os.Executable()
	newPath := self + ".update"
	if err := os.WriteFile(newPath, data, 0o755); err != nil {
		return err
	}
	prev := self + ".prev"
	cur, _ := os.ReadFile(self)
	_ = os.WriteFile(prev, cur, 0o755)
	_ = os.WriteFile(pendingPath(), []byte(prev), 0o600)
	if err := swap(newPath, self); err != nil {
		_ = os.Remove(pendingPath())
		return err
	}
	_ = os.Remove(newPath)
	log.Printf("atualizado para %s; reiniciando", l.Version)
	restart(self)
	return nil
}
