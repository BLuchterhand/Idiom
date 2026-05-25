package resp

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type Kind int

const (
	SimpleKind Kind = iota
	ErrorKind
	BulkKind
	IntegerKind
	ArrayKind
)

type Value struct {
	Kind  Kind
	Text  string
	Int   int64
	Array []Value
	Null  bool
}

func Simple(text string) Value { return Value{Kind: SimpleKind, Text: text} }
func Error(text string) Value  { return Value{Kind: ErrorKind, Text: text} }
func Bulk(text string) Value   { return Value{Kind: BulkKind, Text: text} }
func NullBulk() Value          { return Value{Kind: BulkKind, Null: true} }
func Integer(n int64) Value    { return Value{Kind: IntegerKind, Int: n} }
func Array(values []Value) Value {
	return Value{Kind: ArrayKind, Array: append([]Value(nil), values...)}
}

func Write(w io.Writer, value Value) error {
	_, err := io.WriteString(w, Encode(value))
	return err
}

func Encode(value Value) string {
	switch value.Kind {
	case SimpleKind:
		return "+" + value.Text + "\r\n"
	case ErrorKind:
		return "-" + value.Text + "\r\n"
	case BulkKind:
		if value.Null {
			return "$-1\r\n"
		}
		return fmt.Sprintf("$%d\r\n%s\r\n", len(value.Text), value.Text)
	case IntegerKind:
		return fmt.Sprintf(":%d\r\n", value.Int)
	case ArrayKind:
		var b strings.Builder
		fmt.Fprintf(&b, "*%d\r\n", len(value.Array))
		for _, item := range value.Array {
			b.WriteString(Encode(item))
		}
		return b.String()
	default:
		return "-ERR unsupported RESP value\r\n"
	}
}

func Read(r *bufio.Reader) (Value, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return Value{}, err
	}
	switch prefix {
	case '+':
		line, err := readLine(r)
		return Simple(line), err
	case '-':
		line, err := readLine(r)
		return Error(line), err
	case ':':
		line, err := readLine(r)
		if err != nil {
			return Value{}, err
		}
		n, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			return Value{}, fmt.Errorf("invalid integer %q: %w", line, err)
		}
		return Integer(n), nil
	case '$':
		line, err := readLine(r)
		if err != nil {
			return Value{}, err
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			return Value{}, fmt.Errorf("invalid bulk length %q: %w", line, err)
		}
		if n < 0 {
			return NullBulk(), nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return Value{}, err
		}
		if string(buf[n:]) != "\r\n" {
			return Value{}, fmt.Errorf("bulk string missing CRLF terminator")
		}
		return Bulk(string(buf[:n])), nil
	case '*':
		line, err := readLine(r)
		if err != nil {
			return Value{}, err
		}
		n, err := strconv.Atoi(line)
		if err != nil || n < 0 {
			return Value{}, fmt.Errorf("invalid array length %q", line)
		}
		values := make([]Value, 0, n)
		for i := 0; i < n; i++ {
			item, err := Read(r)
			if err != nil {
				return Value{}, err
			}
			values = append(values, item)
		}
		return Array(values), nil
	default:
		return Value{}, fmt.Errorf("unknown RESP prefix %q", prefix)
	}
}

func Command(value Value) ([]string, error) {
	if value.Kind != ArrayKind {
		return nil, fmt.Errorf("command must be a RESP array")
	}
	args := make([]string, 0, len(value.Array))
	for _, item := range value.Array {
		switch item.Kind {
		case BulkKind:
			if item.Null {
				return nil, fmt.Errorf("command argument cannot be null")
			}
			args = append(args, item.Text)
		case SimpleKind:
			args = append(args, item.Text)
		default:
			return nil, fmt.Errorf("command argument must be a string")
		}
	}
	return args, nil
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(line, "\r\n") {
		return "", fmt.Errorf("RESP line missing CRLF terminator")
	}
	return strings.TrimSuffix(line, "\r\n"), nil
}