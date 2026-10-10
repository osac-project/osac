# AI-Assisted Development Policy for OSAC

## Purpose

The software industry is transitioning to an Agentic Software Development Lifecycle (SDLC), where autonomous agents working alongside humans - plan, code, test, and deploy software. As a modern open-source platform, OSAC is uniquely positioned to set the standard for this era by aggressively adopting AI to accelerate development and eliminate the technical and knowledge debt that often plagues long-term projects.
Given that OSAC is designed for deployment in highly sensitive environments, including sovereign clouds and medical applications, we recognize our responsibility to ensure AI is adopted with rigor. Our goal is not merely to maintain existing standards, but to measurably improve code quality, safeguard open-source integrity, and foster an inclusive, human-centric contributor community.
This (evolving) document establishes the policy for how all contributors, whether Red Hat employees or external community members, utilize AI coding tools (Claude Code, GitHub Copilot, Cursor, Gemini, etc.) and provides pointers to the best practices our engineers are adopting today.

## Guiding Principles

1. **Human accountability is non-negotiable.** Every contribution has a human owner who understands, can explain, and takes responsibility for the code - regardless of how it was produced.

2. **No contributor is blocked.** AI tools are encouraged but never required. Contributors without access to Claude, Jira, or other Red Hat-internal tooling can participate fully through standard open-source workflows (GitHub issues, PRs, mailing lists). As our AI SDLC matures, we will revisit this principle to ensure it balances inclusivity with our ability to adopt the most effective workflows.

3. **Validation is deterministic.** No matter how far we push AI integration, one boundary remains non-negotiable: builds, tests, and CI checks must be reproducible without AI tools. This ensures we move fast without losing trust. AI helps produce code and reviews, but deterministic tests verify correctness - fundamentally different activities with distinct reliability requirements.


4. **Transparency builds trust.** AI usage in contributions should be disclosed. This is not a gatekeeping mechanism - it helps reviewers calibrate their review and builds community norms.

5. **Quality over velocity.** AI tools should help us build better software faster, not generate more code faster. The goal is higher-quality contributions, not higher volume.

6. **Tool-agnostic by design.** Project workflows and documentation should not lock contributors into a specific AI tool. We aim to adopt cross-tool standards (such as AGENTS.md) as they mature, and ensure that tool-specific configuration files (CLAUDE.md, .cursorrules, etc.) serve as useful developer documentation for all contributors.

## Contributor Access & Inclusivity

### How we keep OSAC accessible

- **No AI-specific dependencies in CI/CD.** The build, test, and lint pipelines use standard open-source tooling. No step requires an AI tool to complete.
- **AGENTS.md is the canonical instruction surface.** Root and component
  `AGENTS.md` files route coding agents to maintained project documentation.
  Tool-specific entry points should point to that guidance. Project context in
  `docs/agent-context/` is tracked and available without installing AI tooling.
- **Enhancement proposals are human-readable.** Design documents in `enhancement-proposals/` are written for human review and discussion, regardless of how the initial draft was produced.


### AI Workflow Tooling

OSAC contributors are adopting AI workflow tools - including GSD workflows, MCP plugins, and AI coding skills - to accelerate development. These tools are open source and available to all contributors. We encourage the community to adopt, improve, and contribute to these workflows alongside the code itself.

To ensure no contributor is disadvantaged:


- All project decisions and discussions happen on GitHub
- Contributors can reproduce builds, run tests, and submit PRs without any AI tooling
- No build, test, or deployment specification may require AI-specific tools to execute
- Workflow artifacts (`.planning/` directories, GSD state files) provide useful context but aren’t required for contribution
- **Workflow state files must not be committed.** Session-specific artifacts (`STATE.md`, `SESSION*.md`, `*.lock`, `HANDOFF*.md`, `*.gsd`) vary per developer and cause merge conflicts. Each repo’s `.gitignore` should exclude these. Curated reference documents (e.g., architecture analysis under `.planning/codebase/`) may be committed deliberately when they have lasting value for the team

## AI Disclosure Requirements

### When to disclose

Contributors should use the appropriate trailer in commit messages to disclose AI usage, following the conventions established by the OpenInfra Foundation and Red Hat:

- **`Generated-By:`** - when AI tools were used for **substantive** code generation (e.g., writing functions, implementing features, generating tests). This is the default for agentic coding workflows.
- **`Assisted-By:`** - when AI tools provided lighter assistance (e.g., debugging help, design suggestions, code review feedback) but the human wrote the code.

### What the disclosure means

The `Generated-By:` / `Assisted-By:` trailers are informational. They:
- Helps reviewers understand the contribution's origin
- Builds a transparent development culture
- Does NOT imply lower quality or trigger additional review gates
- Does NOT affect copyright assignment (the human contributor remains the author)

## Code Review Standards

### All contributions (AI-assisted or not)

Every PR must meet the same quality bar:

1. **Passes CI** - all linting, unit tests, integration tests, and end-to-end tests pass. QE-driven feature testing adds an additional layer of intent and side-effect validation.
2. **Follows conventions** - naming, patterns, and architecture documented in AGENTS.md and its referenced project documentation
3. **Has adequate test coverage** - new functionality includes tests; bug fixes include regression tests
4. **Is reviewable** - reasonable PR size, clear commit messages, focused scope

### Additional expectations for AI-assisted contributions

Reviewers should pay particular attention to:

| Review focus | Why |
|---|---|
| **Edge cases and error handling** | AI tools may generate happy-path code that misses failure modes |
| **Security implications** | AI-generated code may introduce subtle vulnerabilities (injection, improper validation, leaked secrets) |
| **Unnecessary complexity** | AI tools sometimes over-engineer solutions or add unused abstractions |
| **Copy-paste patterns** | AI may duplicate code instead of reusing existing utilities |
| **License compatibility** | Verify no externally-sourced snippets with incompatible licenses were introduced |
| **Test quality** | AI-generated tests may test the implementation rather than the behavior (tautological tests) |

### The "explain your code" standard

- **Do** interrogate your AI agent until you understand every edge case and interaction in your changes.
- **Don't** submit code you can't explain without your agent open.
- **Don't** use agents as a substitute for understanding the system. Read the architecture docs.

Reviewers may ask contributors to explain specific implementation choices. "The AI wrote it this way" is not an acceptable answer. If a contributor cannot explain their code, the PR should not be merged.

## Legal & Licensing Considerations

### Developer Certificate of Origin (DCO)

OSAC uses the Apache 2.0 license. Contributors sign off on their commits via the DCO (`Signed-off-by:`), certifying they have the right to submit the contribution.

When using AI tools:
- The human contributor remains the legal author and is responsible for the DCO sign-off
- AI tools cannot sign the DCO - only humans can certify contribution rights
- Contributors should be aware that AI-generated code may inadvertently reproduce training data; use judgment and review output carefully

### Copyright

- US courts have affirmed that purely AI-generated works may lack copyright protection
- In practice, AI-assisted code (where a human directs, reviews, and modifies the output) retains human authorship
- Contributors retain copyright of their contributions under the Apache 2.0 license, consistent with existing project policy

## Reviewer Guidelines

### For maintainers reviewing AI-assisted PRs

1. **Apply the same standards** - don't lower the bar because "AI wrote it" and don't raise it either
2. **Check understanding** - if something looks generated, ask the contributor to explain the design choice
3. **Watch for AI patterns** - overly verbose comments, unnecessary abstractions, boilerplate that doesn't match project style
4. **Verify tests are meaningful** - ensure tests validate behavior, not just that the code compiles
5. **Trust but verify** - AI-assisted code can be excellent; don't assume it's wrong, but do review it thoroughly

### For contributors submitting AI-assisted PRs

1. **Review your own code first** - read every line before submitting; you are the first reviewer
2. **Test locally** - run the full test suite, not just the tests the AI wrote
3. **Simplify** - if the AI over-engineered something, simplify it before submitting
4. **Disclose** - add the appropriate `Generated-By:` or `Assisted-By:` trailer
5. **Be ready to explain** - understand why every line exists

## What This Policy Does NOT Do

- **Mandate AI usage** - no contributor is required to use AI tools
- **Ban AI usage** - AI tools are encouraged for all contributors
- **Create a two-tier review process** - all code meets the same quality bar
- **Require specific AI tools** - use whatever tools work for you
- **Block external contributors** - all essential workflows work without Red Hat-internal tooling
