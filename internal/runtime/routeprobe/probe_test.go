package routeprobe

import (
	"idiom/internal/contracts/autonomy"
	lanecontract "idiom/internal/contracts/lane"
	"idiom/internal/runtime/workerruntime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Route probing", func() {
	It("maps implementation tasks to the implementation lane contract", func() {
		res, err := Lane(LaneOptions{
			TaskFamily:   string(autonomy.TaskFamilyImplementation),
			AllowedTools: []string{"read_file", "write_file"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.InitialLane).To(Equal(string(lanecontract.LaneImplement)))
		Expect(res.Contract).To(Equal(string(workerruntime.ContractImplementationResult)))
		Expect(res.BriefTaskFam).To(Equal(string(autonomy.TaskFamilyImplementation)))
		Expect(res.Reason).To(Equal(explicitTaskFamilyReason))
		Expect(res.AllowedToolsN).To(BeNumerically(">", 0))
	})

	It("maps plan-then-execute planning to the implementation lane shape", func() {
		res, err := Lane(LaneOptions{
			TaskFamily:   string(autonomy.TaskFamilyPlanThenExecutePlanner),
			AllowedTools: []string{"read_file", "submit_plan"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.InitialLane).To(Equal(string(lanecontract.LaneImplement)))
		Expect(res.Contract).To(Equal(string(workerruntime.ContractImplementationResult)))
		Expect(res.BriefTaskFam).To(Equal(string(autonomy.TaskFamilyPlanThenExecutePlanner)))
	})

	It("rejects an empty task family", func() {
		_, err := Lane(LaneOptions{})
		Expect(err).To(MatchError(ContainSubstring("TaskFamily")))
	})

	It("does not require an explicit mode", func() {
		res, err := Lane(LaneOptions{
			TaskFamily:   string(autonomy.TaskFamilyImplementation),
			AllowedTools: []string{"read_file"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Contract).To(Equal(string(workerruntime.ContractImplementationResult)))
	})

	It("formats a single-line human-readable result", func() {
		result := FormatResult("PASS implement", LaneResult{
			InitialLane:   string(lanecontract.LaneImplement),
			Reason:        explicitTaskFamilyReason,
			Contract:      string(workerruntime.ContractImplementationResult),
			BriefTaskFam:  string(autonomy.TaskFamilyImplementation),
			AllowedToolsN: 12,
		})

		Expect(result).NotTo(ContainSubstring("\n"))
		Expect(result).To(And(
			ContainSubstring("initial=implement"),
			ContainSubstring("contract=implementation_result"),
			ContainSubstring("tools=12"),
			ContainSubstring("followup=-"),
		))
	})
})
