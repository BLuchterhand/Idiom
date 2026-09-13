package runner

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Queue client handshake", func() {
	It("bounds status ping and cancel when a connected daemon never replies", func() {
		dir, err := os.MkdirTemp("", "iq-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, dir)
		socket := filepath.Join(dir, "q.sock")
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(listener.Close)
		Expect(listener.SetDeadline(time.Now().Add(time.Second))).To(Succeed())
		requestErrors := make(chan error, 3)
		go func() { _, err := Status(socket); requestErrors <- err }()
		go func() { _, err := Ping(socket); requestErrors <- err }()
		go func() { requestErrors <- Cancel(socket, "waiting-job") }()
		for range 3 {
			conn, err := listener.Accept()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(conn.Close)
		}

		for range 3 {
			var requestErr error
			Eventually(requestErrors, controlRequestTimeout+time.Second).Should(Receive(&requestErr))
			var networkErr net.Error
			Expect(errors.As(requestErr, &networkErr)).To(BeTrue())
			Expect(networkErr.Timeout()).To(BeTrue())
		}
	})

	It("honors the acquire deadline when a connected daemon never replies", func() {
		dir, err := os.MkdirTemp("", "iq-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, dir)
		socket := filepath.Join(dir, "q.sock")
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(listener.Close)
		Expect(listener.SetDeadline(time.Now().Add(time.Second))).To(Succeed())
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		DeferCleanup(cancel)
		acquireErrors := make(chan error, 1)
		go func() {
			lease, err := Acquire(ctx, socket, "default", "test", "test")
			if lease != nil {
				_ = lease.Close()
			}
			acquireErrors <- err
		}()
		conn, err := listener.Accept()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(conn.Close)

		Eventually(acquireErrors, time.Second).Should(Receive(MatchError(context.DeadlineExceeded)))
	})
})
