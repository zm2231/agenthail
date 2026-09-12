# Agenthail for iPhone and iPad

<!-- impeccable:product-schema 1 -->

## Platform

ios

Minimum iOS/iPadOS 18. Textual 0.5.0 supplies maintained native Markdown rendering; this release deliberately raises the minimum from iOS 17.

## Users

People supervising their existing agent sessions from their phone, including the maintainer who uses Claude and Codex on a Mac.

## Product Purpose

Find, start, understand, and continue the same agent session from a phone. Tool calls and results, visible reasoning, context usage, lifecycle, and delivery outcomes are part of the conversation.

## Operating Context

The Mac runs the agents. The paired phone connects through the existing authenticated Tailscale API. GitHub releases distribute the native client through TestFlight.

## Capabilities and Constraints

Controls follow each adapter's reported capabilities. Recorded activity is not a live permission request. Missing results do not prove execution is still running; a delivery timeout does not prove an instruction was rejected. Truncation must remain visible.

## Product Principles

- Sessions have one browser and one consistent opening path.
- Inbox separates actionable delivery problems from historical outcomes.
- Present the meaning of recorded work before its protocol representation.
- Preserve original content and identifiers for inspection.
- Validate actual navigation and real transcript shapes, as well as fixtures.

## Evidence on Hand

The September 12 physical-device audit reproduced a session-list navigation failure and an empty, twice-nested turn-duration card. The reference inventory and pinned source links are in ../docs/research/mobile-agent-clients-2026-09.md.
