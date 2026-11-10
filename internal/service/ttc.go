package service

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// TrueType Collection (.ttc) handling for the PDF renderer.
//
// fpdf's UTF8 font parser only understands a single-face sfnt file: handed a
// collection it fails to read the metrics and silently drops the font, which
// surfaces much later as "undefined font: cjk". The most common CJK fonts on
// Linux are collections (NotoSansCJK-Regular.ttc), so PDF export would stay
// broken on exactly the platform that matters. extractFirstTTCFace rebuilds a
// standalone sfnt from the first face of a collection.

const (
	ttcTag           = "ttcf"
	sfntHeaderSize   = 12
	ttcHeaderSize    = 12
	tableRecordSize  = 16
	tableAlignOffset = 4
)

var (
	errNotTTC        = errors.New("not a TrueType collection")
	errMalformedTTC  = errors.New("malformed TrueType collection")
	errNoTTCFaces    = errors.New("TrueType collection has no faces")
	errTruncatedFont = errors.New("font data is truncated")
)

// isTTC reports whether data starts with a TrueType Collection header.
func isTTC(data []byte) bool {
	return len(data) >= ttcHeaderSize && string(data[0:4]) == ttcTag
}

// extractFirstTTCFace returns a standalone sfnt font holding the first face of
// the collection in data. Table data is copied verbatim (fpdf reads the tables
// directly and does not validate the per-table checksums), only the table
// directory offsets are rewritten and 4-byte aligned.
func extractFirstTTCFace(data []byte) ([]byte, error) {
	if !isTTC(data) {
		return nil, errNotTTC
	}
	faceCount := binary.BigEndian.Uint32(data[8:12])
	if faceCount == 0 {
		return nil, errNoTTCFaces
	}
	if len(data) < ttcHeaderSize+4 {
		return nil, errMalformedTTC
	}
	faceOffset := int(binary.BigEndian.Uint32(data[12:16]))
	if faceOffset < 0 || faceOffset+sfntHeaderSize > len(data) {
		return nil, errMalformedTTC
	}

	header := data[faceOffset : faceOffset+sfntHeaderSize]
	numTables := int(binary.BigEndian.Uint16(header[4:6]))
	if numTables == 0 {
		return nil, errMalformedTTC
	}
	directoryEnd := faceOffset + sfntHeaderSize + numTables*tableRecordSize
	if directoryEnd > len(data) {
		return nil, errTruncatedFont
	}

	type table struct {
		tag    [4]byte
		check  uint32
		offset int
		length int
	}
	tables := make([]table, 0, numTables)
	for i := 0; i < numTables; i++ {
		record := data[faceOffset+sfntHeaderSize+i*tableRecordSize:]
		var tag [4]byte
		copy(tag[:], record[0:4])
		offset := int(binary.BigEndian.Uint32(record[8:12]))
		length := int(binary.BigEndian.Uint32(record[12:16]))
		if offset < 0 || length < 0 || offset+length > len(data) {
			return nil, fmt.Errorf("%w: table %q offset %d length %d", errTruncatedFont, string(tag[:]), offset, length)
		}
		tables = append(tables, table{
			tag: tag, check: binary.BigEndian.Uint32(record[4:8]), offset: offset, length: length,
		})
	}

	next := sfntHeaderSize + numTables*tableRecordSize
	offsets := make([]int, len(tables))
	for i, t := range tables {
		offsets[i] = next
		next += t.length
		if pad := next % tableAlignOffset; pad != 0 {
			next += tableAlignOffset - pad
		}
	}

	out := make([]byte, next)
	copy(out[0:sfntHeaderSize], header[0:sfntHeaderSize])
	for i, t := range tables {
		record := out[sfntHeaderSize+i*tableRecordSize:]
		copy(record[0:4], t.tag[:])
		binary.BigEndian.PutUint32(record[4:8], t.check)
		binary.BigEndian.PutUint32(record[8:12], uint32(offsets[i]))
		binary.BigEndian.PutUint32(record[12:16], uint32(t.length))
		copy(out[offsets[i]:offsets[i]+t.length], data[t.offset:t.offset+t.length])
	}
	return out, nil
}

// sfntFontBytes returns data as a single-face sfnt font, unwrapping a TrueType
// collection when needed.
func sfntFontBytes(data []byte) ([]byte, error) {
	face := data
	if isTTC(data) {
		unwrapped, err := extractFirstTTCFace(data)
		if err != nil {
			return nil, err
		}
		face = unwrapped
	}
	if !hasTrueTypeOutlines(face) {
		return nil, errUnsupportedOutlines
	}
	return face, nil
}

// TrueType-outline detection.
//
// fpdf's UTF8 parser reads `glyf`/`loca` outlines only: an OpenType/CFF face
// (Noto Sans CJK ships as CFF, i.e. `OTTO` with a `CFF ` table) parses to
// nothing and the font is dropped with a bare "get metrics Error" print. A font
// is therefore only usable when the face carries a `glyf` table.

var errUnsupportedOutlines = errors.New("font has no TrueType (glyf) outlines; CFF/OTTO fonts are not supported by the PDF renderer")

// sfntTableTags returns the table tags of a single-face sfnt header.
func sfntTableTags(header []byte) ([]string, error) {
	if len(header) < sfntHeaderSize {
		return nil, errTruncatedFont
	}
	numTables := int(binary.BigEndian.Uint16(header[4:6]))
	if numTables == 0 || len(header) < sfntHeaderSize+numTables*tableRecordSize {
		return nil, errTruncatedFont
	}
	tags := make([]string, 0, numTables)
	for i := 0; i < numTables; i++ {
		tags = append(tags, string(header[sfntHeaderSize+i*tableRecordSize:][0:4]))
	}
	return tags, nil
}

// hasTrueTypeOutlines reports whether the font carries `glyf` outlines.
func hasTrueTypeOutlines(face []byte) bool {
	tags, err := sfntTableTags(face)
	if err != nil {
		return false
	}
	for _, tag := range tags {
		if tag == "glyf" {
			return true
		}
	}
	return false
}

// usableFontFile reports whether the font file at path resolves to a face with
// TrueType outlines, i.e. whether the PDF renderer can actually embed it. It
// reads only the table directory, never the whole (often 20MB+) font.
func usableFontFile(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()

	head := make([]byte, ttcHeaderSize)
	if _, err := io.ReadFull(file, head); err != nil {
		return false
	}
	if !isTTC(head) {
		// Re-read from the start: the first 12 bytes are the sfnt header.
		numTables := int(binary.BigEndian.Uint16(head[4:6]))
		directory := make([]byte, sfntHeaderSize+numTables*tableRecordSize)
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return false
		}
		if _, err := io.ReadFull(file, directory); err != nil {
			return false
		}
		return hasTrueTypeOutlines(directory)
	}
	if len(head) < ttcHeaderSize {
		return false
	}
	faceOffset := int64(binary.BigEndian.Uint32(head[12:16]))
	if faceOffset <= 0 {
		return false
	}
	if _, err := file.Seek(faceOffset, io.SeekStart); err != nil {
		return false
	}
	sfnt := make([]byte, sfntHeaderSize)
	if _, err := io.ReadFull(file, sfnt); err != nil {
		return false
	}
	numTables := int(binary.BigEndian.Uint16(sfnt[4:6]))
	directory := make([]byte, sfntHeaderSize+numTables*tableRecordSize)
	if _, err := file.Seek(faceOffset, io.SeekStart); err != nil {
		return false
	}
	if _, err := io.ReadFull(file, directory); err != nil {
		return false
	}
	return hasTrueTypeOutlines(directory)
}

// pdfFontPath returns the first configured or discovered CJK font the renderer
// can embed. Candidates that exist but only carry CFF outlines (Noto Sans CJK)
// are skipped instead of selected and then failing at render time.
func pdfFontPath() string {
	if path := strings.TrimSpace(os.Getenv("RGX_EXPORT_PDF_FONT")); path != "" {
		return path
	}
	for _, path := range pdfFontCandidates() {
		if usableFontFile(path) {
			return path
		}
	}
	return ""
}

// pdfFontCandidates lists the CJK fonts probed when RGX_EXPORT_PDF_FONT is not
// set. Later Linux entries exist because the Noto packages ship CFF collections
// this renderer cannot embed.
func pdfFontCandidates() []string {
	return []string{
		`C:\Windows\Fonts\Deng.ttf`,
		`C:\Windows\Fonts\msyh.ttc`,
		`C:\Windows\Fonts\simhei.ttf`,
		"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
		"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
		"/usr/share/fonts/truetype/arphic/uming.ttc",
		"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttf",
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
	}
}
