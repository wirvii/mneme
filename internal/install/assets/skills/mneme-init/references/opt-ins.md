# Steps 2, 3 and 5: codegraph, shared memory, SDD files (opt-ins)

Each runs only if the person opts in. Each invokes the existing command; never reimplement indexing, hooks, vault export, the SDD file format, the write-through or the markers yourself. If a command fails, relay its message and skip: no manual workaround, no invented fallback command.

## Step 2: codegraph indexing

11. Run `mneme codegraph index` to build the initial index of the repo.
12. Run `mneme codegraph hooks install` for the git hooks that reindex after commit and checkout.
13. If either fails or is unsupported (the hooks need a git repository), report it and skip.

Check: `mneme codegraph status` reports a non-zero file and symbol count.

## Step 3: shared team memory

It wires the git-native vault: `.mneme/shared/`, committed to the repo, into which project memories materialize once enabled (every persistent type shares except `synthesis` and `session_summary`), plus `post-merge`/`post-checkout` hooks that import teammates' knowledge.

14. Before running anything, tell the person: enabling commits `.mneme/shared/` to the repository; if the remote is or may become public, that knowledge becomes public, with its full history once pushed. Confirm they still want it.
15. Run `mneme team-memory enable`. It is idempotent: creates `.mneme/shared/` and its marker if absent, exports existing shared memories and installs the import hooks.
16. Relay its output verbatim, privacy notice included: never paraphrase, summarize away or suppress it.
17. If it fails (for example, not a git repository), report and skip.

Check: `.mneme/shared/.mneme-vault` exists, the output names the hooks installed, and a re-run says "already enabled" instead of failing.

## Step 5: SDD backlog and specs as files

The SAME backlog items and specs of the local database, also written as reviewable Markdown under `.mneme/sdd/`, so they can be reviewed in a pull request.

18. Run `mneme sdd enable` (preview, no `--apply`) and relay its full output verbatim: the plan (how many items and specs), the remote git reports locally, and the four warnings (publishing to git is not undone; mneme cannot tell whether the remote is public without a network call it never makes; mneme has not scanned the content for sensitive data; the files reach a pull request today, and a teammate's own database on every pull or checkout once that machine has the hooks).
19. Tell the person, in your own words, what it buys and its limit: enabling installs THIS machine's git hooks, so future pulls import automatically; a teammate who clones later runs `mneme sdd hooks install` once. Not automatic yet: two people creating the same correlative (two "BL-050") at once is detected and reported, not resolved.
20. Wait for explicit confirmation, the same read-then-confirm gate as Step 3.
21. On confirmation, run `mneme sdd enable --apply`: it exports every item and spec (archived and done included), writes the committed enable marker (this turns it on for every teammate who clones; enabling is a team decision), updates `.mneme/.gitignore` and installs this machine's hooks.
22. If it fails (not a git repository, or the repository holds SDD records this database does not recognise: an unreadable file or one anchored to another machine's item), relay the message verbatim (it names the files and says to run `mneme sdd import` first) and skip. Never read or merge those files yourself.

Check: `.mneme/sdd/.mneme-sdd` exists and `mneme sdd status` reports the mechanism enabled with a non-zero item and spec count.
