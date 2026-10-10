# Security

Radar runs locally. It does not upload source, call an LLM or send telemetry.

## What runs and what does not

Indexing, discovery, `check`, `merge-check` without `--verify`, and
`radar gate` without `--run` read repository files and Git objects as data.
They do not run application code, package scripts, Git hooks or agent
instructions.

Repository code runs only with `gate --run`, `merge-check --verify
--allow-execution` or `test --allow-execution`. Those commands run in a private
copy of the committed source, with a timeout, an output limit and a reduced
environment: `PATH`, a private `HOME` and temp directory, offline settings for
package managers, and read access to existing dependency caches. Inherited
secrets and language startup variables are removed. Build tools still read
their own user configuration: Gradle reads `gradle.properties` in the shared
`GRADLE_USER_HOME`, and Maven reads `~/.m2/settings.xml`, so values kept there
reach the build. A plan rule can add named
variables (`env`), linked directories (`link`) and `setup` commands, so review
those the same way you review the test command.

**The private copy is not a sandbox.** Test code runs as your user and can
read your files, your credentials and the network. Review code before you run
it, and use a disposable runner without secrets in CI.

## Git and paths

Git is called with argument arrays, never through a shell. Radar's Git
processes disable repository fsmonitor hooks and never enable a pager. The
private candidate repository also runs without hooks and ignores user and
system Git configuration. Radar does not merge, rebase, push, stash, clean or
rewrite anything in your repository. Combined candidates live in a temporary
repository that is deleted afterward. Radar never fetches. When a shallow clone
lacks the history it needs, the error tells you to run `git fetch --unshallow`.

Paths, refs, schemas and PR metadata are treated as untrusted. Paths must stay
inside the repository, symlinks and submodules are refused in combined trees,
remote `$ref` is not followed, and file sizes are capped.

## Stored data

Evidence records store an output digest, never raw stdout or stderr. The last
4 KiB of output is shown to you (and included in `--json`) but not saved.
Command arguments and the names of passed-through variables are saved, so keep
secrets out of arguments.

Integrity hashes catch accidental changes to local records. They do not prove
who made them. A review recorded with `radar approve` is a declaration with
whatever name you pass, not an authenticated identity.

## Agents

`radar mcp` serves one repository over stdio. It does not expose `test`,
`approve` or any execution flag, so an agent cannot run repository code or
record a review through it. It can write Radar state and new plan files, so it
is not read-only. The Claude Code Stop hook only runs `radar verify`. Agent
skills are instructions, not a security boundary.

## CI and installation

The [GitHub Actions example](../integrations/github-actions/README.md) uses
`pull_request` with read-only permissions, pinned actions and no persisted
checkout credentials. Static inspection runs by default. Running tests needs
the repository owner to opt in.

Installers verify the archive checksum, version and member paths, extract only
the binary, and leave an existing binary in place if anything fails. Checksums
do not protect against a compromised publisher. Releases are not signed or
notarized yet.

## Reporting

Please report security issues privately to the maintainers before publishing
details, and keep source and secrets out of public issues. There is no
dedicated private reporting channel yet.
