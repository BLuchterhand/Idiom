# Feature-Planner Testbed

This project exists to exercise larger feature-planner delivery through the TUI. It has several seams that future atoms can extend independently: RESP parsing, in-memory storage, command handling, and CLI behavior.

Good follow-up atoms should add a command plus tests, preserve RESP output compatibility, and keep verification at `go test ./...` unless the atom introduces a narrower package gate.