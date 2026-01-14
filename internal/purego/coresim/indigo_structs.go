package coresim

// #pragma pack(push, 4)

type IndigoQuad struct {
	Field1 float64
	Field2 float64
	Field3 float64
	Field4 float64
}

type IndigoTouch struct {
	Field1  uint32  // 0x30
	Field2  uint32  // 0x34
	Field3  uint32  // 0x38
	XRatio  float64 // 0x3C
	YRatio  float64 // 0x44
	Field6  float64 // 0x4C
	Field7  float64 // 0x54
	Field8  float64 // 0x5C
	Field9  uint32  // 0x64
	Field10 uint32  // 0x68
	Field11 uint32  // 0x6C
	Field12 uint32  // 0x70
	Field13 uint32  // 0x74
	Field14 float64 // 0x78
	Field15 float64 // 0x80
	Field16 float64 // 0x88
	Field17 float64 // 0x90
	Field18 float64 // 0x98
}

type IndigoEvent struct { /* Union, max size */
	Touch IndigoTouch
}

type IndigoPayload struct {
	Field1    uint32      // 0x20
	Timestamp uint64      // 0x24 (packed 4)
	Field3    uint32      // 0x2C
	Event     IndigoEvent // 0x30
}

type MachMessageHeader struct {
	Bits       uint32
	Size       uint32
	RemotePort uint32
	LocalPort  uint32
	Voucher    uint32
	ID         uint32
}

type IndigoMessage struct {
	Header    MachMessageHeader // 0x0 - 0x18
	InnerSize uint32            // 0x18
	EventType uint8             // 0x1C
	_         [3]byte           // Padding to 0x20
	Payload   IndigoPayload     // 0x20
}

const (
	IndigoEventTypeButton = 1
	IndigoEventTypeTouch  = 2
)
