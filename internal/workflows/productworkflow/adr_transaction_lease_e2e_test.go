//go:generate go tool mockgen -destination=mock_io_reader_test.go -package=productworkflow -typed io Reader

package productworkflow

import (
	cryptorand "crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"idiom/internal/platform/atomicfile"
)

type adrTransactionCrashFixture struct {
	effortRoot         string
	lease              *productWorkflowMutationLease
	paths              adrBatchPaths
	manifest           adrBatchManifest
	previousGeneration ADRGenerationToken
	previousBody       []byte
	candidateBody      []byte
}

type artifactWriterResult struct {
	name string
	err  error
}

var _ = ginkgo.Describe(
	"ADR transaction restart and shared mutation lease",
	ginkgo.Label("e2e", "integration", "smoke", "development-scenario", "race-contract"),
	func() {
		ginkgo.DescribeTable("rolls every prepared publication layout back to the prior generation",
			func(layout string) {
				fixture := newADRTransactionCrashFixture("prepared candidate")
				switch layout {
				case "staged":
				case "previous moved":
					gomega.Expect(fixture.lease.root.Rename(fixture.paths.adrs, fixture.paths.previous)).To(gomega.Succeed())
					gomega.Expect(syncADRBatchRollbackDestination(fixture.lease.root, fixture.paths)).To(gomega.Succeed())
				case "candidate published":
					publishADRTransactionForRestart(fixture, adrBatchStatePrepared)
				default:
					ginkgo.Fail("unknown prepared ADR transaction layout: " + layout)
				}
				manifest, err := readADRBatchManifest(fixture.lease.root, fixture.paths.manifest)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				gomega.Expect(manifest.State).To(gomega.Equal(adrBatchStatePrepared))
				fixture.lease.Close()

				restarted, err := openProductWorkflowMutationLease(fixture.effortRoot)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				ginkgo.DeferCleanup(restarted.Close)
				generation, err := restarted.ADRGeneration()
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				gomega.Expect(generation.equal(fixture.previousGeneration)).To(gomega.BeTrue())
				body, err := readEffortArtifactRoot(restarted.root, filepath.Join("adrs", "ADR-001.md"))
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				gomega.Expect(body).To(gomega.Equal(fixture.previousBody))
				restarted.Close()

				_, err = os.Stat(filepath.Join(fixture.effortRoot, fixture.paths.transaction))
				gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
				secondRestart, err := openProductWorkflowMutationLease(fixture.effortRoot)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				ginkgo.DeferCleanup(secondRestart.Close)
				secondGeneration, err := secondRestart.ADRGeneration()
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				gomega.Expect(secondGeneration.equal(fixture.previousGeneration)).To(gomega.BeTrue())
				secondRestart.Close()
			},
			ginkgo.Entry("before publication", "staged"),
			ginkgo.Entry("between the rollback and publication renames", "previous moved"),
			ginkgo.Entry("after candidate publication", "candidate published"),
		)

		ginkgo.It("keeps a published candidate when restart finds a committed manifest", func() {
			fixture := newADRTransactionCrashFixture("committed candidate")
			publishADRTransactionForRestart(fixture, adrBatchStateCommitted)
			manifest, err := readADRBatchManifest(fixture.lease.root, fixture.paths.manifest)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(manifest.State).To(gomega.Equal(adrBatchStateCommitted))
			fixture.lease.Close()

			restarted, err := openProductWorkflowMutationLease(fixture.effortRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ginkgo.DeferCleanup(restarted.Close)
			generation, err := restarted.ADRGeneration()
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(generation.generation.digest).To(gomega.Equal(fixture.manifest.CandidateDigest))
			body, err := readEffortArtifactRoot(restarted.root, filepath.Join("adrs", "ADR-001.md"))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(body).To(gomega.Equal(fixture.candidateBody))
			restarted.Close()

			_, err = os.Stat(filepath.Join(fixture.effortRoot, fixture.paths.transaction))
			gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
			secondRestart, err := openProductWorkflowMutationLease(fixture.effortRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ginkgo.DeferCleanup(secondRestart.Close)
			secondGeneration, err := secondRestart.ADRGeneration()
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(secondGeneration.generation.digest).To(gomega.Equal(fixture.manifest.CandidateDigest))
			secondRestart.Close()
		})

		ginkgo.It("classifies a prepared candidate verification failure as not committed", func() {
			fixture := newADRTransactionCrashFixture("candidate before corruption")
			candidatePath := filepath.Join(fixture.paths.candidate, "ADR-001.md")
			gomega.Expect(atomicfile.WriteFileRoot(
				fixture.lease.root,
				candidatePath,
				append(append([]byte{}, fixture.candidateBody...), []byte("\ncorrupted after prepare\n")...),
				0o644,
			)).To(gomega.Succeed())

			publishErr := publishADRBatch(fixture.lease.root, fixture.paths, fixture.manifest)
			gomega.Expect(publishErr).To(gomega.HaveOccurred())
			gomega.Expect(publishErr.Error()).To(gomega.ContainSubstring("prepared digest"))
			gomega.Expect(artifactWriteCommitted(publishErr)).To(gomega.BeFalse())
			var postCommitErr *atomicfile.PostCommitError
			gomega.Expect(errors.As(publishErr, &postCommitErr)).To(gomega.BeFalse())
			generation, err := fixture.lease.ADRGeneration()
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(generation.equal(fixture.previousGeneration)).To(gomega.BeTrue())
			_, err = fixture.lease.root.Lstat(fixture.paths.transaction)
			gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
		})

		ginkgo.It("classifies prepared-manifest publication failure as precommit", ginkgo.Serial, func() {
			effortRoot := ginkgo.GinkgoT().TempDir()
			_, err := InitializeEffort(CreateEffortOptions{EffortRoot: effortRoot, EffortID: "adr-manifest-precommit"})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			advanceTestEffortToADRDrafting(effortRoot)

			lease, err := openProductWorkflowMutationLease(effortRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ginkgo.DeferCleanup(lease.Close)
			beforeGeneration, err := lease.ADRGeneration()
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			beforeWorkflow := readWorkflowRecoveryFile(filepath.Join(effortRoot, WorkflowStateFileName))
			beforeEvents := readWorkflowRecoveryFile(filepath.Join(effortRoot, productWorkflowDefaultEventsPath))

			controller := gomock.NewController(ginkgo.GinkgoT())
			faultReader := NewMockReader(controller)
			manifestWriteStarted := make(chan struct{})
			manifestPathBlocked := make(chan struct{})
			readCount := 0
			faultReader.EXPECT().Read(gomock.Any()).DoAndReturn(func(buffer []byte) (int, error) {
				readCount++
				for index := range buffer {
					buffer[index] = byte(readCount)
				}
				if readCount == 2 {
					close(manifestWriteStarted)
					<-manifestPathBlocked
				}

				return len(buffer), nil
			}).Times(2)

			originalReader := cryptorand.Reader
			cryptorand.Reader = faultReader
			ginkgo.DeferCleanup(func() {
				cryptorand.Reader = originalReader
			})

			manifestBlockResult := make(chan error, 1)
			go func() {
				<-manifestWriteStarted
				manifestBlockResult <- os.Mkdir(filepath.Join(effortRoot, lease.paths.manifest), 0o700)
				close(manifestPathBlocked)
			}()

			commitErr := commitADRBatch(
				lease,
				beforeGeneration,
				PhaseADRDrafting,
				nil,
				[]adrBatchArtifact{{
					displayPath: "adrs/ADR-001.md",
					relPath:     filepath.Join("adrs", "ADR-001.md"),
					body:        []byte("# candidate that must not publish\n"),
					perm:        0o644,
				}},
			)
			cryptorand.Reader = originalReader
			gomega.Expect(<-manifestBlockResult).To(gomega.Succeed())
			gomega.Expect(commitErr).To(gomega.MatchError(gomega.ContainSubstring("prepare ADR batch journal")))
			gomega.Expect(artifactWriteCommitted(commitErr)).To(gomega.BeFalse())
			var postCommitErr *atomicfile.PostCommitError
			gomega.Expect(errors.As(commitErr, &postCommitErr)).To(gomega.BeFalse())

			afterGeneration, err := lease.ADRGeneration()
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(afterGeneration.equal(beforeGeneration)).To(gomega.BeTrue())
			_, err = lease.root.Lstat(filepath.Join("adrs", "ADR-001.md"))
			gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
			_, err = lease.root.Lstat(lease.paths.transaction)
			gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
			gomega.Expect(readWorkflowRecoveryFile(filepath.Join(effortRoot, WorkflowStateFileName))).To(gomega.Equal(beforeWorkflow))
			gomega.Expect(readWorkflowRecoveryFile(filepath.Join(effortRoot, productWorkflowDefaultEventsPath))).To(gomega.Equal(beforeEvents))
		})

		ginkgo.It("classifies cleanup failure after committed manifest proof as post-commit", func() {
			if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
				ginkgo.Skip("permission-denied RemoveAll fault requires Unix directory permissions")
			}
			if os.Geteuid() == 0 {
				ginkgo.Skip("permission-denied RemoveAll fault requires a non-root test process")
			}

			fixture := newADRTransactionCrashFixture("candidate committed before cleanup")
			blockerPath := filepath.Join(fixture.paths.transaction, "cleanup-blocker")
			gomega.Expect(fixture.lease.root.Mkdir(blockerPath, 0o700)).To(gomega.Succeed())
			gomega.Expect(atomicfile.WriteFileRoot(
				fixture.lease.root,
				filepath.Join(blockerPath, "retained"),
				[]byte("prevent transaction cleanup\n"),
				0o600,
			)).To(gomega.Succeed())
			absoluteBlockerPath := filepath.Join(fixture.effortRoot, blockerPath)
			gomega.Expect(os.Chmod(absoluteBlockerPath, 0)).To(gomega.Succeed())
			ginkgo.DeferCleanup(func() {
				_ = os.Chmod(absoluteBlockerPath, 0o700)
			})

			publishErr := publishADRBatch(fixture.lease.root, fixture.paths, fixture.manifest)
			gomega.Expect(publishErr).To(gomega.HaveOccurred())
			var postCommitErr *atomicfile.PostCommitError
			gomega.Expect(errors.As(publishErr, &postCommitErr)).To(gomega.BeTrue())
			gomega.Expect(postCommitErr.Committed()).To(gomega.BeTrue())
			gomega.Expect(postCommitErr.Op).To(gomega.ContainSubstring("transaction cleanup failed"))
			gomega.Expect(artifactWriteCommitted(publishErr)).To(gomega.BeTrue())
			body, err := readEffortArtifactRoot(fixture.lease.root, filepath.Join("adrs", "ADR-001.md"))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(body).To(gomega.Equal(fixture.candidateBody))

			gomega.Expect(os.Chmod(absoluteBlockerPath, 0o700)).To(gomega.Succeed())
			fixture.lease.Close()
			restarted, err := openProductWorkflowMutationLease(fixture.effortRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ginkgo.DeferCleanup(restarted.Close)
			restartedBody, err := readEffortArtifactRoot(restarted.root, filepath.Join("adrs", "ADR-001.md"))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(restartedBody).To(gomega.Equal(fixture.candidateBody))
			restarted.Close()
			_, err = os.Stat(filepath.Join(fixture.effortRoot, fixture.paths.transaction))
			gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue())
		})

		ginkgo.It("serializes nested artifact writers behind the shared mutation lease", func(ctx ginkgo.SpecContext) {
			effortRoot := ginkgo.GinkgoT().TempDir()
			_, err := InitializeEffort(CreateEffortOptions{EffortRoot: effortRoot, EffortID: "lease-writers"})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			lease, err := openProductWorkflowMutationLease(effortRoot)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ginkgo.DeferCleanup(lease.Close)

			promptPath := filepath.Join("prompts", "lease", "adr.md")
			evidencePath := filepath.Join("evidence", "lease", "cards.jsonl")
			ready := make(chan string, 2)
			start := make(chan struct{})
			results := make(chan artifactWriterResult, 2)
			go func() {
				ready <- "prompt"
				<-start
				results <- artifactWriterResult{name: "prompt", err: writeDraftPrompt(effortRoot, promptPath, "prompt under lease\n")}
			}()
			go func() {
				ready <- "evidence"
				<-start
				results <- artifactWriterResult{name: "evidence", err: writeEvidenceCardsJSONL(effortRoot, evidencePath, []EvidenceCard{{
					CardID:       "lease-card",
					Kind:         "fact",
					SourceHandle: "test",
					Claim:        "writers share one mutation lease",
					Confidence:   "high",
					Freshness:    "current",
					Uncertainty:  "none",
					EvidenceRefs: []string{"test"},
					ResearcherID: "test",
					Timestamp:    "2026-07-12T00:00:00Z",
				}})}
			}()
			started := map[string]bool{}
			for range 2 {
				var name string
				gomega.Eventually(ctx, ready).Should(gomega.Receive(&name))
				started[name] = true
			}
			gomega.Expect(started).To(gomega.Equal(map[string]bool{"prompt": true, "evidence": true}))
			close(start)
			lockKey := filepath.Clean(effortRoot) + "\x00" + filepath.Clean(productWorkflowMutationLockRelPath)
			gomega.Eventually(ctx, func() int {
				effortFileLocalLocks.Lock()
				defer effortFileLocalLocks.Unlock()
				entry := effortFileLocalLocks.byKey[lockKey]
				if entry == nil {
					return 0
				}

				return entry.refs
			}).Should(gomega.Equal(3), "held lease plus both artifact writers must be queued at the shared lock")
			gomega.Expect(results).NotTo(gomega.Receive(), "queued writers must not complete before the held lease is released")
			_, promptStatErr := os.Stat(filepath.Join(effortRoot, promptPath))
			gomega.Expect(os.IsNotExist(promptStatErr)).To(gomega.BeTrue())
			_, evidenceStatErr := os.Stat(filepath.Join(effortRoot, evidencePath))
			gomega.Expect(os.IsNotExist(evidenceStatErr)).To(gomega.BeTrue())

			lease.Close()
			completed := map[string]bool{}
			for range 2 {
				var result artifactWriterResult
				gomega.Eventually(ctx, results).Should(gomega.Receive(&result))
				gomega.Expect(result.err).NotTo(gomega.HaveOccurred())
				completed[result.name] = true
			}
			gomega.Expect(completed).To(gomega.Equal(map[string]bool{"prompt": true, "evidence": true}))
			promptBody, err := os.ReadFile(filepath.Join(effortRoot, promptPath))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(string(promptBody)).To(gomega.Equal("prompt under lease\n"))
			evidenceBody, err := os.ReadFile(filepath.Join(effortRoot, evidencePath))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(string(evidenceBody)).To(gomega.ContainSubstring(`"card_id":"lease-card"`))
		})
	},
)

func newADRTransactionCrashFixture(candidateMarker string) *adrTransactionCrashFixture {
	ginkgo.GinkgoHelper()

	effortRoot := ginkgo.GinkgoT().TempDir()
	_, err := InitializeEffort(CreateEffortOptions{EffortRoot: effortRoot, EffortID: "adr-transaction-restart"})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	advanceTestEffortToADRDrafting(effortRoot)
	adr := validADRDoc()
	adr.BodyMarkdown = validADRBodyWithDecision("Keep the previous ADR generation.")
	persistInitialTestADRDrafts(effortRoot, []ADRDocument{adr}, []ADRIndexEntry{{
		ADRID:        "ADR-001",
		Title:        "Restart-safe ADR transaction",
		Status:       string(ArtifactStateDraft),
		Path:         "ADR-001.md",
		CoverageTier: "unit",
		PRDRefs:      []string{"REQ-001"},
		CreatedAt:    "2026-07-12T00:00:00Z",
	}})

	lease, err := openProductWorkflowMutationLease(effortRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	ginkgo.DeferCleanup(lease.Close)
	previousGeneration, err := lease.ADRGeneration()
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(previousGeneration.generation.state).To(gomega.Equal(workflowArtifactGenerationPresent))
	previousBody, err := readEffortArtifactRoot(lease.root, filepath.Join("adrs", "ADR-001.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	paths := lease.paths
	gomega.Expect(lease.root.Mkdir(paths.transaction, 0o700)).To(gomega.Succeed())
	gomega.Expect(atomicfile.SyncDirRoot(lease.root, ".")).To(gomega.Succeed())
	gomega.Expect(lease.root.Mkdir(paths.candidate, 0o700)).To(gomega.Succeed())
	gomega.Expect(copyADRBatchTree(lease.root, paths.adrs, paths.candidate)).To(gomega.Succeed())
	candidateBody := append(append([]byte{}, previousBody...), []byte("\n<!-- "+candidateMarker+" -->\n")...)
	gomega.Expect(atomicfile.WriteFileRoot(
		lease.root,
		filepath.Join(paths.candidate, "ADR-001.md"),
		candidateBody,
		0o644,
	)).To(gomega.Succeed())
	gomega.Expect(syncADRBatchTree(lease.root, paths.candidate)).To(gomega.Succeed())
	candidateDigest, err := digestADRBatchTree(lease.root, paths.candidate)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	manifest := adrBatchManifest{
		SchemaVersion:   adrBatchManifestSchema,
		State:           adrBatchStatePrepared,
		PreviousExists:  true,
		PreviousDigest:  previousGeneration.generation.digest,
		CandidateDigest: candidateDigest,
	}
	gomega.Expect(writeADRBatchManifest(lease.root, paths.manifest, manifest)).To(gomega.Succeed())

	return &adrTransactionCrashFixture{
		effortRoot:         effortRoot,
		lease:              lease,
		paths:              paths,
		manifest:           manifest,
		previousGeneration: previousGeneration,
		previousBody:       previousBody,
		candidateBody:      candidateBody,
	}
}

func publishADRTransactionForRestart(fixture *adrTransactionCrashFixture, manifestState string) {
	ginkgo.GinkgoHelper()

	gomega.Expect(fixture.lease.root.Rename(fixture.paths.adrs, fixture.paths.previous)).To(gomega.Succeed())
	gomega.Expect(syncADRBatchRollbackDestination(fixture.lease.root, fixture.paths)).To(gomega.Succeed())
	gomega.Expect(fixture.lease.root.Rename(fixture.paths.candidate, fixture.paths.adrs)).To(gomega.Succeed())
	gomega.Expect(syncADRBatchPublishedDestination(fixture.lease.root, fixture.paths)).To(gomega.Succeed())
	gomega.Expect(verifyADRBatchTree(
		fixture.lease.root,
		fixture.paths.adrs,
		fixture.manifest.CandidateDigest,
	)).To(gomega.Succeed())
	if manifestState == adrBatchStatePrepared {
		return
	}

	gomega.Expect(manifestState).To(gomega.Equal(adrBatchStateCommitted))
	committed := fixture.manifest
	committed.State = adrBatchStateCommitted
	gomega.Expect(writeADRBatchManifest(fixture.lease.root, fixture.paths.manifest, committed)).To(gomega.Succeed())
	gomega.Expect(verifyExactADRBatchManifest(fixture.lease.root, fixture.paths.manifest, committed)).To(gomega.Succeed())
}
