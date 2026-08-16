// Package scfingerprint identifies StarCraft: Brood War players by how they
// play — hotkey habits, muscle-memory command loops, action rhythm — rather
// than by what they're named. It operates on already-parsed screp in-memory
// replay models, so callers never pay for a re-parse.
package scfingerprint

import "github.com/icza/screp/rep"

// Replay is the parsed screp replay model that all scfingerprint APIs operate on.
type Replay = rep.Replay
