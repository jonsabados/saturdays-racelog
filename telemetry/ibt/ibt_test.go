package ibt

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildIBT crafts a minimal but structurally valid .ibt: 112-byte header, one
// float VarHeader ("Speed"), a session-info YAML blob, and a single sample.
func buildIBT(yaml string, speed float32) []byte {
	const (
		varOff  = 112
		varSize = 144
		bufLen  = 4
	)
	sessOff := varOff + varSize
	sessLen := len(yaml)
	dataOff := sessOff + sessLen
	b := make([]byte, dataOff+bufLen)
	le := binary.LittleEndian
	le.PutUint32(b[0:], 2)                // Ver
	le.PutUint32(b[8:], 60)               // TickRate
	le.PutUint32(b[16:], uint32(sessLen)) // SessLen
	le.PutUint32(b[20:], uint32(sessOff)) // SessOff
	le.PutUint32(b[24:], 1)               // NumVars
	le.PutUint32(b[28:], uint32(varOff))  // VarOff
	le.PutUint32(b[32:], 1)               // NumBuf
	le.PutUint32(b[36:], uint32(bufLen))  // BufLen
	le.PutUint32(b[52:], uint32(dataOff)) // varBuf[0].bufOffset -> DataOffset
	le.PutUint32(b[varOff+0:], irFloat)   // Type
	le.PutUint32(b[varOff+4:], 0)         // Offset within the sample row
	le.PutUint32(b[varOff+8:], 1)         // Count
	copy(b[varOff+16:], "Speed")          // Name
	copy(b[sessOff:], yaml)               // session YAML
	le.PutUint32(b[dataOff:], math.Float32bits(speed))
	return b
}

func TestOpenDecodesHeaderAndChannel(t *testing.T) {
	yaml := "WeekendInfo:\n TrackName: testtrack\n"
	path := filepath.Join(t.TempDir(), "test.ibt")
	if err := os.WriteFile(path, buildIBT(yaml, 42.5), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if h.Ver != 2 || h.TickRate != 60 || h.NumVars != 1 {
		t.Errorf("header = ver %d tick %d vars %d, want 2/60/1", h.Ver, h.TickRate, h.NumVars)
	}
	if h.NumSamples != 1 {
		t.Errorf("NumSamples = %d, want 1", h.NumSamples)
	}
	v, ok := h.Vars["Speed"]
	if !ok {
		t.Fatal("Speed channel not decoded")
	}
	if got := h.Float(v, 0); math.Abs(got-42.5) > 1e-5 {
		t.Errorf("Float(Speed,0) = %v, want 42.5", got)
	}
	if !strings.Contains(h.SessionYAML(), "TrackName: testtrack") {
		t.Errorf("SessionYAML missing track name: %q", h.SessionYAML())
	}
}

func TestOpenRejectsShortFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short.ibt")
	if err := os.WriteFile(path, make([]byte, 10), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Error("Open on a 10-byte file: want error, got nil (would panic slicing the header)")
	}
}
