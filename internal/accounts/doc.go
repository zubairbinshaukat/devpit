// Package accounts is the engine behind Devpit's Accounts feature: which
// account each developer tool uses in which folder.
//
// It owns four things, all without Bubble Tea and without the network:
//
//   - the store, %APPDATA%\devpit\accounts.toml: the named accounts, the
//     "everywhere" choice per tool and the folder rules. It holds names,
//     emails and folder paths only. Every field is validated against a tight
//     pattern and checked for anything that looks like a secret, so a token
//     cannot be saved in it even by mistake;
//   - names: letters, numbers and dashes, unique per tool, "default"
//     reserved, short forms that match one name;
//   - the resolver: for a tool and a folder, the account and the reason
//     ("everywhere", "folder rule: C:\Work"), with the whole chain of
//     matching rules so a screen can show which one won;
//   - the journal, %APPDATA%\devpit\accounts-journal.jsonl: the last 20
//     changes with the store before and after and every side effect, written
//     before the side effect happens, so a crash is finished or rolled back
//     on the next start and Undo never clobbers a file edited by hand.
//
// Tool-specific work (who am I, sign in, the files a change writes) lives in
// internal/accounts/adapters; starting a tool with an account applied lives
// in internal/accounts/launch; the shim folder and the user PATH live in
// internal/accounts/shims.
package accounts
