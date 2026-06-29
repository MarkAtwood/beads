# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**beads** (command: `bd`) is an issue tracker for AI-supervised coding workflows. Git integration is optional — see `BEADS_DIR` + `--stealth` for git-free operation. We dogfood our own tool.

**IMPORTANT**: See [AGENTS.md](../AGENTS.md) for complete workflow instructions, bd commands, and development guidelines.

## Architecture Overview

### Two-Layer Design

1. **Storage Layer** (`internal/storage/`)
   - Common types and interfaces in `storage.go`

2. **CLI Layer** (`cmd/bd/`)
   - Cobra-based commands (one file per command: `create.go`, `list.go`, etc.)
   - Direct database access (embedded mode for standalone, server mode for orchestrator)
   - All commands support `--json` for programmatic use
   - Main entry point in `main.go`

### Storage Architecture

- **Hash-based IDs**: Automatic collision prevention (v0.20+)

Core implementation:
- Sync commands: `cmd/bd/sync_*.go`

### Key Data Types

See `internal/types/types.go`:
- `Issue`: Core work item (title, description, status, priority, etc.)
- `Dependency`: Four types (blocks, related, parent-child, discovered-from)
- `Label`: Flexible tagging system
- `Comment`: Threaded discussions
- `Event`: Full audit trail

## Development Command Source

Use [AGENT_INSTRUCTIONS.md](../AGENT_INSTRUCTIONS.md#testing-commands-no-ambiguity)
for the current command policy. This file should not duplicate command matrices
or version-management workflows.

> **Do NOT** use `go build -o bd` or `go install` directly — they create
> stale binaries that shadow `~/.local/bin/bd`. Always use `make install`.

## Testing Philosophy

Testing guidance lives in [TESTING.md](TESTING.md) and
[TESTING_PHILOSOPHY.md](TESTING_PHILOSOPHY.md). Architecture-specific notes for
Claude are limited to where tests touch agent setup, hooks, or instruction-file
generation.

## Important Notes

- **Always read AGENTS.md first** - it has the complete workflow
- Check for duplicates proactively: `bd duplicates --auto-merge`
- Use `--json` flags for all programmatic use

## Key Files

- **AGENTS.md** - Complete workflow and development guide (READ THIS!)
- **README.md** - User-facing documentation
- **ADVANCED.md** - Advanced features (rename, merge, compaction)
- **docs/LABELS.md** - Complete label system guide
- **docs/CONFIG.md** - Configuration system

## When Adding Features

See AGENTS.md "Adding a New Command" and "Adding Storage Features" sections for step-by-step guidance.
