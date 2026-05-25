# redic

`redic` is a deliberately small Redis-like command runner used as an Idiom feature-planner TUI testbed. It keeps all state in memory, speaks enough RESP to exercise parser work, and has focused tests for store, command, and CLI-facing behavior.

Supported commands: `PING`, `ECHO`, `SET`, `GET`, `DEL`, `INCR`, `EXPIRE`, `TTL`, `LPUSH`, and `LRANGE`.