package stream

import (
	"errors"
	"strconv"
	"strings"
)

var (
	ErrInvalidRange       = errors.New("invalid Range header")
	ErrUnsatisfiableRange = errors.New("range is not satisfiable")
)

type ByteRange struct {
	Start int64
	End   int64 // Inclusive.
}

func (r ByteRange) Length() int64 { return r.End - r.Start + 1 }

// ParseRange accepts exactly one RFC 7233 byte range. Multiple ranges would
// require a multipart response and are intentionally rejected until there is a
// tested client need. A known object size is mandatory for correct 206 headers.
func ParseRange(raw string, size int64) (ByteRange, error) {
	if size < 0 {
		return ByteRange{}, ErrUnsatisfiableRange
	}
	if !strings.HasPrefix(raw, "bytes=") || strings.Count(raw, ",") != 0 {
		return ByteRange{}, ErrInvalidRange
	}
	spec := strings.TrimSpace(strings.TrimPrefix(raw, "bytes="))
	parts := strings.Split(spec, "-")
	if len(parts) != 2 {
		return ByteRange{}, ErrInvalidRange
	}
	if size == 0 {
		return ByteRange{}, ErrUnsatisfiableRange
	}

	if parts[0] == "" {
		suffixLength, err := positiveInt(parts[1])
		if err != nil {
			return ByteRange{}, ErrInvalidRange
		}
		if suffixLength >= size {
			return ByteRange{Start: 0, End: size - 1}, nil
		}
		return ByteRange{Start: size - suffixLength, End: size - 1}, nil
	}

	start, err := nonNegativeInt(parts[0])
	if err != nil {
		return ByteRange{}, ErrInvalidRange
	}
	if start >= size {
		return ByteRange{}, ErrUnsatisfiableRange
	}
	if parts[1] == "" {
		return ByteRange{Start: start, End: size - 1}, nil
	}
	end, err := nonNegativeInt(parts[1])
	if err != nil || end < start {
		return ByteRange{}, ErrInvalidRange
	}
	if end >= size {
		end = size - 1
	}
	return ByteRange{Start: start, End: end}, nil
}

func nonNegativeInt(value string) (int64, error) {
	if value == "" || strings.HasPrefix(value, "+") {
		return 0, ErrInvalidRange
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, ErrInvalidRange
	}
	return parsed, nil
}

func positiveInt(value string) (int64, error) {
	parsed, err := nonNegativeInt(value)
	if err != nil || parsed == 0 {
		return 0, ErrInvalidRange
	}
	return parsed, nil
}
