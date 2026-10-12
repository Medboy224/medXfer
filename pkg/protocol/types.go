package protocol

const (
	MagicBytes      uint16 = 0x4D58 // 'MX'
	Version1        byte   = 0x01
	FrameHeaderSize        = 8 // untyped, so that int and uint32 arithmetic both work
)

// Protocol v1 (bible §8.2). TypeControl carries the JSON messages of a session; the data
// frames of a transfer are in offer.go.
const (
	TypeControl  byte = 0x10
	TypeDataJoin byte = 0x20 // first frame of a data connection: the transfer it joins (CHAN-06)

	// DataJoinLen is the payload of DATA_JOIN: a 128-bit transfer id.
	DataJoinLen = 16

	// MaxControl is the default bound of a CONTROL payload (LIM-04). Like every limit it is
	// a parameter (rule R4): session.Channel.SetMaxControl.
	MaxControl = 1 << 20
)

type Header struct {
	Type       byte
	PayloadLen uint32
}
