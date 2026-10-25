# Security policy

## Supported versions

Nothing has been released. Only the `main` branch is supported, and fixes land only there.

## Reporting a vulnerability

Report a vulnerability privately, through GitHub's private vulnerability reporting: open the repository's **Security** tab and choose **Report a vulnerability**. Do not report it in a public issue or pull request.

Include:

- the commit on `main` that you tested;
- the affected package, file or command;
- what an attacker can do, and what they need first, such as local access, a configuration or a platform;
- the steps, or a minimal program, that reproduce it;
- your operating system and architecture, and the output of `go version`;
- whether anyone else knows of it, and any date on which you plan to disclose it.

Do not put real credentials or private data in a report. If a reproduction needs a secret, describe it instead.

## What to expect

Responses are best effort. There are no promised times for a reply or a fix.

Disclosure is coordinated. Keep the report private until a fix is on `main` or a publication date has been agreed in the report.

## Scope

- Secrets never belong in this repository: no credentials, API keys, tokens or private keys in code, tests, fixtures or documents. A secret found in the repository or its history is a vulnerability; report it.
- DwarfHarness is pre-release and for testing only (see the [README](README.md#status)). Do not use it for real work.
- Package documentation states what each component guarantees and what it does not. For example, `internal/core/winexec` makes no claim to be a qualified isolation boundary, `internal/core/durable` makes no power-loss guarantee, and `helpers/go-analyzer` sets no CPU or memory limit and is no boundary against hostile code running as the same user. A component that breaks a guarantee its documentation states is in scope; a limit its documentation already states is known.
- A vulnerability in a dependency belongs to its upstream project. Report it here as well if DwarfHarness's use of the dependency makes it exploitable. The check runs govulncheck on every supported target.
