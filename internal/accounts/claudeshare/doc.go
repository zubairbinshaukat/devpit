// Package claudeshare brings a Claude Code setup from the default account to
// another account: the item list (inventory), the four quick answers
// (presets), conflict planning, and the plan → preview → apply cycle, plus
// the changes made later (stop sharing, share instead, repair, materialise)
// and the additive EnsureSkillLinks the shim calls at launch.
//
// The shared home is the default account's own config folder (~/.claude).
// Nothing in it ever moves. Other accounts link into it with NTFS junctions
// (one per skill folder, one for agents\, one for commands\), and their
// CLAUDE.md imports the default one with a single managed `@` line. Because a
// junction points at an ordinary folder, the links keep working after Devpit
// is uninstalled. Plugins are never a shared folder (Claude Code issue
// #82272): only the enabled list and the marketplaces are copied.
//
// Every change is a [Plan] first. [Apply] runs a plan as one journalled,
// undoable change through the accounts engine: each step is recorded before
// it happens, by the effect kinds this package registers ([Register]), so an
// interrupted apply is rolled back on the next start and [accounts.Engine.Undo]
// puts the tree back exactly.
//
// Safety, enforced here and pinned by tests:
//
//   - the login (.credentials.json) and the account file (.claude.json) are
//     never copied, linked or moved, nor are *.lock siblings, backups\,
//     daemon\, sessions\, ide\, statsig\ or shell-snapshots\ ([denied]);
//     only the mcpServers entries of a .claude.json are ever merged;
//   - a link is removed through a handle opened on the link itself, after
//     checking on that same handle that it is a junction to the expected
//     folder; nothing is ever deleted recursively through a link;
//   - copying and sizing never follow a link;
//   - nothing in a target account is overwritten or deleted: conflicts are
//     renamed or moved to a dated backup folder inside the account, and a
//     rename never replaces an existing name.
//
// All paths come from [Roots], so tests point everything at fixtures.
package claudeshare
