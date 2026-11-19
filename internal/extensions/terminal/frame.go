package terminal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// herdr-core's framed mode: every frame is [channel u32 LE][kind u8][len u32
// LE][payload]. Channel 0 carries JSON-RPC; an attach or an event subscription
// gets a channel of its own. See herdr-core's schema/README.md.
const (
	kindData   byte = 0
	kindCredit byte = 1
	kindClose  byte = 2
	kindResize byte = 3

	frameHeader = 9
	// maxFrame is herdr-core's own payload limit.
	maxFrame = 16 << 20
)

type frame struct {
	channel uint32
	kind    byte
	payload []byte
}

func writeFrame(w io.Writer, f frame) error {
	if len(f.payload) > maxFrame {
		return fmt.Errorf("frame of %d bytes is over the %d byte limit", len(f.payload), maxFrame)
	}
	buf := make([]byte, frameHeader+len(f.payload))
	binary.LittleEndian.PutUint32(buf[0:4], f.channel)
	buf[4] = f.kind
	binary.LittleEndian.PutUint32(buf[5:9], uint32(len(f.payload)))
	copy(buf[frameHeader:], f.payload)
	_, err := w.Write(buf)
	return err
}

func readFrame(r io.Reader) (frame, error) {
	var header [frameHeader]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return frame{}, err
	}
	n := binary.LittleEndian.Uint32(header[5:9])
	if n > maxFrame {
		return frame{}, errors.New("frame over the size limit")
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return frame{}, err
	}
	return frame{channel: binary.LittleEndian.Uint32(header[0:4]), kind: header[4], payload: payload}, nil
}

func creditPayload(n int) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(n))
	return b[:]
}

func resizePayload(cols, rows uint16) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint16(b[0:2], cols)
	binary.LittleEndian.PutUint16(b[2:4], rows)
	return b[:]
}
