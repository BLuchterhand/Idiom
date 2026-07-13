package comprehension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"idiom/internal/platform/atomicfile"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type rankerProducerResult struct {
	entry   LearningLogEntry
	weights map[string]float64
	err     error
}

var _ = Describe("ranker state transactions", func() {
	It("excludes a second process until the canonical state lock is released", func() {
		root := GinkgoT().TempDir()
		store, err := openRankerStateStore(context.Background(), root)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)

		output, err := runRankerLockProbe(root)
		Expect(err).NotTo(HaveOccurred(), string(output))
		Expect(strings.Fields(string(output))).NotTo(BeEmpty())
		Expect(strings.Fields(string(output))[0]).To(Equal("blocked"))

		store.Close()
		output, err = runRankerLockProbe(root)
		Expect(err).NotTo(HaveOccurred(), string(output))
		Expect(strings.Fields(string(output))).NotTo(BeEmpty())
		Expect(strings.Fields(string(output))[0]).To(Equal("acquired"))
	})

	It("serializes concurrent producers into one snapshot chain", func() {
		root := GinkgoT().TempDir()
		secondRoot := root
		if runtime.GOOS != "windows" {
			alias := filepath.Join(GinkgoT().TempDir(), "workspace-alias")
			Expect(os.Symlink(root, alias)).To(Succeed())
			secondRoot = alias
		}
		entered := make(chan struct{})
		release := make(chan struct{})
		var firstPending sync.Once
		clearHook, err := setRankerStateTestHook(root, func(stage string) error {
			if stage == rankerStateHookAfterPending {
				firstPending.Do(func() {
					close(entered)
					<-release
				})
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(clearHook)

		firstDone := make(chan rankerProducerResult, 1)
		go func() {
			entry, weights, err := ApplyManualRankerNudge(context.Background(), root, "boost", []string{"a.go"}, "", "producer-a")
			firstDone <- rankerProducerResult{entry: entry, weights: weights, err: err}
		}()
		Eventually(entered).Should(BeClosed())

		secondStarted := make(chan struct{})
		secondDone := make(chan rankerProducerResult, 1)
		go func() {
			close(secondStarted)
			entry, weights, err := ApplyManualRankerNudge(context.Background(), secondRoot, "boost", []string{"b.go"}, "", "producer-b")
			secondDone <- rankerProducerResult{entry: entry, weights: weights, err: err}
		}()
		Eventually(secondStarted).Should(BeClosed())
		close(release)

		var first, second rankerProducerResult
		Eventually(firstDone).Should(Receive(&first))
		Eventually(secondDone).Should(Receive(&second))
		Expect(first.err).NotTo(HaveOccurred())
		Expect(second.err).NotTo(HaveOccurred())
		Expect(first.entry.BeforeWeights).NotTo(BeNil())
		Expect(first.entry.AfterWeights).NotTo(BeNil())
		Expect(*first.entry.BeforeWeights).To(BeEmpty())
		Expect((*first.entry.AfterWeights)["a.go"]).To(BeNumerically("~", 1.15, 1e-12))
		Expect(second.entry.BeforeWeights).NotTo(BeNil())
		Expect(second.entry.AfterWeights).NotTo(BeNil())
		Expect((*second.entry.BeforeWeights)["a.go"]).To(BeNumerically("~", 1.15, 1e-12))
		Expect((*second.entry.AfterWeights)["a.go"]).To(BeNumerically("~", 1.1425, 1e-12))
		Expect((*second.entry.AfterWeights)["b.go"]).To(BeNumerically("~", 1.15, 1e-12))
		Expect(second.entry.Rollback).NotTo(BeNil())
		Expect(second.entry.Rollback.Weights).To(Equal(*second.entry.BeforeWeights))

		weights, err := LoadLearnedWeights(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		Expect(weights).To(Equal(*second.entry.AfterWeights))
		entries, err := ReadLearningLog(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(2))
		Expect(entries[0].EventID).To(Equal(first.entry.EventID))
		Expect(entries[1].EventID).To(Equal(second.entry.EventID))
	})

	It("rolls a committed log event forward after a restart before weights materialize", func() {
		root := GinkgoT().TempDir()
		crash := errors.New("injected crash after ranker log commit")
		clearHook, err := setRankerStateTestHook(root, func(stage string) error {
			if stage == rankerStateHookAfterLogCommit {
				return crash
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(clearHook)

		entry, _, err := ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"committed.go"}, "", "committed-retry")
		Expect(err).To(MatchError(crash))
		var committedErr *RankerStatePostCommitError
		Expect(errors.As(err, &committedErr)).To(BeTrue())
		Expect(committedErr.Committed()).To(BeTrue())
		Expect(committedErr.EventID).To(Equal(entry.EventID))
		_, err = os.Stat(filepath.Join(root, rankerStateWeightsPath))
		Expect(os.IsNotExist(err)).To(BeTrue(), "weights must remain unmaterialized at the injected crash point")
		_, err = os.Stat(filepath.Join(root, rankerStatePendingPath))
		Expect(err).NotTo(HaveOccurred())

		clearHook()
		replayed, weights, err := ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"committed.go"}, "", "committed-retry")
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed.EventID).To(Equal(entry.EventID))
		Expect(weights["committed.go"]).To(BeNumerically("~", 1.15, 1e-12))
		_, err = os.Stat(filepath.Join(root, rankerStatePendingPath))
		Expect(os.IsNotExist(err)).To(BeTrue(), "recovery must clear and sync the pending journal")

		entries, err := ReadLearningLog(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1))
		Expect(entries[0].EventID).To(Equal(entry.EventID))
		_, err = LoadLearnedWeights(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		entries, err = ReadLearningLog(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1), "recovery must be idempotent")
	})

	It("rejects reuse of one request identity for different canonical content", func() {
		root := GinkgoT().TempDir()
		first, weights, err := ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"first.go"}, "", "same-caller-operation")
		Expect(err).NotTo(HaveOccurred())
		Expect(first.RequestID).NotTo(BeEmpty())

		_, _, err = ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"different.go"}, "", "same-caller-operation")
		Expect(err).To(MatchError(ContainSubstring("already used for different canonical content")))
		persisted, err := LoadLearnedWeights(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted).To(Equal(weights))
		entries, err := ReadLearningLog(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1))
	})

	It("does not overwrite corrupted weights while recovering a committed request", func() {
		root := GinkgoT().TempDir()
		crash := errors.New("injected committed recovery corruption")
		clearHook, err := setRankerStateTestHook(root, func(stage string) error {
			if stage == rankerStateHookAfterLogCommit {
				return crash
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(clearHook)

		_, _, err = ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"corrupt-recovery.go"}, "", "corrupt-recovery")
		var committedErr *RankerStatePostCommitError
		Expect(errors.As(err, &committedErr)).To(BeTrue())
		clearHook()
		corrupt := []byte("not-json\n")
		weightsPath := filepath.Join(root, rankerStateWeightsPath)
		Expect(os.WriteFile(weightsPath, corrupt, 0o644)).To(Succeed())

		_, _, err = ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"corrupt-recovery.go"}, "", "corrupt-recovery")
		Expect(errors.As(err, &committedErr)).To(BeTrue())
		persisted, readErr := os.ReadFile(weightsPath)
		Expect(readErr).NotTo(HaveOccurred())
		Expect(persisted).To(Equal(corrupt))
		rawLog, readErr := os.ReadFile(filepath.Join(root, rankerStateLearningLogPath))
		Expect(readErr).NotTo(HaveOccurred())
		entries, parseErr := parseCanonicalLearningLog(rawLog)
		Expect(parseErr).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1))
	})

	It("fails closed before pending publication when the canonical root is replaced", func() {
		parent := GinkgoT().TempDir()
		root := filepath.Join(parent, "workspace")
		moved := filepath.Join(parent, "workspace-moved")
		Expect(os.Mkdir(root, 0o755)).To(Succeed())

		clearHook, err := setRankerStateTestHook(root, func(stage string) error {
			if stage != rankerStateHookBeforePending {
				return nil
			}
			if err := os.Rename(root, moved); err != nil {
				return err
			}
			return os.Mkdir(root, 0o755)
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(clearHook)

		_, _, err = ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"root-swap.go"}, "", "root-swap")
		Expect(err).To(MatchError(ContainSubstring("root identity changed")))
		for _, base := range []string{root, moved} {
			for _, relative := range []string{rankerStatePendingPath, rankerStateLearningLogPath, rankerStateWeightsPath} {
				_, statErr := os.Stat(filepath.Join(base, relative))
				Expect(os.IsNotExist(statErr)).To(BeTrue(), "%s must not be published after a root swap", relative)
			}
		}
	})

	It("reports a committed outcome when the root is replaced before weights publication", func() {
		parent := GinkgoT().TempDir()
		root := filepath.Join(parent, "workspace")
		moved := filepath.Join(parent, "workspace-moved")
		Expect(os.Mkdir(root, 0o755)).To(Succeed())

		clearHook, err := setRankerStateTestHook(root, func(stage string) error {
			if stage != rankerStateHookBeforeWeights {
				return nil
			}
			if err := os.Rename(root, moved); err != nil {
				return err
			}
			return os.Mkdir(root, 0o755)
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(clearHook)

		event, _, err := ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"root-swap.go"}, "", "root-swap-committed")
		var committedErr *RankerStatePostCommitError
		Expect(errors.As(err, &committedErr)).To(BeTrue())
		Expect(committedErr.EventID).To(Equal(event.EventID))
		_, statErr := os.Stat(filepath.Join(moved, rankerStateLearningLogPath))
		Expect(statErr).NotTo(HaveOccurred(), "the synced log is the commit point")
		_, statErr = os.Stat(filepath.Join(moved, rankerStatePendingPath))
		Expect(statErr).NotTo(HaveOccurred(), "committed-but-unmaterialized state must retain its journal")
		_, statErr = os.Stat(filepath.Join(moved, rankerStateWeightsPath))
		Expect(os.IsNotExist(statErr)).To(BeTrue())
		_, statErr = os.Stat(filepath.Join(root, rankerStateLearningLogPath))
		Expect(os.IsNotExist(statErr)).To(BeTrue(), "replacement root must remain untouched")
	})

	It("does not publish through a recreated ranker directory after acquiring its lock", func() {
		if runtime.GOOS == "windows" {
			Skip("renaming a directory with an open process-lock file is not portable on Windows")
		}
		root := GinkgoT().TempDir()
		rankerDirectory := filepath.Join(root, rankerStateDirectoryPath)
		heldDirectory := filepath.Join(root, ".idiom", "ranker-held")

		clearHook, err := setRankerStateTestHook(root, func(stage string) error {
			if stage != rankerStateHookBeforePending {
				return nil
			}
			if err := os.Rename(rankerDirectory, heldDirectory); err != nil {
				return err
			}
			return os.Mkdir(rankerDirectory, 0o755)
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(clearHook)

		_, _, err = ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"directory-swap.go"}, "", "directory-swap")
		Expect(err).To(MatchError(ContainSubstring("ranker state directory identity changed")))
		for _, base := range []string{rankerDirectory, heldDirectory} {
			for _, name := range []string{"pending.json", "learning_log.jsonl", "weights.json"} {
				_, statErr := os.Stat(filepath.Join(base, name))
				Expect(os.IsNotExist(statErr)).To(BeTrue(), "%s must not be published after the ranker directory is recreated", name)
			}
		}
	})

	It("does not publish through a symlink-retargeted ranker directory after acquiring its lock", func() {
		if runtime.GOOS == "windows" {
			Skip("ranker directory symlink retarget is covered on Unix platforms")
		}
		root := GinkgoT().TempDir()
		rankerDirectory := filepath.Join(root, rankerStateDirectoryPath)
		heldDirectory := filepath.Join(root, ".idiom", "ranker-held")
		retargetDirectory := filepath.Join(root, ".idiom", "ranker-retarget")
		Expect(os.MkdirAll(retargetDirectory, 0o755)).To(Succeed())

		clearHook, err := setRankerStateTestHook(root, func(stage string) error {
			if stage != rankerStateHookBeforePending {
				return nil
			}
			if err := os.Rename(rankerDirectory, heldDirectory); err != nil {
				return err
			}
			return os.Symlink(retargetDirectory, rankerDirectory)
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(clearHook)

		_, _, err = ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"directory-retarget.go"}, "", "directory-retarget")
		Expect(err).To(MatchError(ContainSubstring("ranker state directory identity changed")))
		for _, base := range []string{retargetDirectory, heldDirectory} {
			for _, name := range []string{"pending.json", "learning_log.jsonl", "weights.json"} {
				_, statErr := os.Stat(filepath.Join(base, name))
				Expect(os.IsNotExist(statErr)).To(BeTrue(), "%s must not be published after the ranker directory is retargeted", name)
			}
		}
	})

	It("rejects a preexisting symlink as the ranker state directory", func() {
		if runtime.GOOS == "windows" {
			Skip("ranker directory symlink rejection is covered on Unix platforms")
		}
		root := GinkgoT().TempDir()
		idiomDirectory := filepath.Join(root, ".idiom")
		targetDirectory := filepath.Join(idiomDirectory, "ranker-target")
		Expect(os.MkdirAll(targetDirectory, 0o755)).To(Succeed())
		Expect(os.Symlink(targetDirectory, filepath.Join(idiomDirectory, "ranker"))).To(Succeed())

		_, err := LoadRankerStateSnapshot(GinkgoT().Context(), root)
		Expect(err).To(MatchError(ContainSubstring("must be a non-symlink directory")))
	})

	It("cancels before pending publication without leaving transaction state", func() {
		root := GinkgoT().TempDir()
		ctx, cancel := context.WithCancel(GinkgoT().Context())
		DeferCleanup(cancel)
		clearHook, err := setRankerStateTestHook(root, func(stage string) error {
			if stage == rankerStateHookBeforePending {
				cancel()
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(clearHook)

		_, _, err = ApplyManualRankerNudge(ctx, root, "boost", []string{"cancel.go"}, "", "cancel-request")
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())
		for _, relative := range []string{rankerStatePendingPath, rankerStateLearningLogPath, rankerStateWeightsPath} {
			_, statErr := os.Stat(filepath.Join(root, relative))
			Expect(os.IsNotExist(statErr)).To(BeTrue())
		}
	})

	It("reports committed semantics when cancellation arrives after durable log publication", func() {
		root := GinkgoT().TempDir()
		ctx, cancel := context.WithCancel(GinkgoT().Context())
		DeferCleanup(cancel)
		clearHook, err := setRankerStateTestHook(root, func(stage string) error {
			if stage == rankerStateHookAfterLogCommit {
				cancel()
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(clearHook)

		entry, _, err := ApplyManualRankerNudge(ctx, root, "boost", []string{"committed-cancel.go"}, "", "committed-cancel")
		var committedErr *RankerStatePostCommitError
		Expect(errors.As(err, &committedErr)).To(BeTrue())
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())
		Expect(committedErr.EventID).To(Equal(entry.EventID))
		_, statErr := os.Stat(filepath.Join(root, rankerStateLearningLogPath))
		Expect(statErr).NotTo(HaveOccurred(), "durable cancellation must preserve the canonical learning event")
		_, statErr = os.Stat(filepath.Join(root, rankerStatePendingPath))
		Expect(statErr).NotTo(HaveOccurred(), "durable cancellation must retain recovery state")
		_, statErr = os.Stat(filepath.Join(root, rankerStateWeightsPath))
		Expect(os.IsNotExist(statErr)).To(BeTrue(), "canceled post-commit materialization must not claim weights were published")
	})

	It("recovers a committed rollback exactly once", func() {
		root := GinkgoT().TempDir()
		target, _, err := ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"rollback.go"}, "", "rollback-target")
		Expect(err).NotTo(HaveOccurred())

		crash := errors.New("injected rollback crash after log commit")
		clearHook, err := setRankerStateTestHook(root, func(stage string) error {
			if stage == rankerStateHookAfterLogCommit {
				return crash
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(clearHook)

		planned, restoreEntry, err := RestoreLearnedWeightsFromLearningLog(GinkgoT().Context(), root, target.EventID, "rollback-request")
		Expect(err).To(MatchError(crash))
		Expect(planned).To(BeEmpty())
		Expect(restoreEntry.Action).To(Equal("restore"))
		rawWeights, err := os.ReadFile(filepath.Join(root, rankerStateWeightsPath))
		Expect(err).NotTo(HaveOccurred())
		persisted, err := ParsePersistedLearnedWeights(rawWeights)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted["rollback.go"]).To(BeNumerically("~", 1.15, 1e-12))

		clearHook()
		recovered, err := LoadLearnedWeights(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		Expect(recovered).To(BeEmpty())
		entries, err := ReadLearningLog(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(2))
		Expect(entries[1].EventID).To(Equal(restoreEntry.EventID))
		_, err = LoadLearnedWeights(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		entries, err = ReadLearningLog(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(2), "restart recovery must not duplicate the restore event")
	})

	It("truncates a partial event and restores the journal before snapshot", func() {
		root := GinkgoT().TempDir()
		base, _, err := ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"base.go"}, "", "partial-base")
		Expect(err).NotTo(HaveOccurred())

		crash := errors.New("injected crash during ranker log append")
		clearHook, err := setRankerStateTestHook(root, func(stage string) error {
			if stage != rankerStateHookAfterPending {
				return nil
			}
			rawPending, readErr := os.ReadFile(filepath.Join(root, rankerStatePendingPath))
			if readErr != nil {
				return fmt.Errorf("read injected pending journal: %w", readErr)
			}
			pending, parseErr := parseRankerPendingTransaction(rawPending)
			if parseErr != nil {
				return fmt.Errorf("parse injected pending journal: %w", parseErr)
			}
			line, encodeErr := encodeCanonicalLearningLogEntry(pending.Event)
			if encodeErr != nil {
				return fmt.Errorf("encode injected log event: %w", encodeErr)
			}
			file, openErr := os.OpenFile(filepath.Join(root, rankerStateLearningLogPath), os.O_APPEND|os.O_WRONLY, 0)
			if openErr != nil {
				return fmt.Errorf("open injected learning log: %w", openErr)
			}
			if _, writeErr := writeFull(file, line[:len(line)/2]); writeErr != nil {
				_ = file.Close()
				return fmt.Errorf("write partial injected log event: %w", writeErr)
			}
			if syncErr := file.Sync(); syncErr != nil {
				_ = file.Close()
				return fmt.Errorf("sync partial injected log event: %w", syncErr)
			}
			if closeErr := file.Close(); closeErr != nil {
				return fmt.Errorf("close partial injected log event: %w", closeErr)
			}
			return crash
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(clearHook)

		_, _, err = ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"partial.go"}, "", "partial-crash")
		Expect(err).To(MatchError(crash))
		clearHook()

		weights, err := LoadLearnedWeights(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		Expect(weights).To(HaveLen(1))
		Expect(weights["base.go"]).To(BeNumerically("~", 1.15, 1e-12))
		entries, err := ReadLearningLog(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1))
		Expect(entries[0].EventID).To(Equal(base.EventID))
		_, err = os.Stat(filepath.Join(root, rankerStatePendingPath))
		Expect(os.IsNotExist(err)).To(BeTrue())
	})

	It("fails closed on an unknown pending journal field", func() {
		root := GinkgoT().TempDir()
		crash := errors.New("injected crash after pending journal")
		clearHook, err := setRankerStateTestHook(root, func(stage string) error {
			if stage == rankerStateHookAfterPending {
				return crash
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(clearHook)

		_, _, err = ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"strict.go"}, "", "strict-journal")
		Expect(err).To(MatchError(crash))
		clearHook()

		pendingPath := filepath.Join(root, rankerStatePendingPath)
		rawPending, err := os.ReadFile(pendingPath)
		Expect(err).NotTo(HaveOccurred())
		var pending map[string]any
		Expect(json.Unmarshal(rawPending, &pending)).To(Succeed())
		pending["unknown_field"] = true
		corruptPending, err := json.Marshal(pending)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(pendingPath, corruptPending, 0o600)).To(Succeed())

		_, err = LoadLearnedWeights(GinkgoT().Context(), root)
		Expect(err).To(MatchError(ContainSubstring("invalid ranker transaction journal")))
		_, err = os.Stat(pendingPath)
		Expect(err).NotTo(HaveOccurred(), "strict recovery must leave an unrecognized journal for inspection")
	})

	It("never treats malformed or noncanonical weights as baseline state", func() {
		for index, body := range [][]byte{
			[]byte("not-json\n"),
			[]byte("{}\n"),
			[]byte(`{"a.go":1.15}`),
			[]byte("{\n  \"../escape.go\": 1.15\n}\n"),
		} {
			root := GinkgoT().TempDir()
			Expect(os.MkdirAll(filepath.Join(root, rankerStateDirectoryPath), 0o755)).To(Succeed())
			path := filepath.Join(root, rankerStateWeightsPath)
			Expect(os.WriteFile(path, body, 0o644)).To(Succeed())

			_, err := LoadLearnedWeights(GinkgoT().Context(), root)
			Expect(err).To(HaveOccurred(), "fixture %d must fail closed", index)
			_, _, err = ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"safe.go"}, "", fmt.Sprintf("corrupt-%d", index))
			Expect(err).To(HaveOccurred(), "fixture %d must not be overwritten", index)
			persisted, readErr := os.ReadFile(path)
			Expect(readErr).NotTo(HaveOccurred())
			Expect(persisted).To(Equal(body))
		}
	})

	It("rejects forged, unknown, and unsanitized learning-log records on every read path", func() {
		mutations := []func(LearningLogEntry) []byte{
			func(entry LearningLogEntry) []byte {
				entry.EventID = "ranker_0000000000000000"
				encoded, _ := json.Marshal(entry)
				return append(encoded, '\n')
			},
			func(entry LearningLogEntry) []byte {
				entry.Source = "raw secret source"
				encoded, _ := json.Marshal(entry)
				return append(encoded, '\n')
			},
			func(entry LearningLogEntry) []byte {
				encoded, _ := json.Marshal(entry)
				return append(encoded[:len(encoded)-1], []byte(`,"unknown_field":true}\n`)...)
			},
		}

		for index, mutate := range mutations {
			root := GinkgoT().TempDir()
			event, _, err := ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"strict-log.go"}, "", fmt.Sprintf("strict-log-%d", index))
			Expect(err).NotTo(HaveOccurred())
			path := filepath.Join(root, rankerStateLearningLogPath)
			forged := mutate(event)
			Expect(os.WriteFile(path, forged, 0o644)).To(Succeed())

			_, err = ReadLearningLog(GinkgoT().Context(), root)
			Expect(err).To(HaveOccurred(), "fixture %d must fail the log reader", index)
			_, err = LoadRankerStateSnapshot(context.Background(), root)
			Expect(err).To(HaveOccurred(), "fixture %d must fail the snapshot reader", index)
			_, _, err = RestoreLearnedWeightsFromLearningLog(GinkgoT().Context(), root, event.EventID, fmt.Sprintf("forged-restore-%d", index))
			Expect(err).To(HaveOccurred(), "fixture %d must fail rollback", index)
		}
	})

	It("binds event identity to rollback state and independently validates snapshot relationships", func() {
		for _, rebindEventID := range []bool{false, true} {
			root := GinkgoT().TempDir()
			_, _, err := ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"bound.go"}, "", fmt.Sprintf("bound-event-%t", rebindEventID))
			Expect(err).NotTo(HaveOccurred())

			path := filepath.Join(root, rankerStateLearningLogPath)
			raw, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			var entry LearningLogEntry
			Expect(json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &entry)).To(Succeed())
			Expect(entry.Rollback).NotTo(BeNil())
			entry.Rollback.Weights["forged.go"] = 1.4
			if rebindEventID {
				entry.EventID = learningEventID(entry)
			}
			encoded, err := json.Marshal(entry)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(path, append(encoded, '\n'), 0o644)).To(Succeed())

			_, err = ReadLearningLog(GinkgoT().Context(), root)
			if rebindEventID {
				Expect(err).To(MatchError(ContainSubstring("rollback snapshot does not match before weights")))
			} else {
				Expect(err).To(MatchError(ContainSubstring("event id does not match its canonical record")))
			}
		}
	})

	It("rejects a recomputed event whose snapshots break the canonical state chain", func() {
		root := GinkgoT().TempDir()
		_, _, err := ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"chain.go"}, "", "chain-event")
		Expect(err).NotTo(HaveOccurred())

		path := filepath.Join(root, rankerStateLearningLogPath)
		raw, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		var entry LearningLogEntry
		Expect(json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &entry)).To(Succeed())
		forgedBefore := map[string]float64{"seed.go": 1.2}
		forgedAfter := applyLearnedWeightUpdate(forgedBefore, learnedWeightUpdate{boost: entry.Files})
		entry.BeforeWeights = &forgedBefore
		entry.AfterWeights = &forgedAfter
		entry.Rollback = &LearningRollbackData{Weights: cloneWeights(forgedBefore)}
		entry.EventID = learningEventID(entry)
		encoded, err := json.Marshal(entry)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(path, append(encoded, '\n'), 0o644)).To(Succeed())

		_, err = ReadLearningLog(GinkgoT().Context(), root)
		Expect(err).To(MatchError(ContainSubstring("before weights do not match prior materialized state")))
	})

	It("rejects canonical weights that diverge from the canonical learning history", func() {
		root := GinkgoT().TempDir()
		_, _, err := ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"materialized.go"}, "", "materialized-event")
		Expect(err).NotTo(HaveOccurred())

		path := filepath.Join(root, rankerStateWeightsPath)
		forged, err := json.MarshalIndent(map[string]float64{"materialized.go": 1.2}, "", "  ")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(path, append(forged, '\n'), 0o644)).To(Succeed())

		_, err = LoadLearnedWeights(GinkgoT().Context(), root)
		Expect(err).To(MatchError(ContainSubstring("weights do not match the canonical learning log")))
		_, err = ReadLearningLog(GinkgoT().Context(), root)
		Expect(err).To(MatchError(ContainSubstring("weights do not match the canonical learning log")))
	})

	It("rejects a committed journal whose materialization phase was flipped", func() {
		root := GinkgoT().TempDir()
		crash := errors.New("injected crash after committed journal event")
		clearHook, err := setRankerStateTestHook(root, func(stage string) error {
			if stage == rankerStateHookAfterLogCommit {
				return crash
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(clearHook)

		_, _, err = ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"phase.go"}, "", "phase-flip")
		var committedErr *RankerStatePostCommitError
		Expect(errors.As(err, &committedErr)).To(BeTrue())
		clearHook()

		pendingPath := filepath.Join(root, rankerStatePendingPath)
		raw, err := os.ReadFile(pendingPath)
		Expect(err).NotTo(HaveOccurred())
		pending, err := parseRankerPendingTransaction(raw)
		Expect(err).NotTo(HaveOccurred())
		pending.Materialize = false
		forged, err := json.MarshalIndent(pending, "", "  ")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(pendingPath, append(forged, '\n'), 0o600)).To(Succeed())

		_, err = LoadLearnedWeights(GinkgoT().Context(), root)
		Expect(err).To(MatchError(ContainSubstring("materialization phase does not match its event")))
		_, err = os.Stat(pendingPath)
		Expect(err).NotTo(HaveOccurred(), "invalid recovery authority must remain inspectable")
	})

	It("rejects a pending journal with a missing required phase field", func() {
		root := GinkgoT().TempDir()
		crash := errors.New("injected crash after pending journal")
		clearHook, err := setRankerStateTestHook(root, func(stage string) error {
			if stage == rankerStateHookAfterPending {
				return crash
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(clearHook)

		_, _, err = ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"required.go"}, "", "required-phase")
		Expect(err).To(MatchError(crash))
		clearHook()

		pendingPath := filepath.Join(root, rankerStatePendingPath)
		raw, err := os.ReadFile(pendingPath)
		Expect(err).NotTo(HaveOccurred())
		var pending map[string]any
		Expect(json.Unmarshal(raw, &pending)).To(Succeed())
		delete(pending, "capture_snapshot")
		forged, err := json.MarshalIndent(pending, "", "  ")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(pendingPath, append(forged, '\n'), 0o600)).To(Succeed())

		_, err = LoadLearnedWeights(GinkgoT().Context(), root)
		Expect(err).To(MatchError(ContainSubstring("missing a required field")))
	})

	It("reports a full unsynced event as ambiguous and recovers one request exactly once", func() {
		root := GinkgoT().TempDir()
		syncFailure := errors.New("injected learning log sync failure")
		clearHook, err := setRankerStateTestHook(root, func(stage string) error {
			if stage == rankerStateHookBeforeLogSync {
				return syncFailure
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(clearHook)

		entry, _, err := ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"ambiguous.go"}, "", "ambiguous-retry")
		var ambiguous *RankerStateAmbiguousCommitError
		Expect(errors.As(err, &ambiguous)).To(BeTrue())
		Expect(ambiguous.RecoveryRequired()).To(BeTrue())
		Expect(ambiguous.EventID).To(Equal(entry.EventID))
		var committed interface{ Committed() bool }
		Expect(errors.As(err, &committed)).To(BeFalse(), "an unsynced event must not claim committed durability")
		_, statErr := os.Stat(filepath.Join(root, rankerStateWeightsPath))
		Expect(os.IsNotExist(statErr)).To(BeTrue())

		clearHook()
		replayed, weights, err := ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"ambiguous.go"}, "", "ambiguous-retry")
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed.EventID).To(Equal(entry.EventID))
		Expect(weights["ambiguous.go"]).To(BeNumerically("~", 1.15, 1e-12))
		entries, err := ReadLearningLog(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1))
	})

	It("keeps a file-synced event ambiguous until recovery syncs the full directory chain", func() {
		root := GinkgoT().TempDir()
		directoryFailure := errors.New("injected learning log directory sync failure")
		clearInitialHook, err := setRankerStateTestHook(root, func(stage string) error {
			if stage == rankerStateHookBeforeLogDirectorySync {
				return directoryFailure
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(clearInitialHook)

		entry, _, err := ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"directory.go"}, "", "directory-sync-retry")
		var ambiguous *RankerStateAmbiguousCommitError
		Expect(errors.As(err, &ambiguous)).To(BeTrue())
		Expect(ambiguous.EventID).To(Equal(entry.EventID))
		var committed interface{ Committed() bool }
		Expect(errors.As(err, &committed)).To(BeFalse(), "file sync alone must not claim durable commit")
		clearInitialHook()

		recoveryFailure := errors.New("injected recovery directory sync failure")
		clearRecoveryHook, err := setRankerStateTestHook(root, func(stage string) error {
			if stage == rankerStateHookBeforeRecoveryDirectorySync {
				return recoveryFailure
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(clearRecoveryHook)

		_, err = LoadRankerStateSnapshot(GinkgoT().Context(), root)
		Expect(errors.As(err, &ambiguous)).To(BeTrue(), "exact suffix must remain ambiguous when directory-chain durability cannot be re-established")
		_, statErr := os.Stat(filepath.Join(root, rankerStatePendingPath))
		Expect(statErr).NotTo(HaveOccurred(), "ambiguous recovery must retain its journal")
		_, statErr = os.Stat(filepath.Join(root, rankerStateWeightsPath))
		Expect(os.IsNotExist(statErr)).To(BeTrue(), "ambiguous recovery must not promote weights")
		clearRecoveryHook()

		replayed, weights, err := ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"directory.go"}, "", "directory-sync-retry")
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed.EventID).To(Equal(entry.EventID))
		Expect(weights["directory.go"]).To(BeNumerically("~", 1.15, 1e-12))
		entries, err := ReadLearningLog(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1), "durability recovery must promote the exact suffix once")
	})

	It("changes owner generation for audit-only events while repeated snapshots remain current", func() {
		root := GinkgoT().TempDir()
		before, err := LoadRankerStateSnapshot(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		Expect(before.Generation).To(BeEmpty())

		_, err = RecordVerifiedRankerEvidence(GinkgoT().Context(), root, VerifiedRankerLearningRequest{
			Source:   LearningSourceRecoveryReconciliation,
			Files:    []string{"generation.go"},
			Verified: true,
			Reason:   LearningReasonVerifiedRecovery,
		})
		Expect(err).NotTo(HaveOccurred())

		after, err := LoadRankerStateSnapshot(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		Expect(after.LearnedWeights()).To(Equal(before.LearnedWeights()))
		Expect(after.Generation).NotTo(BeEmpty())
		Expect(after.Generation).NotTo(Equal(before.Generation))
		repeated, err := LoadRankerStateSnapshot(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		Expect(repeated.Generation).To(Equal(after.Generation))
	})

	It("keeps ephemeral ranker state out of JSON and returns defensive copies", func() {
		root := GinkgoT().TempDir()
		_, _, err := ApplyManualRankerNudge(
			GinkgoT().Context(),
			root,
			"boost",
			[]string{"private/customer.go"},
			"",
			"private-request-id",
		)
		Expect(err).NotTo(HaveOccurred())

		snapshot, err := LoadRankerStateSnapshot(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		encoded, err := json.Marshal(snapshot)
		Expect(err).NotTo(HaveOccurred())
		var publicFields map[string]any
		Expect(json.Unmarshal(encoded, &publicFields)).To(Succeed())
		Expect(publicFields).To(HaveLen(3))
		Expect(publicFields).To(HaveKey("generation"))
		Expect(publicFields).To(HaveKey("weights_materialized"))
		Expect(publicFields).To(HaveKey("learning_log_published"))
		Expect(string(encoded)).NotTo(ContainSubstring("private/customer.go"))
		Expect(string(encoded)).NotTo(ContainSubstring("private-request-id"))
		Expect(string(encoded)).NotTo(ContainSubstring("before_weights"))
		Expect(string(encoded)).NotTo(ContainSubstring(`"learning_log":`))
		snapshot.LearningLog[0].Evidence = &LearningEvidenceRef{
			Files:   []string{"private/customer.go"},
			Metrics: map[string]float64{"customer_metric": 0.9},
		}
		snapshot.LearningLog[0].Verification = &LearningLogVerificationEvidence{
			EditPaths: []string{"private/customer.go"},
		}

		weights := snapshot.LearnedWeights()
		weights["private/customer.go"] = 99
		Expect(snapshot.LearnedWeights()["private/customer.go"]).To(BeNumerically("~", 1.15, 1e-12))

		entries := snapshot.LearningEntries()
		Expect(entries).To(HaveLen(1))
		Expect(entries[0].BeforeWeights).NotTo(BeNil())
		entries[0].Files[0] = "mutated.go"
		(*entries[0].BeforeWeights)["mutated.go"] = 99
		Expect(entries[0].Rollback).NotTo(BeNil())
		entries[0].Rollback.Weights["mutated.go"] = 99
		entries[0].Evidence.Files[0] = "mutated.go"
		entries[0].Evidence.Metrics["customer_metric"] = 99
		entries[0].Verification.EditPaths[0] = "mutated.go"

		unchanged := snapshot.LearningEntries()
		Expect(unchanged[0].Files).To(Equal([]string{"private/customer.go"}))
		Expect(*unchanged[0].BeforeWeights).NotTo(HaveKey("mutated.go"))
		Expect(unchanged[0].Rollback.Weights).NotTo(HaveKey("mutated.go"))
		Expect(unchanged[0].Evidence.Files).To(Equal([]string{"private/customer.go"}))
		Expect(unchanged[0].Evidence.Metrics["customer_metric"]).To(BeNumerically("~", 0.9, 1e-12))
		Expect(unchanged[0].Verification.EditPaths).To(Equal([]string{"private/customer.go"}))

		snapshot.Weights["private/customer.go"] = 99
		snapshot.LearningLog[0].Files[0] = "mutated.go"
		persisted, err := LoadRankerStateSnapshot(GinkgoT().Context(), root)
		Expect(err).NotTo(HaveOccurred())
		Expect(persisted.LearnedWeights()["private/customer.go"]).To(BeNumerically("~", 1.15, 1e-12))
		Expect(persisted.LearningEntries()[0].Files).To(Equal([]string{"private/customer.go"}))
	})

	It("does not leak file-publication committed semantics before the ranker commit point", func() {
		cause := errors.New("pending parent sync failed")
		translated := rankerStatePreCommitCause(&atomicfile.PostCommitError{Op: "pending journal published", Err: cause})

		Expect(errors.Is(translated, cause)).To(BeTrue())
		var committed interface{ Committed() bool }
		Expect(errors.As(translated, &committed)).To(BeFalse())
	})

	It("cancels while waiting for in-process ranker state authority", func() {
		root := GinkgoT().TempDir()
		store, err := openRankerStateStore(context.Background(), root)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		done := make(chan error, 1)
		go func() {
			second, openErr := openRankerStateStore(ctx, root)
			if second != nil {
				second.Close()
			}
			done <- openErr
		}()

		key := rankerStateRootLockKey(store.canonicalRoot)
		Eventually(func() int {
			rankerStateLocalLocks.Lock()
			defer rankerStateLocalLocks.Unlock()
			if entry := rankerStateLocalLocks.byRoot[key]; entry != nil {
				return entry.refs
			}
			return 0
		}).Should(Equal(2))
		cancel()
		var openErr error
		Eventually(done).Should(Receive(&openErr))
		Expect(errors.Is(openErr, context.Canceled)).To(BeTrue())
	})

	It("cancels after the process lock reports external contention", func() {
		root := GinkgoT().TempDir()
		initialized, err := openRankerStateStore(context.Background(), root)
		Expect(err).NotTo(HaveOccurred())
		initialized.Close()

		canonicalRoot, err := canonicalRankerStateRoot(root)
		Expect(err).NotTo(HaveOccurred())
		rootHandle, err := os.OpenRoot(canonicalRoot)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(rootHandle.Close)
		lockFile, err := openRankerStateLockFile(rootHandle)
		Expect(err).NotTo(HaveOccurred())
		acquired, unlock, err := tryLockRankerStateProcess(lockFile)
		Expect(err).NotTo(HaveOccurred())
		Expect(acquired).To(BeTrue())
		DeferCleanup(unlock)

		blocked := make(chan struct{})
		var blockedOnce sync.Once
		clearHook, err := setRankerStateTestHook(root, func(stage string) error {
			if stage == rankerStateHookProcessLockBlocked {
				blockedOnce.Do(func() { close(blocked) })
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(clearHook)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		done := make(chan error, 1)
		go func() {
			store, openErr := openRankerStateStore(ctx, root)
			if store != nil {
				store.Close()
			}
			done <- openErr
		}()

		Eventually(blocked).Should(BeClosed())
		cancel()
		var openErr error
		Eventually(done).Should(Receive(&openErr))
		Expect(errors.Is(openErr, context.Canceled)).To(BeTrue())
	})
})

func TestRankerStateLockHelperProcess(t *testing.T) {
	if os.Getenv("IDIOM_RANKER_LOCK_HELPER") != "1" {
		return
	}

	canonicalRoot, err := canonicalRankerStateRoot(os.Getenv("IDIOM_RANKER_LOCK_ROOT"))
	if err != nil {
		t.Fatalf("resolve ranker helper root: %v", err)
	}
	root, err := os.OpenRoot(canonicalRoot)
	if err != nil {
		t.Fatalf("open ranker helper root: %v", err)
	}
	defer root.Close()
	file, err := openRankerStateLockFile(root)
	if err != nil {
		t.Fatalf("open ranker helper lock file: %v", err)
	}
	acquired, unlock, err := tryLockRankerStateProcess(file)
	if err != nil {
		_ = file.Close()
		t.Fatalf("probe ranker helper lock: %v", err)
	}
	if !acquired {
		_ = file.Close()
		fmt.Fprintln(os.Stdout, "blocked")
		return
	}
	defer unlock()
	fmt.Fprintln(os.Stdout, "acquired")
}

func runRankerLockProbe(root string) ([]byte, error) {
	cmd := exec.Command(os.Args[0], "-test.run=TestRankerStateLockHelperProcess")
	cmd.Env = append(os.Environ(),
		"IDIOM_RANKER_LOCK_HELPER=1",
		"IDIOM_RANKER_LOCK_ROOT="+root,
	)

	return cmd.CombinedOutput()
}

func setRankerStateTestHook(workspaceRoot string, hook func(string) error) (func(), error) {
	canonicalRoot, err := canonicalRankerStateRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}

	rankerStateTestHooks.Lock()
	previous := rankerStateTestHooks.byRoot[canonicalRoot]
	if hook == nil {
		delete(rankerStateTestHooks.byRoot, canonicalRoot)
	} else {
		rankerStateTestHooks.byRoot[canonicalRoot] = hook
	}
	rankerStateTestHooks.Unlock()

	return func() {
		rankerStateTestHooks.Lock()
		if previous == nil {
			delete(rankerStateTestHooks.byRoot, canonicalRoot)
		} else {
			rankerStateTestHooks.byRoot[canonicalRoot] = previous
		}
		rankerStateTestHooks.Unlock()
	}, nil
}
