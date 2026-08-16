package llama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	"idiom/internal/platform/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Model capability profiles", func() {
	It("projects MLX chat-template constraints without inventing limits", func() {
		profile := BackendModelCapabilityProfile(config.BackendMLX, "Qwen3.8-27B")

		Expect(profile.Validate()).To(Succeed())
		Expect(profile.ChatTemplate.SystemMessagesMustLead).To(Equal(CapabilitySupported))
		Expect(profile.StructuredOutput.NativeTools).To(BeFalse())
		Expect(profile.ContextWindow.Status).To(Equal(CapabilityUnknown))
		Expect(profile.Fingerprint).To(HaveLen(64))
	})

	It("changes its fingerprint when model identity changes", func() {
		first := BackendModelCapabilityProfile(config.BackendMLX, "one")
		second := BackendModelCapabilityProfile(config.BackendMLX, "two")

		Expect(first.Fingerprint).NotTo(Equal(second.Fingerprint))
		Expect(first.WithFingerprint().Fingerprint).To(Equal(first.Fingerprint))
	})

	It("bounds configured context only when the profile knows a limit", func() {
		unknown := BackendModelCapabilityProfile(config.BackendMLX, "model")
		Expect(unknown.EffectiveContextLimit(200000)).To(Equal(200000))

		known := unknown
		known.ContextWindow = CapabilityLimit{Value: 131072, Status: CapabilitySupported}
		Expect(known.EffectiveContextLimit(200000)).To(Equal(131072))
	})

	It("records model identity discovered from the backend endpoint", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			Expect(r.URL.Path).To(Equal("/v1/models"))
			_, _ = w.Write([]byte(`{"data":[{"id":"local-model"}]}`))
		}))
		DeferCleanup(server.Close)

		client := NewClient(server.URL, time.Second).WithBackend(config.BackendMLX).WithModel("local-model")
		profile, err := client.DiscoverCapabilityProfile(context.Background(), "local-model")

		Expect(err).NotTo(HaveOccurred())
		Expect(profile.Source).To(Equal("models_endpoint"))
		Expect(profile.Fingerprint).To(HaveLen(64))
	})

	It("fails discovery when the configured model is not advertised", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data":[{"id":"other-model"}]}`))
		}))
		DeferCleanup(server.Close)

		client := NewClient(server.URL, time.Second).WithBackend(config.BackendMLX)
		_, err := client.DiscoverCapabilityProfile(context.Background(), "missing-model")

		Expect(err).To(MatchError(ContainSubstring(`model "missing-model" was not advertised`)))
	})
})
