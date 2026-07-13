package app

import (
	"fmt"
	"slices"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("slash command specs drive dispatch and suggestions", func() {
	want := []slashSuggestionEntry{
		{command: "/feature-planner", usage: "<objective>"},
		{command: "/plan-execute", usage: "<objective>"},
		{command: "/implement", usage: "<objective>"},
		{command: "/product-create", usage: "[--effort-root <path>]"},
		{command: "/product-status", usage: "[--effort-root <path>]"},
		{command: "/product-continue", usage: "<action> [--effort-root <path>]"},
		{command: "/product-approve", usage: "[--effort-root <path>]"},
		{command: "/recover-status", usage: "[--packet <path>|<path>]"},
		{command: "/quit"},
		{command: "/exit"},
		{command: "/help"},
		{command: "/clear"},
		{command: "/new"},
		{command: "/compact"},
		{command: "/save"},
		{command: "/copy", usage: "[last|all|visible|selected]"},
		{command: "/select"},
		{command: "/context"},
		{command: "/workspace"},
		{command: "/pwd"},
		{command: "/resume", usage: "[id]"},
		{command: "/debuglog", usage: "[mode redacted|full]"},
		{command: "/trace", usage: "[compact|detail]"},
		{command: "/mode", usage: "[build|plan|general]"},
		{command: "/approve"},
		{command: "/deny"},
		{command: "/undo"},
		{command: "/panel"},
		{command: "/plan"},
		{command: "/system", usage: "<prompt>"},
		{command: "/temp", usage: "<value>"},
		{command: "/preset", usage: "[list|name]"},
		{command: "/server", usage: "<start|stop|restart|status>"},
		{command: "/profile", usage: "<list|add|remove>"},
		{command: "/model"},
	}

	{
		got := allSlashSuggestions()
		gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("slash suggestions = %#v, want %#v", got, want))
	}

	specs := slashCommandSpecs()
	seen := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		{
			_, duplicate := seen[spec.command]
			gomega.Expect(duplicate).To(gomega.BeFalse(), fmt.Sprintf("duplicate slash-command spec for %s", spec.command))
		}
		seen[spec.command] = struct{}{}

		indexed, ok := slashCommandSpecFor(spec.command)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("slash-command spec %s is not dispatchable", spec.command))
		gomega.Expect(indexed.handler).To(gomega.Not(gomega.BeNil()), fmt.Sprintf("slash-command spec %s is not dispatchable", spec.command))
	}
})

var _ = ginkgo.It("slash command specs preserve help order and usage", func() {
	want := "Commands: /save /copy [last|all|visible|selected] /select /clear /new /compact /system <text> /temp <value> /preset [list|name] /server <start|stop|restart|status> /profile <list|add|remove> /model /product-create [--effort-root <path>] /product-status [--effort-root <path>] /product-continue <action> [--effort-root <path>] /product-approve [--effort-root <path>] /recover-status [--packet <path>|<path>] /context /workspace /pwd /debuglog [mode redacted|full] /trace [compact|detail] /mode [build|plan|general] /approve [all] /deny /undo /panel /plan /feature-planner <objective> /implement <objective> /plan-execute <objective> /resume [id] /quit /exit /help"

	got, _, _ := strings.Cut((&Model{}).helpText(), "\n")
	gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("slash help command line = %q, want %q", got, want))
})
