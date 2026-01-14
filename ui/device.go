package ui

// Device stub for AX
type Device struct{}

func SharedDevice() *Device            { return &Device{} }
func (d *Device) PressHome()           {}
func (d *Device) PressVolumeUp()       {}
func (d *Device) PressVolumeDown()     {}
func (d *Device) PressLock()           {}
func (d *Device) PressButton(id int)   {}
func (d *Device) SetOrientation(o int) {}
func (d *Device) Orientation() int     { return 0 }
