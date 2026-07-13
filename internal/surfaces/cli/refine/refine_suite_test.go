package refine

import (
	"fmt"
	"os"
	"strconv"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRefine(t *testing.T) {
	if os.Getenv("IDIOM_REFINE_HELPER_PROCESS") == "1" {
		fmt.Fprintln(os.Stdout, "stdout marker")
		fmt.Fprintln(os.Stderr, "stderr marker")
		exitCode, _ := strconv.Atoi(os.Getenv("IDIOM_REFINE_HELPER_EXIT"))
		os.Exit(exitCode)
	}

	RegisterFailHandler(Fail)
	RunSpecs(t, "CLI Refine Suite")
}
