# Ark, Hunyuan, Qianfan and TokenHub

Verified against official documentation on 2026-09-10. These presets use the
existing OpenAI-compatible HTTP adapter with protected API-key references. They
are classified as `cloud_api`, remote inference and metered billing. Sources
start disabled, not Auto-approved, with unrated models and unknown prices/tools.
No live account or model capability is inferred from registration.

| Preset | Base URL | Registered protocols |
| --- | --- | --- |
| `volcengine-ark` | `https://ark.cn-beijing.volces.com/api/v3` | Chat, Responses |
| `tencent-hunyuan` | `https://api.hunyuan.cloud.tencent.com/v1` | Chat |
| `baidu-qianfan` | `https://qianfan.baidubce.com/v2` | Chat |
| `tencent-tokenhub` | `https://tokenhub.tencentmaas.com/v1` | Chat, Responses |
| `tencent-tokenhub-sg` | `https://tokenhub-intl.tencentmaas.com/v1` | Chat, Responses |

Ark requires an Ark API key and the enabled model or endpoint ID from the user's
console. Its official SDK demonstrates Chat streaming; the current product
guide documents the Responses endpoint. Responses availability depends on the
selected model. IAM access-key/secret-key signing is not implemented by these
Bearer presets. See the [official SDK example](https://github.com/volcengine/volcengine-python-sdk/blob/master/volcenginesdkexamples/volcenginesdkarkruntime/completions.py)
and [Ark product guide](https://www.volcengine.com/docs/82379/1795150).

The older Hunyuan API uses a Hunyuan API key rather than the cloud SDK signing
pair. Tencent's documentation says new model services are moving to TokenHub;
existing purchased services can continue using the older API. Choose the preset
that matches the account's actual entitlement. See [Hunyuan compatibility](https://cloud.tencent.cn/document/product/1729/111007).

TokenHub keys and service access are region/site-specific. The two presets above
follow the China-site documentation for Guangzhou and Singapore. International-
site accounts may have a different console-provided domain; use an explicit
Base URL override. No automatic cross-region fallback or credential reuse is
performed. Responses is available only for supported models. See [TokenHub API
usage](https://cloud.tencent.com/document/product/1823/130078), [protocol support](https://cloud.tencent.com/document/product/1823/130079)
and [international-site quick start](https://intl.cloud.tencent.com/document/product/1300/78939?lang=en).

Qianfan v2 uses an API key in the Bearer header, not the older v1 access-token
flow. Choose the enabled model ID from the console. The optional `appid` header
for application-specific billing attribution is not added by this preset. See
the [Qianfan quick start](https://cloud.baidu.com/doc/qianfan/s/rmh4stn9m).

## Setup and verification

1. Open **Providers**, select the preset and create an account. Store the
   service API key as an `api_key` credential in the protected store.
2. Add a source for that provider, bind the account, and enter the real upstream
   model/endpoint ID. Review protocol selection, service region and quota domain.
3. Apply the disabled configuration, then explicitly validate each intended
   model/protocol before approving routing. Each check can incur provider usage.

These are metered service endpoints, not Coding Plan/Token Plan endpoints or
consumer browser sessions. Subscription keys may require different endpoints
and must not be assumed interchangeable. Model listing is a separate operation;
an OpenAI-compatible generation endpoint does not guarantee `/models` support.

Local tests use a synthetic HTTP transport to verify exact path composition,
protected-reference Bearer authentication and one request per operation. Browser
tests cover catalog selection and disabled cloud-source setup for all five
presets. They do not contact the real services or establish live compatibility.
