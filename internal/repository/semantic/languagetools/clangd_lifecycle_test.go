package languagetools

import (
	"context"
	"os"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("clangd workspace lifecycle", func() {
	newTestManager := func() (*Manager, *atomic.Int32) {
		manager := NewManager(nil)
		manager.clangdIdleTimeout = 25 * time.Millisecond
		starts := &atomic.Int32{}
		root := GinkgoT().TempDir()
		manager.clangdClientFactory = func(processCtx context.Context, _ context.Context) (*clangdLSPClient, error) {
			starts.Add(1)
			base, err := newLSPBaseClient(
				processCtx,
				root,
				"clangd-test",
				os.Args[0],
				[]string{"-test.run=TestLSPHelperProcess", "--", "lifecycle"},
				nil,
			)
			if err != nil {
				return nil, err
			}
			return &clangdLSPClient{lspBaseClient: base}, nil
		}

		return manager, starts
	}

	It("reuses a live client between leases", func() {
		manager, starts := newTestManager()
		first, err := manager.acquireClangdLSPClient(context.Background())
		Expect(err).NotTo(HaveOccurred())
		firstClient := first.client
		first.close()

		second, err := manager.acquireClangdLSPClient(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(second.client).To(BeIdenticalTo(firstClient))
		Expect(starts.Load()).To(Equal(int32(1)))
		second.close()
		manager.Close()
		Eventually(firstClient.process.Alive, time.Second).Should(BeFalse())
	})

	It("shuts down an idle client", func() {
		manager, _ := newTestManager()
		lease, err := manager.acquireClangdLSPClient(context.Background())
		Expect(err).NotTo(HaveOccurred())
		client := lease.client
		lease.close()

		Eventually(client.process.Alive, time.Second).Should(BeFalse())
		manager.Close()
	})

	It("restarts a client after a crash", func() {
		manager, starts := newTestManager()
		first, err := manager.acquireClangdLSPClient(context.Background())
		Expect(err).NotTo(HaveOccurred())
		firstClient := first.client
		firstClient.forceClose()
		first.close()

		second, err := manager.acquireClangdLSPClient(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(second.client).NotTo(BeIdenticalTo(firstClient))
		Expect(starts.Load()).To(Equal(int32(2)))
		second.close()
		manager.Close()
	})

	It("does not leak a client when the manager closes", func() {
		manager, _ := newTestManager()
		lease, err := manager.acquireClangdLSPClient(context.Background())
		Expect(err).NotTo(HaveOccurred())
		client := lease.client
		lease.close()

		manager.Close()
		Eventually(client.process.Alive, time.Second).Should(BeFalse())
	})
})
