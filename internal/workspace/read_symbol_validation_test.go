package workspace

import (
	"strings"
	"testing"
)

func TestValidateSymbolPatternAcceptsRealSymbols(t *testing.T) {
	cases := []string{
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
	for _, p := range cases {
		if err := validateSymbolPattern("cpp_read_symbol", p); err != nil {
			t.Errorf("validateSymbolPattern rejected %q but should accept: %v", p, err)
		}
	}
}

func TestValidateSymbolPatternRejectsProseFromDonkeyRun(t *testing.T) {
	// These are the patterns the model passed during the doom loop in
	// the Colony donkey run, plus close cousins. Each must reject with
	// a directional error pointing at search_content / ast_grep so the
	// model can pivot on the next turn instead of looping.
	cases := []string{
		"animal treadmill outputs 5 EU while assigned donkey works",
		"ANIMAL_FEED_CATTLE > ANIMAL_FEED_CHICKEN",
		"base_hp_for_animal(AnimalType::CAT)",
		"static_cast<int>(AnimalType::DONKEY)",
		"CAVE_BEETLE) == 20",
		"is_equipped && energy > 0",
		"Set(Animal::DONKEY)",
	}
	for _, p := range cases {
		err := validateSymbolPattern("cpp_read_symbol", p)
		if err == nil {
			t.Errorf("validateSymbolPattern accepted %q but should reject (prose/expression)", p)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "search_content") {
			t.Errorf("error for %q must name search_content as the alternative; got: %s", p, msg)
		}
		if !strings.Contains(msg, "ast_grep") {
			t.Errorf("error for %q must name ast_grep as the alternative; got: %s", p, msg)
		}
		if !strings.Contains(msg, "cpp_read_symbol") {
			t.Errorf("error for %q must name the rejecting tool; got: %s", p, msg)
		}
	}
}

func TestValidateSymbolPatternEmptyAllowed(t *testing.T) {
	// Empty patterns are handled by the caller's existing "pattern is
	// required" path; the validator must not steal that error so the
	// existing message stays stable.
	if err := validateSymbolPattern("go_read_symbol", ""); err != nil {
		t.Errorf("empty pattern should be left to caller's required-field check, got: %v", err)
	}
	if err := validateSymbolPattern("go_read_symbol", "   "); err != nil {
		t.Errorf("whitespace-only pattern should be left to caller's required-field check, got: %v", err)
	}
}
