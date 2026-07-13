package failures_test

import (
	"errors"
	"fmt"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/platform/failures"
)

const (
	reasonQueueFull failures.Reason = 1001
	reasonBadInput  failures.Reason = 1002
)

type customFailure struct {
	code   failures.Code
	reason failures.Reason
}

func (e customFailure) Error() string           { return "custom failure" }
func (e customFailure) Code() failures.Code     { return e.code }
func (e customFailure) Reason() failures.Reason { return e.reason }

var _ = ginkgo.Describe("Structured failures", func() {
	ginkgo.DescribeTable("renders stable failure codes",
		func(code failures.Code, want string) {
			gomega.Expect(code.String()).To(gomega.Equal(want))
		},
		ginkgo.Entry("unknown", failures.Unknown, "unknown"),
		ginkgo.Entry("canceled", failures.Canceled, "canceled"),
		ginkgo.Entry("invalid argument", failures.InvalidArgument, "invalid_argument"),
		ginkgo.Entry("deadline exceeded", failures.DeadlineExceeded, "deadline_exceeded"),
		ginkgo.Entry("not found", failures.NotFound, "not_found"),
		ginkgo.Entry("already exists", failures.AlreadyExists, "already_exists"),
		ginkgo.Entry("permission denied", failures.PermissionDenied, "permission_denied"),
		ginkgo.Entry("resource exhausted", failures.ResourceExhausted, "resource_exhausted"),
		ginkgo.Entry("failed precondition", failures.FailedPrecondition, "failed_precondition"),
		ginkgo.Entry("aborted", failures.Aborted, "aborted"),
		ginkgo.Entry("out of range", failures.OutOfRange, "out_of_range"),
		ginkgo.Entry("unavailable", failures.Unavailable, "unavailable"),
		ginkgo.Entry("internal", failures.Internal, "internal"),
		ginkgo.Entry("data loss", failures.DataLoss, "data_loss"),
		ginkgo.Entry("unauthenticated", failures.Unauthenticated, "unauthenticated"),
		ginkgo.Entry("unimplemented", failures.Unimplemented, "unimplemented"),
		ginkgo.Entry("out-of-range code", failures.Code(255), "unknown"),
	)

	ginkgo.It("unwraps causes and exposes code and reason through wrapped errors", func() {
		cause := errors.New("disk unavailable")
		failureErr := failures.Wrap(failures.Unavailable, reasonQueueFull, "cannot read queue", cause)
		wrapped := fmt.Errorf("outer: %w", failureErr)

		gomega.Expect(errors.Is(wrapped, cause)).To(gomega.BeTrue())
		gomega.Expect(errors.Is(wrapped, failures.New(failures.Unavailable, reasonQueueFull, ""))).To(gomega.BeTrue())
		gomega.Expect(errors.Is(wrapped, failures.New(failures.Unavailable, reasonBadInput, ""))).To(gomega.BeFalse())

		var typedFailure *failures.Error
		gomega.Expect(errors.As(wrapped, &typedFailure)).To(gomega.BeTrue())
		gomega.Expect(typedFailure.Code).To(gomega.Equal(failures.Unavailable))
		gomega.Expect(failures.CodeOf(wrapped)).To(gomega.Equal(failures.Unavailable))
		gomega.Expect(failures.ReasonOf(wrapped)).To(gomega.Equal(reasonQueueFull))
	})

	ginkgo.It("reads custom code and reason providers through wrapping", func() {
		err := fmt.Errorf("outer: %w", customFailure{
			code:   failures.InvalidArgument,
			reason: reasonBadInput,
		})

		gomega.Expect(failures.CodeOf(err)).To(gomega.Equal(failures.InvalidArgument))
		gomega.Expect(failures.ReasonOf(err)).To(gomega.Equal(reasonBadInput))
	})

	ginkgo.It("uses unknown and no-reason fallbacks for plain and nil errors", func() {
		plain := errors.New("plain")

		gomega.Expect(failures.CodeOf(plain)).To(gomega.Equal(failures.Unknown))
		gomega.Expect(failures.CodeOf(nil)).To(gomega.Equal(failures.Unknown))
		gomega.Expect(failures.ReasonOf(plain)).To(gomega.Equal(failures.NoReason))
		gomega.Expect(failures.CodeOf(plain)).NotTo(gomega.Equal(failures.Internal))
	})
})
