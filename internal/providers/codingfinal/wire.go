package codingfinal

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	connectFlagCompressed = 0x01
	connectFlagEndStream  = 0x02
	maxConnectFrameBytes  = 16 << 20
	maxDecodedFrameBytes  = 16 << 20
)

func encodeVarint(value uint64) []byte {
	var out [10]byte
	pos := 0
	for value >= 0x80 {
		out[pos] = byte(value) | 0x80
		value >>= 7
		pos++
	}
	out[pos] = byte(value)
	return out[:pos+1]
}

func encodeTag(field int, wire byte) []byte {
	return encodeVarint(uint64(field<<3) | uint64(wire))
}

func encodeBytesField(field int, value []byte) []byte {
	var out bytes.Buffer
	out.Write(encodeTag(field, 2))
	out.Write(encodeVarint(uint64(len(value))))
	out.Write(value)
	return out.Bytes()
}

func encodeStringField(field int, value string) []byte {
	if value == "" {
		return nil
	}
	return encodeBytesField(field, []byte(value))
}

func encodeMessageField(field int, parts ...[]byte) []byte {
	var inner bytes.Buffer
	for _, part := range parts {
		inner.Write(part)
	}
	return encodeBytesField(field, inner.Bytes())
}

func encodeVarintField(field int, value uint64) []byte {
	if value == 0 {
		return nil
	}
	out := encodeTag(field, 0)
	out = append(out, encodeVarint(value)...)
	return out
}

type protoField struct {
	number int
	wire   byte
	varint uint64
	bytes  []byte
}

func decodeProtoFields(data []byte) ([]protoField, error) {
	fields := make([]protoField, 0, 8)
	for pos := 0; pos < len(data); {
		tag, next, err := decodeProtoVarint(data, pos)
		if err != nil {
			return nil, err
		}
		pos = next
		number := int(tag >> 3)
		wire := byte(tag & 7)
		if number <= 0 {
			return nil, errors.New("invalid protobuf field number")
		}
		switch wire {
		case 0:
			value, next, err := decodeProtoVarint(data, pos)
			if err != nil {
				return nil, err
			}
			fields = append(fields, protoField{number: number, wire: wire, varint: value})
			pos = next
		case 1:
			if len(data)-pos < 8 {
				return nil, errors.New("truncated protobuf fixed64 field")
			}
			fields = append(fields, protoField{number: number, wire: wire, bytes: data[pos : pos+8]})
			pos += 8
		case 2:
			length, next, err := decodeProtoVarint(data, pos)
			if err != nil || length > uint64(len(data)-next) {
				return nil, errors.New("truncated protobuf bytes field")
			}
			end := next + int(length)
			fields = append(fields, protoField{number: number, wire: wire, bytes: data[next:end]})
			pos = end
		case 5:
			if len(data)-pos < 4 {
				return nil, errors.New("truncated protobuf fixed32 field")
			}
			fields = append(fields, protoField{number: number, wire: wire, bytes: data[pos : pos+4]})
			pos += 4
		default:
			return nil, fmt.Errorf("unsupported protobuf wire type %d", wire)
		}
	}
	return fields, nil
}

func decodeProtoVarint(data []byte, start int) (uint64, int, error) {
	var value uint64
	for i, pos := 0, start; pos < len(data) && i < 10; i, pos = i+1, pos+1 {
		b := data[pos]
		if i == 9 && b > 1 {
			return 0, 0, errors.New("protobuf varint exceeds uint64")
		}
		value |= uint64(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return value, pos + 1, nil
		}
	}
	return 0, 0, errors.New("truncated protobuf varint")
}

func firstField(fields []protoField, number int, wire byte) (protoField, bool) {
	for _, field := range fields {
		if field.number == number && field.wire == wire {
			return field, true
		}
	}
	return protoField{}, false
}

func connectFrame(payload []byte, flags byte) ([]byte, error) {
	if len(payload) > maxConnectFrameBytes {
		return nil, fmt.Errorf("%w: Connect frame exceeds limit", ErrTruncated)
	}
	frame := make([]byte, 5+len(payload))
	frame[0] = flags
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	copy(frame[5:], payload)
	return frame, nil
}

type connectReader struct {
	reader io.Reader
}

func (r *connectReader) next() (byte, []byte, error) {
	var header [5]byte
	n, err := io.ReadFull(r.reader, header[:])
	if err != nil {
		if err == io.EOF && n == 0 {
			return 0, nil, io.EOF
		}
		return 0, nil, fmt.Errorf("%w: truncated Connect frame header", ErrTruncated)
	}
	length := binary.BigEndian.Uint32(header[1:])
	if length > maxConnectFrameBytes {
		return 0, nil, fmt.Errorf("%w: Connect frame exceeds limit", ErrUnsupported)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(r.reader, payload); err != nil {
		return 0, nil, fmt.Errorf("%w: truncated Connect frame", ErrTruncated)
	}
	flags := header[0]
	if flags&^(connectFlagCompressed|connectFlagEndStream) != 0 {
		return 0, nil, fmt.Errorf("%w: invalid Connect frame flags", ErrUnsupported)
	}
	if flags&connectFlagCompressed == 0 {
		return flags, payload, nil
	}
	decoded, err := gunzipBounded(payload)
	if err != nil {
		return 0, nil, err
	}
	return flags &^ connectFlagCompressed, decoded, nil
}

func gunzipBounded(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid gzip Connect frame", ErrTruncated)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(io.LimitReader(reader, maxDecodedFrameBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid gzip Connect frame", ErrTruncated)
	}
	if len(decoded) > maxDecodedFrameBytes {
		return nil, fmt.Errorf("%w: decoded Connect frame exceeds limit", ErrUnsupported)
	}
	return decoded, nil
}

func parseJSONError(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil || value == nil {
		return fmt.Errorf("%w: invalid Connect end-stream trailer", ErrTruncated)
	}
	if raw, ok := value["error"]; ok && raw != nil {
		return fmt.Errorf("coding final adapter: upstream error: %s", streamErrorText(raw))
	}
	return nil
}
