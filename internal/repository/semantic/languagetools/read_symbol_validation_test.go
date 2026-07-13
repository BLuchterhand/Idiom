package languagetools

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Read-symbol pattern validation", func() {
	It("accepts identifiers, declaration hints, and operator overloads", func() {
		patterns := []string{
			// Bare identifiers and scopings.
			"MyClass",
			"my_function",
			"_underscore_lead",
			"MyClass::method",
			"std::vector<int>",
			"std::vector<int>::push_back",
			"Foo<int,double>",
			"~MyClass",
			"^MyClass$",
			"^my_function$",
			"Class.method",
			"module.Class.method",
			"Helper#run",
			// Declaration-shaped hints the existing language tests rely on.
			"struct EntityManager",
			"class Engine",
			"type Runner struct",
			"pub struct Worker",
			"export class Shouter",
			"export default class DefaultGreeter",
			"public void run()",
			"pub const fn default_worker_id",
			"template <typename T> class EntityBox",
			// Operator overloads.
			"operator==",
			"operator()",
			"operator<<",
			"operator new",
		}
		for _, pattern := range patterns {
			Expect(validateSymbolPattern("cpp_read_symbol", pattern)).To(Succeed(), "pattern %q", pattern)
		}
	})

	It("rejects prose and expressions with actionable alternatives", func() {
		patterns := []string{
			"animal treadmill outputs 5 EU while assigned donkey works",
			"ANIMAL_FEED_CATTLE > ANIMAL_FEED_CHICKEN",
			"base_hp_for_animal(AnimalType::CAT)",
			"static_cast<int>(AnimalType::DONKEY)",
			"CAVE_BEETLE) == 20",
			"is_equipped && energy > 0",
			"Set(Animal::DONKEY)",
		}
		for _, pattern := range patterns {
			err := validateSymbolPattern("cpp_read_symbol", pattern)
			Expect(err).To(HaveOccurred(), "pattern %q", pattern)
			Expect(err.Error()).To(And(
				ContainSubstring("exact-text search"),
				ContainSubstring("AST-pattern capability"),
				ContainSubstring("cpp_read_symbol"),
			))
		}
	})

	DescribeTable("leaves empty patterns to required-field validation",
		func(pattern string) {
			Expect(validateSymbolPattern("go_read_symbol", pattern)).To(Succeed())
		},
		Entry("empty", ""),
		Entry("whitespace", "   "),
	)
})
