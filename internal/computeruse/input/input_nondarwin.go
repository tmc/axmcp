//go:build !darwin

package input

import (
	"fmt"

	"github.com/tmc/axmcp/internal/computeruse"
)

func SendKeyCombo(spec string) error {
	if _, err := ParseKeyCombo(spec); err != nil {
		return err
	}
	return computeruse.PlatformUnsupported("send key combo")
}

func SendKeyComboToPID(pid int32, spec string) error {
	if pid <= 0 {
		return fmt.Errorf("pid must be positive")
	}
	if _, err := ParseKeyCombo(spec); err != nil {
		return err
	}
	return computeruse.PlatformUnsupported("send key combo to pid")
}

func ClickScreenPoint(int, int) error {
	return computeruse.PlatformUnsupported("click screen point")
}

func MultiClickScreenPoint(int, int, int) error {
	return computeruse.PlatformUnsupported("click screen point")
}
