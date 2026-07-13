//go:build system_test

package systemtests_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const targetedSystemRunEnv = "IDIOM_SYSTEM_TARGETED"

type fixtureResult struct {
	stdout string
	stderr string
}

var _ = ginkgo.Describe("External system harnesses", func() {
	ginkgo.It("keeps the Unix dependency manifest coverage aligned",
		ginkgo.Label("e2e", "integration", "system", "system-install-dependencies-unix-manifest"),
		func() {
			requireUnixHost()
			requireCommand("bash")
			requireCommand("python3")
			runFixture("bash", []string{"scripts/test-install-dependencies.sh", "--manifest-only"}, nil)
		})

	ginkgo.It("validates the installed Unix discovery toolchain",
		ginkgo.Label("e2e", "integration", "system", "live", "external-toolchain", "system-install-dependencies-unix-live"),
		func() {
			requireUnixHost()
			requireCommand("bash")
			requireCommand("python3")
			runFixture("bash", []string{"scripts/test-install-dependencies.sh"}, nil)
		})

	ginkgo.It("keeps the PowerShell dependency manifest coverage aligned",
		ginkgo.Label("e2e", "integration", "system", "external-toolchain", "system-install-dependencies-powershell-manifest"),
		func() {
			requireWindowsHost()
			powershell := requireCommand("pwsh")
			runFixture(powershell, []string{"-NoLogo", "-NoProfile", "-File", "scripts/test-install-dependencies.ps1", "-ManifestOnly"}, nil)
		})

	ginkgo.It("validates the installed Windows discovery toolchain",
		ginkgo.Label("e2e", "integration", "system", "live", "external-toolchain", "system-install-dependencies-powershell-live"),
		func() {
			requireWindowsHost()
			powershell := requireCommand("pwsh")
			runFixture(powershell, []string{"-NoLogo", "-NoProfile", "-File", "scripts/test-install-dependencies.ps1"}, nil)
		})

	ginkgo.It("preserves the installed Windows binary while adding its tools profile",
		ginkgo.Label("e2e", "integration", "system", "live", "external-toolchain", "system-install-windows-integrity"),
		func() {
			requireWindowsHost()
			powershell := requireCommand("pwsh")
			installDirectory := requireEnvironment("IDIOM_INSTALL_DIR")
			version := requireEnvironment("IDIOM_VERSION")
			idiomPath := requireFile(filepath.Join(installDirectory, "idiom.exe"))
			toolPath := requireFile(filepath.Join(installDirectory, "idiom-tool.cmd"))

			versionResult := runFixtureResult(idiomPath, []string{"version"}, nil)
			gomega.Expect(versionResult.stdout).To(
				gomega.ContainSubstring("version: "+version),
				"installed idiom version output did not identify the snapshot\n--- stdout ---\n%s\n--- stderr ---\n%s",
				versionResult.stdout,
				versionResult.stderr,
			)
			assertWindowsToolProfile(powershell, toolPath)

			beforeHash := fileSHA256(idiomPath)
			runFixture(powershell, []string{"-NoLogo", "-NoProfile", "-File", "install-tools.ps1"}, nil)
			afterHash := fileSHA256(idiomPath)
			gomega.Expect(afterHash).To(
				gomega.Equal(beforeHash),
				"install-tools.ps1 changed the existing idiom.exe: before=%s after=%s",
				beforeHash,
				afterHash,
			)
			assertWindowsToolProfile(powershell, toolPath)
		})

	ginkgo.It("exercises every installed discovery tool through the CLI process boundary",
		ginkgo.Label("e2e", "integration", "system", "live", "external-toolchain", "system-discovery-tools"),
		func() {
			requireInstalledCommand("IDIOM_BIN", idiomExecutableName())
			requireDiscoveryCommand()
			requireGlobalConfig()
			if runtime.GOOS == "windows" {
				python := requirePython()
				runFixture(python, []string{"scripts/test-discovery-tools-e2e.py"}, nil)
				return
			}
			requireCommand("bash")
			requireCommand("python3")
			runFixture("bash", []string{"scripts/test-discovery-tools-e2e.sh"}, nil)
		})

	ginkgo.It("runs an installed harness through global config and its local model peer",
		ginkgo.Label("e2e", "integration", "system", "external-toolchain", "system-post-install-harness"),
		func() {
			python := requirePython()
			requireInstalledCommand("IDIOM_BIN", idiomExecutableName())
			requireGlobalConfig()
			runFixture(python, []string{"scripts/test-post-install-harness.py"}, nil)
		})

	ginkgo.It("installs the tools profile from loopback release assets without replacing idiom",
		ginkgo.Label("e2e", "integration", "system", "external-toolchain", "system-install-tools-local"),
		func() {
			requireUnixHost()
			requireSupportedGoArchitecture()
			for _, command := range []string{"bash", "curl", "go", "python3", "tar"} {
				requireCommand(command)
			}
			runFixture("bash", []string{"scripts/test-install-tools-local.sh"}, nil)
		})

	ginkgo.It("validates queue-daemon profiles without changing a host service",
		ginkgo.Label("e2e", "integration", "system", "system-install-queue-daemon-static"),
		func() {
			requireUnixHost()
			requireCommand("bash")

			stubDirectory := ginkgo.GinkgoT().TempDir()
			unamePath := filepath.Join(stubDirectory, "uname")
			gomega.Expect(os.WriteFile(unamePath, []byte("#!/bin/sh\nprintf 'Linux\\n'\n"), 0o755)).To(gomega.Succeed())
			environment := map[string]string{
				"PATH": stubDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
			}
			runFixture("bash", []string{"scripts/test-install-queue-daemon.sh"}, environment)
		})

	ginkgo.It("round-trips the macOS queue daemon through launchd",
		ginkgo.Label("e2e", "integration", "system", "live", "external-toolchain", "system-install-queue-daemon-live"),
		func() {
			requireDarwinHost()
			requireCommand("bash")
			requireCommand("launchctl")
			requireInstalledCommand("", idiomExecutableName())
			runFixture("bash", []string{"scripts/test-install-queue-daemon.sh"}, nil)
		})

	ginkgo.It("installs a Linux release snapshot inside a clean Docker container",
		ginkgo.Label("e2e", "integration", "system", "live", "external-toolchain", "system-install-docker"),
		func() {
			requireUnixHost()
			requireCommand("bash")
			requireDockerDaemon()
			runFixture("bash", []string{"scripts/test-install-docker.sh"}, nil)
		})

	ginkgo.It("installs a macOS release snapshot into an isolated home",
		ginkgo.Label("e2e", "integration", "system", "live", "external-toolchain", "system-install-macos"),
		func() {
			requireDarwinHost()
			for _, command := range []string{"bash", "curl", "python3", "sh", "tar"} {
				requireCommand(command)
			}
			runFixture("bash", []string{"scripts/test-install-macos.sh"}, nil)
		})
})

func runFixture(command string, args []string, environment map[string]string) {
	runFixtureResult(command, args, environment)
}

func runFixtureResult(command string, args []string, environment map[string]string) fixtureResult {
	cmd := exec.Command(command, args...)
	cmd.Dir = repositoryRoot()
	cmd.Env = mergedEnvironment(environment)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = io.MultiWriter(&stdout, ginkgo.GinkgoWriter)
	cmd.Stderr = io.MultiWriter(&stderr, ginkgo.GinkgoWriter)

	err := cmd.Run()
	gomega.Expect(err).NotTo(
		gomega.HaveOccurred(),
		"external fixture failed: %s\n--- stdout ---\n%s\n--- stderr ---\n%s",
		strings.Join(append([]string{command}, args...), " "),
		stdout.String(),
		stderr.String(),
	)
	return fixtureResult{stdout: stdout.String(), stderr: stderr.String()}
}

func assertWindowsToolProfile(powershell string, toolPath string) {
	result := runFixtureResult(
		powershell,
		[]string{
			"-NoLogo",
			"-NoProfile",
			"-Command",
			"& $env:IDIOM_SYSTEM_TOOL_PATH list --profile discovery; exit $LASTEXITCODE",
		},
		map[string]string{"IDIOM_SYSTEM_TOOL_PATH": toolPath},
	)
	gomega.Expect(result.stdout).To(
		gomega.ContainSubstring("context_status"),
		"installed idiom-tool.cmd did not expose the discovery profile\n--- stdout ---\n%s\n--- stderr ---\n%s",
		result.stdout,
		result.stderr,
	)
}

func fileSHA256(path string) string {
	contents, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read %s for integrity verification", path)
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}

func mergedEnvironment(overrides map[string]string) []string {
	if len(overrides) == 0 {
		return os.Environ()
	}

	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	baseEnvironment := os.Environ()
	environment := make([]string, 0, len(baseEnvironment)+len(overrides))
	for _, entry := range baseEnvironment {
		key, _, found := strings.Cut(entry, "=")
		if found {
			if _, replaced := overrides[key]; replaced {
				continue
			}
		}
		environment = append(environment, entry)
	}
	for _, key := range keys {
		environment = append(environment, key+"="+overrides[key])
	}
	return environment
}

func repositoryRoot() string {
	_, sourcePath, _, ok := runtime.Caller(0)
	gomega.Expect(ok).To(gomega.BeTrue(), "resolve system-test source path")
	return filepath.Clean(filepath.Join(filepath.Dir(sourcePath), "..", ".."))
}

func requireUnixHost() {
	requirePrecondition(runtime.GOOS == "darwin" || runtime.GOOS == "linux", "this fixture requires a macOS or Linux host")
}

func requireWindowsHost() {
	requirePrecondition(runtime.GOOS == "windows", "this fixture requires a Windows host")
}

func requireDarwinHost() {
	requirePrecondition(runtime.GOOS == "darwin", "this fixture requires a macOS host")
}

func requireSupportedGoArchitecture() {
	switch runtime.GOARCH {
	case "amd64", "arm64":
		return
	default:
		requirePrecondition(false, fmt.Sprintf("unsupported installer architecture %s", runtime.GOARCH))
	}
}

func requireCommand(name string) string {
	path, err := exec.LookPath(name)
	requirePrecondition(err == nil, fmt.Sprintf("required command %q is not on PATH", name))
	return path
}

func requirePython() string {
	candidates := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		candidates = []string{"python", "python3"}
	}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	requirePrecondition(false, "python3 or python is required")
	return ""
}

func requireInstalledCommand(environmentName string, fallback string) {
	command := fallback
	if environmentName != "" && strings.TrimSpace(os.Getenv(environmentName)) != "" {
		command = strings.TrimSpace(os.Getenv(environmentName))
	}

	_, statErr := os.Stat(command)
	_, lookupErr := exec.LookPath(command)
	requirePrecondition(statErr == nil || lookupErr == nil, fmt.Sprintf("required installed command %q is unavailable", command))
}

func requireEnvironment(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	requirePrecondition(value != "", fmt.Sprintf("required environment variable %s is empty", name))
	return value
}

func requireFile(path string) string {
	info, err := os.Stat(path)
	requirePrecondition(err == nil && !info.IsDir(), fmt.Sprintf("required fixture file is unavailable at %s", path))
	return path
}

func requireDiscoveryCommand() {
	if configured := strings.TrimSpace(os.Getenv("IDIOM_TOOL_BIN")); configured != "" {
		requireInstalledCommand("IDIOM_TOOL_BIN", "")
		return
	}
	if _, err := exec.LookPath(idiomToolExecutableName()); err == nil {
		return
	}
	requireInstalledCommand("IDIOM_BIN", idiomExecutableName())
}

func requireGlobalConfig() {
	home, err := os.UserHomeDir()
	requirePrecondition(err == nil, fmt.Sprintf("resolve home directory: %v", err))
	configPath := filepath.Join(home, ".idiom", "config.json")
	_, err = os.Stat(configPath)
	requirePrecondition(err == nil, fmt.Sprintf("installer-created global config is unavailable at %s", configPath))
}

func requireDockerDaemon() {
	docker := requireCommand("docker")
	command := exec.Command(docker, "info")
	output, err := command.CombinedOutput()
	requirePrecondition(err == nil, fmt.Sprintf("docker daemon is unavailable: %v\n%s", err, output))
}

func requirePrecondition(satisfied bool, message string) {
	if satisfied {
		return
	}
	if os.Getenv(targetedSystemRunEnv) == "1" {
		gomega.Expect(satisfied).To(gomega.BeTrue(), message)
		return
	}
	ginkgo.Skip(message)
}

func idiomExecutableName() string {
	if runtime.GOOS == "windows" {
		return "idiom.exe"
	}
	return "idiom"
}

func idiomToolExecutableName() string {
	if runtime.GOOS == "windows" {
		return "idiom-tool.cmd"
	}
	return "idiom-tool"
}
