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
		{name: "from zero", header: "bytes=0-", size: 5000, want: ByteRange{Start: 0, End: 4999}},
		{name: "from offset", header: "bytes=1000-", size: 5000, want: ByteRange{Start: 1000, End: 4999}},
		{name: "bounded", header: "bytes=1000-2000", size: 5000, want: ByteRange{Start: 1000, End: 2000}},
		{name: "clamp end", header: "bytes=4900-9999", size: 5000, want: ByteRange{Start: 4900, End: 4999}},
		{name: "suffix", header: "bytes=-250", size: 5000, want: ByteRange{Start: 4750, End: 4999}},
		{name: "invalid syntax", header: "items=0-1", size: 5000, err: ErrInvalidRange},
		{name: "multiple ranges", header: "bytes=0-1,3-4", size: 5000, err: ErrInvalidRange},
		{name: "past EOF", header: "bytes=5000-", size: 5000, err: ErrUnsatisfiableRange},
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
