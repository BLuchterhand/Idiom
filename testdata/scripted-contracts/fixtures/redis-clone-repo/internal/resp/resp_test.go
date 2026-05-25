package resp

import (
	"bufio"
	"strings"
	"testing"
)

func TestReadCommandArray(t *testing.T) {
	input := "*2\r\n$4\r\nPING\r\n$5\r\nhello\r\n"
	value, err := Read(bufio.NewReader(strings.NewReader(input)))
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	args, err := Command(value)
	if err != nil {
		t.Fatalf("Command() error = %v", err)
	}
	if got, want := strings.Join(args, ","), "PING,hello"; got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func TestEncodeRESPValues(t *testing.T) {
	cases := map[string]Value{
		"+OK\r\n": Simple("OK"),
		":7\r\n":  Integer(7),
		"$-1\r\n": NullBulk(),
	}
	for want, value := range cases {
		if got := Encode(value); got != want {
			t.Fatalf("Encode(%#v) = %q, want %q", value, got, want)
		}
	}
}