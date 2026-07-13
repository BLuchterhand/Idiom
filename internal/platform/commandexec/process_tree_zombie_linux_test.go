//go:build linux

package commandexec

import (
	"errors"
	"net"
	"syscall"
	"unsafe"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"golang.org/x/sys/unix"
)

var _ = ginkgo.Describe(
	"Managed process-tree zombie lifecycle",
	ginkgo.Label("development-scenario", "e2e", "integration", "smoke", "race-contract"),
	func() {
		ginkgo.DescribeTable(
			"classifying process states that still own execution",
			func(state byte, active bool) {
				gomega.Expect(linuxProcessStateActive(state)).To(gomega.Equal(active))
			},
			ginkgo.Entry("running", byte('R'), true),
			ginkgo.Entry("sleeping", byte('S'), true),
			ginkgo.Entry("zombie", byte('Z'), false),
			ginkgo.Entry("dead uppercase", byte('X'), false),
			ginkgo.Entry("dead lowercase", byte('x'), false),
		)

		ginkgo.It(
			"returns after active members exit while a subreaper retains the resource-free zombie",
			ginkgo.Serial,
			ginkgo.SpecTimeout(managedProcessTreeSpecTimeout),
			func(ctx ginkgo.SpecContext) {
				var previousSubreaper int32
				gomega.Expect(unix.Prctl(
					unix.PR_GET_CHILD_SUBREAPER,
					uintptr(unsafe.Pointer(&previousSubreaper)),
					0,
					0,
					0,
				)).To(gomega.Succeed())
				gomega.Expect(unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0)).To(gomega.Succeed())

				grandchildPID := 0
				grandchildReaped := false
				ginkgo.DeferCleanup(func() {
					if grandchildPID > 1 && !grandchildReaped {
						_ = syscall.Kill(grandchildPID, syscall.SIGKILL)
						var status unix.WaitStatus
						for {
							_, err := unix.Wait4(grandchildPID, &status, 0, nil)
							if errors.Is(err, unix.EINTR) {
								continue
							}
							break
						}
					}
					restoreErr := unix.Prctl(
						unix.PR_SET_CHILD_SUBREAPER,
						uintptr(previousSubreaper),
						0,
						0,
						0,
					)
					gomega.Expect(restoreErr).NotTo(gomega.HaveOccurred())
				})

				fixture := newManagedProcessTreeFixture()
				process, err := Start(ctx, fixture.retainedZombieSpec())
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				ginkgo.DeferCleanup(process.Close)

				directPID, recordedGrandchildPID := fixture.waitUntilReady(ctx)
				grandchildPID = recordedGrandchildPID
				address := fixture.waitUntilListenerReady(ctx)
				gomega.Expect(process.Close()).To(gomega.Succeed())
				expectManagedProcessGoneImmediately(directPID)

				grandchildStat, err := linuxProcessStatForPID(grandchildPID)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				gomega.Expect(grandchildStat.processGroupID).To(gomega.Equal(directPID))
				gomega.Expect(grandchildStat.state).To(gomega.Equal(byte('Z')))

				successor, err := net.Listen("tcp", address)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				gomega.Expect(successor.Close()).To(gomega.Succeed())

				var status unix.WaitStatus
				reapedPID, err := unix.Wait4(grandchildPID, &status, 0, nil)
				if err == nil && reapedPID == grandchildPID {
					grandchildReaped = true
				}
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				gomega.Expect(reapedPID).To(gomega.Equal(grandchildPID))
				expectManagedProcessGoneImmediately(grandchildPID)
			},
		)
	},
)

func (fixture managedProcessTreeFixture) retainedZombieSpec() Spec {
	return helperSpec(
		"managed-tree-parent-no-wait",
		fixture.directPIDPath,
		fixture.grandchildPIDPath,
		fixture.readyPath,
		fixture.listenerPath,
	)
}
