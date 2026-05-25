package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"redisclone/internal/resp"
	"redisclone/internal/server"
	"redisclone/internal/store"
)

func main() {
	handler := server.New(store.New(nil))
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.EqualFold(line, "QUIT") {
			return
		}
		if err := resp.Write(os.Stdout, handler.Execute(strings.Fields(line))); err != nil {
			fmt.Fprintf(os.Stderr, "write response: %v\n", err)
			os.Exit(1)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "read command: %v\n", err)
		os.Exit(1)
	}
}