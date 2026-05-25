package server

import (
	"strconv"
	"strings"
	"time"

	"redisclone/internal/resp"
	"redisclone/internal/store"
)

type Handler struct {
	store *store.Store
}

func New(s *store.Store) *Handler {
	if s == nil {
		s = store.New(nil)
	}
	return &Handler{store: s}
}

func (h *Handler) Execute(args []string) resp.Value {
	if len(args) == 0 {
		return resp.Error("ERR empty command")
	}
	switch strings.ToUpper(args[0]) {
	case "PING":
		if len(args) == 1 {
			return resp.Simple("PONG")
		}
		if len(args) == 2 {
			return resp.Bulk(args[1])
		}
		return resp.Error("ERR wrong number of arguments for PING")
	case "ECHO":
		if len(args) != 2 {
			return resp.Error("ERR wrong number of arguments for ECHO")
		}
		return resp.Bulk(args[1])
	case "SET":
		return h.set(args)
	case "GET":
		if len(args) != 2 {
			return resp.Error("ERR wrong number of arguments for GET")
		}
		if value, ok := h.store.Get(args[1]); ok {
			return resp.Bulk(value)
		}
		return resp.NullBulk()
	case "DEL":
		if len(args) < 2 {
			return resp.Error("ERR wrong number of arguments for DEL")
		}
		return resp.Integer(int64(h.store.Del(args[1:]...)))
	case "INCR":
		if len(args) != 2 {
			return resp.Error("ERR wrong number of arguments for INCR")
		}
		value, err := h.store.Incr(args[1])
		if err != nil {
			return resp.Error("ERR " + err.Error())
		}
		return resp.Integer(value)
	case "EXPIRE":
		return h.expire(args)
	case "TTL":
		if len(args) != 2 {
			return resp.Error("ERR wrong number of arguments for TTL")
		}
		ttl, ok := h.store.TTL(args[1])
		if !ok {
			return resp.Integer(-2)
		}
		if ttl < 0 {
			return resp.Integer(-1)
		}
		return resp.Integer(int64(ttl.Round(time.Second) / time.Second))
	case "LPUSH":
		if len(args) < 3 {
			return resp.Error("ERR wrong number of arguments for LPUSH")
		}
		length, err := h.store.LPush(args[1], args[2:]...)
		if err != nil {
			return resp.Error("WRONGTYPE " + err.Error())
		}
		return resp.Integer(int64(length))
	case "LRANGE":
		return h.lrange(args)
	default:
		return resp.Error("ERR unknown command " + args[0])
	}
}

func (h *Handler) set(args []string) resp.Value {
	if len(args) != 3 && len(args) != 5 {
		return resp.Error("ERR wrong number of arguments for SET")
	}
	ttl := time.Duration(0)
	if len(args) == 5 {
		if strings.ToUpper(args[3]) != "EX" {
			return resp.Error("ERR unsupported SET option " + args[3])
		}
		seconds, err := strconv.Atoi(args[4])
		if err != nil || seconds <= 0 {
			return resp.Error("ERR invalid expire time")
		}
		ttl = time.Duration(seconds) * time.Second
	}
	h.store.Set(args[1], args[2], ttl)
	return resp.Simple("OK")
}

func (h *Handler) expire(args []string) resp.Value {
	if len(args) != 3 {
		return resp.Error("ERR wrong number of arguments for EXPIRE")
	}
	seconds, err := strconv.Atoi(args[2])
	if err != nil || seconds <= 0 {
		return resp.Error("ERR invalid expire time")
	}
	if h.store.Expire(args[1], time.Duration(seconds)*time.Second) {
		return resp.Integer(1)
	}
	return resp.Integer(0)
}

func (h *Handler) lrange(args []string) resp.Value {
	if len(args) != 4 {
		return resp.Error("ERR wrong number of arguments for LRANGE")
	}
	start, err := strconv.Atoi(args[2])
	if err != nil {
		return resp.Error("ERR invalid start index")
	}
	stop, err := strconv.Atoi(args[3])
	if err != nil {
		return resp.Error("ERR invalid stop index")
	}
	values, ok := h.store.LRange(args[1], start, stop)
	if !ok {
		return resp.Array(nil)
	}
	items := make([]resp.Value, 0, len(values))
	for _, value := range values {
		items = append(items, resp.Bulk(value))
	}
	return resp.Array(items)
}