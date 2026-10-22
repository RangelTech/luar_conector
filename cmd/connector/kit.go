package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// kitDir: onde fica o Python portátil da LUAR (por usuário, sem administrador).
func kitDir() string {
	if v := os.Getenv("LUAR_CONECTOR_KIT"); v != "" {
		return v
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "LUAR", "conector", "kit")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "share", "luar-conector", "kit")
}

func kitPython() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(kitDir(), "python", "python.exe")
	}
	return filepath.Join(kitDir(), "python", "bin", "python3")
}

func kitMarker() string { return filepath.Join(kitDir(), "kit.version") }

func kitInstalled() string {
	b, err := os.ReadFile(kitMarker())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// kitEnsure baixa e instala o kit anunciado pelo hub (se ainda não estiver na versão atual).
// Hash conferido; falha de rede não derruba o conector.
func kitEnsure(c *config) error {
	resp, err := http.Get(c.Hub + "/agent/version")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var l struct {
		Files map[string]struct {
			URL    string `json:"url"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&l); err != nil {
		return err
	}
	f, ok := l.Files["kit-"+runtime.GOOS+"-"+runtime.GOARCH]
	if !ok {
		return nil // este sistema não tem kit publicado
	}
	if kitInstalled() == f.SHA256 {
		return nil
	}
	log.Printf("instalando o kit Python (%s)", f.SHA256[:8])
	r2, err := http.Get(f.URL)
	if err != nil {
		return err
	}
	defer r2.Body.Close()
	tmp, err := os.CreateTemp("", "luar-kit-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), r2.Body); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	if hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return fmt.Errorf("kit recusado: hash diferente")
	}
	dst := kitDir()
	_ = os.RemoveAll(filepath.Join(dst, "python"))
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	if strings.HasSuffix(f.URL, ".zip") {
		err = unzip(tmp.Name(), dst)
	} else {
		err = untargz(tmp.Name(), dst)
	}
	if err != nil {
		return err
	}
	return os.WriteFile(kitMarker(), []byte(f.SHA256), 0o644)
}

func safeJoin(dir, name string) (string, error) {
	p := filepath.Join(dir, filepath.FromSlash(name))
	if !strings.HasPrefix(p, filepath.Clean(dir)+string(os.PathSeparator)) {
		return "", fmt.Errorf("caminho inseguro no pacote: %s", name)
	}
	return p, nil
}

func unzip(src, dir string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		p, err := safeJoin(dir, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(p, 0o755)
			continue
		}
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func untargz(src, dir string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		p, err := safeJoin(dir, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			_ = os.MkdirAll(p, 0o755)
		case tar.TypeSymlink:
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.Remove(p)
			_ = os.Symlink(h.Linkname, p)
		case tar.TypeReg:
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			out, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)|0o100)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, tr)
			out.Close()
			if err != nil {
				return err
			}
		}
	}
}
