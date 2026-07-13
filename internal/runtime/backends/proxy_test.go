package backends

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

func sampleProfile(reasonerPort, coderPort, listen int) Profile {
	return Profile{
		Models: []Model{
			{Alias: "qwen3.6-35b", Binary: "mlx_lm.server", ModelPath: "/abs/qwen3.6-35b", Port: reasonerPort},
			{Alias: "qwen3-coder-30b", Binary: "mlx_lm.server", ModelPath: "/abs/qwen3-coder-30b", Port: coderPort},
		},
		Proxy: Proxy{Listen: listen, DefaultAlias: "qwen3.6-35b"},
	}
}

var _ = ginkgo.Describe("Backend proxy", func() {
	ginkgo.Describe("configuration", func() {
		ginkgo.It("builds routes from a valid profile", func() {
			config, err := BuildProxyConfig(sampleProfile(47780, 47781, 47680))

			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(config.Listen).To(gomega.Equal(47680))
			gomega.Expect(config.DefaultAlias).To(gomega.Equal("qwen3.6-35b"))
			gomega.Expect(config.Routes).To(gomega.HaveKeyWithValue("qwen3-coder-30b", ProxyRoute{
				Alias:       "qwen3-coder-30b",
				BackendAddr: "127.0.0.1:47781",
				AbsPath:     "/abs/qwen3-coder-30b",
			}))
		})

		ginkgo.It("rejects an empty model path", func() {
			_, err := BuildProxyConfig(Profile{Models: []Model{{Alias: "x", Binary: "x", Port: 1}}})
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("model_path")))
		})

		ginkgo.It("rejects an unknown default alias", func() {
			profile := sampleProfile(1, 2, 3)
			profile.Proxy.DefaultAlias = "no-such-alias"

			_, err := BuildProxyConfig(profile)

			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("default_alias")))
		})

		ginkgo.It("rejects an invalid backend port", func() {
			_, err := BuildProxyConfig(Profile{Models: []Model{{Alias: "x", Binary: "x", ModelPath: "/m"}}})
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("invalid port")))
		})

		ginkgo.It("rejects duplicate aliases", func() {
			_, err := BuildProxyConfig(Profile{Models: []Model{
				{Alias: "dup", Binary: "x", ModelPath: "/a", Port: 1},
				{Alias: "dup", Binary: "x", ModelPath: "/b", Port: 2},
			}})
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("duplicate alias")))
		})
	})

	ginkgo.Describe("request routing", func() {
		ginkgo.It("falls back to the default for malformed JSON", func() {
			doer := newStrictProxyHTTPDoerMock()
			var forwardedURL string
			expectSuccessfulProxyRequest(doer, func(request *http.Request, _ []byte) {
				forwardedURL = request.URL.String()
			})
			handler := newProxyHandler(sampleProfile(47780, 47781, 47680), doer)

			response := serveProxyRequest(handler, "POST", "/v1/chat/completions", "not-json{")

			gomega.Expect(response.Code).To(gomega.Equal(http.StatusOK))
			gomega.Expect(forwardedURL).To(gomega.ContainSubstring("127.0.0.1:47780"))
		})

		ginkgo.It("rewrites an alias to its absolute model path", func() {
			doer := newStrictProxyHTTPDoerMock()
			var forwardedURL string
			var forwardedBody []byte
			expectSuccessfulProxyRequest(doer, func(request *http.Request, body []byte) {
				forwardedURL = request.URL.String()
				forwardedBody = body
			})
			handler := newProxyHandler(sampleProfile(47780, 47781, 47680), doer)

			response := serveProxyRequest(handler, "POST", "/v1/chat/completions",
				`{"model":"qwen3-coder-30b","messages":[{"role":"user","content":"hi"}]}`)

			gomega.Expect(response.Code).To(gomega.Equal(http.StatusOK))
			gomega.Expect(forwardedURL).To(gomega.ContainSubstring("127.0.0.1:47781"))
			gomega.Expect(forwardedBody).To(gomega.MatchJSON(`{"model":"/abs/qwen3-coder-30b","messages":[{"role":"user","content":"hi"}]}`))
		})

		ginkgo.It("accepts a provider-prefixed alias", func() {
			doer := newStrictProxyHTTPDoerMock()
			var forwardedURL string
			expectSuccessfulProxyRequest(doer, func(request *http.Request, _ []byte) {
				forwardedURL = request.URL.String()
			})
			handler := newProxyHandler(sampleProfile(47780, 47781, 47680), doer)

			response := serveProxyRequest(handler, "POST", "/v1/chat/completions", `{"model":"mlx/qwen3-coder-30b","messages":[]}`)

			gomega.Expect(response.Code).To(gomega.Equal(http.StatusOK))
			gomega.Expect(forwardedURL).To(gomega.ContainSubstring("127.0.0.1:47781"))
		})

		ginkgo.It("uses the default route for an unknown alias", func() {
			doer := newStrictProxyHTTPDoerMock()
			var forwardedURL string
			var forwardedBody []byte
			expectSuccessfulProxyRequest(doer, func(request *http.Request, body []byte) {
				forwardedURL = request.URL.String()
				forwardedBody = body
			})
			handler := newProxyHandler(sampleProfile(47780, 47781, 47680), doer)

			response := serveProxyRequest(handler, "POST", "/v1/chat/completions", `{"model":"some-unknown-alias"}`)

			gomega.Expect(response.Code).To(gomega.Equal(http.StatusOK))
			gomega.Expect(forwardedURL).To(gomega.ContainSubstring("127.0.0.1:47780"))
			gomega.Expect(forwardedBody).To(gomega.MatchJSON(`{"model":"/abs/qwen3.6-35b"}`))
		})

		ginkgo.It("returns 404 without calling a backend when no route exists", func() {
			profile := sampleProfile(47780, 47781, 47680)
			profile.Proxy.DefaultAlias = ""
			doer := newStrictProxyHTTPDoerMock()
			handler := newProxyHandler(profile, doer)

			response := serveProxyRequest(handler, "POST", "/v1/chat/completions", `{"model":"nope"}`)

			gomega.Expect(response.Code).To(gomega.Equal(http.StatusNotFound))
			gomega.Expect(response.Body.String()).To(gomega.ContainSubstring("qwen3.6-35b"))
		})

		ginkgo.It("merges multiple system messages", func() {
			doer := newStrictProxyHTTPDoerMock()
			var forwardedBody []byte
			expectSuccessfulProxyRequest(doer, func(_ *http.Request, body []byte) {
				forwardedBody = body
			})
			handler := newProxyHandler(sampleProfile(47780, 47781, 47680), doer)

			response := serveProxyRequest(handler, "POST", "/v1/chat/completions", `{
				"model":"qwen3.6-35b",
				"messages":[
					{"role":"system","content":"base rules"},
					{"role":"user","content":"q1"},
					{"role":"system","content":"lane preamble"}
				]
			}`)

			gomega.Expect(response.Code).To(gomega.Equal(http.StatusOK))
			var payload struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			gomega.Expect(json.Unmarshal(forwardedBody, &payload)).To(gomega.Succeed())
			gomega.Expect(payload.Messages).To(gomega.HaveLen(2))
			gomega.Expect(payload.Messages[0].Role).To(gomega.Equal("system"))
			gomega.Expect(payload.Messages[0].Content).To(gomega.And(
				gomega.ContainSubstring("base rules"),
				gomega.ContainSubstring("lane preamble"),
			))
		})

		ginkgo.It("passes through one leading system message byte-for-byte", func() {
			doer := newStrictProxyHTTPDoerMock()
			var forwardedBody []byte
			expectSuccessfulProxyRequest(doer, func(_ *http.Request, body []byte) {
				forwardedBody = body
			})
			handler := newProxyHandler(sampleProfile(47780, 47781, 47680), doer)
			body := `{"messages":[{"role":"system","content":"only"},{"role":"user","content":"hi"}]}`

			response := serveProxyRequest(handler, "POST", "/v1/chat/completions", body)

			gomega.Expect(response.Code).To(gomega.Equal(http.StatusOK))
			gomega.Expect(string(forwardedBody)).To(gomega.Equal(body))
		})

		ginkgo.It("translates an outbound transport error to a JSON 502", func() {
			doer := newStrictProxyHTTPDoerMock()
			doer.EXPECT().Do(gomock.Any()).Return(nil, errors.New("dial tcp: connection refused")).Times(1)
			handler := newProxyHandler(sampleProfile(47780, 47781, 47680), doer)

			response := serveProxyRequest(handler, "POST", "/v1/chat/completions", `{"model":"qwen3.6-35b"}`)

			gomega.Expect(response.Code).To(gomega.Equal(http.StatusBadGateway))
			gomega.Expect(response.Body.String()).To(gomega.ContainSubstring("backend unreachable"))
		})

		ginkgo.It("routes non-v1 paths to the default without a body", func() {
			doer := newStrictProxyHTTPDoerMock()
			var forwardedURL string
			expectSuccessfulProxyRequest(doer, func(request *http.Request, body []byte) {
				forwardedURL = request.URL.String()
				gomega.Expect(body).To(gomega.BeEmpty())
			})
			handler := newProxyHandler(sampleProfile(47780, 47781, 47680), doer)

			response := serveProxyRequest(handler, "GET", "/healthz", "")

			gomega.Expect(response.Code).To(gomega.Equal(http.StatusOK))
			gomega.Expect(forwardedURL).To(gomega.ContainSubstring("127.0.0.1:47780"))
		})
	})

	ginkgo.Describe("HTTP server boundaries", func() {
		ginkgo.It("forwards streaming response chunks from a real backend server", ginkgo.Label("integration", "e2e", "smoke"), func() {
			chunks := []string{"data: token1\n\n", "data: token2\n\n", "data: [DONE]\n\n"}
			backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				writer.WriteHeader(http.StatusOK)
				flusher, _ := writer.(http.Flusher)
				for _, chunk := range chunks {
					_, _ = writer.Write([]byte(chunk))
					if flusher != nil {
						flusher.Flush()
					}
					time.Sleep(10 * time.Millisecond)
				}
			}))
			ginkgo.DeferCleanup(backend.Close)

			config, err := BuildProxyConfig(sampleProfile(47780, 47781, 47680))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			doer := newStrictProxyHTTPDoerMock()
			doer.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *http.Request) (*http.Response, error) {
				outbound, requestErr := http.NewRequestWithContext(
					request.Context(),
					request.Method,
					backend.URL+request.URL.RequestURI(),
					request.Body,
				)
				if requestErr != nil {
					return nil, requestErr
				}
				outbound.Header = request.Header.Clone()

				return http.DefaultClient.Do(outbound)
			}).Times(1)
			handler := NewProxyHandler(config, ProxyOptions{Doer: doer})

			response := serveProxyRequest(handler, "POST", "/v1/chat/completions",
				`{"model":"qwen3.6-35b","stream":true,"messages":[]}`)

			gomega.Expect(response.Code).To(gomega.Equal(http.StatusOK))
			for _, chunk := range chunks {
				gomega.Expect(response.Body.String()).To(gomega.ContainSubstring(chunk))
			}
		})

		ginkgo.It("serves a real listener and shuts down on cancellation", ginkgo.Label("integration", "e2e", "smoke"), func() {
			config, err := BuildProxyConfig(sampleProfile(47780, 47781, 0))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			doer := newStrictProxyHTTPDoerMock()
			expectSuccessfulProxyRequest(doer, nil)
			ctx, cancel := context.WithCancel(context.Background())
			ginkgo.DeferCleanup(cancel)
			address := make(chan string, 1)
			done := make(chan error, 1)
			go func() {
				done <- ServeProxy(ctx, config, ProxyOptions{Doer: doer, Listener: listener}, func(value string) {
					address <- value
				})
			}()

			var readyAddress string
			gomega.Eventually(address).WithTimeout(2 * time.Second).Should(gomega.Receive(&readyAddress))
			gomega.Expect(readyAddress).NotTo(gomega.BeEmpty())
			client := &http.Client{Timeout: 2 * time.Second}
			response, err := client.Post("http://"+readyAddress+"/v1/chat/completions", "application/json",
				strings.NewReader(`{"model":"qwen3.6-35b"}`))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(response.Body.Close()).To(gomega.Succeed())

			cancel()
			var serveErr error
			gomega.Eventually(done).WithTimeout(2 * time.Second).Should(gomega.Receive(&serveErr))
			gomega.Expect(serveErr).NotTo(gomega.HaveOccurred())
		})

		ginkgo.It("requires a configured or injected listener", func() {
			err := ServeProxy(context.Background(), ProxyConfig{
				Routes: map[string]ProxyRoute{"x": {Alias: "x", BackendAddr: "127.0.0.1:1"}},
			}, ProxyOptions{}, nil)

			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("listen")))
		})
	})
})

func newStrictProxyHTTPDoerMock() *MockProxyHTTPDoer {
	ginkgo.GinkgoHelper()
	return NewMockProxyHTTPDoer(gomock.NewController(ginkgo.GinkgoT()))
}

func expectSuccessfulProxyRequest(doer *MockProxyHTTPDoer, inspect func(*http.Request, []byte)) {
	ginkgo.GinkgoHelper()

	doer.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *http.Request) (*http.Response, error) {
		var body []byte
		if request.Body != nil {
			var err error
			body, err = io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
		}
		if inspect != nil {
			inspect(request, body)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"ok":true}`))),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	}).Times(1)
}

func newProxyHandler(profile Profile, doer proxyHTTPDoer) http.Handler {
	ginkgo.GinkgoHelper()

	config, err := BuildProxyConfig(profile)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return NewProxyHandler(config, ProxyOptions{Doer: doer})
}

func serveProxyRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	ginkgo.GinkgoHelper()

	var requestBody io.Reader
	if body != "" {
		requestBody = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, requestBody)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
