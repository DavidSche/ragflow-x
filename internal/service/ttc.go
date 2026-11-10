package service

import (
	"encoding/binary"
	"errors"
	"fmt"
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
	if !isTTC(data) {
		return data, nil
	}
	return extractFirstTTCFace(data)
}
