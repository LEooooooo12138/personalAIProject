// Package voiceaudio validates bounded PCM16 WAV without I/O.
package voiceaudio

import (
	"encoding/binary"
	"errors"
)

var errInvalid = errors.New("invalid PCM16 WAV")

// ValidateWAV requires complete RIFF framing and uncompressed PCM16 audio.
// Duration is derived from sample bytes, never trusted metadata.
func ValidateWAV(b []byte, maxSeconds, maxBytes int) error {
	if len(b) < 44 || len(b) > maxBytes || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" || uint64(binary.LittleEndian.Uint32(b[4:8]))+8 != uint64(len(b)) {
		return errInvalid
	}
	var rate, align uint64
	var dataSize uint64
	foundFmt, foundData := false, false
	for pos := 12; pos < len(b); {
		if len(b)-pos < 8 {
			return errInvalid
		}
		size := uint64(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
		start := pos + 8
		end := uint64(start) + size
		if end > uint64(len(b)) {
			return errInvalid
		}
		chunk := b[start:int(end)]
		switch string(b[pos : pos+4]) {
		case "fmt ":
			if foundFmt || (len(chunk) != 16 && len(chunk) != 18) || binary.LittleEndian.Uint16(chunk[:2]) != 1 {
				return errInvalid
			}
			if len(chunk) == 18 && binary.LittleEndian.Uint16(chunk[16:]) != 0 {
				return errInvalid
			}
			foundFmt = true
			channels := uint64(binary.LittleEndian.Uint16(chunk[2:4]))
			rate = uint64(binary.LittleEndian.Uint32(chunk[4:8]))
			byteRate := uint64(binary.LittleEndian.Uint32(chunk[8:12]))
			align = uint64(binary.LittleEndian.Uint16(chunk[12:14]))
			bits := binary.LittleEndian.Uint16(chunk[14:16])
			if channels < 1 || channels > 2 || rate < 8000 || rate > 192000 || bits != 16 || align != channels*2 || byteRate != rate*align {
				return errInvalid
			}
		case "data":
			if foundData {
				return errInvalid
			}
			foundData = true
			dataSize = size
		}
		next := end + (size % 2)
		if next > uint64(len(b)) {
			return errInvalid
		}
		pos = int(next)
	}
	if !foundFmt || !foundData || dataSize == 0 || dataSize%align != 0 || dataSize > rate*align*uint64(maxSeconds) {
		return errInvalid
	}
	return nil
}
