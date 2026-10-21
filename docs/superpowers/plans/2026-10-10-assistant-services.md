# Assistant services implementation plan

> 历史文档：设计状态与验证结论仅对应文档日期；本次发布仅脱敏，不代表重新执行验收或完成部署。

> **For agentic workers:** Use superpowers:executing-plans and TDD. Independent frontend and speech-provider modules may use superpowers:dispatching-parallel-agents; integration remains with the primary agent.

**Goal:** Deliver administrator personality upload, encrypted MiMo key entry, independent local/cloud/hybrid selectors for LLM, ASR and TTS, and click-to-record conversation.

**Architecture:** Reuse Console authentication and the existing chat pipeline. Store configuration under console.data_dir; cloud calls stay in Go. Hybrid means local first, then one cloud fallback for unavailable/transient local service, never for cancelled, invalid or private requests. Local speech uses configurable loopback OpenAI-compatible services; cloud speech uses MiMo chat/completions. No key or model downloads are needed to implement and test the interfaces.

**Tech Stack:** Go/Gin, Windows DPAPI, React/TypeScript, existing Vitest/Playwright, browser PCM WAV conversion, optional bounded FFmpeg conversion.

**Spec:** 2026-10-09-personality-upload-and-voice-design.md, amended by user's 2026-10-10 instruction to implement and provide all three mode selectors.

## Global constraints
- Preserve latest development worktree and unrelated main changes.
- User authorized implementation directly: proceed after writing/self-reviewing this plan without another approval round.
- Keys never returned, logged or stored in browser persistence. Non-Windows key storage fails closed until a secure adapter exists.
- Modes default local; selecting cloud/hybrid permits user-entered text to be sent for direct conversation and empty-candidate intent classification. Device candidates/results/execution, private knowledge, internal and embedding calls remain local.
- Personality path: personality/default.md under Console data root; maximum UTF-8 content 256 KiB.
- Record at most 60 seconds, upload at most 8 MiB. Audio jobs are owner-bound and bounded; errors cannot auto-resend chat.
- Existing account, history, device-confirmation, browser identity coordinator remain authoritative.

## Review focus
- Switching account/session during recording or synthesis must release microphone and ignore late results.
- Replacing key/config while an operation is queued must not silently change its provider policy.
- Cloud mode must not leak old local conversation history or private RAG/HA data.
- Failed uploads or concurrent saves preserve the previous complete file.
- MiMo protocol differs from generic local speech endpoints; tests verify actual outbound requests.

### Task 1: configuration, key and personality storage
- [x] Add failing filesystem tests under internal/assistantconfig for revision conflict, persistence, invalid UTF-8, size limits, encrypted key secrecy and independent modes.
- [x] Implement Store.Open/Get/Update/Snapshot and personality Get/Save with fixed rooted paths and atomic replacement; Windows DPAPI adapter and unsupported-platform adapter.
- [x] Run package tests, including real Windows protect/unprotect.

### Task 2: provider routing and speech jobs
- [x] Add HTTP contract tests for MiMo ASR/TTS, local adapters, hybrid fallback and cancellation.
- [x] Implement isolated internal/speech package with Settings snapshots, bounded jobs and loopback local endpoints.
- [x] Add MiMo text adapter and context-gated local/cloud/hybrid routing; keep embeddings/internal chains local.
- [x] Run provider and routing tests and verify external secrets are redacted.

### Task 3: authenticated Console integration
- [x] Add gateway tests for admin configuration/personality access, CSRF, key omission, speech owner isolation and chat policy.
- [x] Register admin handlers and speech jobs through existing middleware and capabilities; inject services at Bootstrap.
- [x] Pin conversation LLM mode and mark local-only results; handle legacy sessions conservatively.
- [x] Verify gateway suite and old REST/widget behavior.

### Task 4: frontend settings and voice interaction
- [x] Add behavior tests for upload, secret save/clear, independent mode persistence and recording/playback cleanup.
- [x] Implement admin assistant settings page, personality upload and API contracts; use coordinatedFetch through requestConsole.
- [x] Add bounded voice capture, async jobs, manual/automatic TTS with session/identity cancellation.
- [x] Run frontend tests/typecheck/build, fix integration issues.

### Task 5: delivery verification and documentation
- [x] Run Go full tests/vet/build, frontend full tests/typecheck/build and relevant browser tests.
- [x] Document setup, hybrid behavior, local adapter contracts and unavailable-live-test limitations.
- [x] Perform fresh independent review, fix actionable findings and commit only task files.

## Execution ledger
- Baseline: frontend 128/128 and Go full suite passed. Product baseline d9027b0.
- Ruling: API key provided later; use HTTP fakes and real encrypted storage tests now, no paid external calls or claim of live cloud success.
- Ruling: local speech models are optional external services; deliver working adapters and documented setup, no automatic heavyweight installation.
- Implemented: configuration/DPAPI/personality, independent speech modes and bounded jobs, context-gated MiMo text, session pinning, Console routes and provenance. Provider and gateway targeted tests and full Go suite passed.
- Review findings addressed: local HA prerequisite blocking cloud conversation (empty-candidate preflight, device resolution forced local); frontend stale revision recovery; 60-second recorder codec-padding boundary. Protocol-configuration errors now explicitly forbid hybrid fallback.
- Verification: final Go full suite, vet and build passed; frontend 152/152 unit tests, typecheck and production build passed. Edge browser regression plus native MediaRecorder/Web Audio-to-Go ASR pipeline tested with a generated audio track and local HTTP ASR fixture. Independent review confirmed all findings addressed. No live cloud calls because no user key yet; Go race testing unavailable (CGO/compiler absent).

## 2026-10-10 update: apply uploaded personality to conversation replies

The user requested that the previously stored personality affect conversation. Each ordinary Console answer reads a bounded snapshot of `personality/default.md` through the existing protected store. The 2026-10-12 clarification keeps the system-defined personal-butler identity, duties and confirmation rules present with or without a file; the optional document supplements the name, personality, tone and expression habits. Third-person character descriptions are adopted naturally, without requiring users to redefine the assistant. Base identity, personality and knowledge context remain separate; direct routes still exclude retrieved knowledge. This applies to local RAG and cloud/hybrid direct answers. Existing sessions adopt the latest file on the next ordinary answer. Without an uploaded file, the default butler is used; a present invalid/unreadable file yields `personality_unavailable` with explicit administrator recovery guidance. Personality does not grant permissions, replace confirmation or establish device results: existing server-side checks and control flows remain authoritative, rather than relying on prompts as a security mechanism.

The uploaded document is not added to device intent classification, proposals, control execution, ASR input, or TTS instructions. The settings page states when it takes effect and that cloud/hybrid replies send the personality to MiMo. Regression tests use disposable local data and a mock model HTTP endpoint, covering upload -> real WebSocket -> final model payload for all three modes, in-session replacement, no-file compatibility, corrupt-file failure, and RAG/direct boundaries. Real MiMo responses were not requested during this verification.

## 2026-10-12 runtime identity correction

The user reported that restarting the service still produced a fictional student's identity and only character-related capabilities. The running executable was traced to the deployed automation snapshot; it did not contain the new assistant identity prompt. The existing restart script restarts or installs a prepared binary but does not compile changed source. Source tests alone therefore did not establish that the running Console had the fix.

- [x] Make the fixed personal-butler identity and personality-application rules the sole generated `system` message for ordinary Console answers. Encode raw personality and RAG material as lower-priority `user` reference JSON, preserving the latest document and history while excluding reference material from classifiers and device execution.
- [x] Specify that self-introduction and capability answers start with the personal-butler identity, may use the character name, and retain supported assistant responsibilities. Fictional backgrounds and earlier assistant roleplay cannot redefine the current service identity; explicit discussion of the fictional character remains allowed.
- [x] Verify recorded payloads for buffered/streamed RAG and direct routes, default/changed/removed/conflicting personality, delimiter injection, unchanged legacy callers, and upload-to-WebSocket local/cloud/hybrid contracts.
- [x] Run an opt-in real local `gemma4:12b` evaluation with a synthetic conflicting persona: identity, capabilities, contaminated history, an unseen work question, default identity and fictional-background discussion. The initial wording passed only 1 of 3 checks; the revised first-sentence contract passed all 6 expanded cases. This is local model evidence, not MiMo evidence.
- [x] Build from the deployed source snapshot with only the reviewed identity files overlaid, run full Go checks, and replace the live executable with process-ownership, protected-file and static-content checks plus rollback. Full Go regression passed across 15 packages (1,002 test/subtest pass events); after the final wording change, affected chain/gateway packages passed again (424 test/subtest pass events). Final vet/build also passed.
- [ ] If explicitly authorized, validate a bounded number of real MiMo replies using the existing server-side configuration and ordinary Console entry point. Do not include knowledge or device data, or expose credentials.

The opt-in local evaluation requires `ASSISTANT_IDENTITY_LIVE=1`; normal Go test runs skip generation. The test uses only synthetic material and a fixed loopback Ollama endpoint. Runtime build/deployment evidence is kept under the ignored `.tmp/personality-runtime-repair` and `本机私有运行记录` paths.
An opt-in local evaluation with an operator-selected document additionally covered identity, capabilities, legacy-history correction and fictional-character discussion (historical result: 4/4). The source document remains private. The synthetic suite was rerun on the final prompt and passed 6/6. These checks concern local model behavior; prompts do not provide an absolute behavioral guarantee.

Historical deployment details and rollback evidence remain private. The public evidence above covers the recorded automated and opt-in local checks; it does not establish current deployment or live MiMo availability.

One unrelated Windows file-rename test returned Access denied in the first full run; the focused test passed three repetitions, its package passed, and the subsequent full suite passed. The cause was not established and no unrelated storage code was changed.
