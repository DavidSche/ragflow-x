package service

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-pdf/fpdf"
)

// buildSyntheticTTC assembles a two-face collection whose faces share nothing,
// so the extractor has to rewrite the table directory of the requested face and
// copy only that face's tables.
func buildSyntheticTTC(t *testing.T) ([]byte, [][2]string) {
	t.Helper()
	tagAsArray := func(tag string) [4]byte {
		var out [4]byte
		copy(out[:], tag)
		return out
	}
	makeFace := func(tag string, payload []byte) []byte {
		// sfnt header (12) + one table record (16) + data (28 onwards)
		face := make([]byte, 28+len(payload))
		sfntVersion := tagAsArray("OTTO")
		copy(face[0:4], sfntVersion[:])
		binary.BigEndian.PutUint16(face[4:6], 1) // numTables
		tableTag := tagAsArray(tag)
		copy(face[12:16], tableTag[:])                                // record.tag
		binary.BigEndian.PutUint32(face[24:28], uint32(len(payload))) // record.length
		copy(face[28:], payload)
		return face
	}

	faceA := makeFace("cmap", []byte("face-a-cmap"))
	faceB := makeFace("name", []byte("face-b-name"))

	total := ttcHeaderSize + 2*4 + len(faceA) + len(faceB)
	out := make([]byte, total)
	collectionTag := tagAsArray(ttcTag)
	copy(out[0:4], collectionTag[:])
	binary.BigEndian.PutUint32(out[4:8], 0x00010000)
	binary.BigEndian.PutUint32(out[8:12], 2)
	offsetA := ttcHeaderSize + 2*4
	offsetB := offsetA + len(faceA)
	binary.BigEndian.PutUint32(out[12:16], uint32(offsetA))
	binary.BigEndian.PutUint32(out[16:20], uint32(offsetB))
	copy(out[offsetA:], faceA)
	copy(out[offsetB:], faceB)
	// Face table offsets are absolute file offsets, patch them in place.
	binary.BigEndian.PutUint32(out[offsetA+20:offsetA+24], uint32(offsetA+28))
	binary.BigEndian.PutUint32(out[offsetB+20:offsetB+24], uint32(offsetB+28))
	return out, [][2]string{{"cmap", "face-a-cmap"}}
}

func TestExtractFirstTTCFaceRebuildsStandaloneSfnt(t *testing.T) {
	collection, want := buildSyntheticTTC(t)
	if !isTTC(collection) {
		t.Fatal("synthetic data is not detected as a collection")
	}
	face, err := extractFirstTTCFace(collection)
	if err != nil {
		t.Fatalf("extractFirstTTCFace() error = %v", err)
	}
	if isTTC(face) {
		t.Fatal("extracted face is still a collection")
	}
	if got := string(face[0:4]); got != "OTTO" {
		t.Fatalf("sfnt version = %q, want OTTO", got)
	}
	if got := binary.BigEndian.Uint16(face[4:6]); got != 1 {
		t.Fatalf("numTables = %d, want 1", got)
	}
	record := face[sfntHeaderSize:]
	tag := string(record[0:4])
	offset := int(binary.BigEndian.Uint32(record[8:12]))
	length := int(binary.BigEndian.Uint32(record[12:16]))
	if tag != want[0][0] {
		t.Fatalf("table tag = %q, want %q (second face leaked in)", tag, want[0][0])
	}
	if offset+length > len(face) {
		t.Fatalf("table [%d,%d) exceeds rebuilt font size %d", offset, offset+length, len(face))
	}
	if got := string(face[offset : offset+length]); got != want[0][1] {
		t.Fatalf("table payload = %q, want %q", got, want[0][1])
	}
	if !bytes.Equal(face[offset:offset+length], []byte(want[0][1])) {
		t.Fatal("table payload mismatch")
	}
}

func TestSfntFontBytesRequiresTrueTypeOutlines(t *testing.T) {
	glyfFont := syntheticSfnt(t, "glyf")
	out, err := sfntFontBytes(glyfFont)
	if err != nil {
		t.Fatalf("sfntFontBytes(glyf font) error = %v", err)
	}
	if !bytes.Equal(out, glyfFont) {
		t.Fatal("plain sfnt font must pass through unchanged")
	}

	// OpenType/CFF faces (Noto Sans CJK) parse to nothing in fpdf; they must be
	// rejected up front with an actionable error instead of "undefined font: cjk".
	cffFont := syntheticSfnt(t, "CFF ")
	if _, err := sfntFontBytes(cffFont); err == nil {
		t.Fatal("sfntFontBytes(CFF font) = nil error, want unsupported-outlines rejection")
	}
}

// syntheticSfnt builds a minimal single-face sfnt with one table named tag.
func syntheticSfnt(t *testing.T, tag string) []byte {
	t.Helper()
	face := make([]byte, 28+4)
	copy(face[0:4], []byte("OTTO"))
	binary.BigEndian.PutUint16(face[4:6], 1)
	copy(face[12:16], []byte(tag))
	binary.BigEndian.PutUint32(face[20:24], 28) // record.offset
	binary.BigEndian.PutUint32(face[24:28], 4)  // record.length
	copy(face[28:], []byte("data"))
	return face
}

func TestExtractFirstTTCFaceRejectsBadInput(t *testing.T) {
	collection, _ := buildSyntheticTTC(t)
	cases := []struct {
		name string
		data []byte
	}{
		{name: "plain font", data: []byte("\x00\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")},
		{name: "empty", data: nil},
		{name: "no faces", data: func() []byte {
			out := make([]byte, ttcHeaderSize)
			copy(out[0:4], []byte(ttcTag))
			return out
		}()},
		{name: "face offset past end", data: func() []byte {
			out := make([]byte, len(collection))
			copy(out, collection)
			binary.BigEndian.PutUint32(out[12:16], uint32(len(out)+8))
			return out
		}()},
		{name: "truncated directory", data: collection[:ttcHeaderSize+2*4+4]},
		{name: "table past end", data: func() []byte {
			out := make([]byte, len(collection))
			copy(out, collection)
			faceOffset := ttcHeaderSize + 2*4
			binary.BigEndian.PutUint32(out[faceOffset+20:faceOffset+24], uint32(len(out)-4))
			binary.BigEndian.PutUint32(out[faceOffset+24:faceOffset+28], 64)
			return out
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := extractFirstTTCFace(tc.data); err == nil {
				t.Fatalf("extractFirstTTCFace(%s) = nil error, want rejection", tc.name)
			}
		})
	}
}

// TestTrueTypeCollectionUnwrapsIntoUsablePDFFont proves the whole point of the
// extractor: fpdf rejects a collection but accepts the face we rebuild. It runs
// wherever a real collection exists (Linux CI: Noto CJK; Windows: Microsoft
// YaHei) and skips otherwise.
func TestTrueTypeCollectionUnwrapsIntoUsablePDFFont(t *testing.T) {
	candidates := []string{
		`C:\Windows\Fonts\msyh.ttc`,
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
	}
	var raw []byte
	var source string
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err == nil {
			raw, source = data, path
			t.Logf("collection under test: %s", path)
			break
		}
	}
	if raw == nil {
		t.Skip("no TrueType collection on this machine")
	}
	if !isTTC(raw) {
		t.Skip("candidate font is not a collection")
	}
	if !usableFontFile(source) {
		t.Fatalf("usableFontFile(%s) = false for a real, embeddable collection", source)
	}

	dropped := fpdf.New("P", "mm", "A4", "")
	dropped.AddUTF8FontFromBytes("cjk", "", raw)
	dropped.SetFont("cjk", "", 10)
	if err := dropped.Error(); err == nil {
		t.Log("fpdf accepted the raw collection; the unwrap is then a no-op for this font")
	}

	face, err := extractFirstTTCFace(raw)
	if err != nil {
		t.Fatalf("extractFirstTTCFace() error = %v", err)
	}
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddUTF8FontFromBytes("cjk", "", face)
	pdf.SetFont("cjk", "", 10)
	if err := pdf.Error(); err != nil {
		t.Fatalf("fpdf rejected the unwrapped face: %v", err)
	}
	pdf.AddPage()
	pdf.CellFormat(0, 8, "安全导出 PDF 回退字体", "0", 1, "L", false, 0, "")
	if err := pdf.Output(&bytes.Buffer{}); err != nil {
		t.Fatalf("render with unwrapped face: %v", err)
	}
}

func TestUsableFontFileFiltersByOutlines(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}
	glyf := write("glyf.ttf", syntheticSfnt(t, "glyf"))
	cff := write("cff.ttf", syntheticSfnt(t, "CFF "))
	short := write("short.ttf", []byte("xx"))

	if !usableFontFile(glyf) {
		t.Fatal("font with glyf outlines must be selectable")
	}
	if usableFontFile(cff) {
		t.Fatal("CFF font must be filtered out of the candidate list")
	}
	if usableFontFile(short) {
		t.Fatal("truncated font must be filtered out")
	}
	if usableFontFile(filepath.Join(dir, "absent.ttf")) {
		t.Fatal("missing font must be filtered out")
	}
}

// TestUsableFontFileHandlesCollections pins the collection branch of the probe:
// reading only the 12-byte collection header used to slice past the buffer
// (slice bounds out of range [:16] with capacity 12) as soon as a .ttc was the
// first discovered candidate.
func TestUsableFontFileHandlesCollections(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}
	build := func(tableTag string) []byte {
		face := syntheticSfnt(t, tableTag)
		offset := ttcHeaderSize + 4
		out := make([]byte, offset+len(face))
		copy(out[0:4], []byte(ttcTag))
		binary.BigEndian.PutUint32(out[4:8], 0x00010000)
		binary.BigEndian.PutUint32(out[8:12], 1)
		binary.BigEndian.PutUint32(out[12:16], uint32(offset))
		copy(out[offset:], face)
		// The face's single table lives 28 bytes into the face.
		binary.BigEndian.PutUint32(out[offset+20:offset+24], uint32(offset+28))
		return out
	}
	glyfCollection := write("glyf.ttc", build("glyf"))
	cffCollection := write("cff.ttc", build("CFF "))
	headerOnly := write("header-only.ttc", func() []byte {
		out := make([]byte, ttcHeaderSize)
		copy(out[0:4], []byte(ttcTag))
		binary.BigEndian.PutUint32(out[8:12], 1)
		return out
	}())

	if !usableFontFile(glyfCollection) {
		t.Fatal("collection with glyf face must be selectable")
	}
	if usableFontFile(cffCollection) {
		t.Fatal("collection with CFF face must be filtered out")
	}
	if usableFontFile(headerOnly) {
		t.Fatal("truncated collection header must be filtered out, not panic")
	}
}
