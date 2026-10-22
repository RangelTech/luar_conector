//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

const unitName = "luar-conector.service"

func installService() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("rode com sudo: sudo %s install", os.Args[0])
	}
	self, _ := os.Executable()
	dst := "/usr/local/bin/luar-conector"
	if self != dst {
		b, err := os.ReadFile(self)
		if err != nil {
			return err
		}
		_ = os.Remove(dst)
		if err := os.WriteFile(dst, b, 0o755); err != nil {
			return err
		}
	}
	who := os.Getenv("SUDO_USER")
	if who == "" {
		who = "root"
	}
	u, err := user.Lookup(who)
	if err != nil {
		return err
	}
	// O servidor SSH precisa do mesmo diretório de configuração do pareamento.
	home := configDir()
	_ = os.Chmod(home, 0o755)
	unit := strings.Join([]string{
		"[Unit]", "Description=LUAR conector de computadores", "After=network-online.target", "Wants=network-online.target", "",
		"[Service]", "User=" + u.Username, "Environment=LUAR_CONECTOR_HOME=" + home,
		"ExecStart=" + dst + " run", "Restart=always", "RestartSec=5", "",
		"[Install]", "WantedBy=multi-user.target", "",
	}, "\n")
	if who != "root" {
		_ = exec.Command("chown", "-R", who, home).Run()
	}
	if err := os.WriteFile(filepath.Join("/etc/systemd/system", unitName), []byte(unit), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "--now", unitName}} {
		if out, err := exec.Command("systemctl", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl %v: %s", args, out)
		}
	}
	fmt.Println("Serviço instalado e iniciado (systemd, reinicia sozinho).")
	return nil
}

func uninstallService() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("rode com sudo")
	}
	_ = exec.Command("systemctl", "disable", "--now", unitName).Run()
	_ = os.Remove(filepath.Join("/etc/systemd/system", unitName))
	_ = exec.Command("systemctl", "daemon-reload").Run()
	_ = os.Remove("/usr/local/bin/luar-conector")
	_ = os.RemoveAll(configDir())
	fmt.Println("Conector removido.")
	return nil
}
