package stream

import (
	"errors"
	"testing"
)

func TestParseRange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		header string
		size   int64
		want   ByteRange
		err    error
	}{
		{name: "zero to EOF", header: "bytes=0-", size: 1000000, want: ByteRange{Start: 0, End: 999999}},
		{name: "one to EOF", header: "bytes=1-", size: 1000000, want: ByteRange{Start: 1, End: 999999}},
		{name: "middle to EOF", header: "bytes=500000-", size: 1000000, want: ByteRange{Start: 500000, End: 999999}},
		{name: "bounded", header: "bytes=100000-199999", size: 1000000, want: ByteRange{Start: 100000, End: 199999}},
		{name: "suffix", header: "bytes=-50000", size: 1000000, want: ByteRange{Start: 950000, End: 999999}},
		{name: "clamp end", header: "bytes=999900-1999999", size: 1000000, want: ByteRange{Start: 999900, End: 999999}},
		{name: "empty range", header: "bytes=", size: 1000000, err: ErrInvalidRange},
		{name: "non numeric", header: "bytes=a-b", size: 1000000, err: ErrInvalidRange},
		{name: "reverse range", header: "bytes=100-50", size: 1000000, err: ErrInvalidRange},
		{name: "invalid syntax", header: "items=0-1", size: 5000, err: ErrInvalidRange},
		{name: "multiple ranges", header: "bytes=0-1,3-4", size: 5000, err: ErrInvalidRange},
		{name: "at EOF", header: "bytes=1000000-", size: 1000000, err: ErrUnsatisfiableRange},
		{name: "past EOF", header: "bytes=2000000-", size: 1000000, err: ErrUnsatisfiableRange},
		{name: "empty object", header: "bytes=0-", size: 0, err: ErrUnsatisfiableRange},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseRange(test.header, test.size)
			if !errors.Is(err, test.err) {
				t.Fatalf("error = %v, want %v", err, test.err)
			}
			if err == nil && got != test.want {
				t.Fatalf("range = %#v, want %#v", got, test.want)
			}
		})
	}
}
