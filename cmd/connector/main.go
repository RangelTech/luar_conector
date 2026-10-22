// luar-conector: serviço que liga o computador do cliente à plataforma LUAR (spec 11 §26).
//
//	luar-conector pair --hub https://conector.rangeltech.net --code ABC123   (parear)
//	luar-conector install                                                    (instalar como serviço)
//	luar-conector run                                                        (rodar em primeiro plano)
//	luar-conector uninstall | status | version
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Version é definida no build (-ldflags "-X main.Version=...").
var Version = "dev"

type config struct {
	Hub         string `json:"hub"`
	HostID      string `json:"host_id"`
	DeviceToken string `json:"device_token"`
	ConnPub     string `json:"conn_pub"`
	Name        string `json:"name"`
}

func configDir() string {
	if v := os.Getenv("LUAR_CONECTOR_HOME"); v != "" {
		return v
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "LUAR", "conector")
	}
	if os.Geteuid() == 0 {
		return "/etc/luar-conector"
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "luar-conector")
}

func loadConfig() (*config, error) {
	b, err := os.ReadFile(filepath.Join(configDir(), "config.json"))
	if err != nil {
		return nil, err
	}
	var c config
	return &c, json.Unmarshal(b, &c)
}

func saveConfig(c *config) error {
	if err := os.MkdirAll(configDir(), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(filepath.Join(configDir(), "config.json"), b, 0o600)
}

func main() {
	if len(os.Args) < 2 {
		guided()
		return
	}
	switch os.Args[1] {
	case "version":
		fmt.Println(Version)
	case "pair":
		fs := flag.NewFlagSet("pair", flag.ExitOnError)
		hub := fs.String("hub", "https://conector.rangeltech.net", "endereço do hub")
		code := fs.String("code", "", "código de pareamento")
		_ = fs.Parse(os.Args[2:])
		if err := doPair(strings.TrimRight(*hub, "/"), *code); err != nil {
			log.Fatalf("falha no pareamento: %v", err)
		}
	case "run":
		c, err := loadConfig()
		if err != nil {
			log.Fatalf("computador não pareado: %v", err)
		}
		setupLog()
		if os.Getenv("LUAR_WORKER") != "1" {
			supervise() // processo pai: reinicia o trabalhador se ele cair
			return
		}
		runForever(c)
	case "kit":
		c, err := loadConfig()
		if err != nil {
			log.Fatalf("computador não pareado: %v", err)
		}
		if len(os.Args) > 2 && os.Args[2] == "status" {
			fmt.Printf("kit: %q em %s\n", kitInstalled(), kitPython())
			return
		}
		if err := kitEnsure(c); err != nil {
			log.Fatalf("falha no kit: %v", err)
		}
		fmt.Println("kit pronto:", kitPython())
	case "install":
		if err := installService(); err != nil {
			log.Fatalf("falha ao instalar: %v", err)
		}
		if c, err := loadConfig(); err == nil {
			fmt.Println("Baixando o Python portátil da LUAR (uma vez)...")
			if err := kitEnsure(c); err != nil {
				fmt.Println("Aviso: o kit Python não foi baixado agora (tenta de novo sozinho):", err)
			}
		}
	case "uninstall":
		if err := uninstallService(); err != nil {
			log.Fatalf("falha ao remover: %v", err)
		}
	case "status":
		c, err := loadConfig()
		if err != nil {
			fmt.Println("não pareado")
			return
		}
		fmt.Printf("pareado como %q (versão %s)\n", c.Name, Version)
	default:
		fmt.Println("comando desconhecido")
		os.Exit(2)
	}
}

func setupLog() {
	dir := configDir()
	_ = os.MkdirAll(dir, 0o755)
	if f, err := os.OpenFile(filepath.Join(dir, "conector.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		if st, _ := f.Stat(); st != nil && st.Size() > 5<<20 {
			_ = f.Truncate(0)
		}
		log.SetOutput(f)
	}
}

// guided: duplo clique no instalador. Pede o código, pareia e instala como serviço.
func guided() {
	fmt.Println("LUAR - conector de computadores, versão " + Version)
	if c, err := loadConfig(); err == nil {
		fmt.Printf("Este computador já está pareado como %q.\n", c.Name)
	}
	hub := os.Getenv("LUAR_HUB")
	if hub == "" {
		hub = "https://conector.rangeltech.net"
	}
	fmt.Print("Digite o código de pareamento mostrado na plataforma: ")
	code, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if err := doPair(strings.TrimRight(hub, "/"), strings.TrimSpace(code)); err != nil {
		fmt.Println("Não foi possível parear:", err)
	} else if err := installService(); err != nil {
		fmt.Println("Não foi possível instalar:", err)
	} else {
		fmt.Println("Pronto! O computador aparece como online na plataforma.")
	}
	fmt.Print("Pressione Enter para fechar...")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

// supervise roda o conector como processo filho e o reinicia sempre que ele terminar (queda,
// atualização ou erro), com espera crescente. Mantém tudo no usuário, sem exigir administrador.
func supervise() {
	exe, _ := os.Executable()
	wait := 2 * time.Second
	for {
		started := time.Now()
		cmd := exec.Command(exe, "run")
		cmd.Env = append(os.Environ(), "LUAR_WORKER=1")
		hideWindow(cmd)
		err := cmd.Run()
		log.Printf("conector encerrou (%v); reiniciando", err)
		if time.Since(started) > time.Minute {
			wait = 2 * time.Second
		}
		time.Sleep(wait)
		if wait < time.Minute {
			wait *= 2
		}
	}
}
