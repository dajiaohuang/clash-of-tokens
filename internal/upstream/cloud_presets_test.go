package upstream

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"clash-of-tokens/catalog"
)

func TestCloudPresetsUseDocumentedPathsAndProtectedBearer(t *testing.T) {
	for _, tc := range []struct {
		id, base  string
		protocols []string
	}{
		{"volcengine-ark", "https://ark.cn-beijing.volces.com/api/v3", []string{"chat", "responses"}},
		{"tencent-hunyuan", "https://api.hunyuan.cloud.tencent.com/v1", []string{"chat"}},
		{"baidu-qianfan", "https://qianfan.baidubce.com/v2", []string{"chat"}},
		{"tencent-tokenhub", "https://tokenhub.tencentmaas.com/v1", []string{"chat", "responses"}},
		{"tencent-tokenhub-sg", "https://tokenhub-intl.tencentmaas.com/v1", []string{"chat", "responses"}},
	} {
		t.Run(tc.id, func(t *testing.T) {
			source, err := catalog.Preset(tc.id, "operator-model")
			if err != nil {
				t.Fatal(err)
			}
			if source.BaseURL != tc.base || source.SourceKind != "cloud_api" || source.Enabled || source.AutoApproved || source.BillingMode != "metered" || source.Models[0].Tier != "unrated" || source.Models[0].Vision || source.Models[0].InputUSDPerMillion != nil {
				t.Fatalf("unsafe or incorrect preset: %+v", source)
			}
			source.KeyEnv = ""
			source.CredentialRef = "cred://synthetic-cloud"
			source.CredentialResolver = func(ref string) string {
				if ref != "cred://synthetic-cloud" {
					t.Fatal("wrong credential reference")
				}
				return "synthetic-cloud-key"
			}
			client := New(source)
			for _, p := range tc.protocols {
				suffix := "/chat/completions"
				if p == "responses" {
					suffix = "/responses"
				}
				calls := 0
				client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.URL.String() != tc.base+suffix || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer synthetic-cloud-key" {
						t.Fatalf("wrong path, method or protected credential binding: %s %s", r.Method, r.URL)
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: [DONE]\n\n"))}, nil
				})
				response, err := client.Do(context.Background(), p, "operator-model", true, []byte(`{"model":"operator-model","stream":true}`), nil)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if calls != 1 {
					t.Fatal("unexpected request count")
				}
			}
		})
	}
}
