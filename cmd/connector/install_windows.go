//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const taskName = "LUAR Conector"

// No Windows o conector roda como tarefa agendada no logon do usuário que instalou:
// assim os comandos enxergam as pastas dele (Área de Trabalho) e o Chrome abre visível.
func installService() error {
	self, _ := os.Executable()
	dstDir := filepath.Join(os.Getenv("ProgramData"), "LUAR", "conector")
	_ = os.MkdirAll(dstDir, 0o755)
	dst := filepath.Join(dstDir, "luar-conector.exe")
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
	xml := `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers><LogonTrigger><Enabled>true</Enabled></LogonTrigger></Triggers>
  <Principals><Principal id="Author"><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <RestartOnFailure><Interval>PT1M</Interval><Count>999</Count></RestartOnFailure>
    <Hidden>true</Hidden>
  </Settings>
  <Actions Context="Author"><Exec><Command>` + dst + `</Command><Arguments>run</Arguments></Exec></Actions>
</Task>`
	tmp := filepath.Join(os.TempDir(), "luar-task.xml")
	// schtasks exige UTF-16 LE com BOM
	u16 := []byte{0xFF, 0xFE}
	for _, r := range xml {
		u16 = append(u16, byte(r), byte(r>>8))
	}
	if err := os.WriteFile(tmp, u16, 0o600); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if out, err := exec.Command("schtasks", "/Create", "/TN", taskName, "/XML", tmp, "/F").CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks: %s", out)
	}
	if out, err := exec.Command("schtasks", "/Run", "/TN", taskName).CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks run: %s", out)
	}
	fmt.Println("Conector instalado: inicia sozinho quando você entra no Windows.")
	return nil
}

func uninstallService() error {
	_ = exec.Command("schtasks", "/End", "/TN", taskName).Run()
	_ = exec.Command("schtasks", "/Delete", "/TN", taskName, "/F").Run()
	_ = exec.Command("taskkill", "/IM", "luar-conector.exe", "/F").Run()
	fmt.Println("Conector removido.")
	return nil
}
