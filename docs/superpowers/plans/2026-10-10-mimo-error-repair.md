# MiMo endpoint and error reporting repair — 2026-10-10

> 历史文档：设计状态与验证结论仅对应文档日期；本次发布仅脱敏，不代表重新执行验收或完成部署。

## Evidence

Cloud requests could fail with `intent_unavailable`. The implementation always sent cloud requests to the pay-as-you-go endpoint, and the HA intent parser replaced the original provider error with a generic sentinel. The browser then treated almost every terminal error as an uncertain delivery.

The user identified their credential as a Token Plan key. Official documentation confirms that Token Plan credentials and pay-as-you-go credentials are not interchangeable. This makes endpoint mismatch the primary suspected cause of the upstream rejection; the old logs do not establish the original HTTP status. No production credential probe was performed: automatic approval rejected a proposed external diagnostic request.

## Changes

- An allowlisted MiMo service selector supports automatic key-family detection, pay-as-you-go, and Token Plan China/Singapore/Europe. Automatic mode maps `tp-` and `ttp-` to China; users must select the region shown in their MiMo console if different. No cross-region probing or automatic retries.
- The selected endpoint applies to cloud LLM, ASR and TTS. Old encrypted records remain readable and default to automatic mode; an omitted field preserves an explicit saved region. Saving does not issue model requests. Explicit mismatched credential families are rejected without replacing the prior record.
- Safe typed failures survive model classification and generation. Authentication, region/permission denial, quota, throttling/quota exhaustion, timeout, content filtering and malformed responses have distinct local messages. Raw upstream text and credentials are not rendered.
- WebSocket terminal frames distinguish known failure from uncertain persistence/publication. Unsent handshake failures restore the draft. Truly lost responses retain reconciliation and duplicate-prevention behavior. No automatic resend.
- Speech jobs preserve safe upstream failure categories. HTTP errors without known application codes use specific HTTP status guidance, and invalid JSON responses are not mislabeled as network failures.

## Validation

Regression tests reproduce incorrect default cloud destinations and dropped error causes before the fixes. Tests cover fixed-host resolution, encrypted settings retention, credential mismatch rejection, classifier and ordinary generation failures, post-publication uncertainty, frontend draft preservation and safe rendering. Browser integration uses the real Go WebSocket and authentication worker with a disposable offline model adapter; the authentication error is visible and a subsequent turn succeeds. Production cloud connectivity remains unverified without the separately requested credential-probe permission.

## Official references

- [Token Plan quick access](https://mimo.mi.com/docs/zh-CN/tokenplan/Token%20Plan/quick-access)
- [MiMo API error codes](https://mimo.mi.com/docs/zh-CN/api/guidance/error-codes)
- [Subscription scope and model availability](https://mimo.mi.com/docs/tokenplan/subscription)

HTTP 429 can mean either rate limiting or exhausted Token Plan quota, so the UI lists both possibilities rather than claiming certainty. Token Plan usage eligibility remains governed by the official subscription scope; endpoint compatibility alone does not establish eligibility.

## Verified delivery

2026-10-10 recorded verification: frontend 184 tests passed; 27 Edge browser integration tests passed; `go test ./...`, `go vet ./...`, TypeScript checking and production builds passed. Machine-specific service settings and deployment records remain private. These results do not establish current production connectivity or a real provider response.
