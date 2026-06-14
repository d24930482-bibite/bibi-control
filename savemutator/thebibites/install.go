package thebibites

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// BibitesSavefilesDirEnv overrides the default The Bibites save directory.
	BibitesSavefilesDirEnv = "BIBITES_SAVEFILES_DIR"
	bibitesCompanyName     = "The Bibites"
	bibitesProductName     = "The Bibites"
)

// DefaultSavefilesDir returns the usual per-user Savefiles directory for The
// Bibites. Set BIBITES_SAVEFILES_DIR to override discovery.
func DefaultSavefilesDir() (string, error) {
	if override := strings.TrimSpace(os.Getenv(BibitesSavefilesDirEnv)); override != "" {
		return filepath.Clean(override), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	if home == "" {
		return "", fmt.Errorf("resolve user home: empty home directory")
	}

	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "LocalLow", bibitesCompanyName, bibitesProductName, "Savefiles"), nil
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", bibitesCompanyName, bibitesProductName, "Savefiles"), nil
	default:
		configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
		if configHome == "" {
			configHome = filepath.Join(home, ".config")
		}
		return filepath.Join(configHome, "unity3d", bibitesCompanyName, bibitesProductName, "Savefiles"), nil
	}
}

// InstallSaveFile copies srcPath into the default The Bibites Savefiles
// directory. If dstName is empty, the source file's base name is used.
func InstallSaveFile(srcPath, dstName string) (string, error) {
	dir, err := DefaultSavefilesDir()
	if err != nil {
		return "", err
	}
	return InstallSaveFileToDir(srcPath, dir, dstName)
}

// InstallSaveFileToDir copies srcPath into dstDir under dstName. The copy is
// written to a temp file in dstDir, then renamed over the final path.
func InstallSaveFileToDir(srcPath, dstDir, dstName string) (string, error) {
	if srcPath == "" {
		return "", fmt.Errorf("source save path is required")
	}
	if dstDir == "" {
		return "", fmt.Errorf("destination save directory is required")
	}
	if dstName == "" {
		dstName = filepath.Base(srcPath)
	}
	if err := validateSaveFileName(dstName); err != nil {
		return "", err
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return "", fmt.Errorf("open source save %q: %w", srcPath, err)
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return "", fmt.Errorf("stat source save %q: %w", srcPath, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("source save %q is not a regular file", srcPath)
	}

	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return "", fmt.Errorf("create save directory %q: %w", dstDir, err)
	}

	dstPath := filepath.Join(dstDir, dstName)
	tmp, err := os.CreateTemp(dstDir, "."+dstName+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp save in %q: %w", dstDir, err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("copy save to %q: %w", tmpPath, err)
	}
	if err := tmp.Chmod(0o664); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("chmod temp save %q: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp save %q: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		return "", fmt.Errorf("install save %q: %w", dstPath, err)
	}
	committed = true
	return dstPath, nil
}

func validateSaveFileName(name string) error {
	if name == "" || name == "." || name == string(filepath.Separator) {
		return fmt.Errorf("destination save name is required")
	}
	if filepath.Base(name) != name {
		return fmt.Errorf("destination save name %q must not include a directory", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("destination save name %q must not include path separators", name)
	}
	if !strings.HasSuffix(strings.ToLower(name), ".zip") {
		return fmt.Errorf("destination save name %q must end in .zip", name)
	}
	return nil
}
