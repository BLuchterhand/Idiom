package llama

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/backends"
)

func TestDebugListModels(t *testing.T) {
	if os.Getenv("IDIOM_RUN_DEBUG_TESTS") != "1" {
		t.Skip("set IDIOM_RUN_DEBUG_TESTS=1 to run local debug smoke tests")
	}

	cfg, err := config.Load("/workspace/idiom/.idiom/config.json")
	if err != nil {
		t.Fatal("config load error:", err)
	}

	fmt.Println("endpoint:", cfg.LlamaServer.EndpointBaseURL())
	fmt.Println("model:", cfg.LlamaServer.Model)
	fmt.Println("kind:", cfg.LlamaServer.Kind)
	fmt.Println("host:", cfg.LlamaServer.Host)
	fmt.Println("port:", cfg.LlamaServer.Port)

	callTimeout := time.Duration(cfg.Chat.RequestTimeoutSeconds) * time.Second
	fmt.Println("callTimeout:", callTimeout)

	client := NewClientForServer(cfg.LlamaServer, callTimeout)
	fmt.Println("client baseURL:", client.baseURL)
	fmt.Println("client backend:", client.Backend())
	fmt.Println("client timeout:", client.http.Timeout)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	models, err := client.ListModels(ctx)
	fmt.Println("models:", models, "err:", err)

	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	}
}

func TestDebugRoleSpec(t *testing.T) {
	if os.Getenv("IDIOM_RUN_DEBUG_TESTS") != "1" {
		t.Skip("set IDIOM_RUN_DEBUG_TESTS=1 to run local debug smoke tests")
	}

	projectRoot := "/workspace/idiom"

	cfg, err := config.Load("/workspace/idiom/.idiom/config.json")
	if err != nil {
		t.Fatal("config load error:", err)
	}

	fmt.Println("=== backends.json resolution ===")

	file, found, err := backends.LoadFromProjectRoot(projectRoot)
	fmt.Println("found:", found, "err:", err)

	if found {
		fmt.Println("default_profile:", file.DefaultProfile)

		prof, ok := file.Profile("")
		fmt.Println("default profile ok:", ok, "name:", file.DefaultProfile)

		if ok {
			for _, m := range prof.Models {
				fmt.Printf("  model alias=%s port=%d host=%s roles=%v\n", m.Alias, m.Port, m.Host, m.Roles)
			}

			fmt.Println("  proxy:", prof.Proxy)

			alias, ok := prof.AliasForRole("planner")
			fmt.Println("  planner alias:", alias, "ok:", ok)
		}

		// Check explicit alias resolution
		explicit := strings.TrimSpace(cfg.LlamaServer.Model)
		fmt.Println("\nexplicit model from config:", explicit)

		m, ok := file.ModelByAlias(explicit)
		fmt.Println("file.ModelByAlias:", ok)

		if ok {
			fmt.Printf("  alias=%s port=%d host=%s binary=%s roles=%v\n", m.Alias, m.Port, m.Host, m.Binary, m.Roles)
		}
	}

	// Check what the plannerSpec would look like
	// Simulating roleSpec logic
	fmt.Println("\n=== planner spec ===")

	explicit := strings.TrimSpace(cfg.LlamaServer.Model)
	if explicit != "" {
		prof, _ := file.Profile("")
		m, ok := prof.ModelByAlias(explicit)
		fmt.Println("prof.ModelByAlias:", ok)

		if !ok {
			m, ok = file.ModelByAlias(explicit)
			fmt.Println("file.ModelByAlias:", ok)

			if ok {
				fmt.Printf("  alias=%s port=%d host=%s roles=%v\n", m.Alias, m.Port, m.Host, m.Roles)
			}
		}
	}
}
