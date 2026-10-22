package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// hostKey carrega (ou cria) a chave de host do servidor SSH embutido.
func hostKey(dir string) (ssh.Signer, error) {
	path := filepath.Join(dir, "ssh_host_ed25519")
	if b, err := os.ReadFile(path); err == nil {
		return ssh.ParsePrivateKey(b)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	blk, err := ssh.MarshalPrivateKey(priv, "luar-conector")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(blk), 0o600); err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(priv)
}

func serverConfig(dir, connPub string) (*ssh.ServerConfig, error) {
	allowed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(connPub))
	if err != nil {
		return nil, err
	}
	signer, err := hostKey(dir)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), allowed.Marshal()) {
				return nil, nil
			}
			return nil, io.ErrUnexpectedEOF
		},
	}
	cfg.AddHostKey(signer)
	return cfg, nil
}

// serveSSH atende uma conexão SSH já aberta (vinda do túnel do hub).
func serveSSH(conn net.Conn, cfg *ssh.ServerConfig) {
	defer conn.Close()
	sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		log.Printf("ssh handshake: %v", err)
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		switch nc.ChannelType() {
		case "session":
			go handleSession(nc)
		case "direct-tcpip":
			go handleDirectTCP(nc)
		default:
			_ = nc.Reject(ssh.UnknownChannelType, "tipo nao suportado")
		}
	}
}

func handleDirectTCP(nc ssh.NewChannel) {
	var d struct {
		Host     string
		Port     uint32
		OrigHost string
		OrigPort uint32
	}
	if err := ssh.Unmarshal(nc.ExtraData(), &d); err != nil {
		_ = nc.Reject(ssh.ConnectionFailed, "dados invalidos")
		return
	}
	if d.Host != "localhost" && d.Host != "127.0.0.1" && d.Host != "::1" {
		_ = nc.Reject(ssh.Prohibited, "apenas localhost")
		return
	}
	dst, err := net.Dial("tcp", net.JoinHostPort(d.Host, itoa(d.Port)))
	if err != nil {
		_ = nc.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		dst.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	go func() { _, _ = io.Copy(ch, dst); ch.Close() }()
	go func() { _, _ = io.Copy(dst, ch); dst.Close() }()
}

func itoa(n uint32) string {
	var b [10]byte
	i := len(b)
	if n == 0 {
		return "0"
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func shellCmd(command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		pre := "[Console]::OutputEncoding=[Text.Encoding]::UTF8;$OutputEncoding=[Text.Encoding]::UTF8;$ProgressPreference='SilentlyContinue';"
		return exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", pre+command)
	}
	if command == "" {
		return exec.Command("/bin/sh", "-l")
	}
	return exec.Command("/bin/sh", "-c", command)
}

func handleSession(nc ssh.NewChannel) {
	ch, reqs, err := nc.Accept()
	if err != nil {
		return
	}
	defer ch.Close()
	var env []string
	var wg sync.WaitGroup
	for req := range reqs {
		switch req.Type {
		case "env":
			var kv struct{ Name, Value string }
			if ssh.Unmarshal(req.Payload, &kv) == nil && !strings.ContainsAny(kv.Name, "= \x00") {
				env = append(env, kv.Name+"="+kv.Value)
			}
			_ = req.Reply(true, nil)
		case "pty-req":
			_ = req.Reply(false, nil) // sem pty: execucao nao interativa
		case "subsystem":
			var s struct{ Name string }
			_ = ssh.Unmarshal(req.Payload, &s)
			if s.Name != "sftp" {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			srv, err := sftp.NewServer(ch)
			if err == nil {
				if err := srv.Serve(); err != nil && err != io.EOF {
					log.Printf("sftp: %v", err)
				}
				_ = srv.Close()
			}
			_, _ = ch.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
			return
		case "exec", "shell":
			command := ""
			if req.Type == "exec" {
				var c struct{ Command string }
				_ = ssh.Unmarshal(req.Payload, &c)
				command = c.Command
			}
			_ = req.Reply(true, nil)
			code := runCommand(ch, command, env, &wg)
			var st [4]byte
			binary.BigEndian.PutUint32(st[:], uint32(code))
			_, _ = ch.SendRequest("exit-status", false, st[:])
			return
		default:
			_ = req.Reply(false, nil)
		}
	}
	wg.Wait()
}

func runCommand(ch ssh.Channel, command string, env []string, wg *sync.WaitGroup) int {
	cmd := shellCmd(command)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = ch
	cmd.Stdout = ch
	cmd.Stderr = ch.Stderr()
	if err := cmd.Start(); err != nil {
		_, _ = io.WriteString(ch.Stderr(), "luar-conector: "+err.Error()+"\n")
		return 127
	}
	err := cmd.Wait()
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return 1
}
