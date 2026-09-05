# fullsend Configuration for OSAC

This directory contains fullsend-specific configuration and guidance documents to improve review quality.

## Files

### Core Configuration
- **config.yaml** - fullsend installation configuration with explicit context_files list

### Review Guidance Documents
- **REVIEW_GUIDE.md** - Architecture facts, framework knowledge, scope boundaries, anti-hallucination guide
- **COMPONENT_MAP.md** - Cross-file relationships and coverage checklists

Note: Architectural invariants and bug patterns are in `.claude/rules/architecture-patterns.md` (loaded via context_files).

## How fullsend Uses These Documents

fullsend automatically loads these files via `context_files` in config.yaml:

1. **Core guidance** (.fullsend/*.md) - Provides OSAC-specific review knowledge
2. **Root architecture** (AGENTS.md, CLAUDE.md, docs/, .claude/rules/) - Repo-level patterns
3. **Component docs** (*/AGENTS.md) - Component-specific design context

This gives fullsend the context to:
- Avoid hallucinations (BSR version pins, framework misunderstandings)
- Check architectural invariants (tenant isolation, owner references, RBAC sync, cross-component dependency order)
- Detect common bug patterns (UpdateMask omissions, CRD immutability bypass, Ansible wait loops, regex bugs, cross-PR gaps, RBAC drift, test coverage gaps, doc staleness)
- Understand cross-file relationships (proto → CRD → controller → playbook)

## Expected Improvements

Based on team feedback from PRs #236, #229, #159, #199:

| Metric | Baseline | Target |
|--------|----------|--------|
| Signal-to-noise ratio | 50% | ≥60% |
| File type coverage | 30% | ≥70% |
| Hallucination rate | 15% | ≤5% |

## What fullsend Should Do

With this configuration, fullsend should:

✅ **DO Review**:
- Cross-file documentation consistency (README, AUTH.md vs code)
- Architectural risks (blocking calls, Kafka topics, SQL safety)
- Pattern violations (validation markers, RBAC annotations)
- Cross-PR dependencies (CRD field without reconciler?)
- Deep logic bugs (regex, retry loops, immutability bypass)

❌ **DO NOT Review** (Avoid Noise):
- Generic security hardening irrelevant to PR
- Pre-existing patterns unchanged by PR
- Out-of-scope documentation
- Intentional design decisions in component AGENTS.md

## Testing This Configuration

After merge, verify on next bot PR:

```bash
# Check if fullsend references the guidance docs
# Look for phrases like:
#   - "Per REVIEW_GUIDE.md..."
#   - "architecture-patterns.md lists this as..."
#   - "COMPONENT_MAP.md suggests checking..."

# Track quality improvements
# - Fewer hallucinations from REVIEW_GUIDE.md's list?
# - Better file type coverage per COMPONENT_MAP.md?
# - References component AGENTS.md design context?
```

## Feedback & Iteration

If fullsend finding is a false positive:
1. Check which document should have prevented it
2. Update REVIEW_GUIDE.md, .claude/rules/architecture-patterns.md, or COMPONENT_MAP.md
3. File issue with fullsend if systemic tool problem

These docs are living documents - update monthly based on review patterns.

## Notes

- fullsend uses its default review agent (security, performance, code quality)
- These docs ADD OSAC-specific context to the default agent
- No custom agent replacement to avoid losing default capabilities
