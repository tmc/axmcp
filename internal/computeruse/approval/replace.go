package approval

import (
	"fmt"
	"os"
	"path/filepath"
)

// replaceAuthority returns whether rename made data visible, even when a later
// durability step fails. Callers hold the sidecar lock through this operation.
func replaceAuthority(path string, data []byte) (committed bool, err error) {
	return replaceAuthorityWithSync(path, data, syncAuthorityDirectory)
}

func replaceAuthorityWithSync(path string, data []byte, syncDir func(string) error) (bool, error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".approvals-*")
	if err != nil {
		return false, fmt.Errorf("create approvals temporary file: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return false, fmt.Errorf("write approvals temporary file: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return false, fmt.Errorf("sync approvals temporary file: %w", err)
	}
	if err := f.Close(); err != nil {
		return false, fmt.Errorf("close approvals temporary file: %w", err)
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return false, fmt.Errorf("replace approvals: %w", err)
	}
	if err := syncDir(dir); err != nil {
		return true, fmt.Errorf("approvals replaced; sync directory: %w", err)
	}
	return true, nil
}

func syncAuthorityDirectory(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}
