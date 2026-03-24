# Repository Guidelines

## Project Structure & Module Organization
- `demo/` is the main Go workspace.
- `demo/cmd/chatbot/` contains the basic multi-turn chatbot loop.
- `demo/cmd/tool_use/` contains the tool-calling agent loop and tool handlers.
- `demo/internal/common/` contains shared config/client setup.
- `docs/` stores project docs (for example `docs/response-api.md`).
- `ref/` contains reference implementations and learning materials; do not treat as production code.
- `.agents/skills/` contains local skill assets and scripts used by agent workflows.

## Build, Test, and Development Commands
- `cd demo && go run ./cmd/tool_use` — run the tool-use agent locally.
- `cd demo && go run ./cmd/chatbot` — run the basic chatbot loop.
- `cd demo && go test ./...` — run all Go tests/packages (currently compiles packages; some may have no test files).
- `cd demo && go build ./...` — verify everything builds cleanly.

## Coding Style & Naming Conventions
- Language: Go. Follow idiomatic Go and `gofmt` formatting.
- Use tabs/standard Go formatting (do not manually align spacing).
- Package names are short, lowercase, no underscores.
- File names use lowercase with underscores only when needed (for example `tools.go`, `config.go`).
- Keep changes minimal and scoped; extend the existing dispatch/handler pattern instead of rewriting loop structure.

## Testing Guidelines
- Add tests alongside code using `*_test.go` naming.
- Prefer table-driven tests for parser/validator logic (for example TODO validation and tool arg parsing).
- Run `go test ./...` before opening a PR.
- If no tests are added, include a short manual verification note in the PR.

## Commit & Pull Request Guidelines
- Follow conventional-style history seen in this repo: `feat(scope): ...`, `fix(scope): ...`, `docs: ...`, `chore: ...`.
- Keep subject lines concise and imperative.
- PRs should include:
  - clear summary of behavior changes,
  - affected paths (for example `demo/cmd/tool_use/tools.go`),
  - test/build evidence (`go test ./...`, `go build ./...`),
  - linked issue/task if applicable.

## Security & Configuration Tips
- Configure runtime via `demo/.env` (`OPENAI_BASE_URL`, `OPENAI_API_KEY`, `OPENAI_MODEL`, `DEBUG_HTTP`).
- Never commit real API keys or secrets.
- Preserve existing safety checks (path traversal guard, dangerous command blocking, command timeout) when modifying tool execution.
