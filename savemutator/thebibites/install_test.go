package thebibites

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallSaveFileToDirCopiesZipAtomically(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "source.zip")
	want := []byte("zip bytes")
	if err := os.WriteFile(srcPath, want, 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}

	dstPath, err := InstallSaveFileToDir(srcPath, dstDir, "installed.zip")
	if err != nil {
		t.Fatalf("InstallSaveFileToDir() error = %v", err)
	}
	if dstPath != filepath.Join(dstDir, "installed.zip") {
		t.Fatalf("dst path = %q, want %q", dstPath, filepath.Join(dstDir, "installed.zip"))
	}
	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("ReadFile(installed) error = %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("installed bytes = %q, want %q", got, want)
	}
}

func TestInstallSaveFileUsesEnvOverride(t *testing.T) {
	dstDir := filepath.Join(t.TempDir(), "savefiles")
	t.Setenv(BibitesSavefilesDirEnv, dstDir)

	srcPath := filepath.Join(t.TempDir(), "smoke.zip")
	if err := os.WriteFile(srcPath, []byte("zip bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}

	dstPath, err := InstallSaveFile(srcPath, "")
	if err != nil {
		t.Fatalf("InstallSaveFile() error = %v", err)
	}
	if dstPath != filepath.Join(dstDir, "smoke.zip") {
		t.Fatalf("dst path = %q, want env override path", dstPath)
	}
}

func TestInstallSaveFileRejectsUnsafeDestinationName(t *testing.T) {
	srcPath := filepath.Join(t.TempDir(), "source.zip")
	if err := os.WriteFile(srcPath, []byte("zip bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}

	for _, name := range []string{"../bad.zip", "nested/bad.zip", "bad.txt"} {
		if _, err := InstallSaveFileToDir(srcPath, t.TempDir(), name); err == nil {
			t.Fatalf("InstallSaveFileToDir(%q) error = nil, want rejection", name)
		}
	}
}
