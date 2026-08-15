// Package ibt decodes iRacing .ibt telemetry files (the binary irsdk on-disk
// format): a fixed header, an array of variable headers, a session-info YAML
// blob, and fixed-length sample buffers recorded at the session tick rate.
//
// It is intentionally dependency-free and read-only so it can be shared between
// the local coaching CLI (cmd/ibt) and any future server-side ingestion.
package ibt

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// irsdk var types
const (
	irChar    = 0
	irBool    = 1
	irInt     = 2
	irBitfeld = 3
	irFloat   = 4
	irDouble  = 5
)

var typeSize = map[int32]int{irChar: 1, irBool: 1, irInt: 4, irBitfeld: 4, irFloat: 4, irDouble: 8}

// VarHeader describes one telemetry channel (its type, byte offset within a
// sample buffer, element count, and metadata).
type VarHeader struct {
	Type   int32
	Offset int32
	Count  int32
	Name   string
	Desc   string
	Unit   string
}

// IBT is a parsed .ibt file held entirely in memory, with the variable headers
// indexed by name and the sample count precomputed.
type IBT struct {
	data       []byte
	Ver        int32
	TickRate   int32
	SessLen    int32
	SessOff    int32
	NumVars    int32
	VarOff     int32
	NumBuf     int32
	BufLen     int32
	DataOffset int32
	Vars       map[string]VarHeader
	VarList    []VarHeader
	NumSamples int
}

func cstr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// Open reads and parses an .ibt file from disk.
func Open(path string) (*IBT, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// The fixed header is 112 bytes; we read offsets up through byte 55. Bail
	// with an error rather than slicing out of bounds on a short/corrupt file.
	const headerSize = 112
	if len(data) < headerSize {
		return nil, fmt.Errorf("ibt: file too small: %d bytes (need >= %d for the header)", len(data), headerSize)
	}
	le := binary.LittleEndian
	h := &IBT{data: data}
	h.Ver = int32(le.Uint32(data[0:]))
	h.TickRate = int32(le.Uint32(data[8:]))
	h.SessLen = int32(le.Uint32(data[16:]))
	h.SessOff = int32(le.Uint32(data[20:]))
	h.NumVars = int32(le.Uint32(data[24:]))
	h.VarOff = int32(le.Uint32(data[28:]))
	h.NumBuf = int32(le.Uint32(data[32:]))
	h.BufLen = int32(le.Uint32(data[36:]))
	// varBuf[0] starts at offset 48: {tickCount int, bufOffset int, pad[2]}
	h.DataOffset = int32(le.Uint32(data[52:]))

	h.Vars = make(map[string]VarHeader)
	for i := int32(0); i < h.NumVars; i++ {
		base := h.VarOff + i*144
		if int(base)+144 > len(data) {
			break
		}
		v := VarHeader{
			Type:   int32(le.Uint32(data[base:])),
			Offset: int32(le.Uint32(data[base+4:])),
			Count:  int32(le.Uint32(data[base+8:])),
			Name:   cstr(data[base+16 : base+48]),
			Desc:   cstr(data[base+48 : base+112]),
			Unit:   cstr(data[base+112 : base+144]),
		}
		h.Vars[v.Name] = v
		h.VarList = append(h.VarList, v)
	}
	if h.BufLen > 0 && int(h.DataOffset) >= 0 && int(h.DataOffset) <= len(data) {
		h.NumSamples = (len(data) - int(h.DataOffset)) / int(h.BufLen)
	}
	return h, nil
}

// SessionYAML returns the session-info YAML blob embedded in the file.
func (h *IBT) SessionYAML() string {
	if h.SessOff < 0 || int(h.SessOff) > len(h.data) {
		return ""
	}
	end := h.SessOff + h.SessLen
	if end < h.SessOff || int(end) > len(h.data) {
		end = int32(len(h.data))
	}
	return cstr(h.data[h.SessOff:end])
}

// sample buffer for sample i
func (h *IBT) row(i int) []byte {
	start := int(h.DataOffset) + i*int(h.BufLen)
	return h.data[start : start+int(h.BufLen)]
}

// Float reads channel v at sample i as a float64, coercing whatever the
// underlying irsdk type is.
func (h *IBT) Float(v VarHeader, i int) float64 {
	row := h.row(i)
	b := row[v.Offset:]
	switch v.Type {
	case irFloat:
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
	case irDouble:
		return math.Float64frombits(binary.LittleEndian.Uint64(b))
	case irInt, irBitfeld:
		return float64(int32(binary.LittleEndian.Uint32(b)))
	case irBool, irChar:
		return float64(b[0])
	}
	return 0
}

// Int reads channel v at sample i as an int32.
func (h *IBT) Int(v VarHeader, i int) int32 {
	row := h.row(i)
	b := row[v.Offset:]
	switch v.Type {
	case irInt, irBitfeld:
		return int32(binary.LittleEndian.Uint32(b))
	case irBool, irChar:
		return int32(b[0])
	case irFloat:
		return int32(math.Float32frombits(binary.LittleEndian.Uint32(b)))
	case irDouble:
		return int32(math.Float64frombits(binary.LittleEndian.Uint64(b)))
	}
	return 0
}

// FloatArr reads a channel that may have Count>1 (per-corner data etc.).
func (h *IBT) FloatArr(v VarHeader, i int) []float64 {
	out := make([]float64, v.Count)
	sz := typeSize[v.Type]
	row := h.row(i)
	for k := int32(0); k < v.Count; k++ {
		off := int(v.Offset) + int(k)*sz
		if off < 0 || off+sz > len(row) { // corrupt Offset/Count: stop rather than slice OOB
			break
		}
		b := row[off:]
		switch v.Type {
		case irFloat:
			out[k] = float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
		case irDouble:
			out[k] = math.Float64frombits(binary.LittleEndian.Uint64(b))
		case irInt, irBitfeld:
			out[k] = float64(int32(binary.LittleEndian.Uint32(b)))
		default:
			out[k] = float64(b[0])
		}
	}
	return out
}
