//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const runKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

// No Windows o conector roda na sessão do usuário que instalou (sem administrador): inicia no logon
// pela chave Run do usuário e é mantido de pé por um supervisor interno. Assim os comandos enxergam
// as pastas dele (Área de Trabalho) e o Chrome abre visível.
func installService() error {
	self, _ := os.Executable()
	dir := configDir()
	_ = os.MkdirAll(dir, 0o755)
	dst := filepath.Join(dir, "luar-conector.exe")
	if self != dst {
		b, err := os.ReadFile(self)
		if err != nil {
			return err
		}
		_ = os.Remove(dst + ".old")
		_ = os.Rename(dst, dst+".old")
		if err := os.WriteFile(dst, b, 0o755); err != nil {
			return err
		}
	}
	if out, err := exec.Command("reg", "add", runKey, "/v", "LUARConector", "/t", "REG_SZ",
		"/d", `"`+dst+`" run`, "/f").CombinedOutput(); err != nil {
		return fmt.Errorf("registro do Windows: %s", out)
	}
	cmd := exec.Command(dst, "run")
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	fmt.Println("Conector instalado: inicia sozinho quando você entra no Windows.")
	return nil
}

func uninstallService() error {
	_ = exec.Command("reg", "delete", runKey, "/v", "LUARConector", "/f").Run()
	_ = exec.Command("taskkill", "/IM", "luar-conector.exe", "/F").Run()
	fmt.Println("Conector removido.")
	return nil
}
