package runner

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func newLifecycleSocketPath() string {
	GinkgoHelper()
	dir, err := os.MkdirTemp("", "iq-")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(os.RemoveAll, dir)
	return filepath.Join(dir, "q.sock")
}

var _ = Describe("Queue server lifecycle", func() {
	It("admits only one daemon when concurrent starts recover a stale socket", func() {
		socket := newLifecycleSocketPath()
		stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
		Expect(err).NotTo(HaveOccurred())
		stale.SetUnlinkOnClose(false)
		Expect(stale.Close()).To(Succeed())

		const contenders = 16
		type startResult struct {
			server *Server
			err    error
		}
		results := make(chan startResult, contenders)
		start := make(chan struct{})
		for range contenders {
			server := NewServer(DefaultConfig(), socket)
			DeferCleanup(server.Stop)
			go func() {
				<-start
				results <- startResult{server: server, err: server.Start()}
			}()
		}
		close(start)
		var winners []*Server
		for range contenders {
			var result startResult
			Eventually(results, 3*time.Second).Should(Receive(&result))
			if result.err == nil {
				winners = append(winners, result.server)
			}
		}
		Expect(winners).To(HaveLen(1))
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		DeferCleanup(cancel)
		lease, err := Acquire(ctx, socket, "default", "winner", "test")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(lease.Close)
		Eventually(lease.Granted()).Should(BeClosed())
		Expect(winners[0].Snapshot().InFlight).To(HaveLen(1))
	})

	It("rejects startup after shutdown without creating a listener", func() {
		socket := newLifecycleSocketPath()
		server := NewServer(DefaultConfig(), socket)
		server.Stop()
		Expect(server.Start()).To(MatchError(ContainSubstring("stopped server")))
		_, err := os.Lstat(socket)
		Expect(os.IsNotExist(err)).To(BeTrue())
	})

	It("does not leak a listener when startup and shutdown race", func() {
		for range 16 {
			socket := newLifecycleSocketPath()
			server := NewServer(DefaultConfig(), socket)
			DeferCleanup(server.Stop)
			start := make(chan struct{})
			started := make(chan error, 1)
			stopped := make(chan struct{})
			go func() {
				<-start
				started <- server.Start()
			}()
			go func() {
				<-start
				server.Stop()
				close(stopped)
			}()
			close(start)
			Eventually(started).Should(Receive())
			Eventually(stopped).Should(BeClosed())
			Expect(server.Start()).To(MatchError(ContainSubstring("stopped server")))
			_, err := os.Lstat(socket)
			Expect(os.IsNotExist(err)).To(BeTrue())
		}
	})

	It("rejects a directory in place of the startup lock", func() {
		socket := newLifecycleSocketPath()
		Expect(os.Mkdir(socket+".lock", 0o700)).To(Succeed())
		server := NewServer(DefaultConfig(), socket)
		DeferCleanup(server.Stop)
		Expect(server.Start()).To(MatchError(ContainSubstring("must be a regular file")))
	})
})
